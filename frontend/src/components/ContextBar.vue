<script setup lang="ts">
import type { Context } from '../types/api'

defineProps<{
  version: string
  updatedAt: string | null
  activeScopes: string[]
  contexts: Context[]
  selectedContext: Context
  availableVersions: string[]
}>()

const emit = defineEmits<{
  'update:version': [version: string]
  'toggle-scope': [scope: string]
  'update:context': [ctx: Context]
}>()

const SCOPES = [
  { id: 'all', label: 'All' },
  { id: 'common', label: 'Common' },
  { id: 'ppgcommon', label: 'PPG Common' },
  { id: 'version', label: 'Version' },
  { id: 'container', label: 'Container' },
  { id: 'release', label: 'Release' },
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

function scopeStyle(_id: string, active: boolean): string {
  return active
    ? 'background: var(--brand-purple-tint); color: var(--brand-purple); padding: 3px 10px; border-radius: 8px; border: 2px solid var(--brand-purple); font-size: 11.5px; font-weight: 600; cursor: pointer; font-family: inherit;'
    : 'background: transparent; color: var(--text-secondary); padding: 4px 11px; border-radius: 8px; border: 1px solid var(--border); font-size: 11.5px; font-weight: 500; cursor: pointer; font-family: inherit;'
}
</script>

<template>
  <div style="background: var(--bg-card); border: 1px solid var(--border); border-radius: 14px; padding: 14px 18px; display: flex; flex-direction: column; gap: 13px;">
    <!-- Top row: tech badge + context selector + version tabs + updated -->
    <div style="display: flex; align-items: center; gap: 16px; flex-wrap: wrap;">
      <span style="display: inline-flex; align-items: center; gap: 7px; padding: 5px 12px; border-radius: 8px; background: var(--tint-postgres); color: var(--tech-postgres); font-size: 12px; font-weight: 700; border: 1px solid rgba(0,94,214,0.15);">
        <svg width="14" height="14" viewBox="0 0 1123.51 1123.51" style="flex-shrink:0;">
          <path fill="currentColor" fill-rule="evenodd" d="M1082.02,1010.89H41.49L561.75,109.77l520.26,901.12ZM69.93,994.47h983.65L561.75,142.61,69.93,994.47Z"/>
          <polygon fill="currentColor" points="681.51 899.1 625.22 899.1 567.09 798.9 609.3 726.62 620.68 791.4 681.51 899.1"/>
          <path fill="currentColor" d="M718.7,562.3h-14.61l-6.06,10.37-45.26,78.39-33.43,57.9-3.84,6.66h107.44l32.67,32.67v59.03h-52.4c-5.34,0-9.67,4.33-9.67,9.67v39.11h62.55c26.37,0,47.78-21.13,48.27-47.38h.02v-160.72c0-47.33-38.37-85.7-85.7-85.7ZM740.29,646.03c0,5.32-4.35,9.67-9.67,9.67h-8.99c-5.32,0-9.67-4.35-9.67-9.67v-8.99c0-5.32,4.35-9.67,9.67-9.67h8.99c5.32,0,9.67,4.35,9.67,9.67v8.99Z"/>
          <polygon fill="currentColor" points="305.31 827.37 328.46 787.35 378.35 787.35 442.63 898.29 498.94 898.29 433.67 787.35 548.29 787.45 573.7 743.44 516.78 645.54 473.31 569.38 374.65 570.25 305.94 688.19 305.88 704.68 279.05 752.33 279.05 799.28 306.72 752.18"/>
          <polygon fill="currentColor" points="586.76 720.72 689.75 542.35 483.78 542.35 586.76 720.72"/>
          <path fill="currentColor" d="M557.54,809.88h-30.45v79.55c0,5.34,4.33,9.67,9.67,9.67h46.53l-.23-44.84-25.52-44.38Z"/>
          <path fill="currentColor" d="M362.46,867.89l23.21-40.05-10.35-17.95h-43.93l-22.88,39.62v39.32c0,5.34,4.33,9.67,9.67,9.67h61.73l-17.45-30.6Z"/>
        </svg>
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
        </div>
      </div>

      <div style="margin-left: auto; display: flex; align-items: center; gap: 16px; font-size: 12px; color: var(--text-muted);">
        <span>Updated <strong style="color: var(--text-secondary); font-weight: 600;">{{ formatTime(updatedAt) }}</strong></span>
        <span style="display: inline-flex; align-items: center; gap: 6px;">
          <span style="width: 7px; height: 7px; border-radius: 99px; background: var(--ok);"></span>Auto-refresh 5 min
        </span>
      </div>
    </div>

    <!-- Scope chips -->
    <div style="display: flex; align-items: center; gap: 9px; flex-wrap: wrap; border-top: 1px solid var(--border); padding-top: 12px;">
      <span style="font-size: 11px; color: var(--text-muted); font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; margin-right: 2px;">Scope</span>
      <button
        v-for="s in SCOPES"
        :key="s.id"
        @click="s.id === 'all' ? emit('toggle-scope', 'all') : emit('toggle-scope', s.id)"
        :style="scopeStyle(s.id, s.id === 'all' ? activeScopes.length === 0 : activeScopes.includes(s.id))"
      >{{ s.label }}</button>
    </div>
  </div>
</template>
