import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'
import AccountSubscriptionResetModal from '../AccountSubscriptionResetModal.vue'

const mocks = vi.hoisted(() => ({ preview: vi.fn(), configure: vi.fn(), reset: vi.fn() }))
vi.mock('@/api/admin/accountSubscriptionReset', () => ({
  getSubscriptionResetPreview: mocks.preview,
  configureSubscriptionReset: mocks.configure,
  resetAccountGroupSubscriptions: mocks.reset
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))
const account = { id: 27, name: 'Codex account' } as Account
function preview() {
  return { enabled: false, status: 'disabled', fingerprint: 'a'.repeat(64), last_checked_at: null, last_event_at: null, poll_error: '',
    subscriptions: [{ id: 3, user_id: 4, group_id: 5, group_name: 'Group', daily_usage_usd: 10, weekly_usage_usd: 40 }] }
}
function create() { return mount(AccountSubscriptionResetModal, { props: { show: true, account }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } } }) }
function button(wrapper: ReturnType<typeof create>, text: string) {
  const match = wrapper.findAll('button').find(b => b.text() === text)
  if (!match) throw new Error(`Missing button: ${text}`)
  return match
}
beforeEach(() => { vi.clearAllMocks(); mocks.preview.mockResolvedValue(preview()); mocks.configure.mockResolvedValue(undefined); mocks.reset.mockResolvedValue(1) })
describe('account subscription resets', () => {
  it('requires a fresh preview and a second explicit confirmation', async () => {
    const wrapper = create(); await flushPromises()
    expect(mocks.reset).not.toHaveBeenCalled()
    await button(wrapper, '手动重置日、周额度').trigger('click'); await flushPromises()
    expect(mocks.preview).toHaveBeenCalledTimes(2)
    expect(mocks.reset).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('月额度和到期时间不变')
    await button(wrapper, '确认重置全部所列订阅').trigger('click'); await flushPromises()
    expect(mocks.reset).toHaveBeenCalledWith(27, expect.any(String), 'a'.repeat(64))
    expect(wrapper.text()).toContain('已重置订阅数：1')
    wrapper.unmount()
  })
  it('reuses the operation ID after a lost response and never retries automatically', async () => {
    mocks.reset.mockRejectedValueOnce(new Error('timeout'))
    const wrapper = create(); await flushPromises()
    await button(wrapper, '手动重置日、周额度').trigger('click'); await flushPromises()
    await button(wrapper, '确认重置全部所列订阅').trigger('click'); await flushPromises()
    expect(mocks.reset).toHaveBeenCalledTimes(1)
    const first = mocks.reset.mock.calls[0]
    await button(wrapper, '确认重置全部所列订阅').trigger('click'); await flushPromises()
    expect(mocks.reset.mock.calls[1]).toEqual(first)
    wrapper.unmount()
  })
  it('saves automatic policy separately without resetting subscriptions', async () => {
    const wrapper = create(); await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await button(wrapper, '保存自动设置').trigger('click'); await flushPromises()
    expect(mocks.configure).toHaveBeenCalledWith(27, true)
    expect(mocks.reset).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('does not allow resets after a failed preview', async () => {
    mocks.preview.mockRejectedValue(new Error('unavailable'))
    const wrapper = create(); await flushPromises()
    expect(wrapper.findAll('button').some(b => b.text() === '手动重置日、周额度')).toBe(false)
    expect(mocks.reset).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it.each([27, 28])('discards an old reset preparation after reopening account %s', async (id) => {
    const wrapper = create(); await flushPromises()
    let resolveOldPreview!: (value: ReturnType<typeof preview>) => void
    mocks.preview.mockImplementationOnce(() => new Promise(resolve => { resolveOldPreview = resolve }))
    await button(wrapper, '手动重置日、周额度').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, account: { ...account, id } })
    await flushPromises()
    resolveOldPreview(preview()); await flushPromises()
    expect(wrapper.findAll('button').some(b => b.text() === '确认重置全部所列订阅')).toBe(false)
    expect(button(wrapper, '手动重置日、周额度').exists()).toBe(true)
    expect(mocks.reset).not.toHaveBeenCalled()
    await button(wrapper, '手动重置日、周额度').trigger('click'); await flushPromises()
    await button(wrapper, '确认重置全部所列订阅').trigger('click'); await flushPromises()
    expect(mocks.reset).toHaveBeenCalledWith(id, expect.any(String), 'a'.repeat(64))
    wrapper.unmount()
  })
})
