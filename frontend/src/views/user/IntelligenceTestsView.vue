<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.retentionHint', { count: retention }) }}</p>
        <button class="btn btn-secondary btn-sm" :disabled="refreshing" @click="refresh">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" />
          {{ t('common.refresh') }}
        </button>
      </div>
      <div v-if="listError" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ listError }}</div>
      <div v-if="loading" class="card p-12 text-center text-gray-500">{{ t('common.loading') }}</div>
      <div v-else-if="!records.length && !listError" class="card p-12 text-center">
        <Icon name="sparkles" size="xl" class="mx-auto mb-3 text-primary-500" />
        <p class="font-medium text-gray-900 dark:text-gray-100">{{ t('intelligenceTests.empty') }}</p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.emptyHint') }}</p>
      </div>
      <div v-else-if="records.length" class="grid items-start gap-5 xl:grid-cols-[320px_minmax(0,1fr)]">
        <div class="space-y-3" :aria-label="t('intelligenceTests.records')">
          <button
            v-for="record in records"
            :key="record.id"
            type="button"
            class="card w-full space-y-2 p-4 text-left transition-colors hover:border-primary-400"
            :class="{ 'border-primary-500 ring-1 ring-primary-500': selectedId === record.id }"
            :aria-pressed="selectedId === record.id"
            :data-testid="`intelligence-test-record-${record.id}`"
            @click="selectRecord(record.id)"
          >
            <div class="flex items-start justify-between gap-2">
              <span class="break-all font-medium text-gray-900 dark:text-gray-100">{{ record.model }}</span>
              <span class="shrink-0 rounded-full px-2 py-1 text-xs font-medium" :class="statusClass(record.status)">{{ t(`intelligenceTests.statuses.${record.status}`) }}</span>
            </div>
            <p class="break-all text-xs text-gray-600 dark:text-gray-300">{{ t('intelligenceTests.account') }}: {{ accountName(record) }}</p>
            <div class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
              <span>{{ runnerLabel(record) }}</span>
              <span v-if="record.runner_version">{{ t('intelligenceTests.runnerVersion') }}: {{ record.runner_version }}</span>
              <span>{{ record.platform }}</span>
              <span>{{ t(`intelligenceTests.efforts.${record.reasoning_effort || 'default'}`) }}</span>
              <span v-if="record.duration_ms > 0">{{ formatDuration(record.duration_ms) }}</span>
            </div>
            <p v-if="record.effective_model && record.effective_model !== record.model" class="break-all text-xs text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.effectiveModel') }}: {{ record.effective_model }}</p>
            <p v-if="record.artifact_name" class="break-all text-xs text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.artifactName') }}: {{ record.artifact_name }}</p>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ formatTime(record.created_at) }}</p>
          </button>
        </div>
        <section class="card min-w-0 overflow-hidden" :aria-label="t('intelligenceTests.result')">
          <div v-if="detailLoading" class="p-12 text-center text-gray-500">{{ t('common.loading') }}</div>
          <div v-else-if="detailError" class="space-y-3 p-6">
            <p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ detailError }}</p>
            <button class="btn btn-secondary btn-sm" @click="loadSelected">{{ t('common.tryAgain') }}</button>
          </div>
          <template v-else-if="selected">
            <div class="space-y-4 border-b border-gray-100 p-5 dark:border-dark-700">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <h2 class="break-all text-lg font-semibold text-gray-900 dark:text-gray-100">{{ selected.model }}</h2>
                <span class="rounded-full px-3 py-1 text-xs font-medium" :class="statusClass(selected.status)">{{ t(`intelligenceTests.statuses.${selected.status}`) }}</span>
              </div>
              <dl class="grid gap-x-5 gap-y-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.account') }}</dt><dd class="mt-1 break-all text-gray-900 dark:text-gray-100" data-testid="intelligence-test-account">{{ accountName(selected) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.runner') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100" data-testid="intelligence-test-runner">{{ runnerLabel(selected) }}</dd></div>
                <div v-if="selected.runner_version"><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.runnerVersion') }}</dt><dd class="mt-1 break-all text-gray-900 dark:text-gray-100">{{ selected.runner_version }}</dd></div>
                <div v-if="selected.effective_model && selected.effective_model !== selected.model"><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.effectiveModel') }}</dt><dd class="mt-1 break-all text-gray-900 dark:text-gray-100">{{ selected.effective_model }}</dd></div>
                <div v-if="selected.artifact_name"><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.artifactName') }}</dt><dd class="mt-1 break-all text-gray-900 dark:text-gray-100">{{ selected.artifact_name }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.platform') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100">{{ selected.platform }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.reasoningEffort') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100">{{ t(`intelligenceTests.efforts.${selected.reasoning_effort || 'default'}`) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.duration') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100">{{ selected.duration_ms > 0 ? formatDuration(selected.duration_ms) : '—' }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.createdAt') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100">{{ formatTime(selected.created_at) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.startedAt') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100">{{ formatTime(selected.started_at) }}</dd></div>
                <div><dt class="text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.completedAt') }}</dt><dd class="mt-1 text-gray-900 dark:text-gray-100">{{ formatTime(selected.completed_at) }}</dd></div>
              </dl>
              <details>
                <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('intelligenceTests.prompt') }}</summary>
                <p class="mt-2 whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-sm text-gray-700 dark:bg-dark-900 dark:text-gray-300">{{ selected.prompt }}</p>
              </details>
              <details v-if="selected.final_message && selected.final_message !== selected.output" data-testid="intelligence-test-final-message">
                <summary class="cursor-pointer text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('intelligenceTests.finalMessage') }}</summary>
                <p class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-sm text-gray-700 dark:bg-dark-900 dark:text-gray-300">{{ selected.final_message }}</p>
              </details>
            </div>
            <div v-if="isIntelligenceTestActive(selected)" class="flex items-center gap-3 p-8 text-sm text-gray-500 dark:text-gray-400" data-testid="intelligence-test-active">
              <Icon name="refresh" size="md" class="shrink-0 animate-spin text-primary-500" />
              <p>{{ t('intelligenceTests.runningHint') }}</p>
            </div>
            <div v-else-if="selected.status === 'failed'" class="space-y-2 p-5">
              <p class="font-medium text-red-600 dark:text-red-400">{{ t('intelligenceTests.failed') }}</p>
              <p role="alert" class="whitespace-pre-wrap break-words text-sm text-red-600 dark:text-red-400">{{ selected.error || t('intelligenceTests.unknownError') }}</p>
            </div>
            <div v-if="selected.output" class="space-y-4 p-5">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div v-if="preview" class="flex gap-2" role="group" :aria-label="t('intelligenceTests.result')">
                  <button class="btn btn-sm" :class="tab === 'preview' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === 'preview'" @click="tab = 'preview'">{{ t('intelligenceTests.preview') }}</button>
                  <button class="btn btn-sm" :class="tab === 'source' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === 'source'" data-testid="intelligence-test-source-tab" @click="tab = 'source'">{{ t('intelligenceTests.source') }}</button>
                </div>
                <h3 v-else class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('intelligenceTests.result') }}</h3>
                <button class="btn btn-secondary btn-sm" @click="copyOutput">{{ t('intelligenceTests.copyOutput') }}</button>
              </div>
              <iframe
                v-if="tab === 'preview' && preview"
                :key="selected.id"
                :srcdoc="preview"
                sandbox="allow-scripts"
                referrerpolicy="no-referrer"
                :title="t('intelligenceTests.previewTitle')"
                class="h-[65vh] min-h-[400px] w-full rounded-xl border border-gray-200 bg-white dark:border-dark-600"
                data-testid="intelligence-test-preview"
              />
              <pre v-else-if="preview" class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words rounded-xl bg-gray-950 p-4 text-xs text-gray-100" data-testid="intelligence-test-source"><code>{{ selected.output }}</code></pre>
              <p v-else class="max-h-[70vh] overflow-auto whitespace-pre-wrap break-words rounded-xl bg-gray-50 p-4 text-sm leading-relaxed text-gray-900 dark:bg-dark-900 dark:text-gray-100" data-testid="intelligence-test-source">{{ selected.output }}</p>
            </div>
            <p v-else-if="selected.status === 'succeeded'" class="p-5 text-sm text-gray-500">{{ t('intelligenceTests.noOutput') }}</p>
          </template>
        </section>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { getIntelligenceTest, isIntelligenceTestActive, listIntelligenceTests, type IntelligenceTestRecord, type IntelligenceTestStatus } from '@/api/intelligenceTests'
import { buildIntelligenceTestPreview } from '@/utils/intelligenceTestPreview'
import { useClipboard } from '@/composables/useClipboard'

const { t, locale } = useI18n()
const route = useRoute()
const { copyToClipboard } = useClipboard()
const records = ref<IntelligenceTestRecord[]>([])
const selectedId = ref<number | null>(null)
const selected = ref<IntelligenceTestRecord | null>(null)
const retention = ref(10)
const loading = ref(true)
const refreshing = ref(false)
const detailLoading = ref(false)
const listError = ref('')
const detailError = ref('')
const tab = ref<'preview' | 'source'>('preview')
const preview = computed(() => selected.value?.output ? buildIntelligenceTestPreview(selected.value.output) : null)
let timer: ReturnType<typeof setTimeout> | undefined
let destroyed = false
let detailRequest = 0
let listController: AbortController | undefined
let detailController: AbortController | undefined

function errorMessage(error: unknown, fallback: string): string {
  return error && typeof error === 'object' && 'message' in error && typeof error.message === 'string' ? error.message : fallback
}

function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(locale.value)
}

function formatDuration(milliseconds: number): string {
  return t('intelligenceTests.durationSeconds', { seconds: (milliseconds / 1000).toFixed(1) })
}

function accountName(record: IntelligenceTestRecord): string {
  return record.account_name?.trim() || t('intelligenceTests.unknownAccount')
}

function runnerLabel(record: IntelligenceTestRecord): string {
  return t(`intelligenceTests.runners.${record.runner === 'codex_cli' ? 'codex_cli' : 'http'}`)
}

function statusClass(status: IntelligenceTestStatus): string {
  if (status === 'succeeded') return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
  if (status === 'failed') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (status === 'running') return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
  return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
}

async function loadSelected() {
  if (selectedId.value === null) return
  const id = selectedId.value
  const request = ++detailRequest
  detailController?.abort()
  detailController = new AbortController()
  detailLoading.value = selected.value?.id !== id
  detailError.value = ''
  try {
    const record = await getIntelligenceTest(id, { signal: detailController.signal })
    if (destroyed || request !== detailRequest) return
    selected.value = record
    if (record.output && !preview.value) tab.value = 'source'
  } catch (error) {
    if (!destroyed && request === detailRequest) detailError.value = errorMessage(error, t('intelligenceTests.loadDetailFailed'))
  } finally {
    if (request === detailRequest) detailLoading.value = false
  }
}

async function selectRecord(id: number) {
  selectedId.value = id
  tab.value = 'preview'
  await loadSelected()
}

function scheduleRefresh() {
  clearTimeout(timer)
  if (!destroyed && records.value.some(isIntelligenceTestActive)) timer = setTimeout(() => { void refresh() }, 5000)
}

async function refresh() {
  if (refreshing.value || destroyed) return
  refreshing.value = true
  listController = new AbortController()
  try {
    const response = await listIntelligenceTests({ signal: listController.signal })
    if (destroyed) return
    records.value = response.items
    retention.value = response.retention
    listError.value = ''
    if (!records.value.length) {
      ++detailRequest
      detailController?.abort()
      selectedId.value = null
      selected.value = null
    } else if (!records.value.some((record) => record.id === selectedId.value)) {
      const requestedId = Number(route.query.test)
      await selectRecord(records.value.find((record) => record.id === requestedId)?.id ?? records.value[0]!.id)
    } else {
      const current = records.value.find((record) => record.id === selectedId.value)
      if (!selected.value || isIntelligenceTestActive(selected.value) || current?.status !== selected.value.status) await loadSelected()
    }
  } catch (error) {
    if (!destroyed) listError.value = errorMessage(error, t('intelligenceTests.loadFailed'))
  } finally {
    loading.value = false
    refreshing.value = false
    scheduleRefresh()
  }
}

async function copyOutput() {
  if (!selected.value?.output) return
  await copyToClipboard(selected.value.output, t('intelligenceTests.copied'))
}

function onVisibilityChange() {
  if (!document.hidden) void refresh()
}

onMounted(() => {
  void refresh()
  document.addEventListener('visibilitychange', onVisibilityChange)
})
onBeforeUnmount(() => {
  destroyed = true
  ++detailRequest
  clearTimeout(timer)
  listController?.abort()
  detailController?.abort()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>
