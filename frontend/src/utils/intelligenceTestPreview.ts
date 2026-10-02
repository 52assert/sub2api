// Generated documents run in an opaque sandbox. Permit inline animation while
// blocking fetches, external resources, frames, forms, and base URLs.
const PREVIEW_CSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; media-src 'none'; form-action 'none'; base-uri 'none'"

export function extractIntelligenceTestArtifact(output: string): string | null {
  const blocks = [...output.matchAll(/```([^\r\n`]*)\r?\n([\s\S]*?)```/g)]
  for (const block of blocks) {
    const language = (block[1] ?? '').trim().toLowerCase()
    const body = (block[2] ?? '').trim()
    if (['html', 'svg', 'xml', ''].includes(language) && /<(?:!doctype\s+html|html|body|svg|div|main|canvas)(?:\s|>)/i.test(body)) {
      return body
    }
  }

  const raw = output.replace(/```[^\r\n`]*\r?\n[\s\S]*?```/g, '')
  const htmlStart = raw.search(/<!doctype\s+html(?:\s|>)|<html(?:\s|>)/i)
  if (htmlStart >= 0) {
    const htmlEnd = raw.toLowerCase().lastIndexOf('</html>')
    return raw.slice(htmlStart, htmlEnd >= htmlStart ? htmlEnd + 7 : undefined).trim()
  }
  const svgStart = raw.search(/<svg(?:\s|>)/i)
  const svgEnd = raw.toLowerCase().lastIndexOf('</svg>')
  if (svgStart >= 0 && svgEnd >= svgStart) return raw.slice(svgStart, svgEnd + 6).trim()
  if (/^\s*<(?:body|div|main|canvas)(?:\s|>)/i.test(raw)) return raw.trim()
  return null
}

export function buildIntelligenceTestPreview(output: string, nonce = globalThis.document.querySelector<HTMLScriptElement>('script[nonce]')?.nonce ?? ''): string | null {
  const artifact = extractIntelligenceTestArtifact(output)
  if (!artifact) return null
  // Apply the fixed policy before the parser encounters any untrusted resource
  // tags, including in browsers that fetch resources from detached documents.
  const parsingPolicy = `<meta http-equiv="Content-Security-Policy" content="${PREVIEW_CSP}">`
  const document = new DOMParser().parseFromString(parsingPolicy + artifact, 'text/html')
  document.querySelectorAll('base, iframe, frame, object, embed, meta[http-equiv]').forEach((element) => element.remove())
  // srcdoc inherits the application's CSP. Reuse its trusted nonce so inline
  // animations also work when the production CSP disallows unsafe-inline.
  const trustedNonce = /^[A-Za-z0-9+/_=-]+$/.test(nonce) ? nonce : ''
  if (trustedNonce) document.querySelectorAll('script:not([src])').forEach((script) => script.setAttribute('nonce', trustedNonce))
  const csp = document.createElement('meta')
  csp.httpEquiv = 'Content-Security-Policy'
  // Do not authorize nonce sources in this separate policy: doing so would let
  // generated code copy its nonce onto remote script tags. The inherited CSP
  // and this inline-only policy both apply, keeping remote scripts blocked.
  csp.content = PREVIEW_CSP
  document.head.prepend(csp)
  return `<!DOCTYPE html>\n${document.documentElement.outerHTML}`
}
