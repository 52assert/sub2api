import type { Account, ClaudeModel } from '@/types'

function mappedModel(account: Account, requested: string): string {
  const mapping = account.credentials?.model_mapping
  if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) return requested
  const entries = Object.entries(mapping).filter((entry): entry is [string, string] => typeof entry[1] === 'string')
  const exact = entries.find(([pattern]) => pattern === requested)
  if (exact) return exact[1]
  const wildcards = entries.filter(([pattern]) => pattern.endsWith('*') && requested.startsWith(pattern.slice(0, -1)))
  wildcards.sort(([left], [right]) => right.length - left.length || left.localeCompare(right))
  return wildcards[0]?.[1] ?? requested
}

function isMediaModel(value: string): boolean {
  const model = value.trim().toLowerCase().replace(/^models\//, '')
  return /^(?:gpt-image-|dall-e-|grok-video|grok-imagine-video|grok-imagine-image|veo-)/.test(model)
    || ['grok-imagine', 'grok-imagine-edit'].includes(model)
    || /^gemini-(?:3\.1-flash|3-pro|2\.5-flash)-image(?:-|$)/.test(model)
    || ['audio', 'realtime', 'voice', 'embedding', 'tts', 'whisper', 'imagen'].some((part) => model.includes(part))
}

export function intelligenceTestTextModels(models: ClaudeModel[], account: Account): ClaudeModel[] {
  return models.filter((model) => !isMediaModel(mappedModel(account, model.id)))
}
