import DOMPurify from 'dompurify'
import { renderToString } from 'katex'
import { Marked, type Token, type Tokens, type TokenizerAndRendererExtension } from 'marked'

const MAX_MARKDOWN_LENGTH = 256 * 1024
const MAX_MATH_LENGTH = 8 * 1024
const MAX_MATH_EXPRESSIONS = 256

function escapeHtml(text: string): string {
  return text.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  })[character]!)
}

interface MathToken extends Tokens.Generic {
  type: 'intelligence_math'
  text: string
  display: boolean
  literal?: boolean
}

// Recognize math before Markdown consumes backslash escapes. Native code and
// code-span tokens are left alone, so examples retain their literal delimiters.
function readMath(source: string): MathToken | undefined {
  const delimiter = [
    { open: '\\[', close: '\\]', display: true },
    { open: '$$', close: '$$', display: true },
    { open: '\\(', close: '\\)', display: false },
    { open: '$', close: '$', display: false }
  ].find(({ open }) => source.startsWith(open))
  if (!delimiter || (delimiter.open === '$' && /\s/.test(source[1] ?? ''))) return

  let braces = 0
  for (let index = delimiter.open.length; index < Math.min(source.length, MAX_MATH_LENGTH); index++) {
    if (braces === 0 && source.startsWith(delimiter.close, index)) {
      const text = source.slice(delimiter.open.length, index)
      const end = index + delimiter.close.length
      if (!text.trim()) return
      // Avoid turning ordinary prices such as "$5 and $10" into equations.
      if (delimiter.open === '$' && (/\s$/.test(text) || /\d/.test(source[end] ?? ''))) return
      return { type: 'intelligence_math', raw: source.slice(0, end), text, display: delimiter.display }
    }
    const character = source[index]
    if (character === '\\') index++
    else if (character === '{') braces++
    else if (character === '}') braces = Math.max(0, braces - 1)
    else if (character === '\n' && delimiter.open === '$') return
  }
}

function renderMath(token: Token): string {
  const math = token as MathToken
  if (!math.text || math.literal) return escapeHtml(math.raw)
  try {
    return renderToString(math.text, {
      displayMode: math.display,
      output: 'htmlAndMathml',
      throwOnError: true,
      trust: false,
      strict: 'ignore',
      maxExpand: 1000,
      maxSize: 10
    })
  } catch {
    return escapeHtml(math.raw)
  }
}

const blockMath: TokenizerAndRendererExtension = {
  name: 'intelligence_math',
  level: 'block',
  start(source) { return source.match(/^ {0,3}(?:\$\$|\\\[)/m)?.index },
  tokenizer(source) {
    const indentation = source.match(/^ {0,3}/)![0]
    const math = readMath(source.slice(indentation.length))
    if (!math?.display) return
    const ending = source.slice(indentation.length + math.raw.length).match(/^[ \t]*(?:\n|$)/)
    if (!ending) return
    return { ...math, raw: indentation + math.raw + ending[0] }
  },
  renderer: renderMath
}

const markdown = new Marked({
  async: false,
  gfm: true,
  breaks: false,
  extensions: [blockMath, {
    name: 'intelligence_math',
    level: 'inline',
    start(source) { return source.match(/\\(?:\(|\[)|\$/)?.index },
    tokenizer(source) {
      return readMath(source) ?? (/^\\[([]/.test(source)
        ? { type: 'intelligence_math', raw: source.slice(0, 2), text: '', display: false }
        : undefined)
    },
    renderer: renderMath
  }],
  renderer: {
    html({ text }) { return escapeHtml(text) },
    image({ text }) { return escapeHtml(text) },
    link({ href, tokens }) {
      const label = this.parser.parseInline(tokens)
      if (!/^(?:https?:|mailto:|#)/i.test(href)) return label
      return `<a href="${escapeHtml(href)}" target="_blank" rel="noopener noreferrer">${label}</a>`
    }
  }
})

const ALLOWED_TAGS = [
  'p', 'br', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'strong', 'em', 'del',
  'blockquote', 'ol', 'ul', 'li', 'pre', 'code', 'a', 'table', 'thead', 'tbody',
  'tr', 'th', 'td', 'span',
  // KaTeX's visual output and its accessible MathML representation.
  'svg', 'path', 'line', 'math', 'semantics', 'annotation', 'mrow', 'mi', 'mo',
  'mn', 'mtext', 'mspace', 'msup', 'msub', 'msubsup', 'mfrac', 'mroot', 'msqrt',
  'mstyle', 'mtable', 'mtr', 'mtd', 'mover', 'munder', 'munderover', 'mpadded',
  'mphantom', 'menclose'
]

export function renderIntelligenceTestMarkdown(output: string): string {
  // Very large results stay readable without expanding a multi-megabyte
  // Markdown/math document on the browser's main thread.
  const plainText = () => `<pre><code>${escapeHtml(output)}</code></pre>`
  let html: string
  try {
    let formulas = 0
    html = output.length > MAX_MARKDOWN_LENGTH ? plainText() : markdown.parse(output, {
      async: false,
      walkTokens(token) {
        if (token.type === 'intelligence_math') (token as MathToken).literal = formulas++ >= MAX_MATH_EXPRESSIONS
      }
    })
  } catch {
    // Pathological nesting can exceed the Markdown parser's stack limit.
    html = plainText()
  }
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS,
    ADD_ATTR: ['target', 'rel'],
    ALLOW_DATA_ATTR: false,
    FORBID_TAGS: ['img', 'image', 'script', 'style', 'link', 'iframe', 'object', 'embed', 'form', 'input', 'video', 'audio', 'source'],
    FORBID_ATTR: ['src', 'srcset', 'xlink:href', 'formaction', 'id', 'name']
  })
}
