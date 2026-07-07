<script setup lang="ts">
import { computed } from 'vue'
import type { WindowKey } from '../types/overview'
import { useOverviewData } from '../composables/useOverviewData'
import { PROJECT_ACCENTS } from '../lib/overview'
import StatCard from './StatCard.vue'
import RebuildBarChart from './RebuildBarChart.vue'
import CveExposureTable from './CveExposureTable.vue'

const props = defineProps<{
  overviewWindow: WindowKey
}>()

const emit = defineEmits<{
  'update:overviewWindow': [w: WindowKey]
}>()

const win = computed<WindowKey>({
  get: () => props.overviewWindow,
  set: (v) => emit('update:overviewWindow', v),
})

const {
  snapshot, loading, error,
  totalRebuilds, rebuildDeltaPct, topPackage, topRepo,
  totalCritical, totalHigh, affectedImageCount, avgFixDays, oldestOpenDays,
  rebuildBars, projects,
} = useOverviewData(win)

const WINDOWS: WindowKey[] = ['24h', '48h', '7d']

// Accent by the project's NAME-sorted position (not snapshot ordering, which
// is rebuild-sorted and would shuffle colors between windows/refetches), so
// a given project keeps its color across windows within the same project set.
const accentOrder = computed(() => [...projects.value].map(p => p.project).sort())

function accentOf(project: string): string {
  const idx = accentOrder.value.indexOf(project)
  return PROJECT_ACCENTS[Math.max(0, idx) % PROJECT_ACCENTS.length]
}
</script>

<template>
  <div class="flex flex-col gap-4 max-w-[1360px] mx-auto w-full px-4 pb-10">
    <!-- Header -->
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div class="flex items-center gap-3">
        <div class="w-[34px] h-[34px] rounded-[9px] bg-[var(--brand-purple)] text-white grid place-items-center font-extrabold text-[16px]">P</div>
        <div>
          <h1 class="m-0 text-[21px] font-bold">Overview</h1>
          <div class="text-[12.5px] text-text-muted">Rebuild activity &amp; CVE exposure across all Percona OBS projects</div>
        </div>
      </div>
      <div class="flex items-center gap-[10px]">
        <span class="text-[11px] font-bold uppercase tracking-[0.06em] text-text-muted">Window</span>
        <div class="flex gap-[3px] bg-bg-muted p-[3px] rounded-[9px]">
          <button
            v-for="w in WINDOWS"
            :key="w"
            type="button"
            class="px-[13px] py-[5px] text-[12.5px] rounded-[7px]"
            :class="win === w
              ? 'bg-bg-card text-[var(--brand-purple)] font-bold shadow-[0_1px_2px_rgba(0,0,0,0.10)]'
              : 'text-text-muted'"
            :aria-pressed="win === w"
            @click="win = w"
          >{{ w }}</button>
        </div>
      </div>
    </div>

    <!-- Error banner -->
    <div v-if="error" class="text-fail bg-fail-tint rounded-[10px] px-4 py-2 text-[13px]">
      {{ error }}
    </div>

    <!-- Loading skeletons -->
    <template v-if="loading && !snapshot">
      <div class="grid grid-cols-2 min-[760px]:grid-cols-3 min-[1100px]:grid-cols-5 gap-3.5">
        <div v-for="i in 5" :key="i" class="h-[112px] bg-bg-muted rounded-[14px] animate-pulse" />
      </div>
      <div class="flex flex-col gap-3 bg-bg-card border border-border rounded-[14px] p-[18px_20px]">
        <div v-for="i in 6" :key="i" class="h-[22px] bg-bg-muted rounded-md animate-pulse" />
      </div>
      <div class="flex flex-col gap-2 bg-bg-card border border-border rounded-[14px] p-[15px_20px]">
        <div v-for="i in 5" :key="i" class="h-[38px] bg-bg-muted rounded-[8px] animate-pulse" />
      </div>
    </template>

    <template v-else>
      <!-- Stat cards -->
      <div class="grid grid-cols-2 min-[760px]:grid-cols-3 min-[1100px]:grid-cols-5 gap-3.5">
        <StatCard label="Total Rebuilds" tint="brand">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><path d="M21 12a9 9 0 1 1-2.6-6.3"/><polyline points="21 3 21 9 15 9"/></svg>
          </template>
          <template #value>
            <div class="flex items-baseline gap-[10px]">
              <span class="text-[34px] font-extrabold leading-none tracking-[-0.02em] tabular-nums">{{ totalRebuilds }}</span>
              <span
                class="text-[13px] font-bold"
                :class="rebuildDeltaPct >= 0 ? 'text-ok' : 'text-fail'"
              >{{ rebuildDeltaPct >= 0 ? '▲' : '▼' }} {{ Math.abs(rebuildDeltaPct) }}%</span>
            </div>
          </template>
          <template #footnote>
            across <b class="text-text-secondary">{{ projects.length }}</b> projects · last {{ win }}
          </template>
        </StatCard>

        <StatCard label="Most Rebuilt" tint="info">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></svg>
          </template>
          <template #value>
            <div class="font-mono text-[16px] font-bold">{{ topPackage?.name ?? '—' }}</div>
          </template>
          <template #footnote>
            <template v-if="topPackage">
              <b class="text-text-secondary">{{ topPackage.count }}</b> rebuilds · <span class="font-mono text-[10.5px]">{{ topPackage.project }}</span>
            </template>
            <template v-else>no rebuilds in this window</template>
          </template>
        </StatCard>

        <StatCard label="Most Rebuilt Repo" tint="high">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5"/><path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/></svg>
          </template>
          <template #value>
            <div class="font-mono text-[16px] font-bold">{{ topRepo?.name ?? '—' }}</div>
          </template>
          <template #footnote>
            <template v-if="topRepo">
              <b class="text-text-secondary">{{ topRepo.count }}</b> rebuilds · last {{ win }}
            </template>
            <template v-else>no rebuilds in this window</template>
          </template>
        </StatCard>

        <StatCard label="Open CVEs" tint="crit">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><line x1="12" y1="8" x2="12" y2="12"/><circle cx="12" cy="15.5" r="0.5"/></svg>
          </template>
          <template #value>
            <div class="flex items-baseline gap-[10px]">
              <span class="text-[34px] font-extrabold leading-none tracking-[-0.02em] tabular-nums">{{ totalCritical + totalHigh }}</span>
              <span class="text-[11.5px] font-bold px-2 py-0.5 rounded-md text-crit bg-crit-tint">{{ totalCritical }} Crit</span>
              <span class="text-[11.5px] font-bold px-2 py-0.5 rounded-md text-high bg-high-tint">{{ totalHigh }} High</span>
            </div>
          </template>
          <template #footnote>
            across <b class="text-text-secondary">{{ affectedImageCount }}</b> container images
          </template>
        </StatCard>

        <StatCard label="Avg CVE Fix Time" tint="ok">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><circle cx="12" cy="13" r="8"/><path d="M12 9v4l2.5 2.5"/><path d="M9 2h6"/></svg>
          </template>
          <template #value>
            <div class="flex items-baseline gap-[10px]">
              <span class="text-[34px] font-extrabold leading-none tracking-[-0.02em] tabular-nums">{{ avgFixDays || '—' }}</span>
              <span v-if="avgFixDays" class="text-[14px] text-text-muted">days</span>
            </div>
          </template>
          <template #footnote>
            <template v-if="oldestOpenDays > 0">
              oldest open: <b class="text-high">{{ oldestOpenDays }} days</b>
            </template>
            <template v-else>no open CVEs</template>
          </template>
        </StatCard>
      </div>

      <RebuildBarChart :bars="rebuildBars" :window-label="win" :accent-of="accentOf" />
      <CveExposureTable :projects="projects" :accent-of="accentOf" />
    </template>
  </div>
</template>
