import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountIntelligenceTestModal from '../AccountIntelligenceTestModal.vue'
import { DEFAULT_INTELLIGENCE_TEST_PROMPT } from '@/api/intelligenceTests'
import type { Account } from '@/types'

const { getAvailableModels, createIntelligenceTest, showSuccess } = vi.hoisted(() => ({ getAvailableModels: vi.fn(), createIntelligenceTest: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels } } }))
vi.mock('@/api/intelligenceTests', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api/intelligenceTests')>(), createIntelligenceTest }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))

const account = { id: 42, name: 'Private upstream account', platform: 'openai', type: 'oauth' } as Account
const models = [{ id: 'gpt-test', display_name: 'GPT Test' }, { id: 'gpt-custom', display_name: 'Custom' }]
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolver) => { resolve = resolver })
  return { promise, resolve }
}
function mountModal() {
  return mount(AccountIntelligenceTestModal, {
    props: { show: true, account },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        Select: {
          props: ['modelValue', 'options', 'disabled'], emits: ['update:modelValue'],
          template: '<select :value="modelValue" :disabled="disabled" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>'
        }
      }
    }
  })
}

describe('AccountIntelligenceTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockReset().mockResolvedValue(models)
    createIntelligenceTest.mockReset().mockResolvedValue({ id: 9, status: 'queued' })
    showSuccess.mockReset()
  })
  afterEach(() => { vi.restoreAllMocks() })

  it('submits the selected discovered model, effort, and editable prompt then closes after background acceptance', async () => {
    const accepted = deferred<{ id: number; status: string }>()
    createIntelligenceTest.mockReturnValue(accepted.promise)
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get('textarea').element.value).toBe(DEFAULT_INTELLIGENCE_TEST_PROMPT)
    await wrapper.get('[data-testid="intelligence-test-model"]').setValue('gpt-custom')
    await wrapper.get('[data-testid="intelligence-test-effort"]').setValue('xhigh')
    await wrapper.get('textarea').setValue('  generate an SVG animation  ')
    await wrapper.get('form').trigger('submit')
    expect(createIntelligenceTest).toHaveBeenCalledWith(42, { model: 'gpt-custom', reasoning_effort: 'xhigh', prompt: 'generate an SVG animation' })
    expect(wrapper.get('[data-testid="intelligence-test-submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('close')).toBeUndefined()
    accepted.resolve({ id: 9, status: 'queued' })
    await flushPromises()
    expect(wrapper.emitted('created')?.[0]).toEqual([{ id: 9, status: 'queued' }])
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(showSuccess).toHaveBeenCalledWith('intelligenceTests.submitted', 6000)
    wrapper.unmount()
  })

  it('prevents submitting an account without models and allows retry after discovery fails', async () => {
    getAvailableModels.mockRejectedValueOnce(new Error('Model discovery unavailable'))
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Model discovery unavailable')
    expect(wrapper.get('[data-testid="intelligence-test-submit"]').attributes('disabled')).toBeDefined()
    getAvailableModels.mockResolvedValueOnce([])
    await wrapper.findAll('button').find((button) => button.text() === 'common.tryAgain')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('intelligenceTests.noModels')
    await wrapper.get('form').trigger('submit')
    expect(createIntelligenceTest).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('ignores stale model discovery when a different account is opened', async () => {
    const stale = deferred<typeof models>()
    getAvailableModels.mockReturnValueOnce(stale.promise).mockResolvedValueOnce([{ id: 'claude-custom', display_name: 'Claude' }])
    const wrapper = mountModal()
    await wrapper.setProps({ account: { ...account, id: 43, platform: 'anthropic' } })
    await flushPromises()
    stale.resolve(models)
    await flushPromises()
    expect(wrapper.get('select[data-testid="intelligence-test-model"]').element.value).toBe('claude-custom')
    expect(wrapper.get('select[data-testid="intelligence-test-model"]').text()).not.toContain('GPT Test')
    wrapper.unmount()
  })

  it('keeps the form open and reports submission failure for retry', async () => {
    createIntelligenceTest.mockRejectedValueOnce(new Error('Capacity reached'))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Capacity reached')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.get('[data-testid="intelligence-test-submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
