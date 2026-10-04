<template>
  <BaseDialog :show="show" :title="t('customAccountSchedules.title')" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
    <div class="space-y-5 text-sm">
      <p v-if="account" class="break-words font-medium text-gray-900 dark:text-gray-100">{{ account.name }}</p>
      <div class="space-y-2 rounded-xl bg-gray-50 p-4 text-gray-700 dark:bg-dark-900 dark:text-gray-300">
        <p><strong>{{ t('customAccountSchedules.actions.reset_card') }}：</strong>{{ t('customAccountSchedules.cardScope') }}</p>
        <p><strong>{{ t('customAccountSchedules.actions.reset_subscriptions') }}：</strong>{{ t('customAccountSchedules.subscriptionScope') }}</p>
        <p>{{ t('customAccountSchedules.independent') }}</p>
      </div>

      <div v-if="!loading && loaded" class="space-y-2" data-testid="schedule-target-groups">
        <p class="font-medium text-gray-900 dark:text-gray-100">{{ t('customAccountSchedules.targetGroups') }}</p>
        <div v-if="groups.length" class="flex flex-wrap gap-2">
          <span v-for="group in groups" :key="group.id" class="break-all rounded-md bg-primary-50 px-2 py-1 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ group.name }} (#{{ group.id }})</span>
        </div>
        <p v-else class="text-gray-500 dark:text-gray-400">{{ t('customAccountSchedules.noGroups') }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('customAccountSchedules.groupChanges') }}</p>
      </div>

      <p v-if="error" role="alert" class="whitespace-pre-wrap break-words text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="success" role="status" class="text-emerald-700 dark:text-emerald-400">{{ success }}</p>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="btn btn-primary" :disabled="busy || loading || !loaded" data-testid="schedule-add" @click="startNew">{{ t('customAccountSchedules.add') }}</button>
        <button type="button" class="btn btn-secondary" :disabled="busy || loading" data-testid="schedule-refresh" @click="load">{{ t('customAccountSchedules.refresh') }}</button>
      </div>

      <form v-if="formOpen" class="space-y-4 rounded-xl border border-primary-200 bg-primary-50/30 p-4 dark:border-primary-800 dark:bg-primary-900/10" data-testid="schedule-form" @submit.prevent="save">
        <h4 class="font-medium text-gray-900 dark:text-gray-100">{{ t(editingId === null ? 'customAccountSchedules.add' : 'customAccountSchedules.edit') }}</h4>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label :for="controlId('action')" class="mb-1 block font-medium">{{ t('customAccountSchedules.action') }}</label>
            <select :id="controlId('action')" v-model="form.action" class="input w-full" :disabled="busy" data-testid="schedule-action">
              <option value="reset_card">{{ t('customAccountSchedules.actions.reset_card') }}</option>
              <option value="reset_subscriptions">{{ t('customAccountSchedules.actions.reset_subscriptions') }}</option>
            </select>
          </div>
          <div>
            <label :for="controlId('frequency')" class="mb-1 block font-medium">{{ t('customAccountSchedules.frequency') }}</label>
            <select :id="controlId('frequency')" v-model="form.frequency" class="input w-full" :disabled="busy" data-testid="schedule-frequency">
              <option v-for="frequency in frequencies" :key="frequency" :value="frequency">{{ t(`customAccountSchedules.frequencies.${frequency}`) }}</option>
            </select>
          </div>
          <div v-if="form.frequency === 'once'">
            <label :for="controlId('run-at')" class="mb-1 block font-medium">{{ t('customAccountSchedules.runAt') }}</label>
            <input :id="controlId('run-at')" v-model="form.run_at" type="datetime-local" class="input w-full" :disabled="busy" data-testid="schedule-run-at" />
          </div>
          <div v-else-if="form.frequency !== 'cron'">
            <label :for="controlId('time')" class="mb-1 block font-medium">{{ t('customAccountSchedules.timeOfDay') }}</label>
            <input :id="controlId('time')" v-model="form.time_of_day" type="time" class="input w-full" :disabled="busy" data-testid="schedule-time" />
          </div>
          <div v-if="form.frequency === 'weekly'">
            <label :for="controlId('weekday')" class="mb-1 block font-medium">{{ t('customAccountSchedules.weekday') }}</label>
            <select :id="controlId('weekday')" v-model="form.weekday" class="input w-full" :disabled="busy" data-testid="schedule-weekday">
              <option :value="null">{{ t('customAccountSchedules.weekday') }}</option>
              <option v-for="(day, index) in weekdays" :key="day" :value="index">{{ t(`customAccountSchedules.weekdays.${day}`) }}</option>
            </select>
          </div>
          <div v-if="form.frequency === 'cron'" class="sm:col-span-2">
            <label :for="controlId('cron')" class="mb-1 block font-medium">{{ t('customAccountSchedules.cronExpression') }}</label>
            <input :id="controlId('cron')" v-model="form.cron_expression" class="input w-full font-mono" :disabled="busy" placeholder="0 8 * * *" data-testid="schedule-cron" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('customAccountSchedules.cronHint') }}</p>
            <div class="mt-2 flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="form.cron_expression = '0 8 * * *'">{{ t('customAccountSchedules.cronDaily') }} · <code>0 8 * * *</code></button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" data-testid="schedule-cron-weekly-example" @click="form.cron_expression = '0 8 * * 1'">{{ t('customAccountSchedules.cronWeekly') }} · <code>0 8 * * 1</code></button>
            </div>
          </div>
          <div>
            <label :for="controlId('timezone')" class="mb-1 block font-medium">{{ t('customAccountSchedules.timezone') }}</label>
            <input :id="controlId('timezone')" v-model="form.timezone" :list="controlId('timezones')" class="input w-full" :disabled="busy" data-testid="schedule-timezone" />
            <datalist :id="controlId('timezones')"><option v-for="zone in timezoneOptions" :key="zone" :value="zone" /></datalist>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('customAccountSchedules.timezoneHint') }}</p>
          </div>
        </div>
        <p class="text-sm text-gray-700 dark:text-gray-300" data-testid="schedule-action-scope">{{ t(form.action === 'reset_card' ? 'customAccountSchedules.cardScope' : 'customAccountSchedules.subscriptionScope') }}</p>
        <label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" :disabled="busy" data-testid="schedule-enabled" />{{ t('customAccountSchedules.enabled') }}</label>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="cancelForm">{{ t('customAccountSchedules.cancel') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="busy || loading || !loaded" data-testid="schedule-save">{{ t('customAccountSchedules.save') }}</button>
        </div>
      </form>

      <p v-if="loading" role="status" class="py-5 text-center text-gray-500">{{ t('customAccountSchedules.loading') }}</p>
      <p v-else-if="loaded && !schedules.length" class="rounded-xl border border-dashed border-gray-300 p-5 text-gray-500 dark:border-dark-600 dark:text-gray-400">{{ t('customAccountSchedules.empty') }}</p>
      <div v-else-if="loaded" class="space-y-3">
        <article v-for="schedule in schedules" :key="schedule.id" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600" :data-testid="`schedule-${schedule.id}`">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="min-w-0 space-y-1">
              <h4 class="break-words font-medium text-gray-900 dark:text-gray-100">{{ t(`customAccountSchedules.actions.${schedule.action}`) }}</h4>
              <p class="break-words text-xs text-gray-500 dark:text-gray-400">{{ scheduleDescription(schedule) }}</p>
            </div>
            <span class="rounded-full px-2 py-1 text-xs" :class="schedule.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'">{{ t(schedule.enabled ? 'customAccountSchedules.active' : 'customAccountSchedules.inactive') }}</span>
          </div>
          <dl class="grid gap-2 text-xs sm:grid-cols-2">
            <div><dt class="text-gray-500">{{ t('customAccountSchedules.nextRun') }}</dt><dd class="mt-1 break-words">{{ formatTime(schedule.next_run_at, schedule.timezone) }} · {{ schedule.timezone }}</dd></div>
            <div><dt class="text-gray-500">{{ t('customAccountSchedules.lastRun') }}</dt><dd class="mt-1 break-words">{{ formatTime(schedule.last_run_at, schedule.timezone) }} · {{ schedule.timezone }}</dd></div>
          </dl>
          <p v-if="schedule.last_status" class="break-words text-xs text-gray-600 dark:text-gray-300">{{ t('customAccountSchedules.lastResult') }}：{{ statusLabel(schedule.last_status) }}</p>
          <p v-if="schedule.last_message" class="whitespace-pre-wrap break-words text-xs text-gray-500 dark:text-gray-400">{{ schedule.last_message }}</p>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" :data-testid="`schedule-edit-${schedule.id}`" @click="startEdit(schedule)">{{ t('customAccountSchedules.edit') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" :data-testid="`schedule-toggle-${schedule.id}`" @click="toggle(schedule)">{{ t(schedule.enabled ? 'customAccountSchedules.disable' : needsReview(schedule) ? 'customAccountSchedules.reviewEnable' : 'customAccountSchedules.enable') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" :data-testid="`schedule-delete-${schedule.id}`" @click="deletingId = schedule.id">{{ t('customAccountSchedules.delete') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || loading" :aria-expanded="expandedId === schedule.id" :data-testid="`schedule-history-${schedule.id}`" @click="toggleHistory(schedule)">{{ t(expandedId === schedule.id ? 'customAccountSchedules.hideHistory' : 'customAccountSchedules.history') }}</button>
          </div>
          <div v-if="deletingId === schedule.id" class="space-y-2 rounded-lg bg-red-50 p-3 dark:bg-red-900/20">
            <p>{{ t('customAccountSchedules.deleteConfirm') }}</p>
            <button type="button" class="btn btn-danger btn-sm" :disabled="busy" :data-testid="`schedule-confirm-delete-${schedule.id}`" @click="remove(schedule)">{{ t('customAccountSchedules.confirmDelete') }}</button>
            <button type="button" class="btn btn-secondary btn-sm ml-2" :disabled="busy" @click="deletingId = null">{{ t('customAccountSchedules.cancel') }}</button>
          </div>
          <div v-if="expandedId === schedule.id" class="space-y-2 border-t border-gray-100 pt-3 dark:border-dark-600" :data-testid="`schedule-runs-${schedule.id}`">
            <p v-if="historyLoading" role="status">{{ t('customAccountSchedules.loading') }}</p>
            <div v-else-if="historyError" class="space-y-2"><p role="alert" class="text-red-600 dark:text-red-400">{{ historyError }}</p><button type="button" class="btn btn-secondary btn-sm" @click="loadRuns(schedule)">{{ t('customAccountSchedules.refresh') }}</button></div>
            <p v-else-if="!runs.length" class="text-gray-500">{{ t('customAccountSchedules.emptyHistory') }}</p>
            <div v-for="run in runs" v-else :key="run.id" class="space-y-1 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">
              <p class="font-medium">{{ statusLabel(run.status) }}</p>
              <p>{{ t('customAccountSchedules.action') }}：{{ t(`customAccountSchedules.actions.${run.action}`) }}</p>
              <p>{{ t('customAccountSchedules.plannedTime') }}：{{ formatTime(run.scheduled_at, schedule.timezone) }} · {{ schedule.timezone }}</p>
              <p>{{ t('customAccountSchedules.startedTime') }}：{{ formatTime(run.started_at, schedule.timezone) }}</p>
              <p v-if="run.finished_at">{{ t('customAccountSchedules.finishedTime') }}：{{ formatTime(run.finished_at, schedule.timezone) }}</p>
              <p v-if="run.action === 'reset_subscriptions'">{{ t('customAccountSchedules.resetCount', { count: run.reset_count }) }}</p>
              <p v-if="run.message" class="whitespace-pre-wrap break-words">{{ run.message }}</p>
            </div>
          </div>
        </article>
      </div>
    </div>
    <template #footer><button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ t('customAccountSchedules.close') }}</button></template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { Account } from '@/types'
import { createAccountActionSchedule, deleteAccountActionSchedule, listAccountActionScheduleRuns, listAccountActionSchedules, updateAccountActionSchedule, type AccountActionSchedule, type AccountActionScheduleRun, type AccountScheduleFrequency } from '@/api/admin/accountActionSchedules'
import { accountScheduleForm, accountScheduleInput, accountScheduleValidation, formatAccountScheduleTime, type AccountScheduleForm } from '@/utils/accountActionSchedules'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const { t, te, locale } = useI18n()
const schedules = ref<AccountActionSchedule[]>([])
const groups = ref<Array<{ id: number; name: string }>>([])
const serverTimezone = ref('UTC')
const loading = ref(false)
const loaded = ref(false)
const busy = ref(false)
const error = ref('')
const success = ref('')
const formOpen = ref(false)
const editingId = ref<number | null>(null)
const deletingId = ref<number | null>(null)
const expandedId = ref<number | null>(null)
const historyLoading = ref(false)
const historyError = ref('')
const runs = ref<AccountActionScheduleRun[]>([])
const form = ref<AccountScheduleForm>(newForm())
const frequencies: AccountScheduleFrequency[] = ['once', 'daily', 'weekly', 'cron']
const weekdays = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday']
const timezoneOptions = computed(() => [...new Set([serverTimezone.value, form.value.timezone, 'Asia/Shanghai', 'UTC', 'Asia/Hong_Kong', 'Asia/Tokyo', 'Europe/London', 'America/New_York'])])
let session = 0
let listRequest = 0
let historyRequest = 0

function newForm(): AccountScheduleForm { return { action: 'reset_card', frequency: 'once', timezone: serverTimezone.value, run_at: '', time_of_day: '08:00', weekday: null, cron_expression: '0 8 * * *', enabled: true } }
function controlId(field: string) { return `account-action-schedule-${props.account?.id || 'none'}-${field}` }
function formatTime(value: string | null | undefined, timezone: string) { return formatAccountScheduleTime(value, timezone, locale.value) }
function statusLabel(status: string) { const key = `customAccountSchedules.statuses.${status}`; return te(key) ? t(key) : status }
function scheduleDescription(schedule: AccountActionSchedule) {
  if (schedule.frequency === 'once') return t('customAccountSchedules.onceAt', { time: formatTime(schedule.run_at, schedule.timezone), timezone: schedule.timezone })
  if (schedule.frequency === 'cron') return t('customAccountSchedules.cronAt', { expression: schedule.cron_expression || '', timezone: schedule.timezone })
  if (schedule.frequency === 'weekly') return t('customAccountSchedules.weeklyAt', { day: t(`customAccountSchedules.weekdays.${weekdays[schedule.weekday ?? 0]}`), time: schedule.time_of_day || '', timezone: schedule.timezone })
  return t('customAccountSchedules.dailyAt', { time: schedule.time_of_day || '', timezone: schedule.timezone })
}
function errorMessage(cause: unknown, fallback: string): string {
  const value = cause as { message?: string; response?: { data?: { message?: string } } } | null
  return value?.response?.data?.message || value?.message || t(fallback)
}
function close() { if (!busy.value) emit('close') }
function cancelForm() { formOpen.value = false; editingId.value = null }
function startNew() { editingId.value = null; form.value = newForm(); formOpen.value = true; error.value = ''; success.value = ''; deletingId.value = null }
function startEdit(schedule: AccountActionSchedule) { editingId.value = schedule.id; form.value = accountScheduleForm(schedule); formOpen.value = true; error.value = ''; success.value = ''; deletingId.value = null }
function needsReview(schedule: AccountActionSchedule) { return !schedule.enabled && (schedule.action === 'reset_subscriptions' || schedule.last_status === 'failed' || schedule.last_status === 'unknown') }

async function load(): Promise<boolean> {
  if (!props.show || !props.account) return false
  const accountId = props.account.id
  const current = session
  const request = ++listRequest
  loading.value = true
  error.value = ''
  try {
    const data = await listAccountActionSchedules(accountId)
    if (current !== session || request !== listRequest) return false
    schedules.value = data.items ?? []
    groups.value = data.groups ?? []
    serverTimezone.value = data.timezone || 'UTC'
    loaded.value = true
    expandedId.value = null
    return true
  } catch (cause) {
    if (current === session && request === listRequest) { loaded.value = false; error.value = errorMessage(cause, 'customAccountSchedules.loadFailed') }
    return false
  }
  finally { if (current === session && request === listRequest) loading.value = false }
}

async function mutate(operation: (accountId: number) => Promise<unknown>, message: string, after?: () => void) {
  if (!props.show || !props.account || busy.value || loading.value || !loaded.value) return
  const accountId = props.account.id
  const current = session
  busy.value = true
  error.value = ''
  success.value = ''
  try {
    await operation(accountId)
    if (current !== session) return
    after?.()
    success.value = t(message)
    emit('updated')
    await load()
  } catch (cause) { if (current === session) error.value = errorMessage(cause, 'customAccountSchedules.operationFailed') }
  finally { if (current === session) busy.value = false }
}

async function save() {
  if (busy.value || loading.value || !loaded.value) return
  const invalid = accountScheduleValidation(form.value)
  if (invalid) { error.value = t(`customAccountSchedules.validation.${invalid}`); return }
  if (form.value.action === 'reset_subscriptions' && !groups.value.length) { error.value = t('customAccountSchedules.validation.noGroups'); return }
  const input = accountScheduleInput(form.value)
  const id = editingId.value
  await mutate(accountId => id === null ? createAccountActionSchedule(accountId, input) : updateAccountActionSchedule(accountId, id, input), 'customAccountSchedules.saved', cancelForm)
}

async function toggle(schedule: AccountActionSchedule) {
  if (busy.value || loading.value || !loaded.value) return
  if (needsReview(schedule)) {
    const current = session
    if (!await load() || current !== session) return
    const latest = schedules.value.find(item => item.id === schedule.id)
    if (latest) { startEdit(latest); form.value.enabled = true }
    return
  }
  const input = accountScheduleInput({ ...accountScheduleForm(schedule), enabled: !schedule.enabled })
  await mutate(accountId => updateAccountActionSchedule(accountId, schedule.id, input), 'customAccountSchedules.toggled')
}

async function remove(schedule: AccountActionSchedule) {
  await mutate(accountId => deleteAccountActionSchedule(accountId, schedule.id), 'customAccountSchedules.deleted', () => { deletingId.value = null; if (editingId.value === schedule.id) cancelForm() })
}

async function loadRuns(schedule: AccountActionSchedule) {
  if (!props.show || !props.account) return
  const current = session
  const request = ++historyRequest
  const accountId = props.account.id
  historyLoading.value = true
  historyError.value = ''
  runs.value = []
  try {
    const data = await listAccountActionScheduleRuns(accountId, schedule.id, 20)
    if (current === session && request === historyRequest) runs.value = data
  } catch (cause) { if (current === session && request === historyRequest) historyError.value = errorMessage(cause, 'customAccountSchedules.historyFailed') }
  finally { if (current === session && request === historyRequest) historyLoading.value = false }
}
async function toggleHistory(schedule: AccountActionSchedule) {
  if (expandedId.value === schedule.id) { expandedId.value = null; historyRequest++; return }
  expandedId.value = schedule.id
  await loadRuns(schedule)
}

watch(() => [props.show, props.account?.id], () => {
  session++; listRequest++; historyRequest++
  schedules.value = []; groups.value = []; serverTimezone.value = 'UTC'; loaded.value = false; loading.value = false; busy.value = false
  error.value = ''; success.value = ''; formOpen.value = false; editingId.value = null; deletingId.value = null; expandedId.value = null
  runs.value = []; historyLoading.value = false; historyError.value = ''
  if (props.show && props.account) void load()
}, { immediate: true })
onBeforeUnmount(() => { session++; listRequest++; historyRequest++ })
</script>
