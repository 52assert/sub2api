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
  it('defaults to choosing a date, previews a 30-day renewal, and converts the maximum to local time', async () => {
    const wrapper = await openAdjustment()
    const expected = Date.parse(subscription.expires_at!) + 30 * 86400000
    expect(wrapper.get('[data-test="subscription-mode-date"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-test="subscription-expiration-30"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get<HTMLInputElement>('#subscription-expiration').element.value).toBe(formatSubscriptionExpirationInput(expected))
    expect(wrapper.get('#subscription-expiration').attributes('max')).toBe(formatSubscriptionExpirationInput(MAX_SUBSCRIPTION_EXPIRATION))
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(new Date(expected).toISOString())
    expect(wrapper.get('[data-test="subscription-new-expiration"]').text()).toContain('admin.subscriptions.expirationTimezoneHint')
  })

  it('submits a local date as an absolute ISO timestamp with milliseconds and no days', async () => {
    const wrapper = await openAdjustment()
    await wrapper.get('#subscription-expiration').setValue('2026-12-09T21:42:15.125')
    const expected = new Date(2026, 11, 9, 21, 42, 15, 125).toISOString()
    expect(wrapper.get('[data-test="subscription-new-expiration"] time').attributes('datetime')).toBe(expected)
    await wrapper.get('#extend-subscription-form').trigger('submit')
    await flushPromises()
    expect(extend).toHaveBeenCalledWith(42, { expires_at: expected })
    expect(wrapper.find('#extend-subscription-form').exists()).toBe(false)
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
    await wrapper.get('[data-test="subscription-expiration-30"]').trigger('click')
    await wrapper.get('#extend-subscription-form').trigger('submit')
    expect(extend).toHaveBeenCalledWith(42, { expires_at: new Date(now + 30 * 86400000).toISOString() })
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
