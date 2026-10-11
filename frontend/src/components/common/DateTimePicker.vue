<template>
  <button
    :id="id"
    ref="triggerRef"
    type="button"
    class="group flex min-h-[52px] w-full items-center gap-3 rounded-xl border bg-white px-3.5 py-3 text-left shadow-sm transition hover:border-primary-400 focus:outline-none focus-visible:ring-4 focus-visible:ring-primary-500/15 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-800"
    :class="isOpen ? 'border-primary-500 ring-4 ring-primary-500/10' : 'border-gray-200 dark:border-dark-600'"
    :disabled="disabled"
    :aria-label="triggerLabel"
    aria-haspopup="dialog"
    :aria-expanded="isOpen"
    :aria-controls="panelId"
    data-testid="datetime-picker-trigger"
    @click="togglePicker"
    @keydown.down.prevent="openPicker"
  >
    <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-400">
      <Icon name="calendar" size="md" />
    </span>
    <span v-if="committedDate" class="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-x-3 gap-y-1">
      <span class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{{ formatDate(committedDate) }}</span>
      <span class="inline-flex items-center gap-1.5 rounded-md bg-gray-50 px-2 py-1 text-sm font-semibold tabular-nums text-gray-600 dark:bg-dark-700 dark:text-gray-300">
        <Icon name="clock" size="sm" />{{ formatTime(committedDate) }}
      </span>
    </span>
    <span v-else class="flex-1 text-sm text-gray-400">{{ t('dateTimePicker.selectDateTime') }}</span>
    <Icon name="chevronDown" size="sm" class="shrink-0 text-gray-400 transition-transform" :class="isOpen && 'rotate-180'" />
  </button>

  <Teleport to="body">
    <Transition name="datetime-picker">
      <div
        v-if="isOpen"
        :id="panelId"
        ref="panelRef"
        role="dialog"
        :aria-label="t('dateTimePicker.selectDateTime')"
        aria-modal="false"
        class="fixed z-[70] flex flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl shadow-gray-900/15 dark:border-dark-600 dark:bg-dark-800 dark:shadow-black/30"
        :style="panelStyle"
        data-testid="datetime-picker-panel"
        @click.stop
        @keydown.tab="trapTab"
      >
        <div class="min-h-0 overflow-y-auto overscroll-contain p-4" data-testid="datetime-picker-content">
        <div class="mb-4 flex items-center justify-between gap-2">
          <button type="button" :class="navigationClass" :aria-label="navigationLabel(-1)" :disabled="!canNavigate(-1)" data-testid="datetime-picker-prev" @click="navigate(-1)">
            <Icon name="chevronLeft" size="sm" />
          </button>
          <div class="flex items-center gap-1">
            <button type="button" class="rounded-lg px-2 py-1.5 text-sm font-semibold text-gray-900 transition hover:bg-gray-100 dark:text-gray-100 dark:hover:bg-dark-700" :aria-label="t('dateTimePicker.selectMonth')" data-testid="datetime-picker-month" @click="calendarView = calendarView === 'months' ? 'days' : 'months'">
              {{ monthNames[viewMonth] }}<Icon name="chevronDown" size="xs" class="ml-1 inline-block text-gray-400" />
            </button>
            <button type="button" class="rounded-lg px-2 py-1.5 text-sm font-semibold tabular-nums text-gray-900 transition hover:bg-gray-100 dark:text-gray-100 dark:hover:bg-dark-700" :aria-label="t('dateTimePicker.selectYear')" data-testid="datetime-picker-year" @click="showYears">
              {{ viewYear }}<Icon name="chevronDown" size="xs" class="ml-1 inline-block text-gray-400" />
            </button>
          </div>
          <button type="button" :class="navigationClass" :aria-label="navigationLabel(1)" :disabled="!canNavigate(1)" data-testid="datetime-picker-next" @click="navigate(1)">
            <Icon name="chevronRight" size="sm" />
          </button>
        </div>

        <div v-if="calendarView === 'days'" :aria-label="t('dateTimePicker.date')">
          <div class="mb-1 grid grid-cols-7" aria-hidden="true">
            <span v-for="weekday in weekdayNames" :key="weekday" class="py-2 text-center text-[11px] font-medium text-gray-400 dark:text-gray-500">{{ weekday }}</span>
          </div>
          <div class="grid grid-cols-7 gap-y-1" role="grid" :aria-label="`${monthNames[viewMonth]} ${viewYear}`">
            <div v-for="week in calendarWeeks" :key="week[0].key" role="row" class="contents">
            <button
              v-for="day in week"
              :key="day.key"
              type="button"
              role="gridcell"
              :data-date="day.key"
              :aria-label="formatAccessibleDate(day.date)"
              :aria-selected="isSelectedDay(day.date)"
              :aria-current="day.isToday ? 'date' : undefined"
              :tabindex="day.key === focusedDayKey ? 0 : -1"
              :disabled="day.disabled"
              class="relative mx-auto flex h-9 w-9 items-center justify-center rounded-lg text-sm font-medium tabular-nums transition focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-400 focus-visible:ring-offset-1 disabled:cursor-not-allowed disabled:opacity-30 dark:focus-visible:ring-offset-dark-800"
              :class="isSelectedDay(day.date) ? 'bg-primary-600 text-white shadow-sm shadow-primary-600/20' : day.inMonth ? 'text-gray-700 hover:bg-primary-50 hover:text-primary-700 dark:text-gray-200 dark:hover:bg-primary-500/10 dark:hover:text-primary-300' : 'text-gray-400 hover:bg-gray-50 dark:text-gray-600 dark:hover:bg-dark-700'"
              @click="selectDay(day.date)"
              @keydown="onDayKeydown($event, day.date)"
            >
              {{ day.date.getDate() }}
              <span v-if="day.isToday" class="absolute bottom-1 h-1 w-1 rounded-full" :class="isSelectedDay(day.date) ? 'bg-white' : 'bg-primary-500'" aria-hidden="true"></span>
            </button>
            </div>
          </div>
        </div>

        <div v-else-if="calendarView === 'months'" class="grid min-h-[292px] grid-cols-3 content-center gap-2" :aria-label="t('dateTimePicker.selectMonth')">
          <button v-for="(month, index) in monthNames" :key="month" type="button" :disabled="!monthAvailable(viewYear, index)" :aria-pressed="viewMonth === index" :data-month="index" :class="choiceClass(viewMonth === index)" @click="chooseMonth(index)">{{ month }}</button>
        </div>

        <div v-else class="min-h-[292px]" :aria-label="t('dateTimePicker.selectYear')">
          <p class="mb-4 text-center text-xs font-medium tabular-nums text-gray-400" aria-live="polite">{{ yearPageStart }} – {{ yearPageStart + 11 }}</p>
          <div class="grid grid-cols-3 gap-2">
            <button v-for="year in yearChoices" :key="year" type="button" :disabled="!yearAvailable(year)" :aria-pressed="viewYear === year" :data-year="year" :class="choiceClass(viewYear === year)" @click="chooseYear(year)">{{ year }}</button>
          </div>
        </div>

        <div class="mt-4 flex items-center justify-between gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <span class="flex items-center gap-2 text-xs font-medium text-gray-500 dark:text-gray-400"><Icon name="clock" size="sm" />{{ t('dateTimePicker.time') }}</span>
          <div class="flex items-center gap-2">
            <div v-for="part in timeParts" :key="part" class="relative">
              <button
                type="button"
                class="flex min-w-[68px] items-center justify-between gap-2 rounded-lg border border-gray-200 bg-gray-50 px-2.5 py-2 text-sm font-semibold tabular-nums text-gray-900 transition hover:border-primary-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-400 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-100"
                :aria-label="t(`dateTimePicker.${part}`)"
                aria-haspopup="listbox"
                :aria-expanded="timeMenu === part"
                :data-testid="`datetime-picker-${part}`"
                @click="toggleTimeMenu(part)"
                @keydown.down.prevent="openTimeMenu(part)"
              >
                {{ pad(part === 'hour' ? draftDate.getHours() : draftDate.getMinutes()) }}<Icon name="chevronDown" size="xs" class="text-gray-400" />
              </button>
              <div v-if="timeMenu === part" role="listbox" :aria-label="t(`dateTimePicker.${part}`)" class="absolute bottom-full left-0 z-10 mb-2 max-h-48 w-full overflow-y-auto overscroll-contain rounded-lg border border-gray-200 bg-white p-1 shadow-lg dark:border-dark-500 dark:bg-dark-700" :data-testid="`datetime-picker-${part}-options`" @keydown="onTimeMenuKeydown($event, part)">
                <button
                  v-for="value in part === 'hour' ? 24 : 60"
                  :key="value - 1"
                  type="button"
                  role="option"
                  :aria-selected="(part === 'hour' ? draftDate.getHours() : draftDate.getMinutes()) === value - 1"
                  :disabled="!timeAvailable(part, value - 1)"
                  :data-time-value="value - 1"
                  class="block w-full rounded-md px-2 py-1.5 text-center text-sm tabular-nums transition disabled:cursor-not-allowed disabled:opacity-25"
                  :class="(part === 'hour' ? draftDate.getHours() : draftDate.getMinutes()) === value - 1 ? 'bg-primary-600 font-semibold text-white' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-600'"
                  @click="chooseTime(part, value - 1)"
                >{{ pad(value - 1) }}</button>
              </div>
            </div>
          </div>
        </div>

        <div class="mt-4 rounded-lg bg-primary-50/70 px-3 py-2.5 dark:bg-primary-500/10" data-testid="datetime-picker-draft" aria-live="polite">
          <p class="text-[11px] font-medium text-primary-600 dark:text-primary-400">{{ t('dateTimePicker.selectedDateTime') }}</p>
          <time :datetime="formatLocal(draftDate)" class="mt-1 block text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ formatDateTime(draftDate) }}</time>
        </div>
        <p v-if="validationMessage" role="alert" class="mt-2 text-xs leading-5 text-red-600 dark:text-red-400" data-testid="datetime-picker-validation">{{ validationMessage }}</p>
        </div>

        <div class="flex shrink-0 items-center justify-between gap-2 border-t border-gray-100 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800" data-testid="datetime-picker-footer">
          <button type="button" class="rounded-lg px-2 py-2 text-xs font-medium text-primary-600 transition hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-40 dark:text-primary-400 dark:hover:bg-primary-500/10" :disabled="!dayAvailable(today)" data-testid="datetime-picker-today" @click="selectDay(today)">{{ t('dateTimePicker.today') }}</button>
          <div class="flex gap-2">
            <button type="button" class="btn btn-secondary btn-sm" data-testid="datetime-picker-cancel" @click="closePicker">{{ t('dateTimePicker.cancel') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!canConfirm" data-testid="datetime-picker-confirm" @click="confirm">{{ t('dateTimePicker.confirm') }}</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'

const props = withDefaults(defineProps<{ modelValue: string; id?: string; min?: string; max?: string; disabled?: boolean }>(), { min: '', max: '', disabled: false })
const emit = defineEmits<{ (event: 'update:modelValue', value: string): void }>()
const { t, locale } = useI18n()
const instanceId = `datetime-picker-${Math.random().toString(36).slice(2, 9)}`
const panelId = computed(() => `${props.id || instanceId}-panel`)
const triggerRef = ref<HTMLButtonElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const isOpen = ref(false)
const draftDate = ref(new Date())
const today = ref(new Date())
const viewYear = ref(today.value.getFullYear())
const viewMonth = ref(today.value.getMonth())
const yearPageStart = ref(Math.floor(viewYear.value / 12) * 12)
const calendarView = ref<'days' | 'months' | 'years'>('days')
const timeParts = ['hour', 'minute'] as const
type TimePart = typeof timeParts[number]
const timeMenu = ref<TimePart | null>(null)
const panelStyle = ref<Record<string, string>>({})
const navigationClass = 'flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 transition hover:bg-gray-100 hover:text-gray-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-400 disabled:cursor-not-allowed disabled:opacity-25 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-100'

const pad = (value: number) => String(value).padStart(2, '0')
const dateKey = (date: Date) => `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
const formatTime = (date: Date) => `${pad(date.getHours())}:${pad(date.getMinutes())}`
const formatDate = (date: Date) => new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: '2-digit' }).format(date)
const formatDateTime = (date: Date) => `${formatDate(date)} ${formatTime(date)}`
const formatAccessibleDate = (date: Date) => new Intl.DateTimeFormat(locale.value, { dateStyle: 'full' }).format(date)

function parseLocal(value: string): Date | null {
  const seconds = parseDateTimeLocalInput(value)
  if (seconds === null || formatDateTimeLocalInput(seconds) !== value.slice(0, 16)) return null
  const fraction = value.match(/\.(\d+)$/)?.[1]
  return new Date(seconds * 1000 + (fraction ? Number(fraction.slice(0, 3).padEnd(3, '0')) : 0))
}

function formatLocal(date: Date): string {
  return `${dateKey(date)}T${formatTime(date)}:${pad(date.getSeconds())}.${String(date.getMilliseconds()).padStart(3, '0')}`
}

const committedDate = computed(() => parseLocal(props.modelValue))
const minimum = computed(() => parseLocal(props.min)?.getTime() ?? -Infinity)
const maximum = computed(() => parseLocal(props.max)?.getTime() ?? Infinity)
const canConfirm = computed(() => !props.disabled && withinBounds(draftDate.value.getTime()))
const validationMessage = computed(() => {
  const timestamp = draftDate.value.getTime()
  if (timestamp < minimum.value) return t('dateTimePicker.beforeMinimum', { time: formatDateTime(new Date(minimum.value)) })
  if (timestamp > maximum.value) return t('dateTimePicker.afterMaximum', { time: formatDateTime(new Date(maximum.value)) })
  return ''
})
const triggerLabel = computed(() => committedDate.value ? `${t('dateTimePicker.selectDateTime')}: ${formatDate(committedDate.value)} ${formatTime(committedDate.value)}` : t('dateTimePicker.selectDateTime'))
const monthNames = computed(() => Array.from({ length: 12 }, (_, month) => new Intl.DateTimeFormat(locale.value, { month: 'long' }).format(new Date(2026, month, 1))))
const weekdayNames = computed(() => Array.from({ length: 7 }, (_, day) => new Intl.DateTimeFormat(locale.value, { weekday: 'short' }).format(new Date(2026, 0, 5 + day))))
const yearChoices = computed(() => Array.from({ length: 12 }, (_, index) => yearPageStart.value + index))
const focusedDayKey = computed(() => {
  const selected = calendarDays.value.find(day => isSelectedDay(day.date) && !day.disabled)
  return selected?.key ?? calendarDays.value.find(day => day.inMonth && !day.disabled)?.key
})
const calendarDays = computed(() => {
  const first = new Date(viewYear.value, viewMonth.value, 1, 12)
  first.setDate(first.getDate() - (first.getDay() + 6) % 7)
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(first)
    date.setDate(first.getDate() + index)
    return { date, key: dateKey(date), inMonth: date.getMonth() === viewMonth.value, isToday: dateKey(date) === dateKey(today.value), disabled: !dayAvailable(date) }
  })
})
const calendarWeeks = computed(() => Array.from({ length: 6 }, (_, week) => calendarDays.value.slice(week * 7, week * 7 + 7)))

function withinBounds(timestamp: number): boolean {
  return Number.isFinite(timestamp) && timestamp >= minimum.value && timestamp <= maximum.value
}

function dayAvailable(date: Date): boolean {
  const start = new Date(date); start.setHours(0, 0, 0, 0)
  const end = new Date(date); end.setHours(23, 59, 59, 999)
  return end.getTime() >= minimum.value && start.getTime() <= maximum.value
}

function monthAvailable(year: number, month: number): boolean {
  if (year < 1 || year > 9999) return false
  const start = new Date(year, month, 1)
  const end = new Date(year, month + 1, 0, 23, 59, 59, 999)
  return end.getTime() >= minimum.value && start.getTime() <= maximum.value
}

function yearAvailable(year: number): boolean {
  return Array.from({ length: 12 }, (_, month) => monthAvailable(year, month)).some(Boolean)
}

function isSelectedDay(date: Date): boolean { return dateKey(date) === dateKey(draftDate.value) }
function choiceClass(selected: boolean): string {
  return `rounded-lg px-2 py-3 text-sm font-medium transition focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-400 disabled:cursor-not-allowed disabled:opacity-25 ${selected ? 'bg-primary-600 text-white' : 'text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-dark-700'}`
}

function showYears() {
  yearPageStart.value = Math.floor(viewYear.value / 12) * 12
  calendarView.value = calendarView.value === 'years' ? 'days' : 'years'
}
function selectYearMonth(year: number, month: number) {
  const lastDay = new Date(year, month + 1, 0).getDate()
  const next = new Date(draftDate.value)
  next.setFullYear(year, month, Math.min(next.getDate(), lastDay))
  draftDate.value = next
  viewYear.value = year; viewMonth.value = month
  timeMenu.value = null
}
function chooseMonth(month: number) {
  if (!monthAvailable(viewYear.value, month)) return
  selectYearMonth(viewYear.value, month)
  calendarView.value = 'days'
}
function chooseYear(year: number) {
  if (!yearAvailable(year)) return
  selectYearMonth(year, viewMonth.value)
  calendarView.value = 'months'
}
function navigationLabel(direction: number): string {
  if (calendarView.value === 'years') return `${t('dateTimePicker.selectYear')} ${yearPageStart.value + direction * 12}`
  return t(direction < 0 ? 'dateTimePicker.previousMonth' : 'dateTimePicker.nextMonth')
}
function canNavigate(direction: number): boolean {
  if (calendarView.value === 'years') return Array.from({ length: 12 }, (_, index) => yearAvailable(yearPageStart.value + direction * 12 + index)).some(Boolean)
  if (calendarView.value === 'months') return yearAvailable(viewYear.value + direction)
  const next = new Date(viewYear.value, viewMonth.value + direction, 1)
  return monthAvailable(next.getFullYear(), next.getMonth())
}
function navigate(direction: number) {
  if (!canNavigate(direction)) return
  if (calendarView.value === 'years') yearPageStart.value += direction * 12
  else if (calendarView.value === 'months') viewYear.value += direction
  else {
    const next = new Date(viewYear.value, viewMonth.value + direction, 1)
    viewYear.value = next.getFullYear(); viewMonth.value = next.getMonth()
  }
  timeMenu.value = null
}

function selectDay(date: Date) {
  if (!dayAvailable(date)) return
  const next = new Date(draftDate.value)
  next.setFullYear(date.getFullYear(), date.getMonth(), date.getDate())
  draftDate.value = next
  viewYear.value = date.getFullYear(); viewMonth.value = date.getMonth()
  calendarView.value = 'days'; timeMenu.value = null
}

function timeCandidate(part: TimePart, value: number, minute = draftDate.value.getMinutes()): Date {
  const next = new Date(draftDate.value)
  if (part === 'hour') next.setHours(value, minute)
  else next.setMinutes(value)
  return next
}
function timeAvailable(part: TimePart, value: number): boolean {
  return part === 'minute' ? withinBounds(timeCandidate(part, value).getTime()) : Array.from({ length: 60 }, (_, minute) => withinBounds(timeCandidate(part, value, minute).getTime())).some(Boolean)
}
function chooseTime(part: TimePart, value: number) {
  if (!timeAvailable(part, value)) return
  let next = timeCandidate(part, value)
  if (!withinBounds(next.getTime()) && part === 'hour') {
    const minutes = Array.from({ length: 60 }, (_, minute) => minute).filter(minute => withinBounds(timeCandidate(part, value, minute).getTime()))
    minutes.sort((left, right) => Math.abs(left - draftDate.value.getMinutes()) - Math.abs(right - draftDate.value.getMinutes()))
    next = timeCandidate(part, value, minutes[0])
  }
  draftDate.value = next; timeMenu.value = null
  panelRef.value?.querySelector<HTMLButtonElement>(`[data-testid="datetime-picker-${part}"]`)?.focus()
}
async function openTimeMenu(part: TimePart) {
  timeMenu.value = part
  await nextTick()
  const selected = panelRef.value?.querySelector<HTMLButtonElement>(`[data-testid="datetime-picker-${part}-options"] [aria-selected="true"]:not(:disabled)`)
  const target = selected ?? panelRef.value?.querySelector<HTMLButtonElement>(`[data-testid="datetime-picker-${part}-options"] button:not(:disabled)`)
  target?.focus(); target?.scrollIntoView?.({ block: 'nearest' })
}
function toggleTimeMenu(part: TimePart) { if (timeMenu.value === part) timeMenu.value = null; else void openTimeMenu(part) }
function onTimeMenuKeydown(event: KeyboardEvent, part: TimePart) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const options = Array.from(panelRef.value?.querySelectorAll<HTMLButtonElement>(`[data-testid="datetime-picker-${part}-options"] button:not(:disabled)`) ?? [])
  const current = options.indexOf(document.activeElement as HTMLButtonElement)
  const index = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : Math.max(0, Math.min(options.length - 1, current + (event.key === 'ArrowDown' ? 1 : -1)))
  options[index]?.focus(); options[index]?.scrollIntoView?.({ block: 'nearest' })
}

async function onDayKeydown(event: KeyboardEvent, date: Date) {
  const movements: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7, Home: -(date.getDay() + 6) % 7, End: 6 - (date.getDay() + 6) % 7 }
  if (!(event.key in movements)) return
  event.preventDefault()
  const next = new Date(date); next.setDate(next.getDate() + movements[event.key])
  if (!dayAvailable(next)) return
  selectDay(next)
  await nextTick()
  panelRef.value?.querySelector<HTMLButtonElement>(`[data-date="${dateKey(next)}"]`)?.focus()
}

function updatePosition() {
  const rect = triggerRef.value?.getBoundingClientRect()
  if (!isOpen.value || !rect) return
  const viewport = window.visualViewport
  const viewportWidth = viewport?.width || document.documentElement.clientWidth || window.innerWidth
  const viewportHeight = viewport?.height || document.documentElement.clientHeight || window.innerHeight
  if (viewportWidth < 640) {
    const offsetLeft = viewport?.offsetLeft ?? 0
    const offsetTop = viewport?.offsetTop ?? 0
    if (rect.bottom < offsetTop || rect.top > offsetTop + viewportHeight) { closePicker(); return }
    panelStyle.value = { left: `${offsetLeft + 12}px`, top: `${offsetTop + 12}px`, width: `${Math.min(360, viewportWidth - 24)}px`, maxHeight: `${viewportHeight - 24}px` }
    return
  }
  if (rect.bottom < 0 || rect.top > window.innerHeight) { closePicker(); return }
  const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, { viewportPadding: 12, maxWidth: 360, maxHeightRatio: 0.94, minComfortableHeight: 470, mobileBreakpoint: 640 })
  panelStyle.value = { left: `${position.left}px`, width: `${position.width}px`, maxHeight: `${position.maxHeight}px`, ...(position.top === null ? { bottom: `${position.bottom}px` } : { top: `${position.top}px` }) }
}

async function openPicker() {
  if (props.disabled || isOpen.value) return
  today.value = new Date()
  const fallback = Math.max(minimum.value, Math.min(maximum.value, today.value.getTime()))
  draftDate.value = committedDate.value ? new Date(committedDate.value) : new Date(Number.isFinite(fallback) ? fallback : today.value.getTime())
  viewYear.value = draftDate.value.getFullYear(); viewMonth.value = draftDate.value.getMonth()
  calendarView.value = 'days'; timeMenu.value = null
  isOpen.value = true; updatePosition()
  await nextTick()
  panelRef.value?.querySelector<HTMLButtonElement>('[role="gridcell"][tabindex="0"]')?.focus()
}
function closePicker() { isOpen.value = false; timeMenu.value = null; triggerRef.value?.focus() }
function togglePicker() { if (isOpen.value) closePicker(); else void openPicker() }
function confirm() {
  if (!canConfirm.value) return
  emit('update:modelValue', formatLocal(draftDate.value))
  closePicker()
}
function onOutsidePointer(event: PointerEvent) {
  const target = event.target as Node | null
  if (isOpen.value && target && !panelRef.value?.contains(target) && !triggerRef.value?.contains(target)) closePicker()
}
function onEscape(event: KeyboardEvent) {
  if (!isOpen.value || event.key !== 'Escape') return
  event.preventDefault(); event.stopImmediatePropagation(); closePicker()
}
function trapTab(event: KeyboardEvent) {
  const buttons = Array.from(panelRef.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled):not([tabindex="-1"])') ?? [])
  const first = buttons[0]; const last = buttons[buttons.length - 1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}

watch(() => props.disabled, disabled => { if (disabled && isOpen.value) closePicker() })
watch(() => props.modelValue, () => {
  if (!isOpen.value || !committedDate.value) return
  draftDate.value = new Date(committedDate.value)
  viewYear.value = draftDate.value.getFullYear(); viewMonth.value = draftDate.value.getMonth()
})
onMounted(() => {
  document.addEventListener('pointerdown', onOutsidePointer, true)
  document.addEventListener('keydown', onEscape, true)
  document.addEventListener('scroll', updatePosition, true)
  window.addEventListener('resize', updatePosition)
  window.visualViewport?.addEventListener('resize', updatePosition)
  window.visualViewport?.addEventListener('scroll', updatePosition)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onOutsidePointer, true)
  document.removeEventListener('keydown', onEscape, true)
  document.removeEventListener('scroll', updatePosition, true)
  window.removeEventListener('resize', updatePosition)
  window.visualViewport?.removeEventListener('resize', updatePosition)
  window.visualViewport?.removeEventListener('scroll', updatePosition)
})
</script>

<style scoped>
.datetime-picker-enter-active,
.datetime-picker-leave-active { transition: opacity 120ms ease, transform 120ms ease; }
.datetime-picker-enter-from,
.datetime-picker-leave-to { opacity: 0; transform: translateY(4px); }
</style>
