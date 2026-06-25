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
  <div class="bg-bg-card border border-border rounded-[14px] px-[18px] py-[14px] flex flex-col gap-[13px]">
    <!-- Top row: tech badge + context selector + version tabs + updated -->
    <div class="flex items-center gap-2 sm:gap-4 flex-wrap">
      <span class="inline-flex items-center px-3 py-[5px] rounded-lg bg-[var(--tint-postgres)] text-[var(--tech-postgres)] text-xs font-bold border border-[rgba(0,94,214,0.15)]">
        PostgreSQL
      </span>

      <!-- Context selector: dropdown when multiple contexts exist, plain badge otherwise -->
      <select
        v-if="contexts.length > 1"
        :value="selectedContext.apiBase"
        @change="e => { const apiBase = (e.target as HTMLSelectElement).value; const ctx = contexts.find(c => c.apiBase === apiBase); if (ctx) emit('update:context', ctx) }"
        class="[font-family:var(--font-mono)] text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px] border border-border cursor-pointer"
      >
        <option v-for="ctx in contexts" :key="ctx.apiBase" :value="ctx.apiBase">{{ ctx.prefix }}</option>
      </select>
      <code
        v-else
        class="[font-family:var(--font-mono)] text-[12.5px] text-text-secondary bg-bg-muted px-[10px] py-[5px] rounded-[7px]"
      >{{ selectedContext.prefix }}</code>

      <!-- Version tabs: hidden when no versioned packages exist in the context -->
      <div v-if="availableVersions.length > 0" class="flex items-center gap-1.5">
        <span class="text-[11px] text-text-muted font-semibold uppercase tracking-[0.06em] mr-0.5">Version</span>
        <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px]">
          <button
            v-for="v in availableVersions"
            :key="v"
            @click="emit('update:version', v)"
            :style="tabStyle(v, version)"
          >{{ v }}</button>
          <button @click="emit('update:version', '')" :style="tabStyle('', version)">All</button>
        </div>
      </div>

      <div class="ml-auto flex items-center gap-4 text-xs text-text-muted">
        <span
          title="Click to refresh"
          class="cursor-pointer select-none"
          @click="emit('refresh')"
        >Updated <strong :style="`color: ${refreshing ? 'var(--text-muted)' : 'var(--text-secondary)'}; font-weight: 600;`">{{ refreshing ? 'Refreshing…' : formatTime(updatedAt) }}</strong></span>
        <span class="inline-flex items-center gap-1.5">
          <span class="w-[7px] h-[7px] rounded-full bg-[var(--ok)]"></span>Live
        </span>
      </div>
    </div>

    <!-- Tag pills -->
    <div class="flex items-center gap-[9px] flex-wrap border-t border-border pt-3">
      <span class="text-[11px] text-text-muted font-semibold uppercase tracking-[0.06em] mr-0.5">Tags</span>
      <button
        v-for="t in TAGS"
        :key="t.id"
        @click="emit('toggle-tag', t.id)"
        :style="tagStyle(t.id, activeTags.includes(t.id))"
      >{{ t.label }}</button>
    </div>
  </div>
</template>
