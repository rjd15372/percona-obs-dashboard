<script setup lang="ts">
import { computed, ref } from 'vue'
import TimeWindowPicker from './TimeWindowPicker.vue'
import EventRow from './EventRow.vue'
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
const activeTypes = ref(new Set<EventType>())
const filterRepo = ref('')
const filterArch = ref('')
const filterPackage = ref('')

const TYPE_META: Record<string, { glyph: string; color: string; bg: string; label: string }> = {
  succeeded:      { glyph: '✓', color: 'var(--ok)',            bg: 'var(--ok-tint)',           label: 'succeeded' },
  failed:         { glyph: '✗', color: 'var(--fail)',          bg: 'var(--fail-tint)',         label: 'failed' },
  broken:         { glyph: '✗', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'broken' },
  unresolvable:   { glyph: '⚠', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'unresolvable' },
  blocked:        { glyph: '⊘', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'blocked' },
  published:      { glyph: '↑', color: 'var(--brand-purple)', bg: 'var(--brand-purple-tint)', label: 'published' },
  created:        { glyph: '+', color: 'var(--ok)',            bg: 'var(--ok-tint)',           label: 'created' },
  deleted:        { glyph: '−', color: 'var(--fail)',          bg: 'var(--fail-tint)',         label: 'deleted' },
  build_started:  { glyph: '▶', color: 'var(--info)',          bg: 'var(--info-tint)',         label: 'build started' },
  build_finished: { glyph: '■', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'build finished' },
  version_change: { glyph: '↕', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'version change' },
  updated:        { glyph: '◉', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'updated' },
  triggered:      { glyph: '↻', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'triggered' },
  started:        { glyph: '▶', color: 'var(--blocked)',       bg: 'var(--blocked-tint)',      label: 'started' },
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
  activeTypes.value.size +
  (filterRepo.value ? 1 : 0) +
  (filterArch.value ? 1 : 0) +
  (filterPackage.value ? 1 : 0)
)
const filteredEvents = computed(() =>
  props.events
    .filter(e => activeTypes.value.size === 0 || activeTypes.value.has(e.type))
    .filter(e => filterRepo.value === '' || e.repo === filterRepo.value)
    .filter(e => filterArch.value === '' || e.arch === filterArch.value)
    .filter(e => filterPackage.value === '' ||
      e.what.toLowerCase().includes(filterPackage.value.toLowerCase()))
)

function toggleType(type: EventType) {
  const next = new Set(activeTypes.value)
  if (next.has(type)) next.delete(type)
  else next.add(type)
  activeTypes.value = next
}

function clearFilters() {
  activeTypes.value = new Set()
  filterRepo.value = ''
  filterArch.value = ''
  filterPackage.value = ''
}

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
</script>

<template>
  <div style="position: sticky; top: 16px; background: var(--bg-card); border: 1px solid var(--border); border-radius: 14px; display: flex; flex-direction: column; max-height: calc(100vh - 40px); overflow: hidden;">
    <!-- Header -->
    <div style="padding: 15px 16px 13px; border-bottom: 1px solid var(--border); display: flex; flex-direction: column; gap: 11px;">
      <!-- Title row -->
      <div style="display: flex; align-items: center; gap: 9px;">
        <h2 style="margin: 0; font-size: 15px; font-weight: 700; color: var(--text-primary);">Build events</h2>
        <span style="font-size: 11.5px; color: var(--text-muted); font-family: var(--font-mono);">
          <template v-if="activeFilterCount > 0">{{ filteredEvents.length }} of {{ events.length }}</template>
          <template v-else>{{ events.length }}</template>
          in window
        </span>
        <span style="margin-left: auto; display: inline-flex; align-items: center; gap: 6px; font-size: 11px; color: var(--text-muted);">
          <span style="width: 6px; height: 6px; border-radius: 99px; background: var(--ok);"></span>live
        </span>
      </div>
      <!-- Time window + filter toggle row -->
      <div style="display: flex; align-items: center; gap: 8px;">
        <TimeWindowPicker
          style="flex: 1;"
          :window-min="windowMin"
          :custom-from="customFrom"
          :custom-to="customTo"
          @update:window-min="emit('update:windowMin', $event)"
          @update:custom-from="emit('update:customFrom', $event)"
          @update:custom-to="emit('update:customTo', $event)"
        />
        <button
          @click="filterOpen = !filterOpen"
          :style="{
            flexShrink: '0',
            fontSize: '11px', fontWeight: '700', padding: '4px 10px',
            borderRadius: '6px', border: '1px solid',
            cursor: 'pointer', fontFamily: 'inherit',
            background: activeFilterCount > 0 ? 'var(--brand-purple-tint)' : 'var(--bg-muted)',
            color: activeFilterCount > 0 ? 'var(--brand-purple)' : 'var(--text-muted)',
            borderColor: activeFilterCount > 0 ? 'var(--brand-purple)' : 'var(--border)',
          }"
        >{{ filterOpen ? '⊟' : '⊞' }} Filter{{ activeFilterCount > 0 ? ` · ${activeFilterCount}` : '' }}</button>
      </div>
      <!-- Collapsible filter panel -->
      <div v-if="filterOpen" style="background: var(--bg-muted); border: 1px solid var(--border); border-radius: 8px; padding: 9px 10px; display: flex; flex-direction: column; gap: 8px;">
        <!-- Row 1: type pills -->
        <div style="display: flex; gap: 5px; flex-wrap: wrap;">
          <button
            v-for="type in availableTypes"
            :key="type"
            @click="toggleType(type)"
            :style="{
              fontSize: '10.5px', fontWeight: '700', padding: '2px 9px',
              borderRadius: '20px', border: '1px solid', cursor: 'pointer',
              fontFamily: 'inherit',
              background: activeTypes.has(type) ? (TYPE_META[type]?.bg ?? 'var(--blocked-tint)') : 'transparent',
              color: activeTypes.has(type) ? (TYPE_META[type]?.color ?? 'var(--text-muted)') : 'var(--text-muted)',
              borderColor: activeTypes.has(type) ? (TYPE_META[type]?.color ?? 'var(--border)') : 'var(--border)',
            }"
          >{{ TYPE_META[type]?.glyph ?? '·' }} {{ TYPE_META[type]?.label ?? type }}</button>
        </div>
        <!-- Row 2: repo + arch dropdowns + package search + clear -->
        <div style="display: flex; gap: 6px; align-items: center;">
          <select
            v-model="filterRepo"
            style="flex: 1; background: var(--bg-card); border: 1px solid var(--border); border-radius: 5px; padding: 4px 6px; font-size: 11px; color: var(--text-secondary); font-family: inherit;"
          >
            <option value="">All repos</option>
            <option v-for="repo in availableRepos" :key="repo" :value="repo">{{ repo }}</option>
          </select>
          <select
            v-model="filterArch"
            style="flex: 1; background: var(--bg-card); border: 1px solid var(--border); border-radius: 5px; padding: 4px 6px; font-size: 11px; color: var(--text-secondary); font-family: inherit;"
          >
            <option value="">All arches</option>
            <option v-for="arch in availableArches" :key="arch" :value="arch">{{ arch }}</option>
          </select>
          <input
            v-model="filterPackage"
            placeholder="🔍 package name…"
            style="flex: 2; background: var(--bg-card); border: 1px solid var(--border); border-radius: 5px; padding: 4px 7px; font-size: 11px; color: var(--text-secondary); font-family: var(--font-mono);"
          />
          <button
            v-if="activeFilterCount > 0"
            @click="clearFilters"
            style="font-size: 11px; color: var(--text-muted); background: none; border: none; cursor: pointer; padding: 0 2px; white-space: nowrap; font-family: inherit;"
          >✕ clear</button>
        </div>
      </div>
    </div>

    <!-- Scrollable event list -->
    <div style="overflow-y: auto; padding: 6px 4px 10px;">
      <div v-for="group in grouped" :key="group.bucket">
        <div style="padding: 11px 14px 5px; font-size: 10.5px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.06em;">{{ group.bucket }}</div>
        <EventRow v-for="event in group.events" :key="event.id" :event="event" />
      </div>
      <div v-if="grouped.length === 0" style="padding: 30px 16px; text-align: center; color: var(--text-muted); font-size: 13px;">
        No events in this time window
      </div>
    </div>
  </div>
</template>
