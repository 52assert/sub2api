export interface ResetVoteFailureLike {
  status?: string
  card_status?: string
  subscription_reset_status?: string
  error_code?: string
  error_message?: string
}

export type ResetVoteTranslateFn = (key: string, params?: Record<string, unknown>) => string

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
