import { marked } from 'marked'
import { afterEach, describe, expect, it } from 'vitest'
import { renderIntelligenceTestMarkdown } from '../intelligenceTestMarkdown'

function render(output: string): HTMLTemplateElement {
  const template = document.createElement('template')
  template.innerHTML = renderIntelligenceTestMarkdown(output)
  return template
}

describe('intelligence test Markdown rendering', () => {
  afterEach(() => marked.setOptions({ breaks: false, gfm: true }))

  it('renders the headings, emphasis, lists, quotations and tables of a reasoning answer', () => {
    const html = render('# 解答\n\n最少 **21颗**，*保证配对*。\n\n- 五角星形\n- 圆形\n\n1. 选择形状\n2. 统计数量\n\n> 最坏情况\n\n| 形状 | 数量 |\n| --- | --- |\n| 五角星 | 12 |')
    expect(html.content.querySelector('h1')?.textContent).toBe('解答')
    expect(html.content.querySelector('strong')?.textContent).toBe('21颗')
    expect(html.content.querySelector('em')?.textContent).toBe('保证配对')
    expect(html.content.querySelectorAll('ul li')).toHaveLength(2)
    expect(html.content.querySelectorAll('ol li')).toHaveLength(2)
    expect(html.content.querySelector('blockquote')?.textContent?.trim()).toBe('最坏情况')
    expect(html.content.querySelectorAll('table tbody td')).toHaveLength(2)
  })

  it.each([
    { source: '\\(6+4=10\\)', display: false },
    { source: '$6+4=10$', display: false },
    { source: '\\[6+4=10\\]', display: true },
    { source: '$$6+4=10$$', display: true }
  ])('renders accessible math with $source delimiters', ({ source, display }) => {
    const html = render(source)
    expect(html.content.querySelectorAll('.katex')).toHaveLength(1)
    expect(html.content.querySelector('math')).not.toBeNull()
    expect(html.content.querySelector('annotation')?.textContent).toBe('6+4=10')
    expect(html.content.querySelectorAll('.katex-display')).toHaveLength(display ? 1 : 0)
  })

  it('preserves inline equations beside prose and renders multiline display fractions', () => {
    const html = render('需要 **21颗**：\\(9+12=21\\)。\n\n\\[\n\\frac{21}{2}\n\\]\n\n下一段。')
    expect(html.content.querySelector('strong')?.textContent).toBe('21颗')
    expect(html.content.querySelectorAll('.katex')).toHaveLength(2)
    expect(html.content.querySelectorAll('.katex-display')).toHaveLength(1)
    expect(html.content.querySelector('mfrac')).not.toBeNull()
    expect(html.content.querySelector('.katex-html [style]')).not.toBeNull()
    expect(html.content.textContent).toContain('下一段。')
  })

  it('leaves formula examples in inline, fenced and indented code unchanged', () => {
    const html = render('`\\(x\\) $x$`\n\n```text\n\\[x\\]\n$$x$$\n```\n\n    \\(y\\)')
    expect(html.content.querySelector('.katex')).toBeNull()
    expect(html.content.querySelector('p code')?.textContent).toBe('\\(x\\) $x$')
    expect(html.content.querySelectorAll('pre code')[0]?.textContent).toBe('\\[x\\]\n$$x$$\n')
    expect(html.content.querySelectorAll('pre code')[1]?.textContent).toBe('\\(y\\)\n')
  })

  it('keeps currency, escaped dollars and incomplete delimiters readable', () => {
    const html = render('Prices are $5 and $10. Escaped \\$x\\$.\n\n\\(x+1\n\n$$unfinished')
    expect(html.content.querySelector('.katex')).toBeNull()
    expect(html.content.textContent).toContain('Prices are $5 and $10. Escaped $x$.')
    expect(html.content.textContent).toContain('\\(x+1')
    expect(html.content.textContent).toContain('$$unfinished')
  })

  it('falls back to the visible formula source for malformed and expanding expressions', () => {
    for (const source of ['\\(\\notACommand{1}\\)', '\\(\\frac{1}\\)', '\\(\\def\\a{\\a}\\a\\)']) {
      const html = render(source)
      expect(html.content.querySelector('.katex')).toBeNull()
      expect(html.content.textContent?.trim()).toBe(source)
    }
  })

  it('shows raw HTML as text and prevents Markdown and formula resources or executable links', () => {
    const source = '<img src="https://tracker.example/pixel" onerror="alert(1)"><script>alert(1)</script>\n\n'
      + '![image](https://tracker.example/image) [unsafe](javascript:alert%281%29) [data](data:text/html,hello)\n\n'
      + '\\(\\includegraphics{https://tracker.example/math}\\) '
      + '\\(\\href{javascript:alert(1)}{click}\\) '
      + '\\(\\htmlStyle{background:url(https://tracker.example/css)}{x}\\)'
    const html = render(source)
    expect(html.content.textContent).toContain('<img src="https://tracker.example/pixel" onerror="alert(1)">')
    expect(html.content.textContent).toContain('<script>alert(1)</script>')
    expect(html.content.textContent).toContain('image')
    expect(html.content.querySelector('img, image, script, style, link, iframe, object, embed')).toBeNull()
    expect(html.content.querySelector('[src], [srcset], [onerror], [onclick], [style*="url("]')).toBeNull()
    expect(html.content.querySelector('a')).toBeNull()
  })

  it('preserves safe links with opener protection and rejects other protocols', () => {
    const html = render('[docs](https://example.com/docs?q=1&x=2) [mail](mailto:hello@example.com) [file](file:///etc/passwd) [target](#answer)')
    const links = [...html.content.querySelectorAll('a')]
    expect(links.map((link) => link.getAttribute('href'))).toEqual(['https://example.com/docs?q=1&x=2', 'mailto:hello@example.com', '#answer'])
    expect(links.every((link) => link.target === '_blank' && link.rel === 'noopener noreferrer')).toBe(true)
    expect(html.content.textContent).toContain('file')
  })

  it('does not inherit or change the global Markdown instance used by other pages', () => {
    marked.setOptions({ breaks: true })
    const html = render('first\nsecond\n\n\\(x\\)')
    expect(html.content.querySelector('br')).toBeNull()
    expect(html.content.querySelector('.katex')).not.toBeNull()
    expect(marked.parse('first\nsecond')).toContain('<br>')
  })

  it('keeps very large answers intact using escaped code rather than expanding Markdown or math', () => {
    const output = '# **large** \\(x\\) <img src="https://tracker.example/pixel">\n' + 'x'.repeat(256 * 1024)
    const html = render(output)
    expect(html.content.querySelector('pre code')?.textContent).toBe(output)
    expect(html.content.querySelector('strong, .katex, img')).toBeNull()
  })

  it('falls back to readable text when deeply nested Markdown exceeds the parser stack', () => {
    const output = '> '.repeat(5000) + 'answer'
    const html = render(output)
    expect(html.content.querySelector('pre code')?.textContent).toBe(output)
  })

  it('bounds formula expansion per answer and preserves the remaining formulas as source', () => {
    const html = render('$x$ '.repeat(300))
    expect(html.content.querySelectorAll('.katex')).toHaveLength(256)
    expect(html.content.textContent).toContain('$x$')
    expect(render('$y$').content.querySelectorAll('.katex')).toHaveLength(1)
  })
})
