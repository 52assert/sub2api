import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import IntelligenceTestsView from '../IntelligenceTestsView.vue'
import type { IntelligenceTestRecord } from '@/api/intelligenceTests'

const { listIntelligenceTests, getIntelligenceTest, copyToClipboard, query } = vi.hoisted(() => ({ listIntelligenceTests: vi.fn(), getIntelligenceTest: vi.fn(), copyToClipboard: vi.fn(), query: {} as Record<string, string> }))
vi.mock('@/api/intelligenceTests', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api/intelligenceTests')>(), listIntelligenceTests, getIntelligenceTest }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard }) }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))

function record(overrides: Partial<IntelligenceTestRecord> = {}): IntelligenceTestRecord {
  return { id: 1, account_name: 'Test account', platform: 'openai', model: 'gpt-test', runner: 'http', runner_version: '', effective_model: '', artifact_name: '', final_message: '', reasoning_effort: 'high', prompt: 'Animate a cycling pelican', status: 'running', created_at: '2026-10-02T00:00:00Z', duration_ms: 0, ...overrides }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolver) => { resolve = resolver })
  return { promise, resolve }
}
function mountView() {
  return mount(IntelligenceTestsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } } })
}

describe('shared IntelligenceTestsView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    listIntelligenceTests.mockReset().mockResolvedValue({ items: [record()], retention: 10 })
    getIntelligenceTest.mockReset().mockResolvedValue(record())
    copyToClipboard.mockReset().mockResolvedValue(true)
    for (const key of Object.keys(query)) delete query[key]
  })
  afterEach(() => { vi.useRealTimers() })

  it('polls background jobs through completion and renders the final artifact in an isolated iframe', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="intelligence-test-active"]').exists()).toBe(true)
    const finished = record({ status: 'succeeded', duration_ms: 12345, output: '<html><body><svg><circle /></svg><script>animate()</script></body></html>' })
    listIntelligenceTests.mockResolvedValue({ items: [finished], retention: 10 })
    getIntelligenceTest.mockResolvedValue(finished)
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(listIntelligenceTests).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="intelligence-test-active"]').exists()).toBe(false)
    const frame = wrapper.get('[data-testid="intelligence-test-preview"]')
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('srcdoc')).toContain("connect-src 'none'")
    expect(frame.attributes('srcdoc')).toContain('<svg>')
    await vi.advanceTimersByTimeAsync(10000)
    expect(listIntelligenceTests).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('selects the newly submitted task from the query without any administrator APIs', async () => {
    query.test = '2'
    listIntelligenceTests.mockResolvedValue({ items: [record(), record({ id: 2 })], retention: 10 })
    getIntelligenceTest.mockResolvedValue(record({ id: 2 }))
    const wrapper = mountView()
    await flushPromises()
    expect(getIntelligenceTest).toHaveBeenCalledWith(2, { signal: expect.any(AbortSignal) })
    expect(wrapper.get('[data-testid="intelligence-test-record-2"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.text()).not.toContain('account_id')
    wrapper.unmount()
  })

  it('shows the source account in each record and the selected result', async () => {
    const first = record({ account_name: 'First upstream account' })
    const second = record({ id: 2, account_name: 'Second upstream account' })
    listIntelligenceTests.mockResolvedValue({ items: [first, second], retention: 10 })
    getIntelligenceTest.mockResolvedValueOnce(first).mockResolvedValueOnce(second)
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="intelligence-test-record-1"]').text()).toContain('First upstream account')
    expect(wrapper.get('[data-testid="intelligence-test-record-2"]').text()).toContain('Second upstream account')
    expect(wrapper.get('[data-testid="intelligence-test-account"]').text()).toBe('First upstream account')
    await wrapper.get('[data-testid="intelligence-test-record-2"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="intelligence-test-account"]').text()).toBe('Second upstream account')
    wrapper.unmount()
  })

  it.each(['', '   ', undefined])('shows an unknown account fallback for unavailable historical names: %s', async (account_name) => {
    const historical = record({ account_name })
    listIntelligenceTests.mockResolvedValue({ items: [historical], retention: 10 })
    getIntelligenceTest.mockResolvedValue(historical)
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="intelligence-test-record-1"]').text()).toContain('intelligenceTests.unknownAccount')
    expect(wrapper.get('[data-testid="intelligence-test-account"]').text()).toBe('intelligenceTests.unknownAccount')
    wrapper.unmount()
  })

  it('shows execution provenance and keeps the final message separate from the HTML artifact', async () => {
    const finished = record({
      status: 'succeeded', runner: 'codex_cli', runner_version: '0.120.0', effective_model: 'gpt-actual', artifact_name: 'pelican-bicycle.html',
      final_message: 'Created <img src=x onerror=alert(1)>', output: '<html><body>Animated pelican</body></html>'
    })
    listIntelligenceTests.mockResolvedValue({ items: [finished], retention: 10 })
    getIntelligenceTest.mockResolvedValue(finished)
    const wrapper = mountView()
    await flushPromises()
    const item = wrapper.get('[data-testid="intelligence-test-record-1"]')
    for (const text of ['intelligenceTests.runners.codex_cli', '0.120.0', 'gpt-actual', 'pelican-bicycle.html']) {
      expect(item.text()).toContain(text)
      expect(wrapper.get('section').text()).toContain(text)
    }
    const finalMessage = wrapper.get('[data-testid="intelligence-test-final-message"]')
    expect(finalMessage.element.tagName).toBe('DETAILS')
    expect(finalMessage.text()).toContain(finished.final_message)
    expect(finalMessage.find('img').exists()).toBe(false)
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Animated pelican')
    expect(wrapper.get('iframe').attributes('srcdoc')).not.toContain('onerror')
    wrapper.unmount()
  })

  it('treats historical records without execution metadata as HTTP tests', async () => {
    const historical = record({ runner: undefined, runner_version: undefined, effective_model: undefined, artifact_name: undefined, final_message: undefined })
    listIntelligenceTests.mockResolvedValue({ items: [historical], retention: 10 })
    getIntelligenceTest.mockResolvedValue(historical)
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="intelligence-test-record-1"]').text()).toContain('intelligenceTests.runners.http')
    expect(wrapper.get('[data-testid="intelligence-test-runner"]').text()).toBe('intelligenceTests.runners.http')
    expect(wrapper.get('section').text()).not.toContain('intelligenceTests.runnerVersion')
    expect(wrapper.find('[data-testid="intelligence-test-final-message"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('omits redundant actual model metadata when no mapping changed the model', async () => {
    const finished = record({ status: 'succeeded', effective_model: 'gpt-test' })
    listIntelligenceTests.mockResolvedValue({ items: [finished], retention: 10 })
    getIntelligenceTest.mockResolvedValue(finished)
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).not.toContain('intelligenceTests.effectiveModel')
    wrapper.unmount()
  })

  it('ignores an older detail request when the selected result changes', async () => {
    listIntelligenceTests.mockResolvedValue({ items: [record(), record({ id: 2 }), record({ id: 3 })], retention: 10 })
    const wrapper = mountView()
    await flushPromises()
    const stale = deferred<IntelligenceTestRecord>()
    getIntelligenceTest.mockReturnValueOnce(stale.promise).mockResolvedValueOnce(record({ id: 3, model: 'Latest model', status: 'succeeded', output: '<svg><text>Latest artifact</text></svg>' }))
    await wrapper.get('[data-testid="intelligence-test-record-2"]').trigger('click')
    await wrapper.get('[data-testid="intelligence-test-record-3"]').trigger('click')
    await flushPromises()
    stale.resolve(record({ id: 2, model: 'Stale model', status: 'succeeded', output: '<svg><text>Stale artifact</text></svg>' }))
    await flushPromises()
    expect(wrapper.get('section').text()).toContain('Latest model')
    expect(wrapper.get('section').text()).not.toContain('Stale model')
    expect(wrapper.get('iframe').attributes('srcdoc')).toContain('Latest artifact')
    wrapper.unmount()
  })

  it('shows raw text safely when there is no renderable artifact and copies original output', async () => {
    const finished = record({ status: 'succeeded', output: 'No artifact: <img src=x onerror=alert(1)>' })
    listIntelligenceTests.mockResolvedValue({ items: [finished], retention: 10 })
    getIntelligenceTest.mockResolvedValue(finished)
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('[data-testid="intelligence-test-source"]').text()).toBe(finished.output)
    expect(wrapper.find('img').exists()).toBe(false)
    await wrapper.findAll('button').find((button) => button.text() === 'intelligenceTests.copyOutput')!.trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith(finished.output, 'intelligenceTests.copied')
    wrapper.unmount()
  })

  it('displays a CLI reasoning answer without artifact controls or a duplicate final message', async () => {
    const answer = '至少取出 29 颗糖果。\n最坏情况下可取出 28 颗而不满足条件。'
    const finished = record({ status: 'succeeded', runner: 'codex_cli', prompt: '直接回答糖果推理题，不调用工具。', output: answer, final_message: answer })
    listIntelligenceTests.mockResolvedValue({ items: [finished], retention: 10 })
    getIntelligenceTest.mockResolvedValue(finished)
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="intelligence-test-source"]').text()).toBe(answer)
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.find('[data-testid="intelligence-test-source-tab"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="intelligence-test-final-message"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('intelligenceTests.noPreview')
    expect(wrapper.text()).not.toContain('intelligenceTests.failed')
    wrapper.unmount()
  })

  it('shows failed tasks and allows retrying a failed list request', async () => {
    listIntelligenceTests.mockRejectedValueOnce(new Error('Network unavailable'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Network unavailable')
    const failed = record({ status: 'failed', error: 'Upstream timed out' })
    listIntelligenceTests.mockResolvedValue({ items: [failed], retention: 10 })
    getIntelligenceTest.mockResolvedValue(failed)
    await wrapper.findAll('button').find((button) => button.text() === 'common.refresh')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Upstream timed out')
    wrapper.unmount()
  })

  it('stops polling and aborts outstanding requests when leaving the results page', async () => {
    const wrapper = mountView()
    await flushPromises()
    const listSignal = listIntelligenceTests.mock.calls[0]![0].signal as AbortSignal
    const detailSignal = getIntelligenceTest.mock.calls[0]![1].signal as AbortSignal
    wrapper.unmount()
    expect(listSignal.aborted).toBe(true)
    expect(detailSignal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(15000)
    expect(listIntelligenceTests).toHaveBeenCalledTimes(1)
  })
})
