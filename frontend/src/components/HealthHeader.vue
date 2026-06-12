<script setup lang="ts">
import { computed } from 'vue'
import type { Package } from '../types/api'

const props = defineProps<{ packages: Package[] }>()

const total = computed(() => props.packages.length)
const okCount = computed(() => props.packages.filter(p => p.rollup_state === 'succeeded').length)
const failCount = computed(() => props.packages.filter(p => p.rollup_state === 'failed').length)
const brokenCount = computed(() => props.packages.filter(p => p.rollup_state === 'broken').length)
const unresolvedCount = computed(() => props.packages.filter(p => p.rollup_state === 'unresolvable').length)
const blockedCount = computed(() => props.packages.filter(p => p.rollup_state === 'blocked').length)
const attentionCount = computed(() => total.value - okCount.value)
const progressWidth = computed(() => total.value > 0 ? (okCount.value / total.value) * 100 : 0)
const allGreen = computed(() => total.value > 0 && okCount.value === total.value)
</script>

<template>
  <div class="bg-bg-card border-b border-border px-6 py-4">
    <div class="flex items-center gap-4 flex-wrap">
      <div class="text-2xl font-bold text-text-primary">
        <span :class="allGreen ? 'text-ok' : 'text-fail'">{{ okCount }}</span>
        <span class="text-text-muted">/{{ total }}</span>
        <span class="text-base font-normal text-text-secondary ml-2">packages built</span>
      </div>

      <!-- Progress bar -->
      <div class="flex-1 min-w-32 h-2 bg-border rounded-full overflow-hidden">
        <div
          class="h-full rounded-full transition-all"
          :class="allGreen ? 'bg-ok' : 'bg-fail'"
          :style="{ width: `${progressWidth}%` }"
        />
      </div>

      <!-- All green message -->
      <template v-if="allGreen">
        <span class="text-ok font-medium text-sm">✓ All green</span>
      </template>

      <!-- Attention needed -->
      <template v-else-if="attentionCount > 0">
        <span class="text-text-secondary text-sm">{{ attentionCount }} need attention</span>

        <!-- Breakdown pills -->
        <div class="flex gap-2 flex-wrap">
          <span v-if="brokenCount > 0" class="px-2 py-0.5 rounded-full text-xs font-medium bg-broken-tint text-broken">
            {{ brokenCount }} broken
          </span>
          <span v-if="unresolvedCount > 0" class="px-2 py-0.5 rounded-full text-xs font-medium bg-warn-tint text-warn">
            {{ unresolvedCount }} unresolved
          </span>
          <span v-if="failCount > 0" class="px-2 py-0.5 rounded-full text-xs font-medium bg-fail-tint text-fail">
            {{ failCount }} failed
          </span>
          <span v-if="blockedCount > 0" class="px-2 py-0.5 rounded-full text-xs font-medium bg-blocked-tint text-blocked">
            {{ blockedCount }} blocked
          </span>
        </div>
      </template>
    </div>
  </div>
</template>
