<script setup lang="ts">
import type { Event, EventType } from '../types/api'

defineProps<{ event: Event }>()

const GLYPH: Record<EventType, string> = {
  succeeded: '✓', failed: '✗', broken: '✗', unresolvable: '⚠',
  blocked: '⊘', published: '↑', triggered: '↻', started: '▶',
  created: '+', deleted: '−', build_started: '▶', build_finished: '■', version_change: '↕',
}

const GLYPH_COLOR: Record<EventType, string> = {
  succeeded: 'var(--ok)', failed: 'var(--fail)', broken: 'var(--broken)',
  unresolvable: 'var(--warn)', blocked: 'var(--blocked)',
  published: 'var(--info)', triggered: 'var(--brand-purple)', started: 'var(--info)',
  created: 'var(--ok)', deleted: 'var(--fail)', build_started: 'var(--info)',
  build_finished: 'var(--info)', version_change: 'var(--warn)',
}

const GLYPH_BG: Record<EventType, string> = {
  succeeded: 'var(--ok-tint)', failed: 'var(--fail-tint)', broken: 'var(--broken-tint)',
  unresolvable: 'var(--warn-tint)', blocked: 'var(--blocked-tint)',
  published: 'var(--info-tint)', triggered: 'var(--brand-purple-tint)', started: 'var(--info-tint)',
  created: 'var(--ok-tint)', deleted: 'var(--fail-tint)', build_started: 'var(--info-tint)',
  build_finished: 'var(--info-tint)', version_change: 'var(--warn-tint)',
}

const SCOPE_STYLE: Record<string, string> = {
  version: `background: var(--brand-purple-tint); color: var(--brand-purple);`,
  container: `background: var(--info-tint); color: var(--info);`,
  release: `background: var(--ok-tint); color: var(--ok);`,
  common: `background: var(--blocked-tint); color: var(--blocked);`,
  ppgcommon: `background: var(--blocked-tint); color: var(--blocked);`,
}

function timeStr(iso: string): string {
  const d = new Date(iso)
  const diff = Date.now() - d.getTime()
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return d.toLocaleDateString()
}
</script>

<template>
  <a :href="event.url" target="_blank" rel="noopener" style="display: flex; gap: 11px; padding: 9px 14px; text-decoration: none; border-radius: 9px;">
    <div style="display: flex; flex-direction: column; align-items: center; gap: 0; flex-shrink: 0;">
      <span
        style="width: 24px; height: 24px; border-radius: 7px; display: flex; align-items: center; justify-content: center; font-size: 12px; font-weight: 800;"
        :style="{ color: GLYPH_COLOR[event.type], background: GLYPH_BG[event.type] }"
      >{{ GLYPH[event.type] }}</span>
      <span style="flex: 1; width: 2px; background: var(--border); margin-top: 3px; border-radius: 2px;"></span>
    </div>
    <div style="display: flex; flex-direction: column; gap: 3px; min-width: 0; padding-bottom: 6px;">
      <div style="display: flex; align-items: center; gap: 8px;">
        <span style="font-size: 12.5px; font-weight: 700; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ event.what }}</span>
        <span style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ timeStr(event.at) }}</span>
      </div>
      <span style="font-size: 11.5px; color: var(--text-secondary); line-height: 1.45;">{{ event.why }}</span>
      <div style="display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-top: 2px;">
        <span :style="`font-size: 9px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; padding: 2px 6px; border-radius: 5px; ${SCOPE_STYLE[event.scope] ?? 'background: var(--blocked-tint); color: var(--blocked);'}`">{{ event.scope }}</span>
        <code v-if="event.repo" style="font-family: var(--font-mono); font-size: 10px; color: var(--text-muted);">{{ event.repo }}/{{ event.arch }}</code>
      </div>
    </div>
  </a>
</template>
