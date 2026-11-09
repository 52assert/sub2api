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

export type SubscriberAccountItem = Account

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
): Promise<PaginatedResponse<Account>> {
  const { data } = await apiClient.get<PaginatedResponse<Account>>('/subscriptions/accounts', {
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

export default {
  getMySubscriptions,
  getActiveSubscriptions,
  getSubscriptionsProgress,
  getSubscriptionSummary,
  getSubscriptionProgress,
  listSubscriberAccounts,
  getSubscriberAccountsBatchTodayStats,
  getSubscriberAccountsBatchUsage
}
