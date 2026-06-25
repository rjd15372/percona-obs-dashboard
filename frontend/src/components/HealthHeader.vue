<script setup lang="ts">
import { computed } from 'vue'
import type { Package } from '../types/api'

const props = defineProps<{ packages: Package[]; spotlight: string[] }>()
const emit = defineEmits<{ 'toggle-spotlight': [states: string[]] }>()

const total = computed(() => props.packages.length)
const okCount = computed(() => props.packages.filter(p => p.rollup_state === 'succeeded' || p.rollup_state === 'published').length)
const okTargets = computed(() => props.packages.reduce((s, p) => s + p.ok_targets, 0))
const totalTargets = computed(() => props.packages.reduce((s, p) => s + p.total_targets, 0))
const failCount = computed(() => props.packages.filter(p => p.rollup_state === 'failed').length)
const brokenCount = computed(() => props.packages.filter(p => p.rollup_state === 'broken').length)
const unresolvedCount = computed(() => props.packages.filter(p => p.rollup_state === 'unresolvable').length)
const blockedCount = computed(() => props.packages.filter(p => p.rollup_state === 'blocked').length)
const buildingCount = computed(() => props.packages.filter(p => p.rollup_state === 'building').length)
const finishingCount = computed(() => props.packages.filter(p => p.rollup_state === 'finished').length)
const attentionCount = computed(() => total.value - okCount.value)
const progressWidth = computed(() => total.value > 0 ? Math.round((okCount.value / total.value) * 100) : 0)
const allGreen = computed(() => total.value > 0 && okCount.value === total.value)
const hasFailures = computed(() => props.packages.some(p =>
  p.rollup_state === 'failed' || p.rollup_state === 'broken' || p.rollup_state === 'unresolvable'
))
const activeColor = computed(() => hasFailures.value ? 'var(--fail)' : 'var(--warn)')

const breakdown = computed(() => {
  const items: Array<{ count: number; label: string; states: string[]; color: string; bg: string }> = []
  if (brokenCount.value > 0) items.push({ count: brokenCount.value, label: 'broken', states: ['broken'], color: 'var(--broken)', bg: 'var(--broken-tint)' })
  if (failCount.value > 0) items.push({ count: failCount.value, label: 'failed', states: ['failed'], color: 'var(--fail)', bg: 'var(--fail-tint)' })
  if (unresolvedCount.value > 0) items.push({ count: unresolvedCount.value, label: 'unresolvable', states: ['unresolvable'], color: 'var(--fail)', bg: 'var(--fail-tint)' })
  if (blockedCount.value > 0) items.push({ count: blockedCount.value, label: 'blocked', states: ['blocked'], color: 'var(--blocked)', bg: 'var(--blocked-tint)' })
  if (buildingCount.value > 0) items.push({ count: buildingCount.value, label: 'building', states: ['building'], color: 'var(--info)', bg: 'var(--info-tint)' })
  if (finishingCount.value > 0) items.push({ count: finishingCount.value, label: 'finishing', states: ['finished'], color: 'var(--warn)', bg: 'var(--warn-tint)' })
  return items
})

function isPillActive(states: string[]): boolean {
  if (props.spotlight.length !== states.length) return false
  return states.every(s => props.spotlight.includes(s))
}
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[16px] px-[22px] py-5 flex items-center gap-[30px] flex-wrap">
    <!-- Left: big count + progress bar -->
    <div class="flex flex-col gap-2 min-w-[300px] flex-1">
      <div class="flex items-baseline gap-[10px]">
        <span class="text-[40px] font-extrabold leading-none tracking-[-0.02em] text-text-primary">
          {{ okCount }}<span class="text-text-muted font-semibold">/{{ total }}</span>
        </span>
        <span class="text-[15px] text-text-secondary font-semibold">packages built</span>
      </div>
      <div class="h-[9px] rounded-full bg-bg-muted overflow-hidden">
        <div class="h-full rounded-full transition-all duration-300" :style="{ background: allGreen ? 'var(--ok)' : activeColor, width: `${progressWidth}%` }"></div>
      </div>
      <span class="text-[12px] text-text-muted">{{ okTargets }}/{{ totalTargets }} build targets green</span>
    </div>

    <!-- Right: attention label + pills -->
    <div v-if="total > 0" class="flex flex-col gap-[9px]">
      <span v-if="allGreen" class="text-[13px] font-bold text-ok">✓ All packages green</span>
      <span v-else class="text-[13px] font-bold" :style="{ color: activeColor }">{{ attentionCount }} package{{ attentionCount !== 1 ? 's' : '' }} need attention</span>
      <div class="flex gap-2 flex-wrap max-w-[520px]">
        <span
          v-for="b in breakdown"
          :key="b.label"
          @click="emit('toggle-spotlight', b.states)"
          class="inline-flex items-center gap-[6px] text-[11.5px] font-bold px-[10px] py-1 rounded-[8px] cursor-pointer transition-all duration-[120ms]"
          :style="{
            background: b.bg,
            color: b.color,
            outline: isPillActive(b.states) ? `2px solid ${b.color}` : '2px solid transparent',
            outlineOffset: '1px',
          }"
        >
          <span class="w-2 h-2 rounded-[2px] flex-shrink-0" :style="{ background: b.color }"></span>
          {{ b.count }} {{ b.label }}
        </span>
      </div>
    </div>
  </div>
</template>
