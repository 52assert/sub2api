import { describe, expect, it } from 'vitest'
import type { Account, ClaudeModel } from '@/types'
import { intelligenceTestTextModels } from '../intelligenceTestModels'

const models = (ids: string[]) => ids.map((id) => ({ id, display_name: id })) as ClaudeModel[]
const account = (mapping?: Record<string, string>) => ({ credentials: { model_mapping: mapping } }) as Account

describe('intelligence test text model selection', () => {
  it('excludes image, video, audio, and embedding models from the discovered picker', () => {
    const available = models(['gpt-image-2', 'gemini-3.1-flash-image', 'grok-imagine-video', 'grok-imagine', 'tts-1', 'whisper-1', 'text-embedding-3', 'gemini-3-pro', 'gpt-5.4'])
    expect(intelligenceTestTextModels(available, account()).map((model) => model.id)).toEqual(['gemini-3-pro', 'gpt-5.4'])
  })

  it('filters by the actual mapped target while preserving media-like aliases for text models', () => {
    const available = models(['gpt-image-alias', 'text-alias', 'custom-model'])
    const mapping = { 'gpt-image-alias': 'gpt-5.4', 'text-alias': 'gpt-image-2' }
    expect(intelligenceTestTextModels(available, account(mapping)).map((model) => model.id)).toEqual(['gpt-image-alias', 'custom-model'])
  })

  it('uses exact mapping first and then the longest matching wildcard', () => {
    const available = models(['custom-text', 'custom-other', 'custom-image'])
    const mapping = { '*': 'gpt-image-2', 'custom-*': 'gpt-5.4', 'custom-image*': 'gemini-3.1-flash-image', 'custom-image': 'claude-sonnet-4' }
    expect(intelligenceTestTextModels(available, account(mapping)).map((model) => model.id)).toEqual(['custom-text', 'custom-other', 'custom-image'])
  })
})
