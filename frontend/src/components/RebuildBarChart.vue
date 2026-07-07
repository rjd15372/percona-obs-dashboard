<script setup lang="ts">
import type { RebuildBar } from '../composables/useOverviewData'

defineProps<{
  bars: RebuildBar[]
  windowLabel: string
  accentOf: (project: string) => string
}>()
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] p-[18px_20px] flex flex-col gap-4">
    <div class="flex items-baseline justify-between">
      <h2 class="m-0 text-[14px] font-bold">Rebuilds by project</h2>
      <span class="text-[12px] text-text-muted">last {{ windowLabel }}</span>
    </div>

    <div v-if="bars.length === 0" class="text-[13px] text-text-muted">
      No rebuilds in this window
    </div>

    <div v-else class="flex flex-col gap-3">
      <div
        v-for="bar in bars"
        :key="bar.project"
        class="grid grid-cols-[215px_1fr_54px] items-center gap-3.5"
      >
        <div class="flex items-center gap-2 min-w-0">
          <span class="w-[9px] h-[9px] rounded-[3px] shrink-0" :style="{ background: accentOf(bar.project) }" />
          <span class="font-mono text-[12.5px] font-semibold truncate">{{ bar.project }}</span>
        </div>
        <div
          class="h-[22px] bg-bg-muted rounded-md overflow-hidden"
          role="img"
          :aria-label="`${bar.project} — ${bar.count} rebuilds`"
        >
          <div
            class="h-full rounded-md transition-[width] duration-300"
            :style="{ width: `${bar.pct}%`, background: accentOf(bar.project) }"
          />
        </div>
        <span class="font-mono text-[13px] font-bold text-right">{{ bar.count }}</span>
      </div>
    </div>
  </div>
</template>
