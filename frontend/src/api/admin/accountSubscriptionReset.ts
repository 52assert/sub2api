import { apiClient } from '../client'

export interface SubscriptionResetPreview {
  enabled: boolean
  status: string
  fingerprint: string
  last_checked_at: string | null
  last_event_at: string | null
  poll_error: string
  subscriptions: Array<{
    id: number
    user_id: number
    group_id: number
    group_name: string
    daily_usage_usd: number
    weekly_usage_usd: number
  }>
}
export async function getSubscriptionResetPreview(id: number): Promise<SubscriptionResetPreview> {
  return (await apiClient.get<SubscriptionResetPreview>(`/admin/accounts/${id}/subscription-reset`)).data
}
export async function configureSubscriptionReset(id: number, enabled: boolean): Promise<void> {
  await apiClient.put(`/admin/accounts/${id}/subscription-reset`, { enabled })
}
export async function resetAccountGroupSubscriptions(id: number, operationID: string, fingerprint: string): Promise<number> {
  const { data } = await apiClient.post<{ reset_count: number }>(`/admin/accounts/${id}/subscription-reset`, {
    operation_id: operationID, fingerprint
  })
  return data.reset_count
}
