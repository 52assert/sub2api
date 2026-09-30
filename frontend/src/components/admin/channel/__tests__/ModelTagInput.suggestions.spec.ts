import { afterEach, describe, expect, it, vi } from 'vitest'
import { DOMWrapper, enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import ModelTagInput from '../ModelTagInput.vue'
import PricingEntryCard from '../PricingEntryCard.vue'
import Select from '@/components/common/Select.vue'
import channelsAPI, { type ModelDefaultPricing } from '@/api/admin/channels'
import { createDefaultTimePricingForm, type PricingFormEntry } from '../types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/channels', () => ({ default: { getModelDefaultPricing: vi.fn() } }))
enableAutoUnmount(afterEach)
afterEach(() => {
  document.body.innerHTML = ''
  vi.clearAllMocks()
})

const candidates = ['gpt-6-astra', 'gpt-6-sol', 'gpt-image-2']

function mountInput(props: { models?: string[]; suggestions?: string[]; loading?: boolean } = {}) {
  const wrapper = mount(ModelTagInput, {
    attachTo: document.body,
    props: {
      models: [],
      suggestions: candidates,
      ...props,
      'onUpdate:models': models => { void wrapper.setProps({ models }) },
    },
  })
  return wrapper
}

async function openPicker(wrapper: VueWrapper) {
  await wrapper.findComponent(Select).get('button').trigger('click')
  await nextTick()
  return new DOMWrapper(document.body).get('[role="listbox"]')
}

async function chooseModel(wrapper: VueWrapper, model: string) {
  const dropdown = await openPicker(wrapper)
  await dropdown.findAll('[role="option"]').find(option => option.text() === model)!.trigger('click')
  await flushPromises()
}

function mountPricingEntry() {
  const entry: PricingFormEntry = {
    models: [],
    billing_mode: 'token',
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
  }
  const wrapper = mount(PricingEntryCard, {
    attachTo: document.body,
    props: {
      entry,
      platform: 'openai',
      modelSuggestions: candidates,
      hideTokenIntervals: true,
      onUpdate: updated => { void wrapper.setProps({ entry: updated }) },
    },
  })
  return wrapper
}

describe('model suggestions', () => {
  it('shows unique candidates and excludes models already in the entry', async () => {
    const wrapper = mountInput({
      models: ['gpt-6-sol'],
      suggestions: [' gpt-6-astra ', 'gpt-6-astra', '', 'gpt-6-sol', 'gpt-image-2'],
    })
    const dropdown = await openPicker(wrapper)
    expect(dropdown.findAll('[role="option"]').map(option => option.text()))
      .toEqual(['gpt-6-astra', 'gpt-image-2'])
  })

  it('searches, adds several models, and makes removed models selectable again', async () => {
    const wrapper = mountInput()
    const dropdown = await openPicker(wrapper)
    await dropdown.get('input').setValue('ASTRA')
    expect(dropdown.findAll('[role="option"]').map(option => option.text())).toEqual(['gpt-6-astra'])
    await dropdown.get('[role="option"]').trigger('click')
    await flushPromises()
    await chooseModel(wrapper, 'gpt-6-sol')
    expect(wrapper.props('models')).toEqual(['gpt-6-astra', 'gpt-6-sol'])

    await wrapper.get('span button').trigger('click')
    expect(wrapper.props('models')).toEqual(['gpt-6-sol'])
    await chooseModel(wrapper, 'gpt-6-astra')
    expect(wrapper.props('models')).toEqual(['gpt-6-sol', 'gpt-6-astra'])
  })

  it('keeps custom names, wildcards, and batch paste available alongside selection', async () => {
    const wrapper = mountInput()
    const input = wrapper.get('input')
    await input.setValue('custom-model-*')
    await input.trigger('keydown', { key: 'Enter' })
    await input.trigger('paste', { clipboardData: { getData: () => 'my-alias, custom-model-*\ngpt-6-astra' } })
    expect(wrapper.props('models')).toEqual(['custom-model-*', 'my-alias', 'gpt-6-astra'])
    await chooseModel(wrapper, 'gpt-6-sol')
    expect(wrapper.props('models')).toEqual(['custom-model-*', 'my-alias', 'gpt-6-astra', 'gpt-6-sol'])
  })

  it('updates candidates after loading and allows custom input when no candidates are returned', async () => {
    const wrapper = mountInput({ suggestions: [], loading: true })
    expect(wrapper.findComponent(Select).get('button').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ loading: false, suggestions: ['claude-sonnet-4-6'] })
    await chooseModel(wrapper, 'claude-sonnet-4-6')
    await wrapper.setProps({ suggestions: [] })
    await wrapper.get('input').setValue('custom-fallback')
    await wrapper.get('input').trigger('keydown', { key: 'Enter' })
    expect(wrapper.props('models')).toEqual(['claude-sonnet-4-6', 'custom-fallback'])
  })
})

describe('pricing with model selection', () => {
  it('fills default prices for a selection and preserves edited prices when adding another model', async () => {
    vi.mocked(channelsAPI.getModelDefaultPricing).mockResolvedValue({
      found: true, input_price: 3e-6, output_price: 15e-6,
    })
    const wrapper = mountPricingEntry()
    const modelInput = wrapper.findComponent(ModelTagInput)
    await chooseModel(modelInput, 'gpt-6-astra')
    expect(channelsAPI.getModelDefaultPricing).toHaveBeenCalledWith('gpt-6-astra')
    expect(wrapper.props('entry')).toMatchObject({ models: ['gpt-6-astra'], input_price: 3, output_price: 15 })

    await wrapper.setProps({ entry: { ...wrapper.props('entry'), input_price: 2 } })
    await chooseModel(modelInput, 'gpt-6-sol')
    expect(wrapper.props('entry')).toMatchObject({ models: ['gpt-6-astra', 'gpt-6-sol'], input_price: 2 })
    expect(channelsAPI.getModelDefaultPricing).toHaveBeenCalledTimes(1)
  })

  it('keeps all selections when default pricing responses arrive out of order', async () => {
    const resolvePricing: Array<(result: ModelDefaultPricing) => void> = []
    vi.mocked(channelsAPI.getModelDefaultPricing).mockImplementation(() =>
      new Promise(resolve => { resolvePricing.push(resolve) }),
    )
    const wrapper = mountPricingEntry()
    const modelInput = wrapper.findComponent(ModelTagInput)
    await chooseModel(modelInput, 'gpt-6-astra')
    await chooseModel(modelInput, 'gpt-6-sol')
    expect(resolvePricing).toHaveLength(2)
    resolvePricing[1]({ found: true, input_price: 2e-6 })
    await flushPromises()
    resolvePricing[0]({ found: true, input_price: 3e-6 })
    await flushPromises()
    expect(wrapper.props('entry')).toMatchObject({ models: ['gpt-6-astra', 'gpt-6-sol'], input_price: 2 })
  })

  it('preserves a price entered while default pricing is loading', async () => {
    let resolvePricing!: (result: ModelDefaultPricing) => void
    vi.mocked(channelsAPI.getModelDefaultPricing).mockImplementation(() =>
      new Promise(resolve => { resolvePricing = resolve }),
    )
    const wrapper = mountPricingEntry()
    await chooseModel(wrapper.findComponent(ModelTagInput), 'gpt-6-astra')
    await wrapper.get('input[type="number"]').setValue('1.25')
    resolvePricing({ found: true, input_price: 3e-6, output_price: 15e-6 })
    await flushPromises()
    expect(wrapper.props('entry')).toMatchObject({ models: ['gpt-6-astra'], input_price: '1.25', output_price: null })
  })

  it('does not restore a removed model when its default pricing finishes loading', async () => {
    let resolvePricing!: (result: ModelDefaultPricing) => void
    vi.mocked(channelsAPI.getModelDefaultPricing).mockImplementation(() =>
      new Promise(resolve => { resolvePricing = resolve }),
    )
    const wrapper = mountPricingEntry()
    const modelInput = wrapper.findComponent(ModelTagInput)
    await chooseModel(modelInput, 'gpt-6-astra')
    await modelInput.get('span button').trigger('click')
    resolvePricing({ found: true, input_price: 3e-6 })
    await flushPromises()
    expect(wrapper.props('entry')).toMatchObject({ models: [], input_price: null })
  })
})
