import { formatDateTimeLocalInput, parseDateTimeLocalInput } from './format'

export const MAX_SUBSCRIPTION_EXPIRATION = Date.parse('2099-12-31T23:59:59Z')
const DAY_MS = 24 * 60 * 60 * 1000

export function subscriptionAdjustmentBase(expiresAt: string | null | undefined, now: number): number {
  const expiration = expiresAt ? Date.parse(expiresAt) : NaN
  return Number.isFinite(expiration) ? Math.max(expiration, now) : now
}

export function subscriptionExpirationAfterDays(expiresAt: string | null | undefined, days: number, now: number): number {
  return Math.min(subscriptionAdjustmentBase(expiresAt, now) + days * DAY_MS, MAX_SUBSCRIPTION_EXPIRATION)
}

export function formatSubscriptionExpirationInput(timestamp: number): string {
  const date = new Date(timestamp)
  if (!Number.isFinite(date.getTime())) return ''
  const seconds = String(date.getSeconds()).padStart(2, '0')
  const milliseconds = String(date.getMilliseconds()).padStart(3, '0')
  return `${formatDateTimeLocalInput(timestamp / 1000)}:${seconds}.${milliseconds}`
}

export function parseSubscriptionExpirationInput(value: string): number | null {
  const seconds = parseDateTimeLocalInput(value)
  if (seconds === null || formatDateTimeLocalInput(seconds) !== value.slice(0, 16)) return null
  const fraction = value.match(/\.(\d+)$/)?.[1]
  const milliseconds = fraction ? Number(fraction.slice(0, 3).padEnd(3, '0')) : 0
  return seconds * 1000 + milliseconds
}
