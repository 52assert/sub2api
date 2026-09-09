import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ContainerUpdatePanel from '../ContainerUpdatePanel.vue'

const mocks = vi.hoisted(() => ({ fetchVersion: vi.fn(), getStatus: vi.fn(), update: vi.fn(), clearVersionCache: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ currentVersion: '0.2.3', fetchVersion: mocks.fetchVersion, clearVersionCache: mocks.clearVersionCache }) }))
vi.mock('@/api/admin/system', () => ({ getContainerUpdateStatus: mocks.getStatus, performUpdate: mocks.update }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh-CN' } }) }))

describe('container online update', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
    localStorage.clear()
    mocks.fetchVersion.mockResolvedValue({ current_version: '0.2.3', latest_version: '0.2.3', has_update: true, container_image: 'ghcr.io/52assert/sub2api@sha256:abc' })
    mocks.getStatus.mockResolvedValue(null)
    mocks.update.mockResolvedValue({ need_restart: false, job: { id: 'job-1', stage: 'queued', version: '0.2.3' } })
  })
  afterEach(() => { vi.useRealTimers() })

  it('shows a new build of the same version and submits the selected immutable image after confirmation', async () => {
    const wrapper = mount(ContainerUpdatePanel)
    await flushPromises()
    expect(wrapper.text()).toContain('新的定制构建')
    await wrapper.findAll('button').find(button => button.text() === '在线更新')!.trigger('click')
    expect(mocks.update).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '确认更新')!.trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith('ghcr.io/52assert/sub2api@sha256:abc')
    expect(localStorage.getItem('sub2api.container-update.pending')).toBe('job-1')
    expect(wrapper.text()).toContain('升级任务已提交')
    wrapper.unmount()
  })

  it('treats loss of connectivity during an update as an unknown outcome, not a successful update', async () => {
    localStorage.setItem('sub2api.container-update.pending', 'job-1')
    mocks.getStatus.mockRejectedValue(new Error('network disconnected'))
    const wrapper = mount(ContainerUpdatePanel)
    await flushPromises()
    expect(wrapper.text()).toContain('尚不能确认升级结果')
    expect(wrapper.text()).not.toContain('镜像和应用容器更新完成')
    expect(mocks.clearVersionCache).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('blocks another update when the host reports a failed replacement needing attention', async () => {
    mocks.getStatus.mockResolvedValue({ id: 'job-1', stage: 'needs_attention' })
    const wrapper = mount(ContainerUpdatePanel)
    await flushPromises()
    expect(wrapper.text()).toContain('升级需要管理员处理')
    expect(wrapper.findAll('button').some(button => button.text() === '在线更新')).toBe(false)
    wrapper.unmount()
  })
})
