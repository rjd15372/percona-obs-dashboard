<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMediaQuery } from '../composables/useMediaQuery'
import TimeWindowPicker from './TimeWindowPicker.vue'
import EventRow from './EventRow.vue'
import PackageEventGroup from './PackageEventGroup.vue'
import type { Event, EventType } from '../types/api'

const props = defineProps<{
  events: Event[]
  windowMin: number
  customFrom: string | null
  customTo: string | null
}>()

const emit = defineEmits<{
  'update:windowMin': [min: number]
  'update:customFrom': [date: string]
  'update:customTo': [date: string]
}>()

// ── Filter state ──────────────────────────────────────────────
const filterOpen = ref(false)
const activeTypes = ref<string[]>([])
const activeRepos = ref<string[]>([])
const filterArch = ref('')
const filterPackage = ref('')
const openDropdown = ref<'types' | 'repos' | null>(null)

const TYPE_META: Record<string, { color: string; label: string }> = {
  succeeded:      { color: 'var(--ok)',            label: 'Succeeded' },
  failed:         { color: 'var(--fail)',          label: 'Failed' },
  broken:         { color: 'var(--broken)',        label: 'Broken' },
  unresolvable:   { color: 'var(--warn)',          label: 'Unresolvable' },
  blocked:        { color: 'var(--blocked)',       label: 'Blocked' },
  published:      { color: 'var(--brand-purple)',  label: 'Published' },
  created:        { color: 'var(--ok)',            label: 'Created' },
  deleted:        { color: 'var(--fail)',          label: 'Deleted' },
  build_started:  { color: 'var(--info)',          label: 'Build started' },
  build_finished: { color: 'var(--blocked)',       label: 'Build finished' },
  version_change: { color: 'var(--blocked)',       label: 'Version change' },
  updated:        { color: 'var(--blocked)',       label: 'Updated' },
  triggered:      { color: 'var(--warn)',          label: 'Rebuild triggered' },
  started:        { color: 'var(--info)',          label: 'Build started' },
}

const availableTypes = computed(() =>
  [...new Set(props.events.map(e => e.type))] as EventType[]
)
const availableRepos = computed(() =>
  [...new Set(props.events.map(e => e.repo).filter(Boolean))].sort() as string[]
)
const availableArches = computed(() =>
  [...new Set(props.events.map(e => e.arch).filter(Boolean))].sort() as string[]
)

const activeFilterCount = computed(() =>
  (activeTypes.value.length > 0 ? 1 : 0) +
  (activeRepos.value.length > 0 ? 1 : 0) +
  (filterArch.value ? 1 : 0) +
  (filterPackage.value ? 1 : 0)
)

const filteredEvents = computed(() =>
  props.events
    .filter(e => activeTypes.value.length === 0 || activeTypes.value.includes(e.type))
    .filter(e => activeRepos.value.length === 0 || activeRepos.value.includes(e.repo ?? ''))
    .filter(e => filterArch.value === '' || e.arch === filterArch.value)
    .filter(e => filterPackage.value === '' ||
      e.what.toLowerCase().includes(filterPackage.value.toLowerCase()))
)

watch(availableRepos, (repos) => {
  activeRepos.value = activeRepos.value.filter(r => repos.includes(r))
})
watch(availableArches, (arches) => {
  if (filterArch.value && !arches.includes(filterArch.value)) filterArch.value = ''
})

function toggleType(type: string) {
  activeTypes.value = activeTypes.value.includes(type)
    ? activeTypes.value.filter(t => t !== type)
    : [...activeTypes.value, type]
}

function toggleRepo(repo: string) {
  activeRepos.value = activeRepos.value.includes(repo)
    ? activeRepos.value.filter(r => r !== repo)
    : [...activeRepos.value, repo]
}

function toggleDropdown(which: 'types' | 'repos') {
  openDropdown.value = openDropdown.value === which ? null : which
}

function clearFilters() {
  activeTypes.value = []
  activeRepos.value = []
  filterArch.value = ''
  filterPackage.value = ''
}

const typeDropdownLabel = computed(() => {
  if (activeTypes.value.length === 0) return 'All event types ▾'
  if (activeTypes.value.length === 1) return (TYPE_META[activeTypes.value[0]]?.label ?? activeTypes.value[0]) + ' ▾'
  return activeTypes.value.length + ' event types ▾'
})

const repoDropdownLabel = computed(() => {
  if (activeRepos.value.length === 0) return 'All repos ▾'
  if (activeRepos.value.length === 1) return activeRepos.value[0] + ' ▾'
  return activeRepos.value.length + ' repos ▾'
})

type Bucket = 'Today' | 'Yesterday' | 'Earlier'

function getBucket(iso: string): Bucket {
  const d = new Date(iso)
  const now = new Date()
  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterdayStart = new Date(todayStart.getTime() - 86400000)
  if (d >= todayStart) return 'Today'
  if (d >= yesterdayStart) return 'Yesterday'
  return 'Earlier'
}

const grouped = computed(() => {
  const groups: { bucket: Bucket; events: Event[] }[] = [
    { bucket: 'Today', events: [] },
    { bucket: 'Yesterday', events: [] },
    { bucket: 'Earlier', events: [] },
  ]
  for (const e of filteredEvents.value) {
    const b = getBucket(e.at)
    groups.find(g => g.bucket === b)!.events.push(e)
  }
  return groups.filter(g => g.events.length > 0)
})

const isDesktop = useMediaQuery('(min-width: 1024px)')
const expanded = ref(false)
const showBody = computed(() => isDesktop.value || expanded.value)

// ── Grouped mode ──────────────────────────────────────────────
const groupedMode = ref(false)
const expandedGroups = ref<Map<string, boolean>>(new Map())

function toggleGroup(key: string) {
  const m = new Map(expandedGroups.value)
  m.set(key, !m.get(key))
  expandedGroups.value = m
}

interface PackageGroup {
  key: string
  project: string
  pkg: string
  tags: string[]
  events: Event[]
}

const groupedEvents = computed<PackageGroup[]>(() => {
  // Build a map of all events per project/package (unfiltered within the window)
  const allMap = new Map<string, Event[]>()
  for (const e of props.events) {
    const key = `${e.project}/${e.package}`
    if (!allMap.has(key)) allMap.set(key, [])
    allMap.get(key)!.push(e)
  }

  // Determine which keys have at least one event passing active filters
  const filteredKeys = new Set(filteredEvents.value.map(e => `${e.project}/${e.package}`))

  const result: PackageGroup[] = []
  for (const [key, evts] of allMap) {
    if (!filteredKeys.has(key)) continue
    const sorted = [...evts].sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime())
    result.push({ key, project: sorted[0].project, pkg: sorted[0].package, tags: sorted[0].tags ?? [], events: sorted })
  }

  result.sort((a, b) => new Date(b.events[0].at).getTime() - new Date(a.events[0].at).getTime())
  return result
})

const groupedAndBucketed = computed(() => {
  const buckets: { bucket: Bucket; groups: PackageGroup[] }[] = [
    { bucket: 'Today', groups: [] },
    { bucket: 'Yesterday', groups: [] },
    { bucket: 'Earlier', groups: [] },
  ]
  for (const g of groupedEvents.value) {
    const b = getBucket(g.events[0].at)
    buckets.find(b2 => b2.bucket === b)!.groups.push(g)
  }
  return buckets.filter(b => b.groups.length > 0)
})
</script>

<template>
  <div class="lg:sticky lg:top-4 bg-bg-card border border-border rounded-[14px] flex flex-col lg:max-h-[calc(100vh-40px)] overflow-hidden">
    <!-- Header -->
    <div class="pt-[15px] px-4 pb-[13px] border-b border-border flex flex-col gap-[11px]">
      <!-- Title row -->
      <div class="flex items-center gap-[9px] lg:cursor-default cursor-pointer select-none" @click="!isDesktop && (expanded = !expanded)">
        <h2 class="m-0 text-[15px] font-bold text-text-primary">Build events</h2>
        <span class="text-[11.5px] text-text-muted [font-family:var(--font-mono)]">
          <template v-if="groupedMode">{{ groupedEvents.length }} packages</template>
          <template v-else-if="activeFilterCount > 0">{{ filteredEvents.length }} of {{ events.length }}</template>
          <template v-else>{{ events.length }}</template>
          in window
        </span>
        <span class="ml-auto inline-flex items-center gap-1.5 text-[11px] text-text-muted">
          <span class="w-1.5 h-1.5 rounded-full bg-ok"></span>live
        </span>
        <span class="lg:hidden text-text-muted text-[11px] transition-transform" :class="{ 'rotate-90': expanded }">▶</span>
      </div>
      <!-- Time window + filter toggle row -->
      <div v-show="showBody" class="flex items-center gap-2">
        <TimeWindowPicker
          class="flex-1"
          :window-min="windowMin"
          :custom-from="customFrom"
          :custom-to="customTo"
          @update:window-min="emit('update:windowMin', $event)"
          @update:custom-from="emit('update:customFrom', $event)"
          @update:custom-to="emit('update:customTo', $event)"
        />
        <button
          @click="filterOpen = !filterOpen"
          class="flex-shrink-0 text-[11.5px] font-bold py-1 px-[11px] rounded-[7px] border cursor-pointer [font-family:inherit] inline-flex items-center gap-1.5"
          :style="{
            background: (filterOpen || activeFilterCount > 0) ? 'var(--brand-purple-tint)' : 'var(--bg-card)',
            color: (filterOpen || activeFilterCount > 0) ? 'var(--brand-purple)' : 'var(--text-secondary)',
            borderColor: (filterOpen || activeFilterCount > 0) ? 'var(--brand-purple)' : 'var(--border)',
          }"
        >Filter{{ activeFilterCount > 0 ? ` · ${activeFilterCount}` : '' }}</button>
        <button
          @click="groupedMode = !groupedMode"
          class="flex-shrink-0 text-[11.5px] font-bold py-1 px-[11px] rounded-[7px] border cursor-pointer [font-family:inherit] inline-flex items-center gap-1.5"
          :style="{
            background: groupedMode ? 'var(--brand-purple-tint)' : 'var(--bg-card)',
            color: groupedMode ? 'var(--brand-purple)' : 'var(--text-secondary)',
            borderColor: groupedMode ? 'var(--brand-purple)' : 'var(--border)',
          }"
        >⊞ Group</button>
      </div>
      <!-- Collapsible filter panel -->
      <div
        v-if="filterOpen"
        v-show="showBody"
        class="flex items-center gap-2 flex-wrap bg-bg-card-2 border border-border rounded-[10px] py-[10px] px-[11px] relative"
      >
        <!-- Click-away overlay -->
        <div v-if="openDropdown !== null" @click="openDropdown = null" class="fixed inset-0 z-10" />

        <!-- Event type multi-select dropdown -->
        <div class="relative z-20">
          <button
            @click="toggleDropdown('types')"
            class="py-[5px] px-[10px] rounded-[7px] [font-family:inherit] text-[11.5px] font-semibold cursor-pointer inline-flex items-center gap-[7px] whitespace-nowrap border"
            :style="{
              background: (activeTypes.length > 0 || openDropdown === 'types') ? 'var(--brand-purple-tint)' : 'var(--bg-card)',
              color: (activeTypes.length > 0 || openDropdown === 'types') ? 'var(--brand-purple)' : 'var(--text-secondary)',
              borderColor: (activeTypes.length > 0 || openDropdown === 'types') ? 'var(--brand-purple)' : 'var(--border-strong)',
            }"
          >{{ typeDropdownLabel }}</button>
          <div
            v-if="openDropdown === 'types'"
            class="absolute top-[calc(100%+5px)] left-0 min-w-[200px] bg-bg-card border border-border-strong rounded-[9px] shadow-[0_10px_28px_rgba(0,0,0,0.20)] p-1 flex flex-col max-h-[260px] overflow-y-auto"
          >
            <div
              v-for="type in availableTypes"
              :key="type"
              @click="toggleType(type)"
              class="flex items-center gap-2 py-1.5 px-[9px] rounded-[6px] cursor-pointer text-[11.5px] font-semibold text-text-primary"
              :style="{
                background: activeTypes.includes(type) ? 'var(--bg-muted)' : 'transparent',
              }"
            >
              <span :style="{
                width: '14px', height: '14px', borderRadius: '4px', flexShrink: '0',
                border: '1.5px solid ' + (TYPE_META[type]?.color ?? 'var(--border-strong)'),
                background: activeTypes.includes(type) ? (TYPE_META[type]?.color ?? 'var(--brand-purple)') : 'transparent',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: '9px', fontWeight: '800', color: '#fff',
              }">{{ activeTypes.includes(type) ? '✓' : '' }}</span>
              <span :style="{ width: '8px', height: '8px', borderRadius: '2px', flexShrink: '0', background: TYPE_META[type]?.color ?? 'var(--text-muted)' }"></span>
              {{ TYPE_META[type]?.label ?? type }}
            </div>
          </div>
        </div>

        <!-- Repo multi-select dropdown -->
        <div class="relative z-20">
          <button
            @click="toggleDropdown('repos')"
            class="py-[5px] px-[10px] rounded-[7px] [font-family:inherit] text-[11.5px] font-semibold cursor-pointer inline-flex items-center gap-[7px] whitespace-nowrap border"
            :style="{
              background: (activeRepos.length > 0 || openDropdown === 'repos') ? 'var(--brand-purple-tint)' : 'var(--bg-card)',
              color: (activeRepos.length > 0 || openDropdown === 'repos') ? 'var(--brand-purple)' : 'var(--text-secondary)',
              borderColor: (activeRepos.length > 0 || openDropdown === 'repos') ? 'var(--brand-purple)' : 'var(--border-strong)',
            }"
          >{{ repoDropdownLabel }}</button>
          <div
            v-if="openDropdown === 'repos'"
            class="absolute top-[calc(100%+5px)] left-0 min-w-[180px] bg-bg-card border border-border-strong rounded-[9px] shadow-[0_10px_28px_rgba(0,0,0,0.20)] p-1 flex flex-col max-h-[260px] overflow-y-auto"
          >
            <div
              v-for="repo in availableRepos"
              :key="repo"
              @click="toggleRepo(repo)"
              class="flex items-center gap-2 py-1.5 px-[9px] rounded-[6px] cursor-pointer text-[11.5px] font-semibold text-text-primary"
              :style="{
                background: activeRepos.includes(repo) ? 'var(--bg-muted)' : 'transparent',
              }"
            >
              <span :style="{
                width: '14px', height: '14px', borderRadius: '4px', flexShrink: '0',
                border: '1.5px solid var(--border-strong)',
                background: activeRepos.includes(repo) ? 'var(--brand-purple)' : 'transparent',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: '9px', fontWeight: '800', color: '#fff',
              }">{{ activeRepos.includes(repo) ? '✓' : '' }}</span>
              {{ repo }}
            </div>
          </div>
        </div>

        <!-- Arch single-select -->
        <select
          v-model="filterArch"
          :style="{
            fontFamily: 'inherit', fontSize: '11.5px', fontWeight: '600', cursor: 'pointer',
            color: filterArch ? 'var(--brand-purple)' : 'var(--text-secondary)',
            background: filterArch ? 'var(--brand-purple-tint)' : 'var(--bg-card)',
            border: filterArch ? '1px solid var(--brand-purple)' : '1px solid var(--border-strong)',
            borderRadius: '7px', padding: '5px 8px',
          }"
        >
          <option value="">All arches</option>
          <option v-for="arch in availableArches" :key="arch" :value="arch">{{ arch }}</option>
        </select>

        <!-- Package name search -->
        <input
          v-model="filterPackage"
          placeholder="Package name…"
          class="[font-family:var(--font-mono)] text-[11.5px] text-text-primary bg-bg-card border border-border-strong rounded-[7px] py-[5px] px-[9px] flex-1 min-w-[120px]"
        />

        <!-- Clear button -->
        <button
          v-if="activeFilterCount > 0"
          @click="clearFilters"
          class="[background:none] border-0 cursor-pointer [font-family:inherit] text-[11px] font-bold text-fail py-1 px-0.5 whitespace-nowrap"
        >clear</button>
      </div>
    </div>

    <!-- Scrollable event list -->
    <div v-show="showBody" class="overflow-y-auto pt-1.5 px-1 pb-[10px]">
      <!-- Grouped mode -->
      <template v-if="groupedMode">
        <div v-for="bucket in groupedAndBucketed" :key="bucket.bucket">
          <div class="pt-[11px] px-[14px] pb-[5px] text-[10.5px] font-bold text-text-muted uppercase tracking-[0.06em]">{{ bucket.bucket }}</div>
          <PackageEventGroup
            v-for="group in bucket.groups"
            :key="group.key"
            :project="group.project"
            :package="group.pkg"
            :tags="group.tags"
            :events="group.events"
            :expanded="expandedGroups.get(group.key) ?? false"
            @toggle="toggleGroup(group.key)"
          />
        </div>
        <div v-if="groupedAndBucketed.length === 0" class="py-[30px] px-4 text-center text-text-muted text-[13px]">
          No events in this time window
        </div>
      </template>
      <!-- Flat mode -->
      <template v-else>
        <div v-for="group in grouped" :key="group.bucket">
          <div class="pt-[11px] px-[14px] pb-[5px] text-[10.5px] font-bold text-text-muted uppercase tracking-[0.06em]">{{ group.bucket }}</div>
          <EventRow v-for="event in group.events" :key="event.id" :event="event" />
        </div>
        <div v-if="grouped.length === 0" class="py-[30px] px-4 text-center text-text-muted text-[13px]">
          No events in this time window
        </div>
      </template>
    </div>
  </div>
</template>
