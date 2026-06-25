<script setup lang="ts">
import type { Context } from '../types/api'

defineProps<{
  version: string
  updatedAt: string | null
  refreshing: boolean
  activeTags: string[]
  contexts: Context[]
  selectedContext: Context
  availableVersions: string[]
}>()

const emit = defineEmits<{
  'update:version': [version: string]
  'toggle-tag': [tag: string]
  'update:context': [ctx: Context]
  'refresh': []
}>()

const TAGS = [
  { id: 'ppg', label: 'PPG' },
  { id: 'common', label: 'Common' },
  { id: 'container', label: 'Container' },
]

function formatTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const now = new Date()
  const isToday = d.toDateString() === now.toDateString()
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return `${time} · ${isToday ? 'today' : d.toLocaleDateString()}`
}

function tabStyle(v: string, selected: string): string {
  const active = v === selected
  return active
    ? 'background: var(--bg-card); color: var(--text-primary); font-weight: 700; padding: 4px 12px; border-radius: 7px; border: none; font-size: 13px; cursor: pointer; font-family: inherit;'
    : 'background: transparent; color: var(--text-muted); font-weight: 500; padding: 4px 12px; border-radius: 7px; border: none; font-size: 13px; cursor: pointer; font-family: inherit;'
}

function tagStyle(_id: string, active: boolean): string {
  return active
    ? 'background: var(--brand-purple-tint); color: var(--brand-purple); padding: 3px 10px; border-radius: 8px; border: 2px solid var(--brand-purple); font-size: 11.5px; font-weight: 600; cursor: pointer; font-family: inherit;'
    : 'background: transparent; color: var(--text-secondary); padding: 4px 11px; border-radius: 8px; border: 1px solid var(--border); font-size: 11.5px; font-weight: 500; cursor: pointer; font-family: inherit;'
}
</script>

<template>
  <div style="background: var(--bg-card); border: 1px solid var(--border); border-radius: 14px; padding: 14px 18px; display: flex; flex-direction: column; gap: 13px;">
    <!-- Top row: tech badge + context selector + version tabs + updated -->
    <div style="display: flex; align-items: center; gap: 16px; flex-wrap: wrap;">
      <span style="display: inline-flex; align-items: center; padding: 5px 12px; border-radius: 8px; background: var(--tint-postgres); color: var(--tech-postgres); font-size: 12px; font-weight: 700; border: 1px solid rgba(0,94,214,0.15);">
        PostgreSQL
      </span>

      <!-- Context selector: dropdown when multiple contexts exist, plain badge otherwise -->
      <select
        v-if="contexts.length > 1"
        :value="selectedContext.apiBase"
        @change="e => { const apiBase = (e.target as HTMLSelectElement).value; const ctx = contexts.find(c => c.apiBase === apiBase); if (ctx) emit('update:context', ctx) }"
        style="font-family: var(--font-mono); font-size: 12.5px; color: var(--text-secondary); background: var(--bg-muted); padding: 5px 10px; border-radius: 7px; border: 1px solid var(--border); cursor: pointer;"
      >
        <option v-for="ctx in contexts" :key="ctx.apiBase" :value="ctx.apiBase">{{ ctx.prefix }}</option>
      </select>
      <code
        v-else
        style="font-family: var(--font-mono); font-size: 12.5px; color: var(--text-secondary); background: var(--bg-muted); padding: 5px 10px; border-radius: 7px;"
      >{{ selectedContext.prefix }}</code>

      <!-- Version tabs: hidden when no versioned packages exist in the context -->
      <div v-if="availableVersions.length > 0" style="display: flex; align-items: center; gap: 6px;">
        <span style="font-size: 11px; color: var(--text-muted); font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; margin-right: 2px;">Version</span>
        <div style="display: flex; gap: 3px; background: var(--bg-muted); padding: 3px; border-radius: 9px;">
          <button
            v-for="v in availableVersions"
            :key="v"
            @click="emit('update:version', v)"
            :style="tabStyle(v, version)"
          >{{ v }}</button>
          <button @click="emit('update:version', '')" :style="tabStyle('', version)">All</button>
        </div>
      </div>

      <div style="margin-left: auto; display: flex; align-items: center; gap: 16px; font-size: 12px; color: var(--text-muted);">
        <span
          title="Click to refresh"
          style="cursor: pointer; user-select: none;"
          @click="emit('refresh')"
        >Updated <strong :style="`color: ${refreshing ? 'var(--text-muted)' : 'var(--text-secondary)'}; font-weight: 600;`">{{ refreshing ? 'Refreshing…' : formatTime(updatedAt) }}</strong></span>
        <span style="display: inline-flex; align-items: center; gap: 6px;">
          <span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Live
        </span>
      </div>
    </div>

    <!-- Tag pills -->
    <div style="display: flex; align-items: center; gap: 9px; flex-wrap: wrap; border-top: 1px solid var(--border); padding-top: 12px;">
      <span style="font-size: 11px; color: var(--text-muted); font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; margin-right: 2px;">Tags</span>
      <button
        v-for="t in TAGS"
        :key="t.id"
        @click="emit('toggle-tag', t.id)"
        :style="tagStyle(t.id, activeTags.includes(t.id))"
      >{{ t.label }}</button>
    </div>
  </div>
</template>
