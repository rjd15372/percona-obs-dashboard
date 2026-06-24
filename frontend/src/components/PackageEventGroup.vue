<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Event } from '../types/api'
import { GLYPH, GLYPH_COLOR, GLYPH_BG, TAG_STYLE, TAG_LABEL, eventTitle, timeStr, showReason, displayVersion } from '../composables/useEventDisplay'

const props = defineProps<{
  project: string
  package: string
  tags: string[]
  events: Event[]
  expanded: boolean
}>()

const emit = defineEmits<{ toggle: [] }>()

const REASON_PREVIEW_CHAR_LIMIT = 180

const head = computed(() => props.events[0])
const expandedReasons = ref(new Set<string>())

function toggleReason(eventId: string) {
  const next = new Set(expandedReasons.value)
  if (next.has(eventId)) next.delete(eventId)
  else next.add(eventId)
  expandedReasons.value = next
}

function reasonCanExpand(event: Event): boolean {
  return (event.why?.length ?? 0) > REASON_PREVIEW_CHAR_LIMIT
}
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
      class="group-header"
      @click="emit('toggle')"
    >
      <!-- Expand arrow -->
      <span
        class="expand-arrow"
        :class="{ expanded }"
        aria-hidden="true"
      ></span>

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
          <span class="package-name">{{ props.package }}</span>
          <span style="font-size: 10.5px; font-weight: 600; color: var(--text-muted); background: var(--bg-muted, var(--blocked-tint)); border-radius: 5px; padding: 1px 6px; white-space: nowrap; flex-shrink: 0;">{{ events.length }} events</span>
          <span :title="head.at" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ timeStr(head.at) }}</span>
        </div>
        <!-- Row 2: subtitle (most recent event's what, repo/arch stripped) -->
        <span class="event-title">{{ eventTitle(head) }}</span>
        <!-- Row 3: scope chip + version badge + project path -->
        <div style="display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-top: 1px;">
          <span
            v-for="tag in tags" :key="tag"
            :style="`font-size: 9px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; padding: 2px 6px; border-radius: 5px; ${TAG_STYLE[tag] ?? 'background: var(--blocked-tint); color: var(--blocked);'}`"
          >{{ TAG_LABEL[tag] ?? tag }}</span>
          <span
            v-if="displayVersion(head.version, tags.includes('container'))"
            :style="{
              fontFamily: 'var(--font-mono)',
              fontSize: '10px',
              fontWeight: '700',
              padding: '2px 7px',
              borderRadius: '5px',
              background: tags.includes('container') ? 'var(--brand-purple-tint)' : 'var(--bg-muted, var(--blocked-tint))',
              color: tags.includes('container') ? 'var(--brand-purple)' : 'var(--text-secondary)',
              border: '1px solid var(--border)',
              whiteSpace: 'nowrap',
              flexShrink: '0',
            }"
          >{{ displayVersion(head.version, tags.includes('container')) }}</span>
          <code style="font-family: var(--font-mono); font-size: 10px; color: var(--text-muted);">{{ project }}</code>
        </div>
      </div>
    </div>

    <!-- Expanded child event rows -->
    <div v-if="expanded" style="padding: 0 14px 8px 14px;">
      <div v-for="(event, idx) in events" :key="event.id">
        <div class="child-event-row">
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
          <div class="child-event-content">
            <div style="display: flex; align-items: center; gap: 8px;">
              <span class="child-event-title">{{ eventTitle(event) }}</span>
              <span :title="event.at" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ timeStr(event.at) }}</span>
            </div>
            <div v-if="showReason(event)" class="reason-box">
              <div class="reason-text" :class="{ expanded: expandedReasons.has(event.id) }">{{ event.why }}</div>
              <button v-if="reasonCanExpand(event)" class="reason-toggle" type="button" @click="toggleReason(event.id)">
                {{ expandedReasons.has(event.id) ? 'Show less' : 'Show more' }}
              </button>
            </div>
            <span
              v-if="displayVersion(event.version, tags.includes('container'))"
              :style="{
                fontFamily: 'var(--font-mono)',
                fontSize: '10px',
                fontWeight: '700',
                padding: '2px 7px',
                borderRadius: '5px',
                background: tags.includes('container') ? 'var(--brand-purple-tint)' : 'var(--bg-muted, var(--blocked-tint))',
                color: tags.includes('container') ? 'var(--brand-purple)' : 'var(--text-secondary)',
                border: '1px solid var(--border)',
                whiteSpace: 'nowrap',
                alignSelf: 'flex-start',
              }"
            >{{ displayVersion(event.version, tags.includes('container')) }}</span>
            <code v-if="event.repo" style="font-family: var(--font-mono); font-size: 11px; font-weight: 600; color: var(--text-secondary);">{{ event.repo }}/{{ event.arch }}</code>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.group-header {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 9px 14px;
  cursor: pointer;
  border-radius: 9px;
}

.expand-arrow {
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  width: 16px;
  height: 24px;
  flex-shrink: 0;
  color: var(--text-muted);
}

.expand-arrow::before,
.expand-arrow::after {
  content: '';
  display: block;
  position: absolute;
  left: 4px;
  top: 8px;
  width: 6px;
  height: 6px;
  border: solid currentColor;
  border-width: 2px 2px 0 0;
}

.expand-arrow::before {
  opacity: 1;
  transform: rotate(45deg);
}

.expand-arrow::after {
  opacity: 0;
  transform: rotate(135deg);
}

.expand-arrow.expanded::before {
  opacity: 0;
}

.expand-arrow.expanded::after {
  opacity: 1;
}

.child-event-row {
  display: flex;
  gap: 10px;
  width: 100%;
  padding: 5px 0;
  box-sizing: border-box;
}

.child-event-content {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding-bottom: 4px;
}

.package-name {
  min-width: 0;
  font-size: 12.5px;
  font-weight: 700;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.event-title {
  font-size: 11.5px;
  color: var(--text-secondary);
}

.child-event-title {
  min-width: 0;
  font-size: 12px;
  font-weight: 700;
  color: var(--text-primary);
  overflow-wrap: anywhere;
}

.reason-box {
  color: var(--text-secondary);
  background: var(--bg-muted, var(--blocked-tint));
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 5px 7px;
  font-family: var(--font-mono);
  font-size: 11px;
  word-break: break-word;
}

.reason-text {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  line-height: 1.4;
}

.reason-text.expanded {
  display: block;
  overflow: visible;
}

.reason-toggle {
  margin-top: 4px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--brand-purple);
  cursor: pointer;
  font-family: inherit;
  font-size: 10.5px;
  font-weight: 700;
}
</style>
