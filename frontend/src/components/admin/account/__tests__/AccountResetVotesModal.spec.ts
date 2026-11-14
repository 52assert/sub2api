import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountResetVotesModal from '../AccountResetVotesModal.vue'
import { getAccountResetVotes } from '@/api/admin/accounts'

vi.mock('@/api/admin/accounts', () => ({
  getAccountResetVotes: vi.fn(),
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key,
  }),
}))

function mountModal(props: { show: boolean; account: { id: number; name?: string } | null }) {
  return mount(AccountResetVotesModal, {
    props,
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title', 'width'],
          emits: ['close'],
          template:
            '<div v-if="show" data-testid="base-dialog"><h3>{{ title }}</h3><slot /><div class="footer"><slot name="footer" /></div></div>',
        },
        Icon: true,
      },
    },
  })
}

describe('AccountResetVotesModal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads and displays voters (with initiator tag) and non-voters when opened', async () => {
    vi.mocked(getAccountResetVotes).mockResolvedValue({
      account_id: 101,
      account_name: 'Pro200 Milo',
      supported: true,
      status: 'open',
      round_id: 9,
      vote_count: 1,
      threshold: 3,
      eligible_count: 2,
      initiated_at: '2026-11-14T08:00:00Z',
      expires_at: '2026-11-15T08:00:00Z',
      cooldown_active: false,
      voters: [
        {
          user_id: 7,
          username: 'alice',
          email: 'alice@example.com',
          group_names: ['GPT Pro20x Milo'],
          voted_at: '2026-11-14T08:00:00Z',
          is_initiator: true,
        },
      ],
      non_voters: [
        {
          user_id: 8,
          username: 'bob',
          email: 'bob@example.com',
          group_names: ['GPT Pro20x Milo'],
        },
      ],
      last_round: null,
    })

    const wrapper = mountModal({ show: true, account: { id: 101, name: 'Pro200 Milo' } })
    await flushPromises()

    expect(getAccountResetVotes).toHaveBeenCalledWith(101)
    expect(wrapper.text()).toContain('Pro200 Milo')
    expect(wrapper.text()).toContain('alice')
    expect(wrapper.text()).toContain('alice@example.com')
    expect(wrapper.text()).toContain('userSubscriptions.resetVote.initiatorTag')
    expect(wrapper.text()).toContain('bob')
    expect(wrapper.text()).toContain('GPT Pro20x Milo')
    expect(wrapper.text()).toContain('userSubscriptions.resetVote.votersSection:{"count":1}')
    expect(wrapper.text()).toContain('userSubscriptions.resetVote.nonVotersSection:{"count":1}')
  })

  it('shows expired previous round summary when status is none and last_round exists', async () => {
    vi.mocked(getAccountResetVotes).mockResolvedValue({
      account_id: 102,
      account_name: 'Pro200-2',
      supported: true,
      status: 'none',
      vote_count: 0,
      threshold: 3,
      eligible_count: 1,
      cooldown_active: false,
      voters: [],
      non_voters: [
        {
          user_id: 9,
          username: 'carol',
          email: 'carol@example.com',
          group_names: ['VIP-Sub'],
        },
      ],
      last_round: {
        round_id: 5,
        status: 'open',
        vote_count: 2,
        threshold: 3,
        initiated_at: '2026-11-12T08:00:00Z',
        expires_at: '2026-11-13T08:00:00Z',
        voters: [
          {
            user_id: 9,
            username: 'carol',
            email: 'carol@example.com',
            voted_at: '2026-11-12T08:00:00Z',
            is_initiator: true,
          },
        ],
      },
    })

    const wrapper = mountModal({ show: true, account: { id: 102, name: 'Pro200-2' } })
    await flushPromises()

    expect(wrapper.text()).toContain('userSubscriptions.resetVote.lastRoundSummary')
    expect(wrapper.text()).toContain('carol')
  })

  it('displays error message when fetch fails and allows manual refresh', async () => {
    vi.mocked(getAccountResetVotes)
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce({
        account_id: 103,
        account_name: 'Pro200-3',
        supported: true,
        status: 'none',
        vote_count: 0,
        threshold: 3,
        eligible_count: 0,
        cooldown_active: false,
        voters: [],
        non_voters: [],
      })

    const wrapper = mountModal({ show: true, account: { id: 103, name: 'Pro200-3' } })
    await flushPromises()

    expect(wrapper.text()).toContain('network down')

    await wrapper.find('button').trigger('click')
    await flushPromises()

    expect(getAccountResetVotes).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('userSubscriptions.resetVote.noEligibleMembers')
  })
})
