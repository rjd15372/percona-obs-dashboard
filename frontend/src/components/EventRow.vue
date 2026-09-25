<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Event } from '../types/api'
import { GLYPH, GLYPH_COLOR, GLYPH_BG, TAG_STYLE, TAG_LABEL, eventTitle, timeStr, showReason as _showReason, displayVersion } from '../composables/useEventDisplay'

const props = defineProps<{ event: Event }>()

const REASON_PREVIEW_CHAR_LIMIT = 180

const showReason = computed(() => _showReason(props.event))
const reasonExpanded = ref(false)
const reasonCanExpand = computed(() => (props.event.why?.length ?? 0) > REASON_PREVIEW_CHAR_LIMIT)
</script>

<template>
  <div class="event-row">
    <div class="flex flex-col items-center gap-0 flex-shrink-0">
      <span
        class="w-6 h-6 rounded-[7px] flex items-center justify-center text-[12px] font-black"
        :style="{ color: GLYPH_COLOR[props.event.type], background: GLYPH_BG[props.event.type] }"
      >{{ GLYPH[props.event.type] }}</span>
      <span class="flex-1 w-[2px] bg-border mt-[3px] rounded-[2px]"></span>
    </div>
    <div class="event-content">
      <div class="flex items-center gap-2">
        <span class="package-name">{{ props.event.package }}</span>
        <span :title="props.event.at" class="ml-auto text-[10.5px] text-text-muted font-mono whitespace-nowrap flex-shrink-0">{{ timeStr(props.event.at) }}</span>
      </div>
      <span class="event-title">{{ eventTitle(props.event) }}</span>
      <div v-if="showReason" class="reason-box">
        <div class="reason-text" :class="{ expanded: reasonExpanded }">{{ props.event.why }}</div>
        <button v-if="reasonCanExpand" class="reason-toggle" type="button" @click="reasonExpanded = !reasonExpanded">
          {{ reasonExpanded ? 'Show less' : 'Show more' }}
        </button>
      </div>
      <code v-if="props.event.repo" class="font-mono text-[11px] font-semibold text-text-secondary">{{ props.event.repo }}/{{ props.event.arch }}</code>
      <div class="flex items-center gap-[6px] flex-wrap mt-[2px]">
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
        <code class="font-mono text-[10px] text-text-muted">{{ props.event.project }}</code>
      </div>
    </div>
  </div>
</template>

<style scoped>
.event-row {
  @apply flex gap-[11px] w-full py-[9px] px-[14px] rounded-[9px];
  box-sizing: border-box;
}

.event-content {
  @apply flex flex-1 flex-col gap-[3px] min-w-0 pb-[6px];
}

.package-name {
  @apply min-w-0 text-[12.5px] font-bold text-text-primary;
  overflow-wrap: anywhere;
}

.event-title {
  @apply text-[11.5px] text-text-secondary;
}

.reason-box {
  @apply text-text-secondary bg-bg-muted border border-border rounded-[5px] py-[5px] px-[7px] font-mono text-[11px];
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
  @apply mt-[4px] p-0 border-none bg-transparent text-brand-purple cursor-pointer text-[10.5px] font-bold;
}
</style>
