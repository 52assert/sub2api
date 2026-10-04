import { apiClient } from '../client'

export type AccountScheduleAction = 'reset_card' | 'reset_subscriptions'
export type AccountScheduleFrequency = 'once' | 'daily' | 'weekly' | 'cron'

export interface AccountActionSchedule {
  id: number
  account_id: number
  action: AccountScheduleAction
  frequency: AccountScheduleFrequency
  timezone: string
  run_at?: string | null
  time_of_day?: string
  cron_expression?: string
  weekday?: number | null
  enabled: boolean
  next_run_at?: string | null
  last_run_at?: string | null
  last_status?: string
  last_message?: string
  created_at: string
}

export interface AccountActionScheduleInput {
  action: AccountScheduleAction
  frequency: AccountScheduleFrequency
  timezone: string
  run_at?: string
  time_of_day?: string
  cron_expression?: string
  weekday?: number
  enabled: boolean
}

export interface AccountActionScheduleList {
  items: AccountActionSchedule[]
  timezone: string
  groups: Array<{ id: number; name: string }>
}

export interface AccountActionScheduleRun {
  id: number
  schedule_id: number
  account_id: number
  action: AccountScheduleAction
  status: string
  message: string
  scheduled_at: string
  started_at: string
  finished_at?: string | null
  reset_count: number
  warning_code?: string
}

const accountPath = (accountId: number) => `/admin/accounts/${accountId}/action-schedules`

export async function listAccountActionSchedules(accountId: number): Promise<AccountActionScheduleList> {
  return (await apiClient.get<AccountActionScheduleList>(accountPath(accountId))).data
}

export async function createAccountActionSchedule(accountId: number, input: AccountActionScheduleInput): Promise<AccountActionSchedule> {
  return (await apiClient.post<AccountActionSchedule>(accountPath(accountId), input)).data
}

export async function updateAccountActionSchedule(accountId: number, scheduleId: number, input: AccountActionScheduleInput): Promise<AccountActionSchedule> {
  return (await apiClient.put<AccountActionSchedule>(`${accountPath(accountId)}/${scheduleId}`, input)).data
}

export async function deleteAccountActionSchedule(accountId: number, scheduleId: number): Promise<void> {
  await apiClient.delete(`${accountPath(accountId)}/${scheduleId}`)
}

export async function listAccountActionScheduleRuns(accountId: number, scheduleId: number, limit = 20): Promise<AccountActionScheduleRun[]> {
  return (await apiClient.get<AccountActionScheduleRun[]>(`${accountPath(accountId)}/${scheduleId}/runs`, { params: { limit } })).data ?? []
}
