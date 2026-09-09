<template>
  <div class="space-y-3 text-sm">
    <p class="text-center text-xl font-bold">v{{ app.currentVersion }}</p>
    <p v-if="loading" class="text-gray-500">{{ words.loading }}</p>
    <p v-else-if="info?.warning || error" role="alert" class="break-words text-red-600">
      {{ error || info?.warning }}
    </p>
    <template v-else-if="info">
      <p class="text-center text-gray-500">
        {{ info.has_update ? `${words.available} v${info.latest_version}` : words.latest }}
      </p>
      <p v-if="info.has_update && info.latest_version === info.current_version" class="text-xs text-gray-500">
        {{ words.newBuild }}
      </p>
      <a
        v-if="info.release_info?.html_url"
        :href="info.release_info.html_url"
        target="_blank"
        rel="noopener noreferrer"
        class="block text-center text-primary-600"
      >{{ words.notes }}</a>
    </template>

    <div v-if="job || disconnected" role="status" class="rounded-lg bg-gray-100 p-3 dark:bg-dark-700">
      <p>{{ disconnected ? words.reconnecting : words.stages[job!.stage] || job?.message }}</p>
      <p v-if="disconnected" class="mt-1 text-xs text-gray-500">{{ words.connectionHint }}</p>
      <p v-if="job?.stage === 'needs_attention'" class="mt-1 text-xs">{{ words.manual }}</p>
    </div>

    <div v-if="confirming" class="space-y-2 rounded-lg bg-amber-50 p-3 dark:bg-amber-900/20">
      <p>{{ words.interruption }}</p>
      <button class="w-full rounded bg-primary-600 px-3 py-2 text-white" :disabled="submitting" @click="start">
        {{ words.confirm }}
      </button>
      <button class="w-full px-3 py-1 text-gray-500" :disabled="submitting" @click="confirming = false">
        {{ words.cancel }}
      </button>
    </div>
    <button
      v-else-if="info?.has_update && !info.warning && !active && job?.stage !== 'needs_attention'"
      class="w-full rounded-lg bg-primary-600 px-3 py-2 text-white disabled:opacity-50"
      :disabled="submitting || loading"
      @click="confirming = true"
    >{{ words.update }}</button>
    <button v-if="!active && !confirming" class="w-full text-xs text-gray-500" @click="refresh">
      {{ words.refresh }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getContainerUpdateStatus, performUpdate, type ContainerUpdateJob, type VersionInfo } from '@/api/admin/system'

const app = useAppStore()
const { locale } = useI18n()
const words = computed(() => locale.value.startsWith('zh') ? {
  loading: '正在检查可用版本…', available: '可更新至', latest: '当前已是最新构建',
  newBuild: '此版本有新的定制构建，更新后版本号保持不变。', notes: '查看更新说明',
  update: '在线更新', refresh: '重新检查', confirm: '确认更新', cancel: '取消',
  interruption: '更新会备份数据并重启应用，期间短暂不可用，正在进行的请求可能中断。',
  reconnecting: '暂时无法连接应用，正在等待恢复…',
  connectionHint: '升级任务独立运行。若长时间未恢复，请管理员检查宿主机更新服务；当前尚不能确认升级结果。',
  manual: '备份已保留。请管理员检查任务记录，系统不会自动覆盖数据库。',
  unavailable: '无法获取版本或更新任务，请稍后重试。',
  stages: { queued: '升级任务已提交', pulling: '正在下载和验证镜像，现有服务仍可使用',
    backing_up: '正在备份数据库和数据，服务暂时不可用', recreating: '正在重建应用容器',
    checking: '正在检查新版本', succeeded: '镜像和应用容器更新完成',
    failed: '升级未完成，原应用已保留或恢复运行', needs_attention: '升级需要管理员处理' } as Record<string, string>
} : {
  loading: 'Checking for updates…', available: 'Update available:', latest: 'You are running the latest build',
  newBuild: 'A new custom build is available for the same version.', notes: 'Release notes',
  update: 'Update now', refresh: 'Check again', confirm: 'Confirm update', cancel: 'Cancel',
  interruption: 'The update backs up data and restarts the application. Brief downtime and interrupted requests are possible.',
  reconnecting: 'Application temporarily unreachable. Waiting to reconnect…',
  connectionHint: 'The update continues independently. If connectivity does not return, ask an administrator to inspect the host updater. The result is not yet confirmed.',
  manual: 'Backups are retained. An administrator must inspect the task; the database is never restored automatically.',
  unavailable: 'Unable to fetch the release or update task. Please retry.',
  stages: { queued: 'Update queued', pulling: 'Downloading and verifying the image; service remains available',
    backing_up: 'Backing up database and files; service temporarily unavailable', recreating: 'Recreating application container',
    checking: 'Checking the new version', succeeded: 'Image and application container updated',
    failed: 'Update failed; the previous application was retained or restarted', needs_attention: 'Administrator intervention required' } as Record<string, string>
})

const info = ref<VersionInfo | null>(null)
const job = ref<ContainerUpdateJob | null>(null)
const loading = ref(true)
const error = ref('')
const confirming = ref(false)
const submitting = ref(false)
const disconnected = ref(false)
const active = computed(() => submitting.value || disconnected.value || (!!job.value && !['succeeded', 'failed', 'needs_attention'].includes(job.value.stage)))
const pendingKey = 'sub2api.container-update.pending'
let timer: ReturnType<typeof setTimeout> | undefined
let stopped = false

function remember(id: string | null) {
  try {
    if (id) localStorage.setItem(pendingKey, id)
    else localStorage.removeItem(pendingKey)
  } catch { /* Storage may be unavailable; the host still retains task state. */ }
}

function pending(): string | null {
  try { return localStorage.getItem(pendingKey) } catch { return null }
}

async function poll() {
  if (stopped) return
  try {
    job.value = await getContainerUpdateStatus()
    disconnected.value = false
    if (job.value && ['succeeded', 'failed', 'needs_attention'].includes(job.value.stage)) {
      const ours = pending() === job.value.id
      if (ours) remember(null)
      if (ours && job.value.stage === 'succeeded') {
        app.clearVersionCache()
        window.location.reload()
      }
      return
    }
  } catch {
    disconnected.value = !!pending() || !!job.value
  }
  if (!stopped && (active.value || pending())) timer = setTimeout(poll, 3000)
}

async function refresh() {
  loading.value = true
  error.value = ''
  info.value = await app.fetchVersion(true)
  if (!info.value) error.value = words.value.unavailable
  loading.value = false
}

async function start() {
  if (submitting.value || !info.value?.container_image) return
  submitting.value = true
  error.value = ''
  try {
    const result = await performUpdate(info.value.container_image)
    if (!result.job) throw new Error(words.value.unavailable)
    job.value = result.job
    remember(result.job.id)
    confirming.value = false
    if (timer) clearTimeout(timer)
    timer = setTimeout(poll, 1000)
  } catch (cause) {
    const failure = cause as { response?: { data?: { message?: string } }; message?: string }
    error.value = failure.response?.data?.message || failure.message || words.value.unavailable
  } finally {
    submitting.value = false
  }
}

onMounted(() => { void refresh(); void poll() })
onBeforeUnmount(() => { stopped = true; if (timer) clearTimeout(timer) })
defineExpose({ refresh })
</script>
