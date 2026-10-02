import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import AppSidebar from '../AppSidebar.vue'

const { auth, app, settings } = vi.hoisted(() => ({
  auth: { isAdmin: false, isSimpleMode: false },
  app: { sidebarCollapsed: false, mobileOpen: false, siteName: 'Test', siteLogo: '', siteVersion: '', publicSettingsLoaded: true, backendModeEnabled: false, sidebarScrollTop: 0, cachedPublicSettings: { custom_menu_items: [] }, toggleSidebar: vi.fn(), setMobileOpen: vi.fn() },
  settings: { customMenuItems: [], fetch: vi.fn() }
}))
vi.mock('@/stores', () => ({ useAuthStore: () => auth, useAppStore: () => app, useAdminSettingsStore: () => settings, useOnboardingStore: () => ({ isCurrentStep: () => false }) }))
vi.mock('vue-router', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-router')>(), useRoute: () => ({ path: '/intelligence-tests' }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/utils/featureFlags', () => ({ FeatureFlags: {}, makeSidebarFlag: () => () => true, resolveFeatureFlag: () => true }))
vi.mock('@/composables/useBatchImageAccess', () => ({ useBatchImageAccess: () => ({ canUseBatchImage: ref(false), refreshBatchImageAccess: vi.fn() }) }))

function mountSidebar() {
  return mount(AppSidebar, { global: { stubs: { VersionBadge: true, Icon: true, RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } } })
}

describe('intelligence test navigation visibility', () => {
  beforeEach(() => { auth.isAdmin = false; auth.isSimpleMode = false })

  it.each([false, true])('shows the shared result entry to ordinary users with simple mode %s', async (simple) => {
    auth.isSimpleMode = simple
    const wrapper = mountSidebar()
    await flushPromises()
    expect(wrapper.get('a[href="/intelligence-tests"]').text()).toBe('intelligenceTests.title')
    wrapper.unmount()
  })

  it('shows the results under My Account for administrators', async () => {
    auth.isAdmin = true
    const wrapper = mountSidebar()
    await flushPromises()
    const section = wrapper.findAll('.sidebar-section').find((entry) => entry.text().includes('nav.myAccount'))!
    expect(section.find('a[href="/intelligence-tests"]').exists()).toBe(true)
    expect(section.find('a[href="/profile"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the shared results visible under My Account in admin simple mode without adding other personal pages', async () => {
    auth.isAdmin = true
    auth.isSimpleMode = true
    const wrapper = mountSidebar()
    await flushPromises()
    const section = wrapper.findAll('.sidebar-section').find((entry) => entry.text().includes('nav.myAccount'))!
    expect(section.findAll('a')).toHaveLength(1)
    expect(section.get('a').attributes('href')).toBe('/intelligence-tests')
    expect(wrapper.find('a[href="/keys"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
