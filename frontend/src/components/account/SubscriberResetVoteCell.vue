<template>
  <div v-if="vote && vote.supported" class="flex min-w-[168px] flex-col gap-1">
    <!-- Active or completed round summary bar when round exists -->
    <div v-if="vote.status !== 'none'" class="flex flex-wrap items-center gap-1.5">
      <span
        v-if="vote.status === 'open'"
        class="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-semibold text-amber-800 dark:bg-amber-900/30 dark:text-amber-300"
      >
        {{ t('userSubscriptions.resetVote.progress', { count: vote.vote_count, threshold: vote.threshold }) }}
      </span>
      <span
        v-else-if="vote.status === 'executing'"
        class="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-[11px] font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300"
      >
        <Icon name="refresh" size="xs" class="animate-spin" />
        {{ t('userSubscriptions.resetVote.executing') }}
      </span>
      <span
        v-else-if="vote.status === 'succeeded'"
        class="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
      >
        <Icon name="check" size="xs" />
        {{ t('userSubscriptions.resetVote.succeeded') }}
      </span>
      <span
        v-else-if="vote.status === 'failed'"
        class="inline-flex items-center gap-1 rounded-full bg-rose-100 px-2 py-0.5 text-[11px] font-medium text-rose-700 dark:bg-rose-900/30 dark:text-rose-300"
        :title="failureReasonText"
      >
        <Icon name="exclamationCircle" size="xs" />
        {{ t('userSubscriptions.resetVote.failed') }}
      </span>

      <span
        v-if="vote.status === 'succeeded' && (vote.subscription_reset_count ?? 0) > 0"
        class="text-[11px] text-emerald-700 dark:text-emerald-300"
      >
        {{ t('userSubscriptions.resetVote.subscriptionsResetCount', { count: vote.subscription_reset_count }) }}
      </span>
    </div>

    <!-- Primary eligible action / status -->
    <template v-if="vote.eligible">
      <button
        v-if="vote.can_vote"
        type="button"
        :disabled="voting"
        :class="[
          'inline-flex w-fit items-center gap-1 rounded-md px-2.5 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50',
          isTriggeringVote
            ? 'bg-amber-600 text-white hover:bg-amber-700 dark:bg-amber-500 dark:hover:bg-amber-600'
            : 'bg-primary-600 text-white hover:bg-primary-700 dark:bg-primary-500 dark:hover:bg-primary-600'
        ]"
        @click="$emit('vote', account)"
      >
        <Icon name="refresh" size="xs" :class="{ 'animate-spin': voting }" />
        <span>{{ actionButtonLabel }}</span>
      </button>

      <div
        v-else-if="vote.status === 'open' && vote.has_voted"
        class="text-xs font-medium text-gray-600 dark:text-dark-300"
      >
        {{ t('userSubscriptions.resetVote.votedWaiting', { threshold: vote.threshold }) }}
      </div>

      <div
        v-else-if="vote.status === 'failed' && failureReasonText"
        class="max-w-[200px] truncate text-[11px] text-rose-600 dark:text-rose-400"
        :title="failureReasonText"
      >
        {{ failureReasonText }}
      </div>

      <!-- 24h round remaining / cooldown countdown -->
      <div
        v-if="countdownLabel"
        class="text-[11px] text-gray-500 dark:text-dark-400"
      >
        {{ countdownLabel }}
      </div>
    </template>

    <!-- Ineligible explanation (quota/standard group member or unsubscribed admin) -->
    <template v-else>
      <span class="text-xs text-gray-400 dark:text-dark-500">
        {{
          vote.ineligible_reason === 'quota_group_only'
            ? t('userSubscriptions.resetVote.quotaGroupForbidden')
            : t('userSubscriptions.resetVote.subscriptionRequired')
        }}
      </span>
      <span
        v-if="countdownLabel"
        class="text-[11px] text-gray-400 dark:text-dark-500"
      >
        {{ countdownLabel }}
      </span>
    </template>
  </div>
  <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { SubscriberAccountItem } from '@/api/subscriptions'

const props = defineProps<{
  account: SubscriberAccountItem
  voting?: boolean
}>()

defineEmits<{
  vote: [account: SubscriberAccountItem]
}>()

const { t } = useI18n()

const vote = computed(() => props.account.reset_vote)

const isTriggeringVote = computed(() => {
  if (!vote.value) return false
  return vote.value.status === 'open' && vote.value.vote_count + 1 >= vote.value.threshold
})

const actionButtonLabel = computed(() => {
  if (!vote.value) return ''
  if (vote.value.can_initiate || vote.value.status === 'none') {
    return t('userSubscriptions.resetVote.initiate')
  }
  if (isTriggeringVote.value) {
    return t('userSubscriptions.resetVote.triggerVoteAction')
  }
  return t('userSubscriptions.resetVote.voteAction')
})

const failureReasonText = computed(() => {
  const v = vote.value
  if (!v || v.status !== 'failed') return ''
  if (v.card_status === 'no_credit' || v.error_code === 'NO_RESET_CARD') {
    return t('userSubscriptions.resetVote.noCredit')
  }
  if (v.card_status === 'unknown' || v.error_code === 'RESET_CARD_UNKNOWN') {
    return t('userSubscriptions.resetVote.unknownResult')
  }
  if (v.card_status === 'succeeded' && v.subscription_reset_status === 'failed') {
    return t('userSubscriptions.resetVote.subscriptionResetFailed')
  }
  return v.error_message || ''
})

function formatRemainingWindow(expiresAt?: string): string {
  if (!expiresAt) return ''
  const diffMs = new Date(expiresAt).getTime() - Date.now()
  if (!Number.isFinite(diffMs) || diffMs <= 0) return ''
  const totalMinutes = Math.floor(diffMs / 60000)
  if (totalMinutes < 60) {
    return t('userSubscriptions.resetVote.lessThanOneHour')
  }
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  return t('userSubscriptions.resetVote.hoursMinutes', { hours, minutes })
}

const countdownLabel = computed(() => {
  const v = vote.value
  if (!v || v.status === 'none' || !v.expires_at) return ''
  const remaining = formatRemainingWindow(v.expires_at)
  if (!remaining) return ''
  if (v.status === 'open') {
    return t('userSubscriptions.resetVote.expiresIn', { time: remaining })
  }
  return t('userSubscriptions.resetVote.reinitiateIn', { time: remaining })
})
</script>
