<template>
  <BaseDialog :show="show" :title="words.title" width="wide" @close="close">
    <div class="space-y-4 text-sm">
      <p class="font-medium">{{ account?.name }}</p>
      <p class="text-gray-500">{{ words.scope }}</p>
      <p v-if="error" role="alert" class="text-red-600">{{ error }}</p>
      <p v-if="success" role="status" class="text-green-600">{{ success }}</p>
      <p v-if="loading">{{ words.loading }}</p>
      <template v-else-if="preview">
        <div class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-700">
          <label class="flex items-center gap-2">
            <input v-model="enabled" type="checkbox" :disabled="busy" />
            {{ words.auto }}
          </label>
          <p class="text-xs text-gray-500">{{ words.automaticDetails }}</p>
          <p class="text-xs text-gray-500">{{ words.creditWarning }}</p>
          <label class="flex items-center gap-2">
            <input v-model="naturalProbeEnabled" type="checkbox" :disabled="busy" data-test="natural-window-probe" />
            {{ words.naturalProbe }}
          </label>
          <p class="text-xs text-gray-500">{{ words.naturalProbeDetails }}</p>
          <button class="btn btn-secondary" :disabled="busy || (enabled === preview.enabled && naturalProbeEnabled === preview.natural_probe_enabled)" @click="save">{{ words.save }}</button>
          <p>{{ words.state }}{{ stateLabel }}</p>
          <p v-if="preview.last_checked_at">{{ words.lastCheck }}{{ formatTime(preview.last_checked_at) }}</p>
          <p v-if="preview.last_event_at">{{ words.lastEvent }}{{ formatTime(preview.last_event_at) }}</p>
          <p v-if="preview.poll_error" role="alert" class="text-amber-600">{{ preview.poll_error === 'feed_rate_limited' ? words.rateLimited : words.pollError }}</p>
          <p v-if="preview.enabled && preview.next_check_at">{{ words.nextCheck }}{{ formatTime(preview.next_check_at) }}</p>
        </div>
        <p>{{ words.targets }} ({{ preview.subscriptions.length }})</p>
        <div class="max-h-64 overflow-auto">
          <table class="w-full text-left text-xs">
            <thead><tr><th class="p-2">{{ words.subscription }}</th><th class="p-2">{{ words.user }}</th><th class="p-2">{{ words.group }}</th><th class="p-2">{{ words.daily }}</th><th class="p-2">{{ words.weekly }}</th></tr></thead>
            <tbody><tr v-for="sub in preview.subscriptions" :key="sub.id" class="border-t dark:border-dark-600">
              <td class="p-2">#{{ sub.id }}</td><td class="p-2">#{{ sub.user_id }}</td><td class="p-2">{{ sub.group_name }}</td>
              <td class="p-2">${{ sub.daily_usage_usd.toFixed(4) }}</td><td class="p-2">${{ sub.weekly_usage_usd.toFixed(4) }}</td>
            </tr></tbody>
          </table>
        </div>
        <p v-if="!preview.subscriptions.length" class="text-gray-500">{{ words.empty }}</p>
        <div v-if="confirming" class="space-y-3 rounded-lg bg-amber-50 p-3 dark:bg-amber-900/20">
          <p>{{ words.confirmWarning }}</p>
          <button class="btn btn-primary" :disabled="busy" @click="reset">{{ words.confirm }}</button>
          <button class="btn btn-secondary ml-2" :disabled="busy" @click="confirming = false">{{ words.cancel }}</button>
        </div>
        <button v-else class="btn btn-primary" :disabled="busy || !preview.subscriptions.length" @click="prepareReset">{{ words.manual }}</button>
        <button class="btn btn-secondary ml-2" :disabled="busy || confirming" @click="load">{{ words.refresh }}</button>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { Account } from '@/types'
import { configureSubscriptionReset, getSubscriptionResetPreview, resetAccountGroupSubscriptions, type SubscriptionResetPreview } from '@/api/admin/accountSubscriptionReset'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { locale } = useI18n()
const zh = computed(() => locale.value.startsWith('zh'))
const words = computed(() => zh.value ? {
  title: '重置关联订阅', scope: '范围：该账号所在分组的全部有效用户订阅。仅重置日额度、周额度；月额度和到期时间不变。',
  auto: '官方全局重置后自动联动', automaticDetails: '每 10 分钟统一查询公告。公告后周用量接近零或相较公告前明确下降时执行；新公告后使用 gpt-6-astra 主动探测一次，提示词为 Reply with exactly OK，尽快启动本周期窗口；探测失败不重复发送，等待正常使用返回新快照。首次开启不处理历史公告。按公告时间补偿日、周额度，保留公告后新增的消费；同一分组订阅共用上游账号的周周期起点，按上游窗口时间对齐，首笔消费也计入已用。',
  creditWarning: '系统内手动或自动使用重置卡不会联动订阅。在官方页面用卡无法可靠识别，建议暂停此开关后操作；证据不足时不自动重置。',
  naturalProbe: '周窗口到期后主动探测', naturalProbeDetails: '默认关闭。开启后按账号已知的 7d 重置时间每分钟检查，到期后使用 gpt-6-astra 发送一次 Reply with exactly OK，尽快启动新窗口。无需官方重置公告，可单独开启；不清零关联订阅。失败不重发，开启前已到期的窗口不补探测。',
  save: '保存自动设置', state: '状态：', lastCheck: '最近查询：', lastEvent: '最近公告：', pollError: '公告接口查询失败，将退避重试。', rateLimited: '公告接口限流，正在等待上游允许重试。', nextCheck: '下次查询：',
  targets: '受影响的有效订阅', subscription: '订阅', user: '用户', group: '分组', daily: '日已用', weekly: '周已用', empty: '没有关联的有效订阅。',
  manual: '手动重置日、周额度', confirm: '确认重置全部所列订阅', confirmWarning: '将清零上面所有有效订阅当前的日、周已用额度。这不是使用上游重置卡，也不会改动账号上游额度。',
  cancel: '取消', refresh: '刷新', loading: '加载中…', saved: '自动联动设置已保存。', done: '已重置订阅数：', failed: '操作失败，请刷新后重试。'
} : {
  title: 'Reset linked subscriptions', scope: 'All active user subscriptions in this account’s groups. Reset daily and weekly usage only; monthly usage and expiry remain unchanged.',
  auto: 'Link official global resets automatically', automaticDetails: 'Poll every 10 minutes. Require fresh weekly usage near zero or a clear drop from the pre-announcement snapshot. After a new announcement, send one gpt-6-astra probe with “Reply with exactly OK” to start the new window promptly. Failed probes are not retried; wait for normal traffic to supply fresh usage. Ignore announcements predating activation. Compensate daily and weekly usage as of the announcement, preserving all later charges. Subscriptions in the same group share the verified upstream account cycle; the first charge counts toward usage.',
  creditWarning: 'Local manual/automatic reset cards never trigger subscription resets. Cards used on the official website cannot reliably be identified; disable this option before using them. Inconclusive evidence is not applied.',
  naturalProbe: 'Probe after weekly window expiry', naturalProbeDetails: 'Off by default. Check the account’s known 7d reset time every minute and send one gpt-6-astra request with “Reply with exactly OK” after expiry to start the next window. Works independently of official reset announcements and does not clear linked subscriptions. Failed probes are not retried; windows expired before activation are skipped.',
  save: 'Save automatic settings', state: 'Status: ', lastCheck: 'Last check: ', lastEvent: 'Last announcement: ', pollError: 'Feed query failed; retrying with backoff.', rateLimited: 'Feed rate limit reached; waiting until retry is allowed.', nextCheck: 'Next check: ',
  targets: 'Affected active subscriptions', subscription: 'Subscription', user: 'User', group: 'Group', daily: 'Daily usage', weekly: 'Weekly usage', empty: 'No linked active subscriptions.',
  manual: 'Reset daily and weekly usage', confirm: 'Confirm reset of all listed subscriptions', confirmWarning: 'Clear current daily and weekly usage for all subscriptions listed above. This does not consume an upstream reset card or change upstream quota.',
  cancel: 'Cancel', refresh: 'Refresh', loading: 'Loading…', saved: 'Automatic settings saved.', done: 'Subscriptions reset: ', failed: 'Operation failed. Refresh and retry.'
})
const preview = ref<SubscriptionResetPreview | null>(null)
const enabled = ref(false)
const naturalProbeEnabled = ref(false)
const loading = ref(false)
const busy = ref(false)
const confirming = ref(false)
const error = ref('')
const success = ref('')
let operationID = ''
let generation = 0
const states: Record<string, [string, string]> = {
  disabled: ['未启用', 'Disabled'], watching: ['等待新公告', 'Watching'], pending: ['等待新用量确认', 'Awaiting quota verification'],
  succeeded: ['联动重置完成', 'Reset completed'], reset_card_excluded: ['已排除：存在用卡尝试', 'Excluded: reset card attempt'],
  awaiting_usage: ['已探测，等待新额度及周期', 'Probe attempted; awaiting fresh quota and cycle'], superseded: ['已被更新的公告取代', 'Superseded by a newer announcement'],
  expired: ['核验超时，未重置', 'Verification expired'], probe_failed: ['探测失败，未重置', 'Probe failed'],
  missing_baseline: ['缺少公告前用量，待人工确认', 'Missing baseline'], no_observed_drop: ['未观察到明确下降，待人工确认', 'No observed usage drop'],
  natural_reset_possible: ['可能为自然重置，待人工确认', 'Possible natural rollover'], missing_weekly_window: ['缺少周额度数据，待人工确认', 'Missing weekly quota'],
  missing_subscription_history: ['缺少公告前订阅金额记录，待人工确认', 'Missing pre-announcement subscription history'],
  account_identity_changed: ['上游账号身份缺失或已变更，待人工确认', 'Upstream identity missing or changed'],
  unsupported_account: ['账号类型不支持', 'Unsupported account'], inactive_account: ['账号已停用', 'Inactive account']
}
const stateLabel = computed(() => states[preview.value?.status || 'disabled']?.[zh.value ? 0 : 1] || preview.value?.status)
function formatTime(value: string) { return new Date(value).toLocaleString() }
function fail(cause: unknown) { error.value = (cause as { response?: { data?: { message?: string } } }).response?.data?.message || words.value.failed }
async function load() {
  if (!props.account) return
  const accountID = props.account.id
  const current = ++generation
  loading.value = true
  error.value = ''
  try {
    const data = await getSubscriptionResetPreview(accountID)
    if (current !== generation) return
    preview.value = data
    enabled.value = data.enabled
    naturalProbeEnabled.value = data.natural_probe_enabled ?? false
    return current
  } catch (cause) { if (current === generation) fail(cause) }
  finally { if (current === generation) loading.value = false }
}
async function save() {
  if (!props.account || busy.value) return
  busy.value = true
  error.value = ''
  try { await configureSubscriptionReset(props.account.id, enabled.value, naturalProbeEnabled.value); await load(); success.value = words.value.saved }
  catch (cause) { fail(cause) } finally { busy.value = false }
}
async function prepareReset() {
  if (busy.value || !props.show) return
  const preparedGeneration = await load()
  if (preparedGeneration !== generation || !props.show || error.value || !preview.value?.subscriptions.length) return
  if (typeof crypto.randomUUID === 'function') operationID = crypto.randomUUID()
  else {
    const bytes = crypto.getRandomValues(new Uint8Array(16))
    bytes[6] = (bytes[6]! & 15) | 64
    bytes[8] = (bytes[8]! & 63) | 128
    const hex = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
    operationID = `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  confirming.value = true
  success.value = ''
}
async function reset() {
  if (!props.account || !preview.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const count = await resetAccountGroupSubscriptions(props.account.id, operationID, preview.value.fingerprint)
    confirming.value = false
    success.value = words.value.done + count
    await load()
    emit('updated')
  } catch (cause) { fail(cause) } finally { busy.value = false }
}
function close() { if (!busy.value) emit('close') }
watch(() => [props.show, props.account?.id], () => {
  generation++
  preview.value = null
  confirming.value = false
  success.value = ''
  if (props.show && props.account) void load()
}, { immediate: true })
</script>
