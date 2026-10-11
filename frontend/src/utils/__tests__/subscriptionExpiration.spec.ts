import { describe, expect, it } from 'vitest'
import {
  MAX_SUBSCRIPTION_EXPIRATION,
  formatSubscriptionExpirationInput,
  parseSubscriptionExpirationInput,
  subscriptionExpirationAfterDays
} from '../subscriptionExpiration'

describe('subscription expiration dates', () => {
  it('round-trips local date input without dropping seconds or milliseconds', () => {
    const timestamp = new Date(2026, 10, 20, 18, 24, 10, 125).getTime()
    expect(formatSubscriptionExpirationInput(timestamp)).toBe('2026-11-20T18:24:10.125')
    expect(parseSubscriptionExpirationInput('2026-11-20T18:24:10.125')).toBe(timestamp)
    expect(parseSubscriptionExpirationInput('2026-11-20T18:24')).toBe(new Date(2026, 10, 20, 18, 24).getTime())
  })

  it.each(['', '2026-02-30T18:24', '2026-11-20T24:00', '2026-11-20T18:24Z', '2026-11-20T18:24+08:00'])('rejects invalid or timezone-bearing local input %j', (input) => {
    expect(parseSubscriptionExpirationInput(input)).toBeNull()
  })

  it('extends valid subscriptions from their expiration and expired subscriptions from now', () => {
    const now = Date.parse('2026-10-11T10:30:20.123Z')
    const future = '2026-11-20T18:24:10.125+08:00'
    expect(subscriptionExpirationAfterDays(future, 30, now)).toBe(Date.parse(future) + 30 * 86400000)
    expect(subscriptionExpirationAfterDays('2025-01-01T00:00:00Z', 30, now)).toBe(now + 30 * 86400000)
    expect(subscriptionExpirationAfterDays(null, 7, now)).toBe(now + 7 * 86400000)
  })

  it('caps renewal shortcuts at the server maximum', () => {
    expect(subscriptionExpirationAfterDays('2099-12-20T00:00:00Z', 30, Date.now())).toBe(MAX_SUBSCRIPTION_EXPIRATION)
  })
})
