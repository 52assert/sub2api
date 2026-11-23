import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UserAgentLimitCard from '../UserAgentLimitCard.vue'

const {
  getSettingsMock,
  updateSettingsMock,
  showSuccessMock,
  showErrorMock
} = vi.hoisted(() => ({
  getSettingsMock: vi.fn(),
  updateSettingsMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    settings: {
      getUserAgentLimitSettings: (...args: unknown[]) => getSettingsMock(...args),
      updateUserAgentLimitSettings: (...args: unknown[]) => updateSettingsMock(...args)
    }
  }
}))

describe('UserAgentLimitCard', () => {
  beforeEach(() => {
    getSettingsMock.mockReset()
    updateSettingsMock.mockReset()
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
    getSettingsMock.mockResolvedValue({
      enabled: true,
      max_user_agents: 5,
      window_days: 30,
      exempt_admin: true
    })
    updateSettingsMock.mockImplementation(async (payload) => payload)
  })

  it('loads settings and saves updated limit values', async () => {
    const wrapper = mount(UserAgentLimitCard, {
      global: {
        stubs: {
          Icon: true,
          Toggle: {
            props: ['modelValue'],
            emits: ['update:modelValue'],
            template:
              '<input type="checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', !modelValue)" />'
          }
        }
      }
    })
    await flushPromises()

    expect(getSettingsMock).toHaveBeenCalledOnce()
    const maxInput = wrapper.get('[data-testid="ua-limit-max-input"]')
    const daysInput = wrapper.get('[data-testid="ua-limit-window-days-input"]')
    expect((maxInput.element as HTMLInputElement).value).toBe('5')
    expect((daysInput.element as HTMLInputElement).value).toBe('30')

    await maxInput.setValue('8')
    await daysInput.setValue('14')
    await wrapper.get('[data-testid="ua-limit-save"]').trigger('click')
    await flushPromises()

    expect(updateSettingsMock).toHaveBeenCalledWith({
      enabled: true,
      max_user_agents: 8,
      window_days: 14,
      exempt_admin: true
    })
    expect(showSuccessMock).toHaveBeenCalledWith('admin.settings.userAgentLimit.saved')
  })

  it('rejects out-of-range inputs before calling the API', async () => {
    const wrapper = mount(UserAgentLimitCard, {
      global: {
        stubs: {
          Icon: true,
          Toggle: true
        }
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="ua-limit-max-input"]').setValue('0')
    await wrapper.get('[data-testid="ua-limit-save"]').trigger('click')
    await flushPromises()

    expect(updateSettingsMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledWith('admin.settings.userAgentLimit.invalidRange')
  })
})
