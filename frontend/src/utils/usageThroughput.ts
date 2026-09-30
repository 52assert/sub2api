import { resolveUsageRequestType, type UsageRequestTypeLike } from '@/utils/usageRequestType'

interface UsageThroughputRow extends UsageRequestTypeLike {
  output_tokens?: number | null
  duration_ms?: number | null
  first_token_ms?: number | null
  image_count?: number | null
  image_output_tokens?: number | null
  billing_mode?: string | null
}

const isFiniteNumber = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value)

/** Estimate output throughput from the stored usage and timing data. */
export function calculateUsageTokensPerSecond(row: UsageThroughputRow): number | null {
  const { output_tokens: tokens, duration_ms: duration, first_token_ms: firstToken } = row
  if (!isFiniteNumber(tokens) || tokens < 0 || !isFiniteNumber(duration) || duration <= 0) {
    return null
  }

  // Image/video generation and real-time or blocked requests are not text-generation benchmarks.
  const requestType = resolveUsageRequestType(row)
  if ((row.image_count ?? 0) > 0 || (row.image_output_tokens ?? 0) > 0
    || row.billing_mode === 'image' || row.billing_mode === 'video'
    || requestType === 'cyber' || requestType === 'live') {
    return null
  }

  let generationMs = duration
  const streaming = requestType === 'stream' || requestType === 'ws_v2'
    || (requestType === 'unknown' && (row.stream || row.openai_ws_mode))
  if (streaming) {
    if (!isFiniteNumber(firstToken) || firstToken < 0 || firstToken >= duration) {
      return null
    }
    generationMs -= firstToken
  }

  // Sync responses have no separate generation timing, so use their total duration.
  const throughput = (tokens / generationMs) * 1000
  return Number.isFinite(throughput) ? throughput : null
}

export function formatUsageTokensPerSecond(row: UsageThroughputRow, emptyValue = '-'): string {
  const throughput = calculateUsageTokensPerSecond(row)
  return throughput == null ? emptyValue : throughput.toFixed(1)
}
