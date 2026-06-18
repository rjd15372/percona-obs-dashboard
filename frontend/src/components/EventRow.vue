<script setup lang="ts">
import { computed } from 'vue'
import type { Event } from '../types/api'
import { GLYPH, GLYPH_COLOR, GLYPH_BG, TAG_STYLE, TAG_LABEL, eventTitle, timeStr, showReason as _showReason, displayVersion } from '../composables/useEventDisplay'

const props = defineProps<{ event: Event }>()

const showReason = computed(() => _showReason(props.event))
</script>

<template>
  <a :href="props.event.url" target="_blank" rel="noopener" style="display: flex; gap: 11px; padding: 9px 14px; text-decoration: none; border-radius: 9px;">
    <div style="display: flex; flex-direction: column; align-items: center; gap: 0; flex-shrink: 0;">
      <span
        style="width: 24px; height: 24px; border-radius: 7px; display: flex; align-items: center; justify-content: center; font-size: 12px; font-weight: 800;"
        :style="{ color: GLYPH_COLOR[props.event.type], background: GLYPH_BG[props.event.type] }"
      >{{ GLYPH[props.event.type] }}</span>
      <span style="flex: 1; width: 2px; background: var(--border); margin-top: 3px; border-radius: 2px;"></span>
    </div>
    <div style="display: flex; flex-direction: column; gap: 3px; min-width: 0; padding-bottom: 6px;">
      <div style="display: flex; align-items: center; gap: 8px;">
        <span style="font-size: 12.5px; font-weight: 700; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ eventTitle(props.event) }}</span>
        <span :title="props.event.at" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ timeStr(props.event.at) }}</span>
      </div>
      <span
        v-if="showReason"
        style="font-size:11px;color:var(--text-secondary);background:var(--bg-muted,var(--blocked-tint));border:1px solid var(--border);border-radius:5px;padding:3px 7px;font-family:var(--font-mono);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;"
      >{{ props.event.why }}</span>
      <code v-if="props.event.repo" style="font-family: var(--font-mono); font-size: 11px; font-weight: 600; color: var(--text-secondary);">{{ props.event.repo }}/{{ props.event.arch }}</code>
      <div style="display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-top: 2px;">
        <span
          v-for="tag in (props.event.tags ?? [])" :key="tag"
          :style="`font-size: 9px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; padding: 2px 6px; border-radius: 5px; ${TAG_STYLE[tag] ?? 'background: var(--blocked-tint); color: var(--blocked);'}`"
        >{{ TAG_LABEL[tag] ?? tag }}</span>
        <span
          v-if="displayVersion(props.event.version, (props.event.tags ?? []).includes('container'))"
          :style="{
            fontFamily: 'var(--font-mono)',
            fontSize: '10px',
            fontWeight: '700',
            padding: '2px 7px',
            borderRadius: '5px',
            background: (props.event.tags ?? []).includes('container') ? 'var(--brand-purple-tint)' : 'var(--bg-muted, var(--blocked-tint))',
            color: (props.event.tags ?? []).includes('container') ? 'var(--brand-purple)' : 'var(--text-secondary)',
            border: '1px solid var(--border)',
            whiteSpace: 'nowrap',
            flexShrink: '0',
          }"
        >{{ displayVersion(props.event.version, (props.event.tags ?? []).includes('container')) }}</span>
        <code style="font-family:var(--font-mono);font-size:10px;color:var(--text-muted);">{{ props.event.project }}</code>
      </div>
    </div>
  </a>
</template>
