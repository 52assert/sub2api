import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { LoginIPBanListResponse } from '@/api/admin/audit'
import LoginIPBans from '../LoginIPBans.vue'

const mocks = vi.hoisted(() => ({
  listIPBans: vi.fn(),
  unbanIP: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  t: vi.fn((key: string, params: Record<string, unknown> = {}) => `${key} ${Object.values(params).join(' ')}`.trim())
}))
vi.mock('@/api/admin/audit', () => ({ auditAPI: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: mocks.t }) }))

enableAutoUnmount(afterEach)

const ip = '203.0.113.42'
function response(overrides: Partial<LoginIPBanListResponse> = {}): LoginIPBanListResponse {
  return {
    items: [{ client_ip: ip, failure_count: 3, blocked_at: '2026-10-11T08:00:00Z' }],
    total: 1,
    page: 1,
    page_size: 5,
    threshold: 3,
    failure_window_seconds: 86400,
    ban_duration_seconds: 0,
    ...overrides
  }
}

function mountBans() {
  return mount(LoginIPBans, {
    global: {
      stubs: {
        ConfirmDialog: {
          props: ['show', 'title', 'message'],
          emits: ['confirm', 'cancel'],
          template: '<div v-if="show" role="dialog"><p>{{ message }}</p><button @click="$emit(\'confirm\')">confirm</button><button @click="$emit(\'cancel\')">cancel</button></div>'
        },
        Pagination: {
          props: ['page', 'total', 'pageSize'],
          emits: ['update:page'],
          template: '<button data-pagination @click="$emit(\'update:page\', page + 1)">next</button>'
        }
      }
    }
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.listIPBans.mockReset().mockResolvedValue(response())
  mocks.unbanIP.mockReset().mockResolvedValue({ client_ip: ip })
})

describe('LoginIPBans', () => {
  it('lists permanent bans and uses the policy returned by the server', async () => {
    mocks.listIPBans.mockResolvedValue(response({ threshold: 4, failure_window_seconds: 172800 }))
    const wrapper = mountBans()
    await flushPromises()

    expect(mocks.listIPBans).toHaveBeenCalledWith({ page: 1, page_size: 5 })
    expect(wrapper.get('tbody').text()).toContain(ip)
    expect(wrapper.get('tbody').text()).toContain('admin.audit.ipBans.permanent')
    expect(mocks.t).toHaveBeenCalledWith('admin.audit.ipBans.permanentPolicy', {
      threshold: 4,
      window: 'admin.audit.ipBans.hours 48',
      duration: 'admin.audit.ipBans.hours 0'
    })
  })

  it('requires confirmation before unbanning, then refreshes the list', async () => {
    const wrapper = mountBans()
    await flushPromises()
    await wrapper.get('tbody button').trigger('click')

    expect(wrapper.get('[role="dialog"]').text()).toContain(ip)
    expect(mocks.unbanIP).not.toHaveBeenCalled()
    mocks.listIPBans.mockResolvedValue(response({ items: [], total: 0 }))
    await wrapper.get('[role="dialog"] button').trigger('click')
    await flushPromises()

    expect(mocks.unbanIP).toHaveBeenCalledTimes(1)
    expect(mocks.unbanIP).toHaveBeenCalledWith(ip)
    expect(mocks.listIPBans).toHaveBeenCalledTimes(2)
    expect(mocks.showSuccess).toHaveBeenCalledWith(`admin.audit.ipBans.unbanSuccess ${ip}`)
    expect(wrapper.text()).toContain('admin.audit.ipBans.empty')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
  })

  it('keeps the ban visible when unbanning fails', async () => {
    mocks.unbanIP.mockRejectedValue({ message: 'Access denied' })
    const wrapper = mountBans()
    await flushPromises()
    await wrapper.get('tbody button').trigger('click')
    await wrapper.get('[role="dialog"] button').trigger('click')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalledWith('Access denied')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    expect(mocks.listIPBans).toHaveBeenCalledTimes(1)
    expect(wrapper.get('tbody').text()).toContain(ip)
    expect(wrapper.get('tbody button').attributes('disabled')).toBeUndefined()
  })

  it('shows a loading error and supports retry without displaying an empty list', async () => {
    mocks.listIPBans.mockRejectedValueOnce({ message: 'Service unavailable' })
    const wrapper = mountBans()
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toBe('Service unavailable')
    expect(wrapper.text()).not.toContain('admin.audit.ipBans.empty')
    await wrapper.get('button.btn-secondary').trigger('click')
    await flushPromises()

    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('tbody').text()).toContain(ip)
  })

  it('returns to the previous page after the last ban on a page is removed', async () => {
    mocks.listIPBans.mockResolvedValueOnce(response({ total: 6 }))
    const wrapper = mountBans()
    await flushPromises()
    mocks.listIPBans.mockResolvedValueOnce(response({ total: 6, page: 2 }))
    await wrapper.get('[data-pagination]').trigger('click')
    await flushPromises()
    expect(mocks.listIPBans).toHaveBeenLastCalledWith({ page: 2, page_size: 5 })

    mocks.listIPBans.mockResolvedValueOnce(response({ items: [], total: 5, page: 2 }))
      .mockResolvedValueOnce(response({ total: 5 }))
    await wrapper.get('tbody button').trigger('click')
    await wrapper.get('[role="dialog"] button').trigger('click')
    await flushPromises()

    expect(mocks.listIPBans).toHaveBeenLastCalledWith({ page: 1, page_size: 5 })
    expect(wrapper.get('tbody').text()).toContain(ip)
  })
})
