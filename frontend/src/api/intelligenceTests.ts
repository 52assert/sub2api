import { apiClient } from './client'

export type IntelligenceTestStatus = 'queued' | 'running' | 'succeeded' | 'failed'
export type IntelligenceTestEffort = 'default' | 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'

export interface IntelligenceTestRecord {
  id: number
  account_name: string
  platform: string
  model: string
  reasoning_effort: IntelligenceTestEffort
  prompt: string
  status: IntelligenceTestStatus
  created_at: string
  started_at?: string
  completed_at?: string
  duration_ms: number
  output?: string
  error?: string
}

export interface CreateIntelligenceTestRequest {
  model: string
  reasoning_effort: IntelligenceTestEffort
  prompt: string
}

export interface IntelligenceTestList {
  items: IntelligenceTestRecord[]
  retention: number
}

export const DEFAULT_INTELLIGENCE_TEST_PROMPT = '生成 html，内容是 svg 绘制鹈鹕骑自行车 2D 动画，不用进行测试'

export function isIntelligenceTestActive(record: IntelligenceTestRecord): boolean {
  return record.status === 'queued' || record.status === 'running'
}

export async function createIntelligenceTest(accountId: number, request: CreateIntelligenceTestRequest): Promise<IntelligenceTestRecord> {
  const { data } = await apiClient.post<IntelligenceTestRecord>(`/admin/accounts/${accountId}/intelligence-tests`, request)
  return data
}

export async function listIntelligenceTests(options?: { signal?: AbortSignal }): Promise<IntelligenceTestList> {
  const { data } = await apiClient.get<IntelligenceTestList>('/intelligence-tests', { signal: options?.signal })
  return data
}

export async function getIntelligenceTest(id: number, options?: { signal?: AbortSignal }): Promise<IntelligenceTestRecord> {
  const { data } = await apiClient.get<IntelligenceTestRecord>(`/intelligence-tests/${id}`, { signal: options?.signal })
  return data
}
