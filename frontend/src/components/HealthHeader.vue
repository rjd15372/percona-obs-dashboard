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
  <div style="background: var(--bg-card); border: 1px solid var(--border); border-radius: 16px; padding: 20px 22px; display: flex; align-items: center; gap: 30px; flex-wrap: wrap;">
    <!-- Left: big count + progress bar -->
    <div style="display: flex; flex-direction: column; gap: 8px; min-width: 300px; flex: 1;">
      <div style="display: flex; align-items: baseline; gap: 10px;">
        <span style="font-size: 40px; font-weight: 800; line-height: 1; letter-spacing: -0.02em; color: var(--text-primary);">
          {{ okCount }}<span style="color: var(--text-muted); font-weight: 600;">/{{ total }}</span>
        </span>
        <span style="font-size: 15px; color: var(--text-secondary); font-weight: 600;">packages built</span>
      </div>
      <div style="height: 9px; border-radius: 99px; background: var(--bg-muted); overflow: hidden;">
        <div :style="{ height: '100%', background: allGreen ? 'var(--ok)' : activeColor, borderRadius: '99px', width: `${progressWidth}%`, transition: 'width 0.3s ease' }"></div>
      </div>
      <span style="font-size: 12px; color: var(--text-muted);">{{ okTargets }}/{{ totalTargets }} build targets green</span>
    </div>

    <!-- Right: attention label + pills -->
    <div v-if="total > 0" style="display: flex; flex-direction: column; gap: 9px;">
      <span v-if="allGreen" style="font-size: 13px; font-weight: 700; color: var(--ok);">✓ All packages green</span>
      <span v-else :style="{ fontSize: '13px', fontWeight: '700', color: activeColor }">{{ attentionCount }} package{{ attentionCount !== 1 ? 's' : '' }} need attention</span>
      <div style="display: flex; gap: 8px; flex-wrap: wrap; max-width: 520px;">
        <span
          v-for="b in breakdown"
          :key="b.label"
          @click="emit('toggle-spotlight', b.states)"
          :style="{
            display: 'inline-flex', alignItems: 'center', gap: '6px',
            fontSize: '11.5px', fontWeight: '700', padding: '4px 10px', borderRadius: '8px',
            background: b.bg, color: b.color,
            cursor: 'pointer',
            outline: isPillActive(b.states) ? `2px solid ${b.color}` : '2px solid transparent',
            outlineOffset: '1px',
            transition: 'outline 0.12s',
          }"
        >
          <span :style="{ width: '8px', height: '8px', borderRadius: '2px', background: b.color, flexShrink: '0' }"></span>
          {{ b.count }} {{ b.label }}
        </span>
      </div>
    </div>
  </div>
</template>
