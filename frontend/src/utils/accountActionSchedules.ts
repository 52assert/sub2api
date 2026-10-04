import type { AccountActionSchedule, AccountActionScheduleInput, AccountScheduleAction, AccountScheduleFrequency } from '@/api/admin/accountActionSchedules'

export interface AccountScheduleForm {
  action: AccountScheduleAction
  frequency: AccountScheduleFrequency
  timezone: string
  run_at: string
  time_of_day: string
  cron_expression: string
  weekday: number | null
  enabled: boolean
}

const localDatePattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/
const timePattern = /^(?:[01]\d|2[0-3]):[0-5]\d$/

export function isValidScheduleLocalDate(value: string): boolean {
  const match = localDatePattern.exec(value)
  if (!match) return false
  const [, year, month, day, hour, minute] = match.map(Number)
  const date = new Date(Date.UTC(year!, month! - 1, day!, hour!, minute!))
  return year! >= 1000 && date.getUTCFullYear() === year && date.getUTCMonth() === month! - 1 && date.getUTCDate() === day && date.getUTCHours() === hour && date.getUTCMinutes() === minute
}

export function accountScheduleDateInput(value: string | null | undefined, timezone: string): string {
  if (!value) return ''
  // A server-local value has no offset; preserve its wall-clock components.
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?$/.test(value)) {
    return isValidScheduleLocalDate(value.slice(0, 16)) ? value.slice(0, 16) : ''
  }
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return ''
  try {
    const parts = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(date)
    const part = (type: Intl.DateTimeFormatPartTypes) => parts.find(item => item.type === type)?.value || ''
    return `${part('year')}-${part('month')}-${part('day')}T${part('hour')}:${part('minute')}`
  } catch { return '' }
}

export function accountScheduleForm(schedule: AccountActionSchedule): AccountScheduleForm {
  return {
    action: schedule.action, frequency: schedule.frequency, timezone: schedule.timezone,
    run_at: accountScheduleDateInput(schedule.run_at, schedule.timezone),
    time_of_day: schedule.time_of_day || '', cron_expression: schedule.cron_expression || '', weekday: schedule.weekday ?? null, enabled: schedule.enabled
  }
}

export function accountScheduleValidation(form: AccountScheduleForm): string | null {
  if (!form.timezone.trim()) return 'timezoneRequired'
  try { new Intl.DateTimeFormat('en', { timeZone: form.timezone.trim() }).format() }
  catch { return 'invalidTimezone' }
  if (form.frequency === 'cron') {
    if (!form.cron_expression.trim()) return 'cronRequired'
    if (form.cron_expression.trim().split(/\s+/).length !== 5) return 'invalidCron'
  } else if (form.frequency === 'once') {
    if (!form.run_at) return 'runAtRequired'
    if (!isValidScheduleLocalDate(form.run_at)) return 'invalidRunAt'
  } else {
    if (!form.time_of_day) return 'timeRequired'
    if (!timePattern.test(form.time_of_day)) return 'invalidTime'
    if (form.frequency === 'weekly' && (form.weekday === null || !Number.isInteger(form.weekday) || form.weekday < 0 || form.weekday > 6)) return 'weekdayRequired'
  }
  return null
}

export function accountScheduleInput(form: AccountScheduleForm): AccountActionScheduleInput {
  const input: AccountActionScheduleInput = { action: form.action, frequency: form.frequency, timezone: form.timezone.trim(), enabled: form.enabled }
  if (form.frequency === 'cron') input.cron_expression = form.cron_expression.trim()
  else if (form.frequency === 'once') input.run_at = form.run_at
  else {
    input.time_of_day = form.time_of_day
    if (form.frequency === 'weekly' && form.weekday !== null) input.weekday = form.weekday
  }
  return input
}

export function formatAccountScheduleTime(value: string | null | undefined, timezone: string, locale: string): string {
  if (!value) return '—'
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value)) return value.slice(0, 16).replace('T', ' ')
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  try { return new Intl.DateTimeFormat(locale, { timeZone: timezone, dateStyle: 'medium', timeStyle: 'short' }).format(date) }
  catch { return value }
}
