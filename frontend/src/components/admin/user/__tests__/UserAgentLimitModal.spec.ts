import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { AdminUser } from '@/types'
import UserAgentLimitModal from '../UserAgentLimitModal.vue'

const {
  getDetailMock,
  updatePolicyMock,
  deleteRecordMock,
  clearRecordsMock,
  showSuccessMock,
  showErrorMock
} = vi.hoisted(() => ({
  getDetailMock: vi.fn(),
  updatePolicyMock: vi.fn(),
  deleteRecordMock: vi.fn(),
  clearRecordsMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key,
      locale: { value: 'en' }
    }
  }),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key
  })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: showSuccessMock,
    showError: showErrorMock
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      getUserAgentLimitDetail: (...args: unknown[]) => getDetailMock(...args),
      updateUserAgentLimitPolicy: (...args: unknown[]) => updatePolicyMock(...args),
      deleteUserAgentRecord: (...args: unknown[]) => deleteRecordMock(...args),
      clearUserAgentRecords: (...args: unknown[]) => clearRecordsMock(...args)
    }
  }
}))

const testUser: AdminUser = {
  id: 42,
  username: 'alice',
  email: 'alice@example.com',
  role: 'user',
  balance: 10,
  concurrency: 2,
  status: 'active',
  allowed_groups: [],
  balance_notify_enabled: false,
  balance_notify_threshold: null,
  balance_notify_extra_emails: [],
  created_at: '2026-11-01T00:00:00Z',
  updated_at: '2026-11-01T00:00:00Z',
  notes: ''
}

function makeDetailResponse() {
  return {
    user_id: 42,
    user_email: 'alice@example.com',
    username: 'alice',
    user_role: 'user',
    settings: {
      enabled: true,
      max_user_agents: 5,
      window_days: 30,
      exempt_admin: true
    },
    policy: {
      max_user_agents: null,
      note: ''
    },
    summary: {
      user_id: 42,
      enabled: true,
      active_count: 1,
      effective_limit: 5,
      global_limit: 5,
      custom_limit: null,
      exempt: false,
      at_limit: false,
      exceeded: false,
      window_days: 30,
      note: ''
    },
    records: [
      {
        id: 9,
        user_id: 42,
        ua_key: 'codex desktop (mac os 26.3.1; arm64)',
        ua_label: 'Codex Desktop (Mac OS 26.3.1; arm64)',
        last_raw_ua: 'Codex Desktop/0.171.0-alpha.13 (Mac OS 26.3.1; arm64)',
        first_seen_at: '2026-11-20T10:00:00Z',
        last_seen_at: '2026-11-23T09:00:00Z',
        request_count: 12
      }
    ]
  }
}

const ConfirmDialogStub = {
  props: ['show', 'title', 'message'],
  emits: ['confirm', 'cancel'],
  template:
    '<div v-if="show"><button data-testid="confirm-ok" @click="$emit(\'confirm\')">ok</button></div>'
}

describe('UserAgentLimitModal', () => {
  beforeEach(() => {
    getDetailMock.mockReset()
    updatePolicyMock.mockReset()
    deleteRecordMock.mockReset()
    clearRecordsMock.mockReset()
    showSuccessMock.mockReset()
    showErrorMock.mockReset()

    getDetailMock.mockResolvedValue(makeDetailResponse())
    updatePolicyMock.mockResolvedValue({
      ...makeDetailResponse(),
      policy: { max_user_agents: 0, note: 'vip' },
      summary: { ...makeDetailResponse().summary, custom_limit: 0, exempt: true, effective_limit: 0, note: 'vip' }
    })
    deleteRecordMock.mockResolvedValue({ deleted: true })
    clearRecordsMock.mockResolvedValue({ deleted_count: 1 })
  })

  it('loads detail, saves exempt policy override, and deletes a record', async () => {
    const wrapper = mount(UserAgentLimitModal, {
      props: { show: true, user: testUser },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /></div>' },
          ConfirmDialog: ConfirmDialogStub
        }
      }
    })
    await flushPromises()

    expect(getDetailMock).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('Codex Desktop (Mac OS 26.3.1; arm64)')

    await wrapper.get('[data-testid="ua-policy-mode-exempt"]').trigger('click')
    await wrapper.get('[data-testid="ua-policy-note-input"]').setValue('vip')
    await wrapper.get('[data-testid="ua-policy-save"]').trigger('click')
    await flushPromises()

    expect(updatePolicyMock).toHaveBeenCalledWith(42, {
      max_user_agents: 0,
      note: 'vip'
    })
    expect(wrapper.emitted('updated')).toHaveLength(1)

    await wrapper.get('[data-testid="ua-record-delete-9"]').trigger('click')
    await wrapper.get('[data-testid="confirm-ok"]').trigger('click')
    await flushPromises()

    expect(deleteRecordMock).toHaveBeenCalledWith(42, 9)
    expect(wrapper.emitted('updated')).toHaveLength(2)
  })
})
