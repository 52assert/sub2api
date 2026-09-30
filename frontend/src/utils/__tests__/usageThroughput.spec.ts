import { describe, expect, it } from 'vitest'
import { calculateUsageTokensPerSecond, formatUsageTokensPerSecond } from '../usageThroughput'

const streamRow = {
  request_type: 'stream',
  stream: true,
  output_tokens: 200,
  duration_ms: 10_000,
  first_token_ms: 2_000,
  image_count: 0,
  image_output_tokens: 0,
  billing_mode: 'token',
}

describe('usage output throughput', () => {
  it('excludes first-token latency for stream and WS turns, including legacy records', () => {
    for (const transport of [
      { request_type: 'stream', stream: true },
      { request_type: 'ws_v2', stream: false },
      { request_type: undefined, stream: true },
      { request_type: undefined, stream: false, openai_ws_mode: true },
      { request_type: 'unknown', stream: true },
    ]) {
      const row = { ...streamRow, ...transport, input_tokens: 100_000, cache_read_tokens: 90_000 }
      expect(calculateUsageTokensPerSecond(row)).toBe(25)
      expect(formatUsageTokensPerSecond(row)).toBe('25.0')
    }
  })

  it('uses the full duration for sync responses even if a first-token value was stored', () => {
    expect(calculateUsageTokensPerSecond({ ...streamRow, request_type: 'sync' })).toBe(20)
    expect(calculateUsageTokensPerSecond({
      ...streamRow, request_type: undefined, stream: false, first_token_ms: null,
    })).toBe(20)
  })

  it('distinguishes measured zero output from unavailable speed and rounds only for display', () => {
    expect(formatUsageTokensPerSecond({ ...streamRow, output_tokens: 0 })).toBe('0.0')
    expect(formatUsageTokensPerSecond({ ...streamRow, output_tokens: 154, first_token_ms: 0 })).toBe('15.4')
    expect(calculateUsageTokensPerSecond({ ...streamRow, output_tokens: 1 })).toBe(0.125)
    expect(formatUsageTokensPerSecond({ ...streamRow, output_tokens: 1 })).toBe('0.1')
  })

  it.each([
    { duration_ms: null },
    { duration_ms: undefined },
    { duration_ms: 0 },
    { duration_ms: -1 },
    { duration_ms: NaN },
    { duration_ms: Infinity },
    { output_tokens: null },
    { output_tokens: -1 },
    { output_tokens: NaN },
    { output_tokens: Infinity },
    { first_token_ms: null },
    { first_token_ms: undefined },
    { first_token_ms: -1 },
    { first_token_ms: NaN },
    { first_token_ms: Infinity },
    { first_token_ms: 10_000 },
    { first_token_ms: 10_001 },
  ])('does not invent a speed from invalid or missing measurements: %j', (fields) => {
    const row = { ...streamRow, ...fields }
    expect(calculateUsageTokensPerSecond(row)).toBeNull()
    expect(formatUsageTokensPerSecond(row)).toBe('-')
    expect(formatUsageTokensPerSecond(row, '')).toBe('')
  })

  it.each([
    { image_count: 1 },
    { image_output_tokens: 200 },
    { billing_mode: 'image' },
    { billing_mode: 'video' },
    { request_type: 'live' },
    { request_type: 'cyber' },
  ])('does not present non-text or blocked requests as text throughput: %j', (fields) => {
    expect(calculateUsageTokensPerSecond({ ...streamRow, ...fields })).toBeNull()
  })
})
