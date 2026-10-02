import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildIntelligenceTestPreview, extractIntelligenceTestArtifact } from '../intelligenceTestPreview'

describe('intelligence test artifact preview', () => {
  afterEach(() => {
    document.head.querySelectorAll('script[nonce]').forEach((script) => script.remove())
    vi.restoreAllMocks()
  })

  it('extracts HTML from a Markdown response without rendering the commentary', () => {
    const output = 'Here is the animation:\n```html\n<!doctype html><html><body><svg><circle /></svg></body></html>\n```\nEnjoy!'
    expect(extractIntelligenceTestArtifact(output)).toBe('<!doctype html><html><body><svg><circle /></svg></body></html>')
    const preview = buildIntelligenceTestPreview(output)!
    expect(preview).toContain('<svg>')
    expect(preview).not.toContain('Here is the animation')
    expect(preview).not.toContain('Enjoy!')
  })

  it('renders a raw SVG response and preserves SVG and CSS animations', () => {
    const preview = buildIntelligenceTestPreview('An SVG:\n<svg viewBox="0 0 10 10"><style>@keyframes ride {to {transform:rotate(360deg)}}</style><animate attributeName="x" /></svg>\nEnd')!
    expect(preview).toContain('viewBox="0 0 10 10"')
    expect(preview).toContain('<animate')
    expect(preview).toContain('@keyframes ride')
    expect(preview).not.toContain('An SVG:')
  })

  it('installs the restrictive policy before model scripts and removes navigation and embedding tags', () => {
    const preview = buildIntelligenceTestPreview('<html><head><meta http-equiv="refresh" content="0;url=https://example.com"><meta http-equiv="Content-Security-Policy" content="default-src *"><base href="https://example.com"><script>window.animation = true</script></head><body><iframe src="https://example.com"></iframe><object></object><embed><svg /></body></html>')!
    const parsed = new DOMParser().parseFromString(preview, 'text/html')
    const policy = parsed.head.firstElementChild!
    expect(policy.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(policy.getAttribute('content')).toContain("connect-src 'none'")
    expect(policy.getAttribute('content')).toContain("form-action 'none'")
    expect(policy.getAttribute('content')).not.toContain('unsafe-eval')
    expect(parsed.querySelectorAll('meta[http-equiv]')).toHaveLength(1)
    expect(parsed.querySelectorAll('iframe, object, embed, base')).toHaveLength(0)
    expect(parsed.querySelector('script')?.textContent).toBe('window.animation = true')
  })

  it('establishes the restrictive policy before parsing untrusted resource elements', () => {
    const parse = vi.spyOn(DOMParser.prototype, 'parseFromString')
    buildIntelligenceTestPreview('<html><body><img src="https://example.com/track"><iframe src="https://example.com/embed"></iframe></body></html>')
    const input = String(parse.mock.calls[0]![0])
    expect(input.startsWith('<meta http-equiv="Content-Security-Policy"')).toBe(true)
    expect(input.indexOf("default-src 'none'")).toBeLessThan(input.indexOf('https://example.com/track'))
    expect(input.indexOf("frame-src 'none'")).toBeLessThan(input.indexOf('https://example.com/embed'))
  })

  it('uses the production application nonce to allow generated inline animations under the inherited CSP', () => {
    const applicationScript = document.createElement('script')
    applicationScript.nonce = 'appNonce123=='
    document.head.append(applicationScript)
    const preview = buildIntelligenceTestPreview('<html><head><script nonce="model-nonce">animate()</script></head><body><svg /></body></html>')!
    const parsed = new DOMParser().parseFromString(preview, 'text/html')
    expect(parsed.querySelector('script')?.nonce).toBe('appNonce123==')
    expect(parsed.querySelector('meta')?.content).toContain("script-src 'unsafe-inline'")
    expect(parsed.querySelector('meta')?.content).not.toContain("'nonce-appNonce123=='")
  })

  it('falls back to raw output for ordinary text and code without an HTML or SVG artifact', () => {
    expect(buildIntelligenceTestPreview('I could not create the requested animation.')).toBeNull()
    expect(buildIntelligenceTestPreview('```javascript\nconst svg = "<svg></svg>"\n```')).toBeNull()
  })
})
