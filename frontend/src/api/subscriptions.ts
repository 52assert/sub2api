/**
 * User Subscription API
 * API for regular users to view their own subscriptions and progress
 */

import { apiClient } from './client'
import type {
  Account,
  AccountUsageInfo,
  PaginatedResponse,
  SubscriptionProgress,
  UserSubscription,
  WindowStats
} from '@/types'

/**
 * Subscription summary for user dashboard
 */
export interface SubscriptionSummary {
  active_count: number
  subscriptions: Array<{
    id: number
    group_name: string
    status: string
    daily_progress: number | null
    weekly_progress: number | null
    monthly_progress: number | null
    expires_at: string | null
    days_remaining: number | null
  }>
}

/**
 * Get list of current user's subscriptions
 */
export async function getMySubscriptions(): Promise<UserSubscription[]> {
  const response = await apiClient.get<UserSubscription[]>('/subscriptions')
  return response.data
}

/**
 * Get current user's active subscriptions
 */
export async function getActiveSubscriptions(): Promise<UserSubscription[]> {
  const response = await apiClient.get<UserSubscription[]>('/subscriptions/active')
  return response.data
}

/**
 * Get progress for all user's active subscriptions
 */
export async function getSubscriptionsProgress(): Promise<SubscriptionProgress[]> {
  const response = await apiClient.get<SubscriptionProgress[]>('/subscriptions/progress')
  return response.data
}

/**
 * Get subscription summary for dashboard display
 */
export async function getSubscriptionSummary(): Promise<SubscriptionSummary> {
  const response = await apiClient.get<SubscriptionSummary>('/subscriptions/summary')
  return response.data
}

/**
 * Get progress for a specific subscription
 */
export async function getSubscriptionProgress(
  subscriptionId: number
): Promise<SubscriptionProgress> {
  const response = await apiClient.get<SubscriptionProgress>(
    `/subscriptions/${subscriptionId}/progress`
  )
  return response.data
}

export interface SubscriberResetVoteState {
  supported: boolean
  eligible: boolean
  can_vote: boolean
  has_voted: boolean
  ineligible_reason?: 'quota_group_only' | 'not_subscribed' | 'unsupported_account' | string
  round_id?: number
  status: 'none' | 'open' | 'executing' | 'succeeded' | 'failed' | string
  vote_count: number
  threshold: number
  initiated_at?: string
  expires_at?: string
  triggered_at?: string
  completed_at?: string
  card_status?: string
  subscription_reset_status?: string
  subscription_reset_count?: number
  cooldown_active: boolean
  can_initiate: boolean
  error_code?: string
  error_message?: string
  warning_code?: string
}

export interface SubscriberResetVoteCastResult {
  state: SubscriberResetVoteState
  vote_recorded: boolean
  triggered_reset: boolean
  reset_succeeded: boolean
}

export type SubscriberAccountItem = Account & {
  reset_vote?: SubscriberResetVoteState
}

export interface SubscriberAccountListFilters {
  platform?: string
  type?: string
  status?: string
  group?: string
  search?: string
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

export async function listSubscriberAccounts(
  page: number = 1,
  pageSize: number = 20,
  filters?: SubscriberAccountListFilters,
  options?: { signal?: AbortSignal }
): Promise<PaginatedResponse<SubscriberAccountItem>> {
  const { data } = await apiClient.get<PaginatedResponse<SubscriberAccountItem>>('/subscriptions/accounts', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

export async function getSubscriberAccountsBatchTodayStats(
  accountIds: number[]
): Promise<{ stats: Record<string, WindowStats> }> {
  const { data } = await apiClient.post<{ stats: Record<string, WindowStats> }>(
    '/subscriptions/accounts/today-stats/batch',
    { account_ids: accountIds }
  )
  return data
}

export async function getSubscriberAccountsBatchUsage(
  accountIds: number[]
): Promise<{
  usage: Record<string, AccountUsageInfo | null>
  errors?: Record<string, string>
}> {
  const { data } = await apiClient.post<{
    usage: Record<string, AccountUsageInfo | null>
    errors?: Record<string, string>
  }>('/subscriptions/accounts/usage/batch', {
    account_ids: accountIds,
    force: false
  })
  return data
}

export async function castSubscriberAccountResetVote(
  accountId: number
): Promise<SubscriberResetVoteCastResult> {
  const { data } = await apiClient.post<SubscriberResetVoteCastResult>(
    `/subscriptions/accounts/${accountId}/reset-votes`
  )
  return data
}

export interface SubscriberAccountResetCreditsResult {
  rate_limit_reset_credits?: {
    available_count: number
    credits?: { expires_at?: string }[]
  } | null
  fetched_at: number
  cache_persisted: boolean
}

export async function refreshSubscriberAccountResetCredits(
  accountId: number
): Promise<SubscriberAccountResetCreditsResult> {
  const { data } = await apiClient.post<SubscriberAccountResetCreditsResult>(
    `/subscriptions/accounts/${accountId}/reset-credits/refresh`
  )
  return data
}

export default {
  getMySubscriptions,
  getActiveSubscriptions,
  getSubscriptionsProgress,
  getSubscriptionSummary,
  getSubscriptionProgress,
  listSubscriberAccounts,
  getSubscriberAccountsBatchTodayStats,
  getSubscriberAccountsBatchUsage,
  castSubscriberAccountResetVote,
  refreshSubscriberAccountResetCredits
}
