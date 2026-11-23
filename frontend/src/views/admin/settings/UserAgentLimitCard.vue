<template>
  <div class="card" data-testid="user-agent-limit-card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex items-center gap-2">
        <Icon name="shield" size="md" class="text-primary-500" />
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('admin.settings.userAgentLimit.title') }}
        </h2>
      </div>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.userAgentLimit.description') }}
      </p>
    </div>

    <div class="space-y-5 p-6">
      <div v-if="loading" class="flex items-center gap-2 text-gray-500">
        <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
        {{ t('common.loading') }}
      </div>

      <template v-else>
        <div
          class="rounded-lg border border-sky-200 bg-sky-50 p-4 dark:border-sky-800 dark:bg-sky-900/20"
        >
          <div class="flex items-start">
            <Icon name="infoCircle" size="md" class="mt-0.5 flex-shrink-0 text-sky-500" />
            <p class="ml-3 text-sm text-sky-700 dark:text-sky-300">
              {{ t('admin.settings.userAgentLimit.dedupRuleNote') }}
            </p>
          </div>
        </div>

        <div class="flex items-center justify-between">
          <div>
            <label class="font-medium text-gray-900 dark:text-white">
              {{ t('admin.settings.userAgentLimit.enabled') }}
            </label>
            <p class="text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.userAgentLimit.enabledHint') }}
            </p>
          </div>
          <Toggle v-model="form.enabled" data-testid="ua-limit-enabled-toggle" />
        </div>

        <div
          v-if="form.enabled"
          class="space-y-5 border-t border-gray-100 pt-4 dark:border-dark-700"
        >
          <div class="grid grid-cols-1 gap-6 sm:grid-cols-2">
            <div>
              <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.settings.userAgentLimit.maxUserAgents') }}
              </label>
              <div class="flex items-center gap-2">
                <input
                  v-model.number="form.max_user_agents"
                  data-testid="ua-limit-max-input"
                  type="number"
                  min="1"
                  max="1000"
                  class="input w-32"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.userAgentLimit.countUnit') }}
                </span>
              </div>
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.userAgentLimit.maxUserAgentsHint') }}
              </p>
            </div>

            <div>
              <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.settings.userAgentLimit.windowDays') }}
              </label>
              <div class="flex items-center gap-2">
                <input
                  v-model.number="form.window_days"
                  data-testid="ua-limit-window-days-input"
                  type="number"
                  min="1"
                  max="365"
                  class="input w-32"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.userAgentLimit.daysUnit') }}
                </span>
              </div>
              <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.userAgentLimit.windowDaysHint') }}
              </p>
            </div>
          </div>

          <div
            class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
          >
            <div>
              <label class="font-medium text-gray-900 dark:text-white">
                {{ t('admin.settings.userAgentLimit.exemptAdmin') }}
              </label>
              <p class="text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.userAgentLimit.exemptAdminHint') }}
              </p>
            </div>
            <Toggle v-model="form.exempt_admin" data-testid="ua-limit-exempt-admin-toggle" />
          </div>
        </div>

        <div class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700">
          <button
            type="button"
            data-testid="ua-limit-save"
            :disabled="saving"
            class="btn btn-primary btn-sm"
            @click="saveSettings"
          >
            <svg
              v-if="saving"
              class="mr-1 h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const form = reactive({
  enabled: true,
  max_user_agents: 5,
  window_days: 30,
  exempt_admin: true
})

async function loadSettings(): Promise<void> {
  if (typeof adminAPI.settings?.getUserAgentLimitSettings !== 'function') {
    loading.value = false
    return
  }
  loading.value = true
  try {
    const data = await adminAPI.settings.getUserAgentLimitSettings()
    form.enabled = data.enabled
    form.max_user_agents = data.max_user_agents
    form.window_days = data.window_days
    form.exempt_admin = data.exempt_admin
  } catch (error) {
    console.error('Failed to load User-Agent limit settings:', error)
  } finally {
    loading.value = false
  }
}

async function saveSettings(): Promise<void> {
  if (typeof adminAPI.settings?.updateUserAgentLimitSettings !== 'function') {
    return
  }
  const maxUA = Number(form.max_user_agents)
  const windowDays = Number(form.window_days)
  if (
    !Number.isInteger(maxUA) ||
    maxUA < 1 ||
    maxUA > 1000 ||
    !Number.isInteger(windowDays) ||
    windowDays < 1 ||
    windowDays > 365
  ) {
    appStore.showError(t('admin.settings.userAgentLimit.invalidRange'))
    return
  }

  saving.value = true
  try {
    const saved = await adminAPI.settings.updateUserAgentLimitSettings({
      enabled: form.enabled,
      max_user_agents: maxUA,
      window_days: windowDays,
      exempt_admin: form.exempt_admin
    })
    form.enabled = saved.enabled
    form.max_user_agents = saved.max_user_agents
    form.window_days = saved.window_days
    form.exempt_admin = saved.exempt_admin
    appStore.showSuccess(t('admin.settings.userAgentLimit.saved'))
  } catch (error: unknown) {
    const msg =
      (error as { message?: string; response?: { data?: { message?: string } } })?.response?.data
        ?.message ||
      (error as { message?: string })?.message ||
      t('admin.settings.userAgentLimit.saveFailed')
    appStore.showError(msg)
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void loadSettings()
})
</script>
