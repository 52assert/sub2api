import { apiClient } from '../client'

export interface UserAgentLimitSettings {
  enabled: boolean
  max_user_agents: number
  window_days: number
  exempt_admin: boolean
}

export interface UserAgentLimitUserSummary {
  user_id: number
  enabled: boolean
  active_count: number
  effective_limit: number
  global_limit: number
  custom_limit: number | null
  exempt: boolean
  at_limit: boolean
  exceeded: boolean
  window_days: number
  note: string
}

export interface UserAgentLimitUserPolicy {
  max_user_agents: number | null
  note: string
  updated_at?: string
}

export interface UserAgentLimitRecord {
  id: number
  user_id: number
  ua_key: string
  ua_label: string
  last_raw_ua: string
  first_seen_at: string
  last_seen_at: string
  request_count: number
}

export interface UserAgentLimitUserDetail {
  user_id: number
  user_email: string
  username: string
  user_role: string
  settings: UserAgentLimitSettings
  policy: UserAgentLimitUserPolicy
  summary: UserAgentLimitUserSummary
  records: UserAgentLimitRecord[]
}

export interface BatchUserAgentSummariesResponse {
  settings: UserAgentLimitSettings
  summaries: Record<number, UserAgentLimitUserSummary>
}

export async function getUserAgentLimitSettings(): Promise<UserAgentLimitSettings> {
  const { data } = await apiClient.get<UserAgentLimitSettings>('/admin/settings/user-agent-limit')
  return data
}

export async function updateUserAgentLimitSettings(
  payload: UserAgentLimitSettings
): Promise<UserAgentLimitSettings> {
  const { data } = await apiClient.put<UserAgentLimitSettings>(
    '/admin/settings/user-agent-limit',
    payload
  )
  return data
}

export async function batchGetUserAgentSummaries(
  userIds: number[]
): Promise<BatchUserAgentSummariesResponse> {
  const { data } = await apiClient.post<BatchUserAgentSummariesResponse>(
    '/admin/users/user-agent-summaries',
    { user_ids: userIds }
  )
  return data
}

export async function getUserAgentLimitDetail(userId: number): Promise<UserAgentLimitUserDetail> {
  const { data } = await apiClient.get<UserAgentLimitUserDetail>(
    `/admin/users/${userId}/user-agents`
  )
  return data
}

export async function updateUserAgentLimitPolicy(
  userId: number,
  payload: { max_user_agents: number | null; note?: string }
): Promise<UserAgentLimitUserDetail> {
  const { data } = await apiClient.put<UserAgentLimitUserDetail>(
    `/admin/users/${userId}/user-agents/policy`,
    payload
  )
  return data
}

export async function deleteUserAgentRecord(
  userId: number,
  recordId: number
): Promise<{ deleted: boolean }> {
  const { data } = await apiClient.delete<{ deleted: boolean }>(
    `/admin/users/${userId}/user-agents/${recordId}`
  )
  return data
}

export async function clearUserAgentRecords(
  userId: number
): Promise<{ deleted_count: number }> {
  const { data } = await apiClient.delete<{ deleted_count: number }>(
    `/admin/users/${userId}/user-agents`
  )
  return data
}
