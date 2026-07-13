<script setup lang="ts">
import { computed } from 'vue'

export interface Segment {
  label: string    // display label, e.g. "devel", "staging", "PRs"
  count: number
  colorVar: string // CSS variable reference, e.g. "var(--cat-devel)"
}

const props = defineProps<{ segments: Segment[] }>()

const total = computed(() => props.segments.reduce((s, seg) => s + seg.count, 0))

// Bar slices: only non-zero segments occupy width; zero-count segments keep
// their legend entry but render no slice.
const slices = computed(() =>
  props.segments
    .filter(seg => seg.count > 0)
    .map(seg => ({ ...seg, pct: (seg.count / total.value) * 100 })))
</script>

<template>
  <div class="flex flex-col gap-[4px]">
    <div v-if="total > 0" class="flex h-[6px] rounded-[4px] overflow-hidden gap-[2px]">
      <div
        v-for="s in slices"
        :key="s.label"
        :style="{ width: `${s.pct}%`, background: s.colorVar }"
      />
    </div>
    <div class="flex gap-[10px] flex-wrap text-[10.5px] text-text-muted">
      <span v-for="seg in segments" :key="seg.label" class="inline-flex items-center gap-[4px]">
        <span class="w-[7px] h-[7px] rounded-[2px] inline-block shrink-0" :style="{ background: seg.colorVar }" />
        {{ seg.label }} <b class="font-bold tabular-nums">{{ seg.count }}</b>
      </span>
    </div>
  </div>
</template>
