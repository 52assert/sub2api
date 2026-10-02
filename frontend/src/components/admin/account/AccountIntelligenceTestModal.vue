<template>
  <BaseDialog :show="show" :title="t('intelligenceTests.title')" width="wide" @close="emit('close')">
    <form id="intelligence-test-form" class="space-y-5" @submit.prevent="submit">
      <div v-if="account" class="rounded-xl bg-gray-50 p-3 dark:bg-dark-700">
        <p class="font-medium text-gray-900 dark:text-gray-100">{{ account.name }}</p>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.sharedHint') }}</p>
      </div>
      <div class="space-y-1.5">
        <label id="intelligence-test-runner-label" class="input-label">{{ t('intelligenceTests.runner') }}</label>
        <Select
          v-model="runner"
          :aria-label="t('intelligenceTests.runner')"
          :options="runnerOptions"
          :disabled="submitting || runnerOptions.length === 1"
          data-testid="intelligence-test-runner"
        />
        <p v-if="runner === 'codex_cli'" class="text-xs text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.codexCliHint') }}</p>
      </div>
      <div class="grid gap-4 sm:grid-cols-2">
        <div class="space-y-1.5">
          <label id="intelligence-test-model-label" class="input-label">{{ t('intelligenceTests.model') }}</label>
          <Select
            v-model="model"
            :aria-label="t('intelligenceTests.model')"
            :options="modelOptions"
            :disabled="loadingModels || submitting"
            :placeholder="loadingModels ? t('common.loading') : t('intelligenceTests.selectModel')"
            searchable
            data-testid="intelligence-test-model"
          />
          <div v-if="modelError" class="space-y-2">
            <p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ modelError }}</p>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingModels" @click="loadModels">{{ t('common.tryAgain') }}</button>
          </div>
          <p v-else-if="!loadingModels && !models.length" class="text-sm text-gray-500">{{ t('intelligenceTests.noModels') }}</p>
        </div>
        <div class="space-y-1.5">
          <label id="intelligence-test-effort-label" class="input-label">{{ t('intelligenceTests.reasoningEffort') }}</label>
          <Select
            v-model="reasoningEffort"
            :aria-label="t('intelligenceTests.reasoningEffort')"
            :options="effortOptions"
            :disabled="submitting"
            data-testid="intelligence-test-effort"
          />
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.effortHint') }}</p>
        </div>
      </div>
      <TextArea
        id="intelligence-test-prompt"
        v-model="prompt"
        :label="t('intelligenceTests.prompt')"
        :disabled="submitting"
        required
        :rows="5"
      />
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('intelligenceTests.backgroundHint') }}</p>
      <p v-if="submitError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ submitError }}</p>
    </form>
    <template #footer>
      <router-link to="/intelligence-tests" class="mr-auto text-sm text-primary-600 hover:underline" @click="emit('close')">{{ t('intelligenceTests.viewResults') }}</router-link>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="submit" form="intelligence-test-form" class="btn btn-primary" :disabled="!canSubmit" data-testid="intelligence-test-submit">
        {{ submitting ? t('common.loading') : t('intelligenceTests.start') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { createIntelligenceTest, DEFAULT_INTELLIGENCE_TEST_PROMPT, type IntelligenceTestEffort, type IntelligenceTestRecord, type IntelligenceTestRunner } from '@/api/intelligenceTests'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import type { Account, ClaudeModel } from '@/types'
import { intelligenceTestTextModels } from '@/utils/intelligenceTestModels'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'created', record: IntelligenceTestRecord): void }>()
const { t } = useI18n()
const appStore = useAppStore()
const models = ref<ClaudeModel[]>([])
const model = ref('')
const runner = ref<IntelligenceTestRunner>('http')
const reasoningEffort = ref<IntelligenceTestEffort>('default')
const prompt = ref(DEFAULT_INTELLIGENCE_TEST_PROMPT)
const loadingModels = ref(false)
const submitting = ref(false)
const modelError = ref('')
const submitError = ref('')
let modelRequest = 0

const modelOptions = computed(() => models.value.map((item) => ({ value: item.id, label: item.display_name || item.id })))
const runnerOptions = computed(() => (props.account?.platform === 'openai' ? ['codex_cli', 'http'] as const : ['http'] as const).map((value) => ({ value, label: t(`intelligenceTests.runners.${value}`) })))
const effortOptions = computed(() => (['default', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const).map((effort) => ({ value: effort, label: t(`intelligenceTests.efforts.${effort}`) })))
const canSubmit = computed(() => !!props.account && !loadingModels.value && !submitting.value && models.value.some((item) => item.id === model.value) && !!prompt.value.trim())

function errorMessage(error: unknown, fallback: string): string {
  return error && typeof error === 'object' && 'message' in error && typeof error.message === 'string' ? error.message : fallback
}

async function loadModels() {
  if (!props.account) return
  const request = ++modelRequest
  const accountId = props.account.id
  loadingModels.value = true
  models.value = []
  model.value = ''
  modelError.value = ''
  try {
    const available = await adminAPI.accounts.getAvailableModels(accountId)
    if (request !== modelRequest || !props.show || props.account?.id !== accountId) return
    models.value = intelligenceTestTextModels(available, props.account)
    model.value = models.value[0]?.id ?? ''
  } catch (error) {
    if (request !== modelRequest) return
    modelError.value = errorMessage(error, t('intelligenceTests.loadModelsFailed'))
  } finally {
    if (request === modelRequest) loadingModels.value = false
  }
}

async function submit() {
  if (!canSubmit.value || !props.account) return
  submitting.value = true
  submitError.value = ''
  try {
    const record = await createIntelligenceTest(props.account.id, { model: model.value, runner: runner.value, reasoning_effort: reasoningEffort.value, prompt: prompt.value.trim() })
    appStore.showSuccess(t('intelligenceTests.submitted'), 6000)
    emit('created', record)
    emit('close')
  } catch (error) {
    submitError.value = errorMessage(error, t('intelligenceTests.submitFailed'))
  } finally {
    submitting.value = false
  }
}

watch([() => props.show, () => props.account?.id], ([show]) => {
  ++modelRequest
  if (!show) return
  runner.value = props.account?.platform === 'openai' ? 'codex_cli' : 'http'
  reasoningEffort.value = 'default'
  prompt.value = DEFAULT_INTELLIGENCE_TEST_PROMPT
  submitError.value = ''
  void loadModels()
}, { immediate: true })

onBeforeUnmount(() => { ++modelRequest })
</script>
