<script setup lang="ts">
import { computed } from 'vue'
import type { Event } from '../types/api'
import { GLYPH, GLYPH_COLOR, GLYPH_BG, SCOPE_STYLE, SCOPE_LABEL, eventTitle, timeStr, showReason } from '../composables/useEventDisplay'

const props = defineProps<{
  project: string
  package: string
  scope: string
  events: Event[]
  expanded: boolean
}>()

const emit = defineEmits<{ toggle: [] }>()

const head = computed(() => props.events[0])
</script>

<template>
  <div
    :style="{
      borderRadius: '9px',
      border: expanded ? '1px solid var(--border)' : '1px solid transparent',
      background: expanded ? 'var(--bg-card-2)' : 'transparent',
      marginBottom: expanded ? '4px' : '0',
    }"
  >
    <!-- Header row (always visible, click to toggle) -->
    <div
      @click="emit('toggle')"
      style="display: flex; gap: 9px; padding: 9px 14px; cursor: pointer; border-radius: 9px;"
    >
      <!-- Expand arrow -->
      <span
        style="width: 16px; flex-shrink: 0; font-size: 10px; color: var(--text-muted); display: flex; align-items: flex-start; padding-top: 6px; transition: transform 0.15s;"
        :style="{ transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)' }"
      >▶</span>

      <!-- Glyph -->
      <div style="flex-shrink: 0;">
        <span
          style="width: 24px; height: 24px; border-radius: 7px; display: flex; align-items: center; justify-content: center; font-size: 12px; font-weight: 800;"
          :style="{ color: GLYPH_COLOR[head.type], background: GLYPH_BG[head.type] }"
        >{{ GLYPH[head.type] }}</span>
      </div>

      <!-- Text -->
      <div style="display: flex; flex-direction: column; gap: 2px; min-width: 0; flex: 1;">
        <!-- Row 1: package name + count badge + timestamp -->
        <div style="display: flex; align-items: center; gap: 8px;">
          <span style="font-size: 12.5px; font-weight: 700; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ props.package }}</span>
          <span style="font-size: 10.5px; font-weight: 600; color: var(--text-muted); background: var(--bg-muted, var(--blocked-tint)); border-radius: 5px; padding: 1px 6px; white-space: nowrap; flex-shrink: 0;">{{ events.length }} events</span>
          <span :title="head.at" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ timeStr(head.at) }}</span>
        </div>
        <!-- Row 2: subtitle (most recent event's what, repo/arch stripped) -->
        <span style="font-size: 11.5px; color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ eventTitle(head) }}</span>
        <!-- Row 3: scope chip + project path -->
        <div style="display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-top: 1px;">
          <span :style="`font-size: 9px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; padding: 2px 6px; border-radius: 5px; ${SCOPE_STYLE[scope] ?? 'background: var(--blocked-tint); color: var(--blocked);'}`">{{ SCOPE_LABEL[scope] ?? scope }}</span>
          <code style="font-family: var(--font-mono); font-size: 10px; color: var(--text-muted);">{{ project }}</code>
        </div>
      </div>
    </div>

    <!-- Expanded child event rows -->
    <div v-if="expanded" style="padding: 0 14px 8px 14px;">
      <div v-for="(event, idx) in events" :key="event.id">
        <a
          :href="event.url"
          target="_blank"
          rel="noopener"
          style="display: flex; gap: 10px; padding: 5px 0; text-decoration: none;"
        >
          <!-- Glyph + connector -->
          <div style="display: flex; flex-direction: column; align-items: center; gap: 0; flex-shrink: 0; margin-left: 6px;">
            <span
              style="width: 20px; height: 20px; border-radius: 6px; display: flex; align-items: center; justify-content: center; font-size: 10px; font-weight: 800;"
              :style="{ color: GLYPH_COLOR[event.type], background: GLYPH_BG[event.type] }"
            >{{ GLYPH[event.type] }}</span>
            <span
              v-if="idx < events.length - 1"
              style="flex: 1; width: 2px; background: var(--border); margin-top: 2px; min-height: 8px; border-radius: 2px;"
            ></span>
          </div>
          <!-- Child text -->
          <div style="display: flex; flex-direction: column; gap: 2px; min-width: 0; padding-bottom: 4px; flex: 1;">
            <div style="display: flex; align-items: center; gap: 8px;">
              <span style="font-size: 12px; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ eventTitle(event) }}</span>
              <span :title="event.at" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ timeStr(event.at) }}</span>
            </div>
            <span
              v-if="showReason(event)"
              style="font-size:11px;color:var(--text-secondary);background:var(--bg-muted,var(--blocked-tint));border:1px solid var(--border);border-radius:5px;padding:3px 7px;font-family:var(--font-mono);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;"
            >{{ event.why }}</span>
            <code v-if="event.repo" style="font-family: var(--font-mono); font-size: 11px; font-weight: 600; color: var(--text-secondary);">{{ event.repo }}/{{ event.arch }}</code>
          </div>
        </a>
      </div>
    </div>
  </div>
</template>
