<template>
  <section class="card overflow-hidden" :aria-label="t('admin.audit.ipBans.title')">
    <div class="flex flex-wrap items-start justify-between gap-3 p-4 sm:px-6">
      <div class="min-w-0 flex-1">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">
          {{ t('admin.audit.ipBans.title') }}
        </h2>
        <p v-if="policy" class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t(policy.ban_duration_seconds === 0 ? 'admin.audit.ipBans.permanentPolicy' : 'admin.audit.ipBans.policy', {
            threshold: policy.threshold,
            window: formatDuration(policy.failure_window_seconds),
            duration: formatDuration(policy.ban_duration_seconds)
          }) }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm"
        :disabled="loading || unbanning"
        @click="fetchBans"
      >
        {{ t('common.refresh') }}
      </button>
    </div>

    <p v-if="loadError" role="alert" class="px-4 pb-4 text-sm text-red-600 dark:text-red-400 sm:px-6">
      {{ loadError }}
    </p>
    <div v-else :aria-busy="loading">
      <p v-if="loading && !policy" class="px-4 pb-4 text-sm text-gray-500 sm:px-6">
        {{ t('common.loading') }}
      </p>
      <p v-else-if="bans.length === 0" class="px-4 pb-4 text-sm text-gray-500 dark:text-gray-400 sm:px-6">
        {{ t('admin.audit.ipBans.empty') }}
      </p>
      <div v-else class="max-h-40 overflow-auto">
        <table class="w-full text-left text-sm">
          <thead class="sticky top-0 bg-gray-50 dark:bg-dark-800">
            <tr class="text-gray-500 dark:text-gray-400">
              <th class="whitespace-nowrap px-4 py-2 font-medium sm:px-6">{{ t('admin.audit.columns.clientIp') }}</th>
              <th class="whitespace-nowrap px-4 py-2 font-medium">{{ t('admin.audit.ipBans.failureCount') }}</th>
              <th class="whitespace-nowrap px-4 py-2 font-medium">{{ t('admin.audit.ipBans.blockedAt') }}</th>
              <th class="whitespace-nowrap px-4 py-2 font-medium">{{ t('admin.audit.ipBans.blockedUntil') }}</th>
              <th class="whitespace-nowrap px-4 py-2 font-medium">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="ban in bans" :key="ban.client_ip" class="text-gray-700 dark:text-gray-300">
              <td class="whitespace-nowrap px-4 py-2 font-mono sm:px-6">{{ ban.client_ip }}</td>
              <td class="px-4 py-2">{{ ban.failure_count }}</td>
              <td class="whitespace-nowrap px-4 py-2">{{ formatTime(ban.blocked_at) }}</td>
              <td class="whitespace-nowrap px-4 py-2">
                {{ ban.blocked_until ? formatTime(ban.blocked_until) : t('admin.audit.ipBans.permanent') }}
              </td>
              <td class="px-4 py-2">
                <button
                  type="button"
                  class="whitespace-nowrap font-medium text-primary-600 hover:text-primary-700 disabled:opacity-50 dark:text-primary-400"
                  :disabled="loading || unbanning"
                  @click="selectedIP = ban.client_ip"
                >
                  {{ t('admin.audit.ipBans.unban') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination
        v-if="total > pageSize"
        :total="total"
        :page="page"
        :page-size="pageSize"
        :show-page-size-selector="false"
        @update:page="onPageChange"
      />
    </div>

    <ConfirmDialog
      :show="selectedIP !== null"
      :title="t('admin.audit.ipBans.unbanTitle')"
      :message="t('admin.audit.ipBans.unbanMessage', { ip: selectedIP })"
      :confirm-text="t('admin.audit.ipBans.unban')"
      @confirm="confirmUnban"
      @cancel="selectedIP = null"
    />
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { auditAPI, type LoginIPBan, type LoginIPBanListResponse } from '@/api/admin/audit'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const bans = ref<LoginIPBan[]>([])
const policy = ref<LoginIPBanListResponse | null>(null)
const total = ref(0)
const page = ref(1)
const pageSize = ref(5)
const loading = ref(false)
const loadError = ref('')
const selectedIP = ref<string | null>(null)
const unbanning = ref(false)
let requestSequence = 0

function formatDuration(seconds: number): string {
  if (seconds % 3600 === 0) return t('admin.audit.ipBans.hours', { count: seconds / 3600 })
  if (seconds % 60 === 0) return t('admin.audit.ipBans.minutes', { count: seconds / 60 })
  return t('admin.audit.ipBans.seconds', { count: seconds })
}

function formatTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function errorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') {
    return error.message || fallback
  }
  return fallback
}

async function fetchBans() {
  const sequence = ++requestSequence
  loading.value = true
  loadError.value = ''
  try {
    const response = await auditAPI.listIPBans({ page: page.value, page_size: pageSize.value })
    if (sequence !== requestSequence) return
    policy.value = response
    total.value = response.total
    pageSize.value = response.page_size
    const lastPage = Math.max(1, Math.ceil(response.total / response.page_size))
    if (page.value > lastPage) {
      page.value = lastPage
      await fetchBans()
      return
    }
    page.value = response.page
    bans.value = response.items
  } catch (error: unknown) {
    if (sequence !== requestSequence) return
    loadError.value = errorMessage(error, t('admin.audit.ipBans.loadFailed'))
    appStore.showError(loadError.value)
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function onPageChange(value: number) {
  if (unbanning.value) return
  page.value = value
  fetchBans()
}

async function confirmUnban() {
  if (!selectedIP.value || unbanning.value) return
  const ip = selectedIP.value
  selectedIP.value = null
  unbanning.value = true
  try {
    await auditAPI.unbanIP(ip)
    appStore.showSuccess(t('admin.audit.ipBans.unbanSuccess', { ip }))
    await fetchBans()
  } catch (error: unknown) {
    appStore.showError(errorMessage(error, t('admin.audit.ipBans.unbanFailed')))
  } finally {
    unbanning.value = false
  }
}

onMounted(fetchBans)
onBeforeUnmount(() => { requestSequence++ })
</script>
