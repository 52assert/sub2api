<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap-reverse items-start justify-between gap-3">
          <div class="grid w-full grid-cols-2 gap-3 sm:flex sm:w-auto sm:flex-wrap sm:items-center">
            <SearchInput
              v-model="params.search"
              :placeholder="t('admin.accounts.searchAccounts')"
              class="col-span-2 sm:w-52"
              @search="debouncedReload"
              @update:model-value="debouncedReload"
            />
            <Select
              :model-value="params.platform"
              class="min-w-0 sm:w-40"
              :options="platformOptions"
              @update:model-value="updatePlatform"
              @change="debouncedReload"
            />
            <Select
              :model-value="params.status"
              class="min-w-0 sm:w-40"
              :options="statusOptions"
              @update:model-value="updateStatus"
              @change="debouncedReload"
            />
            <Select
              v-if="groupOptions.length > 1"
              :model-value="params.group"
              class="col-span-2 min-w-0 sm:w-44"
              :options="groupOptions"
              @update:model-value="updateGroup"
              @change="debouncedReload"
            />
          </div>

          <div class="flex flex-wrap items-center justify-end gap-2">
            <button
              @click="handleManualRefresh"
              :disabled="loading"
              class="btn btn-secondary px-2 md:px-3"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="sm" :class="[loading ? 'animate-spin' : '']" />
              <span class="hidden md:inline">{{ t('common.refresh') }}</span>
            </button>

            <!-- Auto Refresh Dropdown -->
            <div class="relative" ref="autoRefreshDropdownRef">
              <button
                @click="showAutoRefreshDropdown = !showAutoRefreshDropdown"
                class="btn btn-secondary px-2 md:px-3"
                :title="t('admin.accounts.autoRefresh')"
              >
                <Icon name="refresh" size="sm" :class="[autoRefreshEnabled ? 'animate-spin' : '']" />
                <span class="hidden md:inline">
                  {{
                    autoRefreshEnabled
                      ? t('admin.accounts.autoRefreshCountdown', { seconds: autoRefreshCountdown })
                      : t('admin.accounts.autoRefresh')
                  }}
                </span>
              </button>
              <div
                v-if="showAutoRefreshDropdown"
                class="absolute right-0 z-50 mt-2 w-56 origin-top-right rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-700 dark:bg-dark-800"
              >
                <div class="p-2">
                  <button
                    @click="setAutoRefreshEnabled(!autoRefreshEnabled)"
                    class="flex w-full items-center justify-between rounded-md px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700"
                  >
                    <span>{{ t('admin.accounts.enableAutoRefresh') }}</span>
                    <Icon v-if="autoRefreshEnabled" name="check" size="sm" class="text-primary-500" />
                  </button>
                  <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
                  <button
                    v-for="sec in autoRefreshIntervals"
                    :key="sec"
                    @click="setAutoRefreshInterval(sec)"
                    class="flex w-full items-center justify-between rounded-md px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700"
                  >
                    <span>{{ autoRefreshIntervalLabel(sec) }}</span>
                    <Icon v-if="autoRefreshIntervalSeconds === sec" name="check" size="sm" class="text-primary-500" />
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>

      <template #table>
        <div class="flex min-h-0 flex-1 flex-col overflow-hidden" :aria-busy="loading" :inert="loading && accounts.length > 0">
          <DataTable
            :columns="allColumns"
            :data="accounts"
            :loading="loading && accounts.length === 0"
            row-key="id"
            :server-side-sort="true"
            @sort="handleSort"
            default-sort-key="name"
            default-sort-order="asc"
            :estimate-row-height="156"
            :overscan="5"
            :virtualize-threshold="50"
          >
            <template #cell-name="{ value }">
              <div class="flex flex-col">
                <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
              </div>
            </template>

            <template #cell-platform_type="{ row }">
              <div class="flex min-w-0 flex-col gap-1">
                <div class="flex flex-wrap items-center gap-1">
                  <PlatformTypeBadge
                    :platform="row.platform"
                    :type="row.type"
                    :auth-mode="getOpenAIAuthMode(row)"
                    :plan-type="getAccountPlanType(row)"
                    :privacy-mode="row.extra?.privacy_mode"
                    :subscription-expires-at="row.credentials?.subscription_expires_at"
                  />
                  <span
                    v-if="getAntigravityTierLabel(row)"
                    :class="['inline-block rounded px-1.5 py-0.5 text-[10px] font-medium', getAntigravityTierClass(row)]"
                  >
                    {{ getAntigravityTierLabel(row) }}
                  </span>
                </div>
                <div
                  v-if="getOpenAICompactMeta(row)"
                  :class="[
                    'inline-flex items-center gap-1.5 pl-0.5 text-[11px] font-medium leading-4',
                    getOpenAICompactMeta(row)?.className
                  ]"
                  :title="getOpenAICompactTitle(row)"
                >
                  <span :class="['h-1.5 w-1.5 rounded-full', getOpenAICompactMeta(row)?.dotClass]" />
                  <span>{{ getOpenAICompactMeta(row)?.label }}</span>
                </div>
              </div>
            </template>

            <template #cell-capacity="{ row }">
              <AccountCapacityCell :account="asAccount(row)" />
            </template>

            <template #cell-status="{ row }">
              <div class="flex items-center gap-1.5">
                <AccountStatusIndicator :account="asAccount(row)" :readonly="true" />
              </div>
            </template>

            <template #cell-schedulable="{ row }">
              <span
                :class="[
                  'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
                  row.schedulable
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                    : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
                ]"
              >
                {{ row.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled') }}
              </span>
            </template>

            <template #cell-today_stats="{ row }">
              <AccountTodayStatsCell
                :stats="todayStatsByAccountId[String(row.id)] ?? null"
                :loading="todayStatsLoading"
                :error="todayStatsError"
              />
            </template>

            <template #cell-groups="{ row }">
              <AccountGroupsCell :groups="(row.groups || []) as any" :max-display="4" />
            </template>

            <template #header-usage="{ column }">
              <div class="flex items-center">
                <span>{{ column.label }}</span>
                <HelpTooltip :content="t('admin.accounts.usageWindowsHint')" width-class="w-72" />
              </div>
            </template>

            <template #cell-usage="{ row }">
              <AccountUsageCell
                :account="asAccount(row)"
                :readonly="true"
                :today-stats="todayStatsByAccountId[String(row.id)] ?? null"
                :today-stats-loading="todayStatsLoading"
                :manual-refresh-token="usageManualRefreshToken"
                :batched-usage="usageBatchByAccountId[String(row.id)] ?? null"
                :batched-usage-error="usageBatchErrorByAccountId[String(row.id)] ?? null"
                :batched-usage-loading="usageBatchLoadingByAccountId[String(row.id)] === true"
                :request-batched-usage="isDesktopViewport ? queueBatchedUsage : null"
                @usage-loaded="handleAccountUsageLoaded(row.id, $event)"
              />
            </template>

            <template #header-reset_vote="{ column }">
              <div class="flex items-center">
                <span>{{ column.label }}</span>
                <HelpTooltip :content="t('userSubscriptions.resetVote.hint')" width-class="w-72" />
              </div>
            </template>

            <template #cell-reset_vote="{ row }">
              <SubscriberResetVoteCell
                :account="row"
                :voting="votingAccountIds[row.id] === true"
                @vote="handleVoteClick"
              />
            </template>

            <template #cell-last_used_at="{ value }">
              <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatRelativeTime(value) }}</span>
            </template>
          </DataTable>
        </div>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <ConfirmDialog
      :show="showVoteConfirmDialog"
      :title="t('userSubscriptions.resetVote.confirmTitle')"
      :message="voteConfirmMessage"
      :confirm-text="t('userSubscriptions.resetVote.confirmButton')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmVoteReset"
      @cancel="cancelVoteConfirm"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, toRaw, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import subscriptionsAPI, { type SubscriberAccountItem } from '@/api/subscriptions'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { useTableLoader } from '@/composables/useTableLoader'
import { useAppStore } from '@/stores/app'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Pagination from '@/components/common/Pagination.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountTodayStatsCell from '@/components/account/AccountTodayStatsCell.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import SubscriberResetVoteCell from '@/components/account/SubscriberResetVoteCell.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import type { Account, AccountUsageInfo, UserSubscription, WindowStats } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

const asAccount = (row: SubscriberAccountItem): Account => row as unknown as Account

const activeSubscriptions = ref<UserSubscription[]>([])
const subscribedGroups = computed(() => {
  const map = new Map<number, string>()
  for (const sub of activeSubscriptions.value) {
    if (sub.group_id && sub.group?.name) {
      map.set(sub.group_id, sub.group.name)
    }
  }
  for (const acc of accounts.value) {
    for (const g of acc.groups || []) {
      if (g.id && g.name && !map.has(g.id)) {
        map.set(g.id, g.name)
      }
    }
  }
  return Array.from(map.entries()).map(([id, name]) => ({ id, name }))
})

const platformOptions = computed(() => [
  { value: '', label: t('admin.accounts.allPlatforms') },
  ...CONCRETE_PLATFORM_OPTIONS
])

const statusOptions = computed(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'error', label: t('admin.accounts.status.error') },
  { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') },
  { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') },
  { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }
])

const groupOptions = computed(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  ...subscribedGroups.value.map((g) => ({ value: String(g.id), label: g.name }))
])

type AccountSortOrder = 'asc' | 'desc'
const sortState = reactive<{ sort_by: string; sort_order: AccountSortOrder }>({
  sort_by: 'name',
  sort_order: 'asc'
})

const showAutoRefreshDropdown = ref(false)
const autoRefreshDropdownRef = ref<HTMLElement | null>(null)
const AUTO_REFRESH_STORAGE_KEY = 'subscriber-account-auto-refresh'
const autoRefreshIntervals = [5, 10, 15, 30] as const
const autoRefreshEnabled = ref(false)
const autoRefreshIntervalSeconds = ref<(typeof autoRefreshIntervals)[number]>(30)
const autoRefreshCountdown = ref(0)
const autoRefreshFetching = ref(false)

const todayStatsByAccountId = ref<Record<string, WindowStats>>({})
const todayStatsLoading = ref(false)
const todayStatsError = ref<string | null>(null)
const todayStatsReqSeq = ref(0)
const pendingTodayStatsRefresh = ref(false)
const usageManualRefreshToken = ref(0)

const desktopViewportQuery = '(min-width: 768px)'
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(desktopViewportQuery).matches
)
let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null

const usageBatchByAccountId = ref<Record<string, AccountUsageInfo | null>>({})
const usageBatchErrorByAccountId = ref<Record<string, string | null>>({})
const usageBatchLoadingByAccountId = ref<Record<string, boolean>>({})
const usageBatchRequestTokenByAccountId = ref<Record<string, number>>({})
const usageBatchCache = new Map<number, { data: AccountUsageInfo; ts: number }>()
const USAGE_BATCH_CACHE_TTL = 5 * 60 * 1000
const pendingUsageBatchIds = new Set<number>()
let usageBatchFlushTimer: ReturnType<typeof setTimeout> | null = null
let queuedUsageBatchForce = false
let usageBatchRequestToken = 0

const buildDefaultTodayStats = (): WindowStats => ({
  requests: 0,
  tokens: 0,
  cost: 0,
  standard_cost: 0,
  user_cost: 0
})

const accountSupportsBatchUsage = (account: Pick<Account, 'platform' | 'type'>) => {
  if (account.platform === 'anthropic') {
    return account.type === 'oauth' || account.type === 'setup-token'
  }
  if (account.platform === 'gemini') return true
  if (account.platform === 'antigravity') return account.type === 'oauth'
  if (account.platform === 'openai') return account.type === 'oauth'
  if (account.platform === 'grok') return account.type === 'oauth'
  return false
}

const setUsageBatchLoading = (accountID: number, loadingState: boolean) => {
  usageBatchLoadingByAccountId.value = {
    ...usageBatchLoadingByAccountId.value,
    [String(accountID)]: loadingState
  }
}

const setUsageBatchState = (accountID: number, usage: AccountUsageInfo | null, error: string | null) => {
  const key = String(accountID)
  usageBatchByAccountId.value = {
    ...usageBatchByAccountId.value,
    [key]: usage
  }
  usageBatchErrorByAccountId.value = {
    ...usageBatchErrorByAccountId.value,
    [key]: error
  }
}

const handleAccountUsageLoaded = (accountID: number, usage: AccountUsageInfo) => {
  if (usageBatchByAccountId.value[String(accountID)] === usage) return
  setUsageBatchState(accountID, usage, null)
}

const flushQueuedUsageBatch = async () => {
  usageBatchFlushTimer = null
  const accountIDs = Array.from(pendingUsageBatchIds)
  pendingUsageBatchIds.clear()
  queuedUsageBatchForce = false

  if (accountIDs.length === 0) return

  const requestTokensByAccount = accountIDs.reduce<Record<string, number>>((acc, accountID) => {
    acc[String(accountID)] = usageBatchRequestTokenByAccountId.value[String(accountID)] ?? 0
    return acc
  }, {})

  try {
    const result = await subscriptionsAPI.getSubscriberAccountsBatchUsage(accountIDs)

    const usageMap = result.usage ?? {}
    const errorMap = result.errors ?? {}
    const now = Date.now()
    const nextUsage = { ...usageBatchByAccountId.value }
    const nextErrors = { ...usageBatchErrorByAccountId.value }
    const nextLoading = { ...usageBatchLoadingByAccountId.value }

    for (const accountID of accountIDs) {
      const key = String(accountID)
      if ((usageBatchRequestTokenByAccountId.value[key] ?? 0) !== requestTokensByAccount[key]) {
        continue
      }
      const usage = usageMap[key] ?? null
      nextUsage[key] = usage
      nextErrors[key] = errorMap[key] ?? null
      nextLoading[key] = false
      if (usage) {
        usageBatchCache.set(accountID, { data: usage, ts: now })
      } else {
        usageBatchCache.delete(accountID)
      }
    }

    usageBatchByAccountId.value = nextUsage
    usageBatchErrorByAccountId.value = nextErrors
    usageBatchLoadingByAccountId.value = nextLoading
  } catch (error) {
    const nextErrors = { ...usageBatchErrorByAccountId.value }
    const nextLoading = { ...usageBatchLoadingByAccountId.value }
    for (const accountID of accountIDs) {
      const key = String(accountID)
      if ((usageBatchRequestTokenByAccountId.value[key] ?? 0) !== requestTokensByAccount[key]) {
        continue
      }
      nextErrors[key] = 'Failed'
      nextLoading[key] = false
    }
    usageBatchErrorByAccountId.value = nextErrors
    usageBatchLoadingByAccountId.value = nextLoading
    console.error('Failed to load subscriber account usage batch:', error)
  }
}

const queueBatchedUsage = (account: Account, options?: { force?: boolean }) => {
  if (!isDesktopViewport.value) return
  if (!accountSupportsBatchUsage(account)) return

  const force = options?.force === true
  const cacheKey = account.id
  const key = String(cacheKey)

  if (force) {
    usageBatchCache.delete(cacheKey)
  } else {
    const cached = usageBatchCache.get(cacheKey)
    if (cached && Date.now() - cached.ts < USAGE_BATCH_CACHE_TTL) {
      setUsageBatchState(cacheKey, cached.data, null)
      setUsageBatchLoading(cacheKey, false)
      return
    }
  }

  usageBatchErrorByAccountId.value = {
    ...usageBatchErrorByAccountId.value,
    [key]: null
  }
  usageBatchRequestTokenByAccountId.value = {
    ...usageBatchRequestTokenByAccountId.value,
    [key]: ++usageBatchRequestToken
  }
  setUsageBatchLoading(cacheKey, true)
  pendingUsageBatchIds.add(cacheKey)
  queuedUsageBatchForce = queuedUsageBatchForce || force

  if (usageBatchFlushTimer !== null) return
  usageBatchFlushTimer = setTimeout(() => {
    void flushQueuedUsageBatch()
  }, 0)
}

const {
  items: accounts,
  loading,
  params,
  pagination,
  load: baseLoad,
  debouncedReload: baseDebouncedReload,
  handlePageChange: baseHandlePageChange,
  handlePageSizeChange: baseHandlePageSizeChange
} = useTableLoader<SubscriberAccountItem, any>({
  fetchFn: subscriptionsAPI.listSubscriberAccounts,
  initialParams: {
    platform: '',
    type: '',
    status: '',
    group: '',
    search: '',
    sort_by: sortState.sort_by,
    sort_order: sortState.sort_order
  }
})

const refreshTodayStatsBatch = async () => {
  const accountIDs = accounts.value.map((account) => account.id)
  const reqSeq = ++todayStatsReqSeq.value
  if (accountIDs.length === 0) {
    todayStatsByAccountId.value = {}
    todayStatsError.value = null
    todayStatsLoading.value = false
    return
  }

  todayStatsLoading.value = true
  todayStatsError.value = null

  try {
    const result = await subscriptionsAPI.getSubscriberAccountsBatchTodayStats(accountIDs)
    if (reqSeq !== todayStatsReqSeq.value) return
    const serverStats = result.stats ?? {}
    const nextStats: Record<string, WindowStats> = {}
    for (const accountID of accountIDs) {
      const key = String(accountID)
      nextStats[key] = serverStats[key] ?? buildDefaultTodayStats()
    }
    todayStatsByAccountId.value = nextStats
  } catch (error) {
    if (reqSeq !== todayStatsReqSeq.value) return
    todayStatsError.value = 'Failed'
    console.error('Failed to load subscriber account today stats:', error)
  } finally {
    if (reqSeq === todayStatsReqSeq.value) {
      todayStatsLoading.value = false
    }
  }
}

const load = async () => {
  await baseLoad()
  await refreshTodayStatsBatch()
}

const debouncedReload = () => {
  pendingTodayStatsRefresh.value = true
  baseDebouncedReload()
}

const handlePageChange = (page: number) => {
  pendingTodayStatsRefresh.value = true
  baseHandlePageChange(page)
}

const handlePageSizeChange = (size: number) => {
  pendingTodayStatsRefresh.value = true
  baseHandlePageSizeChange(size)
}

const handleSort = (key: string, order: AccountSortOrder) => {
  sortState.sort_by = key
  sortState.sort_order = order
  const requestParams = params as any
  requestParams.sort_by = key
  requestParams.sort_order = order
  pagination.page = 1
  pendingTodayStatsRefresh.value = true
  void load()
}

const updatePlatform = (value: string | number | boolean | null) => {
  params.platform = String(value ?? '')
}
const updateStatus = (value: string | number | boolean | null) => {
  params.status = String(value ?? '')
}
const updateGroup = (value: string | number | boolean | null) => {
  params.group = String(value ?? '')
}

const handleManualRefresh = async () => {
  await load()
  usageManualRefreshToken.value += 1
}

const autoRefreshIntervalLabel = (sec: number) => {
  if (sec === 5) return t('admin.accounts.refreshInterval5s')
  if (sec === 10) return t('admin.accounts.refreshInterval10s')
  if (sec === 15) return t('admin.accounts.refreshInterval15s')
  if (sec === 30) return t('admin.accounts.refreshInterval30s')
  return `${sec}s`
}

const loadSavedAutoRefresh = () => {
  try {
    const saved = localStorage.getItem(AUTO_REFRESH_STORAGE_KEY)
    if (!saved) return
    const parsed = JSON.parse(saved) as { enabled?: boolean; interval_seconds?: number }
    autoRefreshEnabled.value = parsed.enabled === true
    const interval = Number(parsed.interval_seconds)
    if (autoRefreshIntervals.includes(interval as any)) {
      autoRefreshIntervalSeconds.value = interval as any
    }
  } catch (e) {
    console.error('Failed to load saved auto refresh settings:', e)
  }
}

const saveAutoRefreshToStorage = () => {
  try {
    localStorage.setItem(
      AUTO_REFRESH_STORAGE_KEY,
      JSON.stringify({
        enabled: autoRefreshEnabled.value,
        interval_seconds: autoRefreshIntervalSeconds.value
      })
    )
  } catch (e) {
    console.error('Failed to save auto refresh settings:', e)
  }
}

if (typeof window !== 'undefined') {
  loadSavedAutoRefresh()
}

const refreshAccountsIncrementally = async () => {
  if (autoRefreshFetching.value) return
  autoRefreshFetching.value = true
  try {
    const result = await subscriptionsAPI.listSubscriberAccounts(
      pagination.page,
      pagination.page_size,
      toRaw(params) as {
        platform?: string
        type?: string
        status?: string
        group?: string
        search?: string
        sort_by?: string
        sort_order?: AccountSortOrder
      }
    )
    pagination.total = result.total || 0
    pagination.pages = result.pages || 0
    accounts.value = result.items || []
    await refreshTodayStatsBatch()
  } catch (error) {
    console.error('Subscriber accounts auto refresh failed:', error)
  } finally {
    autoRefreshFetching.value = false
  }
}

const { pause: pauseAutoRefresh, resume: resumeAutoRefresh } = useIntervalFn(
  async () => {
    if (!autoRefreshEnabled.value) return
    if (typeof document !== 'undefined' && document.hidden) return
    if (loading.value || autoRefreshFetching.value) return
    if (showAutoRefreshDropdown.value) return

    if (autoRefreshCountdown.value <= 0) {
      autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
      await refreshAccountsIncrementally()
      return
    }

    autoRefreshCountdown.value -= 1
  },
  1000,
  { immediate: false }
)

const setAutoRefreshEnabled = (enabled: boolean) => {
  autoRefreshEnabled.value = enabled
  saveAutoRefreshToStorage()
  if (enabled) {
    autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
    resumeAutoRefresh()
  } else {
    pauseAutoRefresh()
    autoRefreshCountdown.value = 0
  }
}

const setAutoRefreshInterval = (seconds: (typeof autoRefreshIntervals)[number]) => {
  autoRefreshIntervalSeconds.value = seconds
  saveAutoRefreshToStorage()
  if (autoRefreshEnabled.value) {
    autoRefreshCountdown.value = seconds
  }
}

watch(loading, (isLoading, wasLoading) => {
  if (wasLoading && !isLoading && pendingTodayStatsRefresh.value) {
    pendingTodayStatsRefresh.value = false
    refreshTodayStatsBatch().catch((error) => {
      console.error('Failed to refresh subscriber account today stats after table load:', error)
    })
  }
})

watch(accounts, (rows) => {
  const visibleIDs = new Set(rows.map((row) => String(row.id)))
  usageBatchByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
  usageBatchErrorByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchErrorByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
  usageBatchLoadingByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchLoadingByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
  usageBatchRequestTokenByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchRequestTokenByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
})

const allColumns = computed(() => [
  { key: 'name', label: t('admin.accounts.columns.name'), sortable: true },
  { key: 'platform_type', label: t('admin.accounts.columns.platformType'), sortable: false },
  { key: 'capacity', label: t('admin.accounts.columns.capacity'), sortable: false },
  { key: 'status', label: t('admin.accounts.columns.status'), sortable: true },
  { key: 'schedulable', label: t('admin.accounts.columns.schedulable'), sortable: true },
  { key: 'today_stats', label: t('admin.accounts.columns.todayStats'), sortable: false },
  { key: 'groups', label: t('admin.accounts.columns.groups'), sortable: false },
  { key: 'usage', label: t('admin.accounts.columns.usageWindows'), sortable: false },
  { key: 'reset_vote', label: t('userSubscriptions.resetVote.column'), sortable: false },
  { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true }
])

const votingAccountIds = ref<Record<number, boolean>>({})
const showVoteConfirmDialog = ref(false)
const pendingVoteAccount = ref<SubscriberAccountItem | null>(null)

const voteConfirmMessage = computed(() => {
  const acc = pendingVoteAccount.value
  const vote = acc?.reset_vote
  return t('userSubscriptions.resetVote.confirmMessage', {
    name: acc?.name || '',
    count: vote?.vote_count ?? 2,
    threshold: vote?.threshold ?? 3
  })
})

function resolveVoteFailureReason(state: SubscriberAccountItem['reset_vote']): string {
  if (!state) return t('userSubscriptions.resetVote.requestFailed')
  if (state.card_status === 'no_credit' || state.error_code === 'NO_RESET_CARD') {
    return t('userSubscriptions.resetVote.noCredit')
  }
  if (state.card_status === 'unknown' || state.error_code === 'RESET_CARD_UNKNOWN') {
    return t('userSubscriptions.resetVote.unknownResult')
  }
  if (state.card_status === 'succeeded' && state.subscription_reset_status === 'failed') {
    return t('userSubscriptions.resetVote.subscriptionResetFailed')
  }
  return state.error_message || t('userSubscriptions.resetVote.requestFailed')
}

async function submitVote(account: SubscriberAccountItem) {
  if (votingAccountIds.value[account.id]) return
  votingAccountIds.value = { ...votingAccountIds.value, [account.id]: true }
  try {
    const result = await subscriptionsAPI.castSubscriberAccountResetVote(account.id)
    accounts.value = accounts.value.map((item) =>
      item.id === account.id ? { ...item, reset_vote: result.state } : item
    )

    if (result.triggered_reset && result.reset_succeeded) {
      appStore.showSuccess(
        result.state.warning_code
          ? t('userSubscriptions.resetVote.triggeredSuccessWarning')
          : t('userSubscriptions.resetVote.triggeredSuccess', {
              count: result.state.subscription_reset_count ?? 0
            })
      )
      usageBatchCache.delete(account.id)
      pendingTodayStatsRefresh.value = true
      await Promise.all([
        subscriptionsAPI
          .getActiveSubscriptions()
          .then((subs) => {
            activeSubscriptions.value = subs || []
          })
          .catch(() => {}),
        load()
      ])
      usageManualRefreshToken.value += 1
    } else if (result.triggered_reset && !result.reset_succeeded) {
      appStore.showError(
        t('userSubscriptions.resetVote.failedToast', {
          reason: resolveVoteFailureReason(result.state)
        })
      )
      await load()
    } else {
      appStore.showSuccess(
        t('userSubscriptions.resetVote.votedToast', {
          count: result.state.vote_count,
          threshold: result.state.threshold
        })
      )
    }
  } catch (error: any) {
    const msg = error?.response?.data?.message || error?.message || t('userSubscriptions.resetVote.requestFailed')
    appStore.showError(msg)
    await load()
  } finally {
    const next = { ...votingAccountIds.value }
    delete next[account.id]
    votingAccountIds.value = next
  }
}

const handleVoteClick = (account: SubscriberAccountItem) => {
  const vote = account.reset_vote
  if (!vote || !vote.can_vote) return
  if (vote.status === 'open' && vote.vote_count + 1 >= vote.threshold) {
    pendingVoteAccount.value = account
    showVoteConfirmDialog.value = true
    return
  }
  void submitVote(account)
}

const confirmVoteReset = () => {
  const account = pendingVoteAccount.value
  showVoteConfirmDialog.value = false
  pendingVoteAccount.value = null
  if (account) {
    void submitVote(account)
  }
}

const cancelVoteConfirm = () => {
  showVoteConfirmDialog.value = false
  pendingVoteAccount.value = null
}

function firstNonBlankString(...values: unknown[]): string | undefined {
  return values.find((value): value is string => (
    typeof value === 'string' && value.trim().length > 0
  ))
}

function getAccountPlanType(row: SubscriberAccountItem): string | undefined {
  if (!row) return undefined
  if (row.platform === 'grok') {
    const extra = (row.extra || {}) as Record<string, any>
    const billing = extra.grok_billing_snapshot as Record<string, any> | undefined
    const usage = extra.grok_usage_snapshot as Record<string, any> | undefined
    const legacyQuota = extra.grok_quota_snapshot as Record<string, any> | undefined
    return firstNonBlankString(
      row.credentials?.subscription_tier,
      billing?.plan,
      usage?.subscription_tier,
      legacyQuota?.subscription_tier,
      extra.subscription_tier,
      row.credentials?.plan_type
    )
  }
  return firstNonBlankString(row.credentials?.plan_type)
}

function getOpenAIAuthMode(row: SubscriberAccountItem): string | undefined {
  if (!row || row.platform !== 'openai' || row.type !== 'oauth') return undefined
  const authMode = row.credentials?.auth_mode
  return typeof authMode === 'string' && authMode.trim() ? authMode : undefined
}

function getAntigravityTierFromRow(row: SubscriberAccountItem): string | null {
  if (row.platform !== 'antigravity') return null
  const extra = row.extra as Record<string, unknown> | undefined
  if (!extra) return null
  const lca = extra.load_code_assist as Record<string, unknown> | undefined
  if (!lca) return null
  const paid = lca.paidTier as Record<string, unknown> | undefined
  if (paid && typeof paid.id === 'string') return paid.id
  const current = lca.currentTier as Record<string, unknown> | undefined
  if (current && typeof current.id === 'string') return current.id
  return null
}

function getAntigravityTierLabel(row: SubscriberAccountItem): string | null {
  const tier = getAntigravityTierFromRow(row)
  switch (tier) {
    case 'free-tier': return t('admin.accounts.tier.free')
    case 'g1-pro-tier': return t('admin.accounts.tier.pro')
    case 'g1-ultra-tier': return t('admin.accounts.tier.ultra')
    default: return null
  }
}

function getAntigravityTierClass(row: SubscriberAccountItem): string {
  const tier = getAntigravityTierFromRow(row)
  switch (tier) {
    case 'free-tier': return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
    case 'g1-pro-tier': return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    case 'g1-ultra-tier': return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    default: return ''
  }
}

type OpenAICompactBadgeState = 'active' | 'blocked' | 'auto'

function getOpenAICompactState(row: SubscriberAccountItem): OpenAICompactBadgeState | null {
  if (row.platform !== 'openai' || (row.type !== 'oauth' && row.type !== 'apikey')) return null
  const extra = row.extra as Record<string, unknown> | undefined
  const mode = typeof extra?.openai_compact_mode === 'string' ? extra.openai_compact_mode : 'auto'
  if (mode === 'force_on') return 'active'
  if (mode === 'force_off') return 'blocked'
  if (typeof extra?.openai_compact_supported === 'boolean') {
    return extra.openai_compact_supported ? 'active' : 'blocked'
  }
  return 'auto'
}

function getOpenAICompactMeta(row: SubscriberAccountItem): { label: string; className: string; dotClass: string } | null {
  const state = getOpenAICompactState(row)
  if (!state) return null
  switch (state) {
    case 'active':
      return {
        label: t('admin.accounts.openai.compactSupported'),
        className: 'text-emerald-600 dark:text-emerald-300',
        dotClass: 'bg-emerald-500 shadow-[0_0_0_2px_rgba(16,185,129,0.14)]'
      }
    case 'blocked':
      return {
        label: t('admin.accounts.openai.compactUnsupported'),
        className: 'text-rose-600 dark:text-rose-300',
        dotClass: 'bg-rose-500 shadow-[0_0_0_2px_rgba(244,63,94,0.14)]'
      }
    case 'auto':
      return {
        label: t('admin.accounts.openai.compactAuto'),
        className: 'text-slate-500 dark:text-slate-400',
        dotClass: 'bg-slate-300 dark:bg-slate-500'
      }
  }
}

function getOpenAICompactTitle(row: SubscriberAccountItem): string {
  const extra = row.extra as Record<string, unknown> | undefined
  const checkedAt = typeof extra?.openai_compact_checked_at === 'string' ? extra.openai_compact_checked_at : ''
  const label = getOpenAICompactMeta(row)?.label || ''
  if (!checkedAt) return label
  return `${label} | ${t('admin.accounts.openai.compactLastChecked')}: ${formatDateTime(new Date(checkedAt))}`
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (autoRefreshDropdownRef.value && !autoRefreshDropdownRef.value.contains(target)) {
    showAutoRefreshDropdown.value = false
  }
}

onMounted(async () => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(desktopViewportQuery)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
    document.addEventListener('click', handleClickOutside)
  }

  subscriptionsAPI
    .getActiveSubscriptions()
    .then((subs) => {
      activeSubscriptions.value = subs || []
    })
    .catch(() => {
      activeSubscriptions.value = []
    })

  await load()
  if (autoRefreshEnabled.value) {
    autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
    resumeAutoRefresh()
  }
})

onUnmounted(() => {
  pauseAutoRefresh()
  if (usageBatchFlushTimer !== null) {
    clearTimeout(usageBatchFlushTimer)
    usageBatchFlushTimer = null
  }
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
  }
  if (typeof document !== 'undefined') {
    document.removeEventListener('click', handleClickOutside)
  }
})
</script>
