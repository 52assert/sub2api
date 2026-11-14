<template>
  <BaseDialog
    :show="show"
    :title="modalTitle"
    width="wide"
    @close="emit('close')"
  >
    <div v-if="loading && !details" class="flex items-center justify-center gap-2 py-10 text-sm text-gray-500 dark:text-dark-400">
      <Icon name="refresh" size="sm" class="animate-spin" />
      <span>{{ t('common.loading') }}</span>
    </div>

    <div
      v-else-if="errorMessage"
      class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-800/40 dark:bg-red-900/20 dark:text-red-300"
    >
      <div class="flex items-center justify-between gap-3">
        <span>{{ errorMessage }}</span>
        <button
          type="button"
          class="btn btn-secondary btn-xs"
          @click="loadDetails"
        >
          {{ t('common.refresh') }}
        </button>
      </div>
    </div>

    <div v-else-if="details && !details.supported" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
      {{ t('userSubscriptions.resetVote.unsupportedHint') }}
    </div>

    <div v-else-if="details" class="space-y-5">
      <!-- Status summary -->
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 bg-gray-50/70 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60">
        <div class="flex flex-wrap items-center gap-2.5 text-sm">
          <span :class="['inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium', statusBadgeClass]">
            {{ statusLabel }}
          </span>
          <span class="font-medium text-gray-800 dark:text-dark-100">
            {{ t('userSubscriptions.resetVote.progress', { count: details.vote_count, threshold: details.threshold || 3 }) }}
          </span>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('userSubscriptions.resetVote.eligibleCount') }}: {{ details.eligible_count }}
          </span>
          <span
            v-if="details.subscription_reset_status === 'succeeded' && (details.subscription_reset_count ?? 0) > 0"
            class="inline-flex items-center rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
          >
            {{ t('userSubscriptions.resetVote.subscriptionsResetCount', { count: details.subscription_reset_count ?? 0 }) }}
          </span>
          <span v-if="details.expires_at && details.status !== 'none'" class="text-xs text-gray-500 dark:text-dark-400">
            · {{ formatDateTime(details.expires_at) }}
          </span>
        </div>

        <button
          type="button"
          data-testid="refresh-vote-details-button"
          class="btn btn-secondary btn-xs inline-flex items-center gap-1.5"
          :disabled="loading"
          @click="loadDetails"
        >
          <Icon name="refresh" size="xs" :class="{ 'animate-spin': loading }" />
          <span>{{ t('common.refresh') }}</span>
        </button>
      </div>

      <!-- Optional failure / warning banner -->
      <div
        v-if="roundFailureBanner"
        class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-2.5 text-xs text-amber-800 dark:border-amber-800/40 dark:bg-amber-900/20 dark:text-amber-200"
      >
        {{ roundFailureBanner }}
      </div>

      <!-- Current cycle participants: voters & non-voters -->
      <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <!-- Voters -->
        <section class="rounded-xl border border-gray-200 dark:border-dark-700">
          <header class="flex items-center justify-between border-b border-gray-200 bg-gray-50/70 px-3.5 py-2.5 dark:border-dark-700 dark:bg-dark-800/50">
            <h4 class="text-xs font-semibold uppercase tracking-wide text-gray-700 dark:text-dark-200">
              {{ t('userSubscriptions.resetVote.votersSection', { count: details.voters.length }) }}
            </h4>
          </header>
          <div v-if="details.voters.length === 0" class="px-3.5 py-6 text-center text-xs text-gray-500 dark:text-dark-400">
            {{ t('userSubscriptions.resetVote.noVotersYet') }}
          </div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 text-xs dark:divide-dark-700">
              <thead class="bg-gray-50/40 text-gray-500 dark:bg-dark-800/30 dark:text-dark-400">
                <tr>
                  <th class="px-3 py-2 text-left font-medium">{{ t('userSubscriptions.resetVote.memberColumn') }}</th>
                  <th class="px-3 py-2 text-left font-medium">{{ t('userSubscriptions.resetVote.groupsColumn') }}</th>
                  <th class="px-3 py-2 text-right font-medium">{{ t('userSubscriptions.resetVote.votedAtColumn') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-for="voter in details.voters" :key="voter.user_id">
                  <td class="px-3 py-2 align-top">
                    <div class="flex flex-wrap items-center gap-1.5">
                      <span class="font-medium text-gray-900 dark:text-dark-100">{{ participantPrimaryName(voter) }}</span>
                      <span
                        v-if="voter.is_initiator"
                        class="inline-flex items-center rounded bg-indigo-100 px-1.5 py-0.5 text-[10px] font-medium text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300"
                      >
                        {{ t('userSubscriptions.resetVote.initiatorTag') }}
                      </span>
                    </div>
                    <div v-if="participantSecondaryEmail(voter)" class="text-[11px] text-gray-500 dark:text-dark-400">
                      {{ participantSecondaryEmail(voter) }}
                    </div>
                  </td>
                  <td class="px-3 py-2 align-top text-gray-600 dark:text-dark-300">
                    {{ formatGroups(voter.group_names) }}
                  </td>
                  <td class="px-3 py-2 align-top text-right tabular-nums text-gray-500 dark:text-dark-400">
                    {{ voter.voted_at ? formatDateTime(voter.voted_at) : '-' }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <!-- Non-voters -->
        <section class="rounded-xl border border-gray-200 dark:border-dark-700">
          <header class="flex items-center justify-between border-b border-gray-200 bg-gray-50/70 px-3.5 py-2.5 dark:border-dark-700 dark:bg-dark-800/50">
            <h4 class="text-xs font-semibold uppercase tracking-wide text-gray-700 dark:text-dark-200">
              {{ t('userSubscriptions.resetVote.nonVotersSection', { count: details.non_voters.length }) }}
            </h4>
          </header>
          <div v-if="details.eligible_count === 0" class="px-3.5 py-6 text-center text-xs text-gray-500 dark:text-dark-400">
            {{ t('userSubscriptions.resetVote.noEligibleMembers') }}
          </div>
          <div v-else-if="details.non_voters.length === 0" class="px-3.5 py-6 text-center text-xs text-emerald-600 dark:text-emerald-400">
            {{ t('userSubscriptions.resetVote.allVoted') }}
          </div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 text-xs dark:divide-dark-700">
              <thead class="bg-gray-50/40 text-gray-500 dark:bg-dark-800/30 dark:text-dark-400">
                <tr>
                  <th class="px-3 py-2 text-left font-medium">{{ t('userSubscriptions.resetVote.memberColumn') }}</th>
                  <th class="px-3 py-2 text-left font-medium">{{ t('userSubscriptions.resetVote.groupsColumn') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-for="member in details.non_voters" :key="member.user_id">
                  <td class="px-3 py-2 align-top">
                    <div class="font-medium text-gray-900 dark:text-dark-100">
                      {{ participantPrimaryName(member) }}
                    </div>
                    <div v-if="participantSecondaryEmail(member)" class="text-[11px] text-gray-500 dark:text-dark-400">
                      {{ participantSecondaryEmail(member) }}
                    </div>
                  </td>
                  <td class="px-3 py-2 align-top text-gray-600 dark:text-dark-300">
                    {{ formatGroups(member.group_names) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <!-- Expired previous round summary (when no active 24h cycle) -->
      <section
        v-if="details.status === 'none' && details.last_round"
        class="rounded-xl border border-dashed border-gray-300 bg-gray-50/50 p-3.5 dark:border-dark-600 dark:bg-dark-800/30"
      >
        <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-xs font-semibold text-gray-700 dark:text-dark-200">
            {{ t('userSubscriptions.resetVote.lastRoundSummary') }}
          </h4>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('userSubscriptions.resetVote.progress', { count: details.last_round.vote_count, threshold: details.last_round.threshold || 3 }) }}
            · {{ formatDateTime(details.last_round.initiated_at) }}
          </span>
        </div>
        <div v-if="details.last_round.voters.length === 0" class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('userSubscriptions.resetVote.noVotersYet') }}
        </div>
        <div v-else class="flex flex-wrap gap-2">
          <span
            v-for="voter in details.last_round.voters"
            :key="voter.user_id"
            class="inline-flex items-center gap-1 rounded-full border border-gray-200 bg-white px-2.5 py-0.5 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-700 dark:text-dark-200"
          >
            <span>{{ participantPrimaryName(voter) }}</span>
            <span
              v-if="voter.is_initiator"
              class="text-[10px] font-medium text-indigo-600 dark:text-indigo-300"
            >
              ({{ t('userSubscriptions.resetVote.initiatorTag') }})
            </span>
          </span>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  getAccountResetVotes,
  type AdminAccountResetVoteDetails,
  type ResetVoteParticipant
} from '@/api/admin/accounts'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  show: boolean
  account: { id: number; name?: string } | null
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()

const loading = ref(false)
const errorMessage = ref('')
const details = ref<AdminAccountResetVoteDetails | null>(null)
let activeRequestId = 0

const modalTitle = computed(() => {
  const base = t('userSubscriptions.resetVote.adminModalTitle')
  return props.account?.name ? `${base} - ${props.account.name}` : base
})

const statusLabel = computed(() => {
  const status = details.value?.status
  switch (status) {
    case 'open':
      return t('userSubscriptions.resetVote.voteAction')
    case 'executing':
      return t('userSubscriptions.resetVote.executing')
    case 'succeeded':
      return t('userSubscriptions.resetVote.succeeded')
    case 'failed':
      return t('userSubscriptions.resetVote.failed')
    default:
      return t('userSubscriptions.resetVote.statusNone')
  }
})

const statusBadgeClass = computed(() => {
  switch (details.value?.status) {
    case 'open':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    case 'executing':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    case 'succeeded':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'failed':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    default:
      return 'bg-gray-200 text-gray-700 dark:bg-dark-700 dark:text-dark-300'
  }
})

const roundFailureBanner = computed(() => {
  const d = details.value
  if (!d) return ''
  if (d.status === 'failed' && d.card_status === 'no_credit') {
    return d.error_message || t('userSubscriptions.resetVote.noCredit')
  }
  if (d.subscription_reset_status === 'failed') {
    return d.error_message || t('userSubscriptions.resetVote.subscriptionResetFailed')
  }
  return d.error_message || ''
})

function participantPrimaryName(p: ResetVoteParticipant): string {
  const username = p.username?.trim()
  if (username) return username
  const email = p.email?.trim()
  if (email) return email
  return `#${p.user_id}`
}

function participantSecondaryEmail(p: ResetVoteParticipant): string {
  const username = p.username?.trim()
  const email = p.email?.trim()
  if (username && email && username !== email) {
    return email
  }
  return ''
}

function formatGroups(groups?: string[]): string {
  return groups && groups.length > 0 ? groups.join(', ') : '-'
}

async function loadDetails() {
  const accountId = props.account?.id
  if (!accountId) return
  const requestId = ++activeRequestId
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await getAccountResetVotes(accountId)
    if (requestId !== activeRequestId) return
    details.value = res
  } catch (err: any) {
    if (requestId !== activeRequestId) return
    errorMessage.value = err?.message || t('userSubscriptions.resetVote.loadFailed')
  } finally {
    if (requestId === activeRequestId) {
      loading.value = false
    }
  }
}

watch(
  () => [props.show, props.account?.id] as const,
  ([show, accountId]) => {
    if (!show || !accountId) {
      activeRequestId++
      loading.value = false
      errorMessage.value = ''
      details.value = null
      return
    }
    details.value = null
    loadDetails()
  },
  { immediate: true }
)
</script>
