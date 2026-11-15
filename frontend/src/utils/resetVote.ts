export interface ResetVoteFailureLike {
  status?: string
  card_status?: string
  subscription_reset_status?: string
  error_code?: string
  error_message?: string
}

export type ResetVoteTranslateFn = (key: string, params?: Record<string, unknown>) => string

/**
 * Extracts unexpired available reset-card count from account.extra.codex_reset_credit_snapshot.
 * Returns `null` when snapshot is absent, malformed, or claims credits whose expirations already passed.
 */
export function readAvailableResetCreditsFromExtra(
  extra: Record<string, unknown> | undefined,
  nowMs = Date.now()
): number | null {
  const cached = extra?.codex_reset_credit_snapshot
  if (!cached || typeof cached !== 'object' || Array.isArray(cached)) return null

  const { available_count: count, credits: rawCredits } = cached as {
    available_count?: unknown
    credits?: unknown
  }
  if (typeof count !== 'number' || !Number.isFinite(count)) return null
  if (count <= 0) return 0

  let unexpiredCount = 0
  if (Array.isArray(rawCredits)) {
    for (const credit of rawCredits) {
      if (!credit || typeof credit !== 'object') continue
      const expiresAt = (credit as { expires_at?: unknown }).expires_at
      if (typeof expiresAt !== 'string' || expiresAt.trim() === '') continue
      const expiryTime = new Date(expiresAt).getTime()
      if (!Number.isNaN(expiryTime) && expiryTime <= nowMs) continue
      unexpiredCount += 1
    }
  }
  const availableCount = Math.min(Math.max(count, 0), unexpiredCount)
  if (count > 0 && availableCount <= 0) return null
  return availableCount
}

/**
 * Resolves a localized human-readable failure reason from reset-vote round state.
 */
export function resolveResetVoteFailureReason(
  state: ResetVoteFailureLike | null | undefined,
  t: ResetVoteTranslateFn,
  fallbackKey?: string
): string {
  if (!state) {
    return fallbackKey ? t(fallbackKey) : ''
  }
  if (state.card_status === 'no_credit' || state.error_code === 'NO_RESET_CARD') {
    return t('userSubscriptions.resetVote.noCredit')
  }
  if (
    state.card_status === 'unknown' ||
    state.error_code === 'RESET_CARD_UNKNOWN' ||
    state.error_code === 'VOTE_EXECUTION_TIMED_OUT'
  ) {
    return t('userSubscriptions.resetVote.unknownResult')
  }
  if (state.subscription_reset_status === 'failed') {
    return t('userSubscriptions.resetVote.subscriptionResetFailed')
  }
  if (state.error_message) {
    return state.error_message
  }
  return fallbackKey ? t(fallbackKey) : ''
}
