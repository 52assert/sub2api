import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'
import { formatSubscriptionExpirationInput, MAX_SUBSCRIPTION_EXPIRATION } from '@/utils/subscriptionExpiration'
import type { UserSubscription } from '@/types'

const { list, extend, showError } = vi.hoisted(() => ({ list: vi.fn(), extend: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: { list, extend },
    groups: { getAll: vi.fn().mockResolvedValue([]) },
    users: { list: vi.fn().mockResolvedValue({ items: [] }) }
  }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

const now = Date.parse('2026-10-11T10:30:20.123Z')
const subscription = {
  id: 42, user_id: 7, group_id: 1, status: 'active',
  expires_at: '2026-11-20T18:24:10.125+08:00', user: { email: 'subscriber@example.com' }
} as UserSubscription

async function openAdjustment(overrides: Partial<UserSubscription> = {}) {
  list.mockResolvedValue({ items: [{ ...subscription, ...overrides }], total: 1, pages: 1 })
  const wrapper = mount(SubscriptionsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
        DataTable: { props: ['data'], emits: ['sort', 'update:selectedKeys'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' },
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        Select: {
          props: ['modelValue', 'options', 'disabled'], emits: ['update:modelValue'],
          template: '<select :value="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>'
        },
        DateTimePicker: {
          props: ['modelValue', 'min', 'max', 'disabled'], emits: ['update:modelValue'],
          template: '<input :value="modelValue" :min="min" :max="max" :disabled="disabled" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        Pagination: true, ConfirmDialog: true, Icon: true, GroupBadge: true,
        GroupOptionItem: true, EmptyState: true, Teleport: true, RouterLink: true
      }
    }
  })
  await flushPromises()
  await wrapper.findAll('button').find((button) => button.text() === 'admin.subscriptions.adjust')!.trigger('click')
  return wrapper
}

enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(now)
  extend.mockResolvedValue(subscription)
})
afterEach(() => { vi.useRealTimers() })

describe('subscription expiration adjustment', () => {
  it('opens with the current expiration and places optional shortcuts after the date picker', async () => {
    const wrapper = await openAdjustment()
    const expected = Date.parse(subscription.expires_at!)
    const picker = wrapper.get<HTMLInputElement>('#subscription-expiration')
    const shortcuts = wrapper.get('[data-test="subscription-expiration-shortcuts"]')
    expect(wrapper.get('[data-test="subscription-mode-date"]').attributes('aria-pressed')).toBe('true')
    expect(picker.element.value).toBe(formatSubscriptionExpirationInput(expected))
    expect(picker.attributes('max')).toBe(formatSubscriptionExpirationInput(MAX_SUBSCRIPTION_EXPIRATION))
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(new Date(expected).toISOString())
    expect(wrapper.get('[data-test="subscription-new-expiration"]').text()).toContain('admin.subscriptions.expirationTimezoneHint')
    expect(picker.element.parentElement?.textContent).toContain('admin.subscriptions.expirationTimeHint')
    expect(picker.element.compareDocumentPosition(shortcuts.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(shortcuts.findAll('button')).toHaveLength(4)
    shortcuts.findAll('button').forEach((button) => {
      expect(button.attributes('type')).toBe('button')
      expect(button.attributes('aria-pressed')).toBeUndefined()
    })
    expect(extend).not.toHaveBeenCalled()
  })

  it('lets a custom date before the original expiration submit without choosing a shortcut', async () => {
    const wrapper = await openAdjustment()
    await wrapper.get('#subscription-expiration').setValue('2026-10-25T21:42:15.125')
    const expected = new Date(2026, 9, 25, 21, 42, 15, 125).toISOString()
    expect(Date.parse(expected)).toBeLessThan(Date.parse(subscription.expires_at!))
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(expected)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    await flushPromises()
    expect(extend).toHaveBeenCalledWith(42, { expires_at: expected })
    expect(wrapper.find('#extend-subscription-form').exists()).toBe(false)
  })

  it('lets a custom date completely replace a shortcut in the preview and request', async () => {
    const wrapper = await openAdjustment()
    await wrapper.get('[data-test="subscription-expiration-30"]').trigger('click')
    const custom = '2026-12-09T21:42:15.125'
    const expected = new Date(2026, 11, 9, 21, 42, 15, 125).toISOString()
    await wrapper.get('#subscription-expiration').setValue(custom)
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(expected)
    wrapper.get('[data-test="subscription-expiration-shortcuts"]').findAll('button').forEach((button) => {
      expect(button.attributes('aria-pressed')).toBeUndefined()
    })
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { expires_at: expected })
  })

  it('keeps a custom expiration when switching to day adjustments and back', async () => {
    const wrapper = await openAdjustment()
    const custom = '2026-12-09T21:42:15.125'
    const expected = new Date(2026, 11, 9, 21, 42, 15, 125).toISOString()
    await wrapper.get('#subscription-expiration').setValue(custom)
    await wrapper.get('[data-test="subscription-mode-days"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('#subscription-adjustment-days').element.value).toBe('30')
    await wrapper.get('#subscription-adjustment-days').setValue(90)
    await wrapper.get('[data-test="subscription-mode-date"]').trigger('click')
    expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe(custom)
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(expected)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { expires_at: expected })
  })

  it.each(['2025-01-01T00:00:00Z', null, 'invalid', new Date(now).toISOString(), new Date(MAX_SUBSCRIPTION_EXPIRATION + 1).toISOString()])('leaves the date empty when the current expiration is %j', async (expires_at) => {
    const wrapper = await openAdjustment({ expires_at })
    expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe('')
    expect(wrapper.find('[data-test="subscription-new-expiration"] time').exists()).toBe(false)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.subscriptions.invalidExpiration')
  })

  it.each(['2025-01-01T00:00:00Z', null])('allows direct customization from an empty expiration %j', async (expires_at) => {
    const wrapper = await openAdjustment({ expires_at, status: 'expired' })
    expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe('')
    const custom = '2026-12-09T21:42:15.125'
    const expected = new Date(2026, 11, 9, 21, 42, 15, 125).toISOString()
    await wrapper.get('#subscription-expiration').setValue(custom)
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(expected)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { expires_at: expected })
  })

  it('keeps the exact maximum supported expiration when opening the adjustment', async () => {
    const expected = new Date(MAX_SUBSCRIPTION_EXPIRATION).toISOString()
    const wrapper = await openAdjustment({ expires_at: expected })
    expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe(formatSubscriptionExpirationInput(MAX_SUBSCRIPTION_EXPIRATION))
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(expected)
  })

  it('refreshes the minimum to the time the adjustment is opened', async () => {
    vi.setSystemTime(now + 60000)
    const wrapper = await openAdjustment()
    expect(wrapper.get('#subscription-expiration').attributes('min')).toBe(formatSubscriptionExpirationInput(now + 60001))
  })

  it.each([7, 30, 90, 365])('offers a %i-day renewal from an existing future expiration', async (days) => {
    const wrapper = await openAdjustment()
    await wrapper.get(`[data-test="subscription-expiration-${days}"]`).trigger('click')
    const expected = Date.parse(subscription.expires_at!) + days * 86400000
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(new Date(expected).toISOString())
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { expires_at: new Date(expected).toISOString() })
  })

  it.each(['2025-01-01T00:00:00Z', null])('starts renewal shortcuts from now when expiration is %j', async (expires_at) => {
    const wrapper = await openAdjustment({ expires_at, status: 'expired' })
    expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe('')
    await wrapper.get('[data-test="subscription-expiration-30"]').trigger('click')
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { expires_at: new Date(now + 30 * 86400000).toISOString() })
  })

  it('fills shortcuts from the original expiration without accumulating previous clicks', async () => {
    const wrapper = await openAdjustment()
    for (const days of [7, 30, 30, 7]) {
      await wrapper.get(`[data-test="subscription-expiration-${days}"]`).trigger('click')
      const expected = Date.parse(subscription.expires_at!) + days * 86400000
      expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe(formatSubscriptionExpirationInput(expected))
      expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(new Date(expected).toISOString())
    }
  })

  it.each([
    { overrides: {}, days: -7 },
    { overrides: { status: 'expired', expires_at: '2025-01-01T00:00:00Z' }, days: 30 }
  ])('keeps relative day adjustments compatible for %o', async ({ overrides, days }) => {
    const wrapper = await openAdjustment(overrides as Partial<UserSubscription>)
    await wrapper.get('[data-test="subscription-mode-days"]').trigger('click')
    await wrapper.get('#subscription-adjustment-days').setValue(days)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { days })
    expect(showError).not.toHaveBeenCalled()
  })

  it.each([
    { value: '', error: 'invalidExpiration' },
    { value: formatSubscriptionExpirationInput(now), error: 'expirationMustBeFuture' },
    { value: formatSubscriptionExpirationInput(MAX_SUBSCRIPTION_EXPIRATION + 1), error: 'expirationTooLate' }
  ])('rejects invalid expiration $value', async ({ value, error }) => {
    const wrapper = await openAdjustment()
    await wrapper.get('#subscription-expiration').setValue(value)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith(`admin.subscriptions.${error}`)
  })

  it.each([0, 1.5, 36501])('rejects invalid relative days %s', async (days) => {
    const wrapper = await openAdjustment()
    await wrapper.get('[data-test="subscription-mode-days"]').trigger('click')
    await wrapper.get('#subscription-adjustment-days').setValue(days)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.subscriptions.bulk.invalidDays')
  })

  it('prevents duplicate submissions while saving the selected expiration', async () => {
    extend.mockReturnValueOnce(new Promise(() => {}))
    const wrapper = await openAdjustment()
    await wrapper.get('#extend-subscription-form').trigger('submit')
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledTimes(1)
    expect(wrapper.get('#subscription-expiration').attributes('disabled')).toBeDefined()
  })
})
