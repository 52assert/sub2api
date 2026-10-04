import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'
import type { AccountActionSchedule, AccountActionScheduleList, AccountActionScheduleRun } from '@/api/admin/accountActionSchedules'
import messages from '@/i18n/locales/zh/customAccountSchedules'
import AccountActionSchedulesModal from '../AccountActionSchedulesModal.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn(), runs: vi.fn() }))
vi.mock('@/api/admin/accountActionSchedules', () => ({ listAccountActionSchedules: api.list, createAccountActionSchedule: api.create, updateAccountActionSchedule: api.update, deleteAccountActionSchedule: api.remove, listAccountActionScheduleRuns: api.runs }))
vi.mock('vue-i18n', () => ({ useI18n: () => {
  const lookup = (key: string) => key.split('.').reduce<unknown>((value, part) => value && typeof value === 'object' ? (value as Record<string, unknown>)[part] : undefined, messages)
  return { locale: { value: 'zh' }, te: (key: string) => typeof lookup(key) === 'string', t: (key: string, params: Record<string, unknown> = {}) => String(lookup(key) ?? key).replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? `{${name}}`)) }
} }))
const account = { id: 27, name: 'Pro20x Echo' } as Account
const schedule = (overrides: Partial<AccountActionSchedule> = {}): AccountActionSchedule => ({ id: 1, account_id: 27, action: 'reset_card', frequency: 'once', timezone: 'Asia/Shanghai', run_at: '2026-10-10T00:30:00Z', enabled: true, next_run_at: '2026-10-10T00:30:00Z', created_at: '2026-10-04T00:00:00Z', ...overrides })
const subscription = schedule({ id: 2, action: 'reset_subscriptions', frequency: 'weekly', run_at: null, time_of_day: '10:00', weekday: 1, enabled: false, last_status: 'failed', last_message: 'Account groups changed; review and save the schedule to resume' })
const view = (items = [schedule(), subscription]): AccountActionScheduleList => ({ items, timezone: 'Asia/Shanghai', groups: [{ id: 5, name: 'Echo group' }] })
const run = (overrides: Partial<AccountActionScheduleRun> = {}): AccountActionScheduleRun => ({ id: 10, schedule_id: 2, account_id: 27, action: 'reset_subscriptions', status: 'succeeded', message: '', scheduled_at: '2026-10-05T02:00:00Z', started_at: '2026-10-05T02:00:01Z', finished_at: '2026-10-05T02:00:03Z', reset_count: 5, ...overrides })
function create(show = true) { return mount(AccountActionSchedulesModal, { props: { show, account }, global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } } } }) }
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (cause: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
async function newForm(wrapper: ReturnType<typeof create>, frequency = 'once', action = 'reset_card') { await wrapper.get('[data-testid="schedule-add"]').trigger('click'); await wrapper.get('[data-testid="schedule-frequency"]').setValue(frequency); await wrapper.get('[data-testid="schedule-action"]').setValue(action) }
async function submit(wrapper: ReturnType<typeof create>) { await wrapper.get('[data-testid="schedule-form"]').trigger('submit'); await flushPromises() }

beforeEach(() => { vi.resetAllMocks(); api.list.mockResolvedValue(view()); api.create.mockResolvedValue(schedule({ id: 3 })); api.update.mockResolvedValue(schedule()); api.remove.mockResolvedValue(undefined); api.runs.mockResolvedValue([run()]) })

describe('account action schedules', () => {
  it('loads only when open and explains independent actions and the exact group scope', async () => {
    const wrapper = create(false)
    expect(api.list).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true }); await flushPromises()
    expect(api.list).toHaveBeenCalledWith(27)
    expect(wrapper.text()).toContain('消耗 1 张')
    expect(wrapper.text()).toContain('月额度和到期时间保持不变')
    expect(wrapper.text()).toContain('两类操作独立执行')
    expect(wrapper.get('[data-testid="schedule-target-groups"]').text()).toContain('Echo group (#5)')
    expect(wrapper.text()).toContain('下次执行')
    expect(wrapper.text()).toContain('Asia/Shanghai')
    wrapper.unmount()
  })

  it('creates a one-time task with a timezone-local date and no unrelated timing fields', async () => {
    const wrapper = create(); await flushPromises(); await newForm(wrapper)
    expect((wrapper.get('[data-testid="schedule-timezone"]').element as HTMLInputElement).value).toBe('Asia/Shanghai')
    await wrapper.get('[data-testid="schedule-run-at"]').setValue('2026-10-10T08:30')
    await submit(wrapper)
    expect(api.create).toHaveBeenCalledWith(27, { action: 'reset_card', frequency: 'once', timezone: 'Asia/Shanghai', run_at: '2026-10-10T08:30', enabled: true })
    expect(wrapper.emitted('updated')).toHaveLength(1)
    expect(wrapper.find('[data-testid="schedule-form"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each(['daily', 'weekly'])('creates an independent %s subscription reset with Sunday represented as zero', async frequency => {
    const wrapper = create(); await flushPromises(); await newForm(wrapper, frequency, 'reset_subscriptions')
    await wrapper.get('[data-testid="schedule-time"]').setValue('09:45')
    if (frequency === 'weekly') await wrapper.get('[data-testid="schedule-weekday"]').setValue('0')
    await submit(wrapper)
    expect(api.create).toHaveBeenCalledWith(27, { action: 'reset_subscriptions', frequency, timezone: 'Asia/Shanghai', time_of_day: '09:45', ...(frequency === 'weekly' ? { weekday: 0 } : {}), enabled: true })
    wrapper.unmount()
  })

  it('supports a custom Cron example while keeping timezone independent', async () => {
    const wrapper = create(); await flushPromises(); await newForm(wrapper, 'cron')
    await wrapper.get('[data-testid="schedule-cron-weekly-example"]').trigger('click')
    await wrapper.get('[data-testid="schedule-timezone"]').setValue('America/New_York')
    await submit(wrapper)
    expect(api.create).toHaveBeenCalledWith(27, { action: 'reset_card', frequency: 'cron', timezone: 'America/New_York', cron_expression: '0 8 * * 1', enabled: true })
    wrapper.unmount()
  })

  it('blocks incomplete dates and invalid timezones without sending a mutation', async () => {
    const wrapper = create(); await flushPromises(); await newForm(wrapper)
    await submit(wrapper)
    expect(wrapper.get('[role="alert"]').text()).toContain('执行日期和时间')
    await wrapper.get('[data-testid="schedule-run-at"]').setValue('2026-10-10T08:30')
    await wrapper.get('[data-testid="schedule-timezone"]').setValue('bad/zone')
    await submit(wrapper)
    expect(wrapper.get('[role="alert"]').text()).toContain('时区无效')
    expect(api.create).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not allow subscription reset creation when no groups are linked', async () => {
    api.list.mockResolvedValue({ ...view(), groups: [] })
    const wrapper = create(); await flushPromises(); await newForm(wrapper, 'daily', 'reset_subscriptions'); await submit(wrapper)
    expect(wrapper.get('[role="alert"]').text()).toContain('至少一个关联分组')
    expect(api.create).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('blocks changes after a failed scope refresh and allows retrying the read', async () => {
    const wrapper = create(); await flushPromises(); await newForm(wrapper, 'daily')
    api.list.mockRejectedValueOnce({ message: 'Scope unavailable' })
    await wrapper.get('[data-testid="schedule-refresh"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Scope unavailable')
    expect(wrapper.get('[data-testid="schedule-save"]').attributes('disabled')).toBeDefined()
    await submit(wrapper)
    expect(api.create).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="schedule-refresh"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="schedule-save"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('edits and disables an absolute one-time schedule without moving its wall-clock time', async () => {
    const wrapper = create(); await flushPromises()
    await wrapper.get('[data-testid="schedule-edit-1"]').trigger('click')
    expect((wrapper.get('[data-testid="schedule-run-at"]').element as HTMLInputElement).value).toBe('2026-10-10T08:30')
    await wrapper.get('[data-testid="schedule-enabled"]').setValue(false)
    await submit(wrapper)
    expect(api.update).toHaveBeenCalledWith(27, 1, { action: 'reset_card', frequency: 'once', timezone: 'Asia/Shanghai', run_at: '2026-10-10T08:30', enabled: false })
    wrapper.unmount()
  })

  it('refreshes the target groups and requires saving before re-enabling a disabled subscription task', async () => {
    api.list.mockResolvedValueOnce(view()).mockResolvedValue({ ...view(), groups: [{ id: 9, name: 'New scope' }] })
    const wrapper = create(); await flushPromises()
    await wrapper.get('[data-testid="schedule-toggle-2"]').trigger('click'); await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="schedule-target-groups"]').text()).toContain('New scope (#9)')
    expect((wrapper.get('[data-testid="schedule-enabled"]').element as HTMLInputElement).checked).toBe(true)
    await submit(wrapper)
    expect(api.update).toHaveBeenCalledWith(27, 2, { action: 'reset_subscriptions', frequency: 'weekly', timezone: 'Asia/Shanghai', time_of_day: '10:00', weekday: 1, enabled: true })
    wrapper.unmount()
  })

  it('requires explicit deletion confirmation and emits updated only after success', async () => {
    const wrapper = create(); await flushPromises()
    await wrapper.get('[data-testid="schedule-delete-1"]').trigger('click')
    expect(api.remove).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="schedule-confirm-delete-1"]').trigger('click'); await flushPromises()
    expect(api.remove).toHaveBeenCalledWith(27, 1)
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

  it('shows recent run times, results and reset count as escaped text', async () => {
    api.runs.mockResolvedValue([run({ message: '<img src=x onerror=alert(1)>' })])
    const wrapper = create(); await flushPromises()
    await wrapper.get('[data-testid="schedule-history-2"]').trigger('click'); await flushPromises()
    expect(api.runs).toHaveBeenCalledWith(27, 2, 20)
    const history = wrapper.get('[data-testid="schedule-runs-2"]')
    expect(history.text()).toContain('重置订阅数：5')
    expect(history.text()).toContain('开始时间')
    expect(history.text()).toContain('<img src=x onerror=alert(1)>')
    expect(history.find('img').exists()).toBe(false)
    wrapper.unmount()
  })

  it('labels the actual run action after schedule edits and translates started status', async () => {
    api.runs.mockResolvedValue([run({ schedule_id: 1, status: 'started', finished_at: null }), run({ id: 11, schedule_id: 1, action: 'reset_card', reset_count: 0 })])
    const wrapper = create(); await flushPromises()
    await wrapper.get('[data-testid="schedule-history-1"]').trigger('click'); await flushPromises()
    const history = wrapper.get('[data-testid="schedule-runs-1"]')
    expect(history.text()).toContain('执行中')
    expect(history.text()).not.toContain('started')
    expect(history.text()).toContain('执行操作：重置关联订阅日、周用量')
    expect(history.text()).toContain('执行操作：使用重置卡')
    expect(history.text()).toContain('重置订阅数：5')
    expect(history.text()).not.toContain('重置订阅数：0')
    wrapper.unmount()
  })

  it('shows run errors and discards stale results when expanding another task', async () => {
    const pending = deferred<AccountActionScheduleRun[]>()
    api.runs.mockReturnValueOnce(pending.promise).mockRejectedValueOnce({ message: 'History unavailable' })
    const wrapper = create(); await flushPromises()
    await wrapper.get('[data-testid="schedule-history-1"]').trigger('click')
    await wrapper.get('[data-testid="schedule-history-2"]').trigger('click'); await flushPromises()
    pending.resolve([run({ message: 'Old task history' })]); await flushPromises()
    expect(wrapper.get('[data-testid="schedule-runs-2"]').text()).toContain('History unavailable')
    expect(wrapper.text()).not.toContain('Old task history')
    wrapper.unmount()
  })

  it('retains the form after a save error and blocks duplicate submits while a request is pending', async () => {
    const pending = deferred<AccountActionSchedule>(); api.create.mockReturnValue(pending.promise)
    const wrapper = create(); await flushPromises(); await newForm(wrapper, 'daily')
    await wrapper.get('[data-testid="schedule-form"]').trigger('submit')
    await wrapper.get('[data-testid="schedule-form"]').trigger('submit')
    expect(api.create).toHaveBeenCalledTimes(1)
    pending.reject({ message: 'Upstream account unavailable' }); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Upstream account unavailable')
    expect(wrapper.find('[data-testid="schedule-form"]').exists()).toBe(true)
    expect(wrapper.emitted('updated')).toBeUndefined()
    wrapper.unmount()
  })

  it('discards a previous account load after switching accounts', async () => {
    const pending = deferred<AccountActionScheduleList>()
    api.list.mockReturnValueOnce(pending.promise).mockResolvedValue({ ...view([]), groups: [{ id: 8, name: 'Account 28 scope' }] })
    const wrapper = create()
    await wrapper.setProps({ account: { ...account, id: 28 } }); await flushPromises()
    pending.resolve(view()); await flushPromises()
    expect(wrapper.text()).toContain('Account 28 scope')
    expect(wrapper.text()).not.toContain('Echo group')
    expect(wrapper.find('[data-testid="schedule-1"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not apply an old mutation response to a newly selected account', async () => {
    const pending = deferred<AccountActionSchedule>(); api.create.mockReturnValue(pending.promise)
    const wrapper = create(); await flushPromises(); await newForm(wrapper, 'daily')
    await wrapper.get('[data-testid="schedule-form"]').trigger('submit')
    await wrapper.setProps({ account: { ...account, id: 28 } }); await flushPromises(); await newForm(wrapper, 'weekly')
    pending.resolve(schedule()); await flushPromises()
    expect(wrapper.emitted('updated')).toBeUndefined()
    expect(wrapper.find('[data-testid="schedule-form"]').exists()).toBe(true)
    expect((wrapper.get('[data-testid="schedule-frequency"]').element as HTMLSelectElement).value).toBe('weekly')
    wrapper.unmount()
  })
})
