import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import SubscriptionAccountsView from '../SubscriptionAccountsView.vue'
import type { SubscriberAccountItem } from '@/api/subscriptions'

const {
  listSubscriberAccounts,
  getActiveSubscriptions,
  getSubscriberAccountsBatchTodayStats,
  getSubscriberAccountsBatchUsage,
  castSubscriberAccountResetVote,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listSubscriberAccounts: vi.fn(),
  getActiveSubscriptions: vi.fn(),
  getSubscriberAccountsBatchTodayStats: vi.fn(),
  getSubscriberAccountsBatchUsage: vi.fn(),
  castSubscriberAccountResetVote: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/subscriptions', () => ({
  default: {
    listSubscriberAccounts,
    getActiveSubscriptions,
    getSubscriberAccountsBatchTodayStats,
    getSubscriberAccountsBatchUsage,
    castSubscriberAccountResetVote
  }
}))

const { mockAuthState } = vi.hoisted(() => ({
  mockAuthState: { isAdmin: false }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => mockAuthState
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key
  })
}))

function mountView() {
  return mount(SubscriptionAccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: {
          props: ['data', 'columns', 'loading'],
          template:
            '<div><div v-for="row in data" :key="row.id" :data-row-id="row.id"><slot name="cell-reset_vote" :row="row" /></div></div>'
        },
        ConfirmDialog: {
          props: ['show', 'title', 'message', 'confirmText', 'cancelText'],
          emits: ['confirm', 'cancel'],
          template:
            '<div v-if="show" data-testid="vote-confirm-dialog"><span>{{ title }}</span><button data-testid="confirm-vote-btn" @click="$emit(\'confirm\')">confirm</button><button data-testid="cancel-vote-btn" @click="$emit(\'cancel\')">cancel</button></div>'
        },
        AccountResetVotesModal: {
          props: ['show', 'account'],
          emits: ['close'],
          template:
            '<div v-if="show" data-testid="admin-reset-votes-modal" :data-account-id="account?.id"></div>'
        },
        HelpTooltip: true,
        Pagination: true,
        SearchInput: true,
        Select: true,
        AccountStatusIndicator: true,
        AccountUsageCell: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountCapacityCell: true,
        PlatformTypeBadge: true,
        Icon: true
      }
    }
  })
}

enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.clearAllMocks()
  mockAuthState.isAdmin = false
  localStorage.clear()
  getActiveSubscriptions.mockResolvedValue([])
  getSubscriberAccountsBatchTodayStats.mockResolvedValue({ stats: {} })
  getSubscriberAccountsBatchUsage.mockResolvedValue({ usage: {} })
})

describe('SubscriptionAccountsView reset voting', () => {
  it('shows eligibility restrictions for quota-group-only and non-subscribed accounts without vote button', async () => {
    const accounts: SubscriberAccountItem[] = [
      {
        id: 1,
        name: 'Quota-Only-Account',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        concurrency: 2,
        current_concurrency: 0,
        priority: 1,
        reset_vote: {
          supported: true,
          eligible: false,
          can_vote: false,
          has_voted: false,
          ineligible_reason: 'quota_group_only',
          status: 'none',
          vote_count: 0,
          threshold: 3,
          cooldown_active: false,
          can_initiate: false
        }
      } as SubscriberAccountItem,
      {
        id: 2,
        name: 'Claude-Account',
        platform: 'anthropic',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        concurrency: 2,
        current_concurrency: 0,
        priority: 1,
        reset_vote: {
          supported: false,
          eligible: false,
          can_vote: false,
          has_voted: false,
          ineligible_reason: 'unsupported_account',
          status: 'none',
          vote_count: 0,
          threshold: 3,
          cooldown_active: false,
          can_initiate: false
        }
      } as SubscriberAccountItem,
      {
        id: 3,
        name: 'Already-Voted-Account',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        concurrency: 2,
        current_concurrency: 0,
        priority: 1,
        reset_vote: {
          supported: true,
          eligible: true,
          can_vote: false,
          has_voted: true,
          status: 'open',
          vote_count: 1,
          threshold: 3,
          expires_at: new Date(Date.now() + 3600_000 * 10).toISOString(),
          cooldown_active: false,
          can_initiate: false
        }
      } as SubscriberAccountItem
    ]
    listSubscriberAccounts.mockResolvedValue({ items: accounts, total: 3, page: 1, page_size: 20, total_pages: 1 })

    const wrapper = mountView()
    await flushPromises()

    const row1 = wrapper.find('[data-row-id="1"]')
    expect(row1.text()).toContain('userSubscriptions.resetVote.quotaGroupForbidden')
    expect(row1.find('button').exists()).toBe(false)

    const row2 = wrapper.find('[data-row-id="2"]')
    expect(row2.text()).toBe('-')
    expect(row2.find('button').exists()).toBe(false)

    const row3 = wrapper.find('[data-row-id="3"]')
    expect(row3.text()).toContain('userSubscriptions.resetVote.votedWaiting')
    expect(row3.find('button').exists()).toBe(false)
  })

  it('initiates a new 24h vote round directly on click and updates vote state', async () => {
    const accounts: SubscriberAccountItem[] = [
      {
        id: 10,
        name: 'Shared-Codex',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        concurrency: 2,
        current_concurrency: 0,
        priority: 1,
        reset_vote: {
          supported: true,
          eligible: true,
          can_vote: true,
          has_voted: false,
          status: 'none',
          vote_count: 0,
          threshold: 3,
          cooldown_active: false,
          can_initiate: true
        }
      } as SubscriberAccountItem
    ]
    listSubscriberAccounts.mockResolvedValue({ items: accounts, total: 1, page: 1, page_size: 20, total_pages: 1 })
    castSubscriberAccountResetVote.mockResolvedValue({
      vote_recorded: true,
      triggered_reset: false,
      reset_succeeded: false,
      state: {
        supported: true,
        eligible: true,
        can_vote: false,
        has_voted: true,
        round_id: 50,
        status: 'open',
        vote_count: 1,
        threshold: 3,
        expires_at: new Date(Date.now() + 3600_000 * 24).toISOString(),
        cooldown_active: false,
        can_initiate: false
      }
    })

    const wrapper = mountView()
    await flushPromises()

    const voteBtn = wrapper.find('[data-row-id="10"] button')
    expect(voteBtn.text()).toContain('userSubscriptions.resetVote.initiate')
    await voteBtn.trigger('click')
    await flushPromises()

    expect(castSubscriberAccountResetVote).toHaveBeenCalledWith(10)
    expect(wrapper.find('[data-testid="vote-confirm-dialog"]').exists()).toBe(false)
    expect(showSuccess).toHaveBeenCalledWith('userSubscriptions.resetVote.votedToast:{"count":1,"threshold":3}')
    expect(wrapper.find('[data-row-id="10"]').text()).toContain('userSubscriptions.resetVote.votedWaiting')
  })

  it('confirms on the 3rd vote and refreshes subscriptions after triggered reset succeeds', async () => {
    const accounts: SubscriberAccountItem[] = [
      {
        id: 11,
        name: 'Shared-Codex-Near-Threshold',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        concurrency: 2,
        current_concurrency: 0,
        priority: 1,
        reset_vote: {
          supported: true,
          eligible: true,
          can_vote: true,
          has_voted: false,
          round_id: 60,
          status: 'open',
          vote_count: 2,
          threshold: 3,
          expires_at: new Date(Date.now() + 3600_000 * 20).toISOString(),
          cooldown_active: false,
          can_initiate: false
        }
      } as SubscriberAccountItem
    ]
    listSubscriberAccounts.mockResolvedValue({ items: accounts, total: 1, page: 1, page_size: 20, total_pages: 1 })
    castSubscriberAccountResetVote.mockResolvedValue({
      vote_recorded: true,
      triggered_reset: true,
      reset_succeeded: true,
      state: {
        supported: true,
        eligible: true,
        can_vote: false,
        has_voted: true,
        round_id: 60,
        status: 'succeeded',
        vote_count: 3,
        threshold: 3,
        card_status: 'succeeded',
        subscription_reset_status: 'succeeded',
        subscription_reset_count: 4,
        cooldown_active: true,
        can_initiate: false
      }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-row-id="11"] button').trigger('click')
    expect(wrapper.find('[data-testid="vote-confirm-dialog"]').exists()).toBe(true)
    expect(castSubscriberAccountResetVote).not.toHaveBeenCalled()

    await wrapper.find('[data-testid="confirm-vote-btn"]').trigger('click')
    await flushPromises()

    expect(castSubscriberAccountResetVote).toHaveBeenCalledWith(11)
    expect(showSuccess).toHaveBeenCalledWith('userSubscriptions.resetVote.triggeredSuccess:{"count":4}')
    expect(getActiveSubscriptions).toHaveBeenCalledTimes(2)
    expect(listSubscriberAccounts).toHaveBeenCalledTimes(2)
  })

  it('shows admin vote details trigger only to admins and opens modal on click', async () => {
    mockAuthState.isAdmin = true
    const accounts: SubscriberAccountItem[] = [
      {
        id: 21,
        name: 'Admin-View-Account',
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        schedulable: true,
        concurrency: 2,
        current_concurrency: 0,
        priority: 1,
        reset_vote: {
          supported: true,
          eligible: false,
          can_vote: false,
          has_voted: false,
          ineligible_reason: 'quota_group_only',
          status: 'open',
          vote_count: 1,
          threshold: 3,
          cooldown_active: false,
          can_initiate: false
        }
      } as SubscriberAccountItem
    ]
    listSubscriberAccounts.mockResolvedValue({ items: accounts, total: 1, page: 1, page_size: 20, total_pages: 1 })

    const wrapper = mountView()
    await flushPromises()

    const detailsBtn = wrapper.find('[data-row-id="21"] [data-testid="admin-vote-details-button"]')
    expect(detailsBtn.exists()).toBe(true)
    await detailsBtn.trigger('click')

    const modal = wrapper.find('[data-testid="admin-reset-votes-modal"]')
    expect(modal.exists()).toBe(true)
    expect(modal.attributes('data-account-id')).toBe('21')
  })
})
