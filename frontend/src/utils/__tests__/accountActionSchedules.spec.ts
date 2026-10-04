import { describe, expect, it } from 'vitest'
import { accountScheduleDateInput, accountScheduleForm, accountScheduleInput, accountScheduleValidation, isValidScheduleLocalDate, type AccountScheduleForm } from '../accountActionSchedules'
import type { AccountActionSchedule } from '@/api/admin/accountActionSchedules'

const form = (overrides: Partial<AccountScheduleForm> = {}): AccountScheduleForm => ({ action: 'reset_card', frequency: 'once', timezone: 'Asia/Shanghai', run_at: '2026-10-10T08:30', time_of_day: '09:45', cron_expression: '0 8 * * *', weekday: 0, enabled: true, ...overrides })

describe('account schedule dates and inputs', () => {
  it('edits an absolute run time in the schedule timezone instead of the browser timezone', () => {
    expect(accountScheduleDateInput('2026-10-10T00:30:00Z', 'Asia/Shanghai')).toBe('2026-10-10T08:30')
    expect(accountScheduleDateInput('2026-07-01T12:00:00Z', 'America/New_York')).toBe('2026-07-01T08:00')
    expect(accountScheduleDateInput('2026-01-01T12:00:00Z', 'America/New_York')).toBe('2026-01-01T07:00')
    expect(accountScheduleDateInput('2026-10-10T08:30', 'America/New_York')).toBe('2026-10-10T08:30')
  })

  it('keeps midnight and rejects impossible calendar dates rather than rolling them forward', () => {
    expect(accountScheduleDateInput('2026-10-10T16:00:00Z', 'Asia/Shanghai')).toBe('2026-10-11T00:00')
    expect(isValidScheduleLocalDate('2026-02-30T10:00')).toBe(false)
    expect(isValidScheduleLocalDate('2028-02-29T10:00')).toBe(true)
    expect(isValidScheduleLocalDate('2026-10-10T24:00')).toBe(false)
    expect(accountScheduleDateInput('bad date', 'Asia/Shanghai')).toBe('')
  })

  it('preserves the instant when disabling a one-time schedule', () => {
    const schedule = { action: 'reset_card', frequency: 'once', timezone: 'Asia/Shanghai', run_at: '2026-10-10T00:30:00Z', enabled: true } as AccountActionSchedule
    expect(accountScheduleInput({ ...accountScheduleForm(schedule), enabled: false })).toEqual({ action: 'reset_card', frequency: 'once', timezone: 'Asia/Shanghai', run_at: '2026-10-10T08:30', enabled: false })
  })

  it('sends only fields relevant to the selected frequency and keeps Sunday zero', () => {
    expect(accountScheduleInput(form({ frequency: 'daily' }))).toEqual({ action: 'reset_card', frequency: 'daily', timezone: 'Asia/Shanghai', time_of_day: '09:45', enabled: true })
    expect(accountScheduleInput(form({ frequency: 'weekly' }))).toEqual({ action: 'reset_card', frequency: 'weekly', timezone: 'Asia/Shanghai', time_of_day: '09:45', weekday: 0, enabled: true })
    expect(accountScheduleInput(form({ frequency: 'cron', cron_expression: '  0 8 * * 1  ' }))).toEqual({ action: 'reset_card', frequency: 'cron', timezone: 'Asia/Shanghai', cron_expression: '0 8 * * 1', enabled: true })
  })

  it('requires a valid timezone, time, weekly day and five-field Cron input', () => {
    expect(accountScheduleValidation(form({ timezone: 'bad/zone' }))).toBe('invalidTimezone')
    expect(accountScheduleValidation(form({ frequency: 'daily', time_of_day: '25:00' }))).toBe('invalidTime')
    expect(accountScheduleValidation(form({ frequency: 'weekly', weekday: null }))).toBe('weekdayRequired')
    expect(accountScheduleValidation(form({ frequency: 'weekly', weekday: 0 }))).toBeNull()
    expect(accountScheduleValidation(form({ frequency: 'cron', cron_expression: '0 0 8 * * *' }))).toBe('invalidCron')
    expect(accountScheduleValidation(form({ frequency: 'cron' }))).toBeNull()
  })
})
