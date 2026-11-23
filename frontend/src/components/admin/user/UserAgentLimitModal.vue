<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.userAgentLimit.title')"
    width="wide"
    @close="$emit('close')"
  >
    <div v-if="user" class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{
            t('admin.users.userAgentLimit.subtitle', {
              email: user.email,
              days: windowDays
            })
          }}
        </p>
        <span
          v-if="detail"
          class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
          :class="summaryBadgeClass"
        >
          {{ summaryBadgeText }}
        </span>
      </div>

      <div v-if="loading" class="py-10 text-center text-sm text-gray-500">
        {{ t('common.loading') }}
      </div>

      <template v-else-if="detail">
        <!-- Per-user policy override -->
        <div
          class="space-y-3 rounded-xl border border-gray-200 bg-gray-50/60 p-4 dark:border-dark-700 dark:bg-dark-800/40"
        >
          <div class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.users.userAgentLimit.policyTitle') }}
          </div>

          <div class="flex flex-wrap gap-2">
            <button
              type="button"
              data-testid="ua-policy-mode-default"
              class="rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
              :class="
                policyMode === 'default'
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                  : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
              "
              @click="policyMode = 'default'"
            >
              {{
                t('admin.users.userAgentLimit.policyFollowGlobal', {
                  limit: detail.settings.max_user_agents
                })
              }}
            </button>
            <button
              type="button"
              data-testid="ua-policy-mode-exempt"
              class="rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
              :class="
                policyMode === 'exempt'
                  ? 'border-emerald-500 bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                  : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
              "
              @click="policyMode = 'exempt'"
            >
              {{ t('admin.users.userAgentLimit.policyExempt') }}
            </button>
            <button
              type="button"
              data-testid="ua-policy-mode-custom"
              class="rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
              :class="
                policyMode === 'custom'
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                  : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
              "
              @click="policyMode = 'custom'"
            >
              {{ t('admin.users.userAgentLimit.policyCustom') }}
            </button>
          </div>

          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div v-if="policyMode === 'custom'">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.users.userAgentLimit.customLimitLabel') }}
              </label>
              <input
                v-model.number="customLimitInput"
                data-testid="ua-policy-custom-input"
                type="number"
                min="1"
                max="1000"
                class="input w-full"
              />
            </div>

            <div :class="policyMode === 'custom' ? '' : 'sm:col-span-2'">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.users.userAgentLimit.noteLabel') }}
              </label>
              <input
                v-model="noteInput"
                data-testid="ua-policy-note-input"
                type="text"
                maxlength="500"
                class="input w-full"
                :placeholder="t('admin.users.userAgentLimit.notePlaceholder')"
              />
            </div>
          </div>

          <div class="flex justify-end">
            <button
              type="button"
              data-testid="ua-policy-save"
              class="btn btn-primary btn-sm"
              :disabled="savingPolicy"
              @click="savePolicy"
            >
              {{
                savingPolicy
                  ? t('common.saving')
                  : t('admin.users.userAgentLimit.savePolicy')
              }}
            </button>
          </div>
        </div>

        <!-- Active User-Agent records table -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <h3 class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.users.userAgentLimit.recordsTitle', { days: windowDays }) }}
            </h3>
            <button
              v-if="detail.records.length > 0"
              type="button"
              data-testid="ua-records-clear-all"
              class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
              :disabled="clearing"
              @click="showClearConfirm = true"
            >
              {{ t('admin.users.userAgentLimit.clearAll') }}
            </button>
          </div>

          <div
            v-if="detail.records.length === 0"
            class="rounded-xl border border-dashed border-gray-200 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
          >
            {{ t('admin.users.userAgentLimit.emptyRecords', { days: windowDays }) }}
          </div>

          <div v-else class="overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
            <table class="min-w-full text-sm">
              <thead class="bg-gray-50 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-400">
                <tr>
                  <th class="px-3 py-2 text-left font-medium">
                    {{ t('admin.users.userAgentLimit.columns.normalized') }}
                  </th>
                  <th class="px-3 py-2 text-left font-medium">
                    {{ t('admin.users.userAgentLimit.columns.raw') }}
                  </th>
                  <th class="px-3 py-2 text-right font-medium">
                    {{ t('admin.users.userAgentLimit.columns.requests') }}
                  </th>
                  <th class="px-3 py-2 text-left font-medium">
                    {{ t('admin.users.userAgentLimit.columns.lastSeen') }}
                  </th>
                  <th class="px-3 py-2 text-right font-medium">
                    {{ t('admin.users.userAgentLimit.columns.actions') }}
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-for="rec in detail.records" :key="rec.id">
                  <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">
                    {{ rec.ua_label }}
                  </td>
                  <td
                    class="max-w-[260px] truncate px-3 py-2 font-mono text-xs text-gray-500 dark:text-gray-400"
                    :title="rec.last_raw_ua"
                  >
                    {{ rec.last_raw_ua || '-' }}
                  </td>
                  <td class="px-3 py-2 text-right font-mono text-xs text-gray-600 dark:text-gray-300">
                    {{ rec.request_count }}
                  </td>
                  <td class="whitespace-nowrap px-3 py-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ formatDateTime(rec.last_seen_at) }}
                  </td>
                  <td class="px-3 py-2 text-right">
                    <button
                      type="button"
                      :data-testid="`ua-record-delete-${rec.id}`"
                      class="text-xs font-medium text-red-600 hover:underline disabled:opacity-50 dark:text-red-400"
                      :disabled="deletingId === rec.id"
                      @click="pendingDeleteRecord = rec"
                    >
                      {{ t('admin.users.userAgentLimit.removeRecord') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </div>

    <ConfirmDialog
      :show="showClearConfirm"
      :title="t('admin.users.userAgentLimit.clearAll')"
      :message="
        t('admin.users.userAgentLimit.clearAllConfirm', {
          email: user?.email || ''
        })
      "
      :danger="true"
      @confirm="confirmClearAll"
      @cancel="showClearConfirm = false"
    />

    <ConfirmDialog
      :show="pendingDeleteRecord !== null"
      :title="t('admin.users.userAgentLimit.removeRecord')"
      :message="
        t('admin.users.userAgentLimit.removeRecordConfirm', {
          label: pendingDeleteRecord?.ua_label || ''
        })
      "
      :danger="true"
      @confirm="confirmDeleteRecord"
      @cancel="pendingDeleteRecord = null"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  UserAgentLimitRecord,
  UserAgentLimitUserDetail
} from '@/api/admin/userAgentLimit'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import type { AdminUser } from '@/types'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  show: boolean
  user: AdminUser | null
}>()

const emit = defineEmits<{
  close: []
  updated: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const savingPolicy = ref(false)
const clearing = ref(false)
const deletingId = ref<number | null>(null)
const showClearConfirm = ref(false)
const pendingDeleteRecord = ref<UserAgentLimitRecord | null>(null)
const detail = ref<UserAgentLimitUserDetail | null>(null)

const policyMode = ref<'default' | 'exempt' | 'custom'>('default')
const customLimitInput = ref<number>(5)
const noteInput = ref('')

const windowDays = computed(() => detail.value?.settings.window_days ?? 30)

const summaryBadgeText = computed(() => {
  const s = detail.value?.summary
  if (!s) return '-'
  if (!s.enabled) return t('admin.users.userAgentLimit.disabledBadge')
  if (s.exempt) return t('admin.users.userAgentLimit.exemptBadge', { count: s.active_count })
  return `${s.active_count} / ${s.effective_limit}`
})

const summaryBadgeClass = computed(() => {
  const s = detail.value?.summary
  if (!s || !s.enabled) {
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
  if (s.exempt) {
    return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  }
  if (s.exceeded) {
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  }
  if (s.at_limit) {
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  }
  return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
})

function syncPolicyDraft(data: UserAgentLimitUserDetail): void {
  const custom = data.policy.max_user_agents
  if (custom === null || custom === undefined) {
    policyMode.value = 'default'
    customLimitInput.value = data.settings.max_user_agents || 5
  } else if (custom === 0) {
    policyMode.value = 'exempt'
    customLimitInput.value = data.settings.max_user_agents || 5
  } else {
    policyMode.value = 'custom'
    customLimitInput.value = custom
  }
  noteInput.value = data.policy.note || ''
}

async function loadDetail(): Promise<void> {
  if (!props.show || !props.user) return
  loading.value = true
  try {
    const data = await adminAPI.users.getUserAgentLimitDetail(props.user.id)
    detail.value = data
    syncPolicyDraft(data)
  } catch (error) {
    console.error('Failed to load User-Agent detail:', error)
    appStore.showError(t('admin.users.userAgentLimit.loadFailed'))
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.show, props.user?.id] as const,
  ([visible, uid]) => {
    if (visible && uid) {
      void loadDetail()
    } else if (!visible) {
      detail.value = null
      showClearConfirm.value = false
      pendingDeleteRecord.value = null
    }
  },
  { immediate: true }
)

async function savePolicy(): Promise<void> {
  if (!props.user || savingPolicy.value) return
  let maxUA: number | null = null
  if (policyMode.value === 'exempt') {
    maxUA = 0
  } else if (policyMode.value === 'custom') {
    const v = Number(customLimitInput.value)
    if (!Number.isInteger(v) || v < 1 || v > 1000) {
      appStore.showError(t('admin.users.userAgentLimit.invalidCustomLimit'))
      return
    }
    maxUA = v
  }

  savingPolicy.value = true
  try {
    const updated = await adminAPI.users.updateUserAgentLimitPolicy(props.user.id, {
      max_user_agents: maxUA,
      note: noteInput.value.trim()
    })
    detail.value = updated
    syncPolicyDraft(updated)
    appStore.showSuccess(t('admin.users.userAgentLimit.policySaved'))
    emit('updated')
  } catch (error) {
    console.error('Failed to update User-Agent policy:', error)
    appStore.showError(t('admin.users.userAgentLimit.policySaveFailed'))
  } finally {
    savingPolicy.value = false
  }
}

async function confirmDeleteRecord(): Promise<void> {
  const target = pendingDeleteRecord.value
  pendingDeleteRecord.value = null
  if (!props.user || !target) return
  deletingId.value = target.id
  try {
    await adminAPI.users.deleteUserAgentRecord(props.user.id, target.id)
    appStore.showSuccess(t('admin.users.userAgentLimit.recordRemoved'))
    await loadDetail()
    emit('updated')
  } catch (error) {
    console.error('Failed to remove User-Agent record:', error)
    appStore.showError(t('admin.users.userAgentLimit.removeRecordFailed'))
  } finally {
    deletingId.value = null
  }
}

async function confirmClearAll(): Promise<void> {
  showClearConfirm.value = false
  if (!props.user || clearing.value) return
  clearing.value = true
  try {
    const res = await adminAPI.users.clearUserAgentRecords(props.user.id)
    appStore.showSuccess(t('admin.users.userAgentLimit.cleared', { count: res.deleted_count ?? 0 }))
    await loadDetail()
    emit('updated')
  } catch (error) {
    console.error('Failed to clear User-Agent records:', error)
    appStore.showError(t('admin.users.userAgentLimit.clearFailed'))
  } finally {
    clearing.value = false
  }
}
</script>
