<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import { useMetrics } from '../composables/useMetrics'

const expanded = ref(false)
const { data, error, fetchedAt } = useMetrics(expanded)

// "updated Ns ago" ticks only while expanded; the last text stays frozen
// when collapsed.
const nowTick = ref(Date.now())
let ageTimer: ReturnType<typeof setInterval> | null = null
watch(expanded, (on) => {
  if (ageTimer) {
    clearInterval(ageTimer)
    ageTimer = null
  }
  if (on) ageTimer = setInterval(() => { nowTick.value = Date.now() }, 1000)
}, { immediate: true })
onUnmounted(() => { if (ageTimer) clearInterval(ageTimer) })

const updatedLabel = computed(() => {
  if (!fetchedAt.value) return ''
  const s = Math.max(0, Math.round((nowTick.value - fetchedAt.value.getTime()) / 1000))
  return `updated ${s}s ago`
})

const endpoints = computed(() =>
  Object.entries(data.value?.obs.by_endpoint ?? {}).sort((a, b) => b[1] - a[1]))

const states = computed(() =>
  Object.entries(data.value?.working_set.by_state ?? {}).sort((a, b) => b[1] - a[1]))

const WINDOW_KEYS = ['6h', '12h', '24h', '7d', '30d'] as const

const WINDOW_MS: Record<string, number> = {
  '6h': 6 * 3_600_000,
  '12h': 12 * 3_600_000,
  '24h': 24 * 3_600_000,
  '7d': 7 * 86_400_000,
}

// Delta vs the previous adjacent period; null renders as a muted "—":
// always for 30d (its baseline would need 60d of samples), and whenever
// the baseline is zero or not fully covered by stored history yet.
function windowDelta(k: string): number | null {
  const obs = data.value?.obs
  if (!obs || !(k in WINDOW_MS)) return null
  const prev = obs.windows_prev?.[k] ?? 0
  if (prev === 0) return null
  if (!obs.oldest_sample) return null
  if (Date.parse(obs.oldest_sample) > Date.now() - 2 * WINDOW_MS[k]) return null
  const cur = obs.windows?.[k] ?? 0
  return Math.round(((cur - prev) / prev) * 100)
}

const tiles = computed(() =>
  WINDOW_KEYS.map((k) => ({
    key: k,
    count: data.value?.obs.windows?.[k] ?? 0,
    delta: windowDelta(k),
  })))

const sparkPoints = computed(() => {
  const series = data.value?.obs.series ?? []
  if (series.length < 2) return ''
  const W = 180
  const H = 34
  const pad = 2
  const max = Math.max(...series, 1)
  return series
    .map((v, i) => {
      const x = (i / (series.length - 1)) * W
      const y = H - pad - (Number(v) / max) * (H - 2 * pad)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
})

const fmt = (n: number) => n.toLocaleString('en-US')

function fmtUptime(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds))
  const d = Math.floor(s / 86_400)
  const h = Math.floor((s % 86_400) / 3_600)
  const m = Math.floor((s % 3_600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${s}s`
}
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] overflow-hidden">
    <button
      type="button"
      class="w-full flex items-center gap-2 p-[12px_20px] text-left"
      :aria-expanded="expanded"
      @click="expanded = !expanded"
    >
      <span class="text-text-muted text-[11px] transition-transform inline-block" :class="expanded ? 'rotate-90' : ''">▸</span>
      <span class="text-[11.5px] font-bold uppercase tracking-[0.05em] text-text-muted">Backend metrics</span>
      <span v-if="data" class="ml-auto text-[11px] text-text-muted">{{ data.sse_clients }} connection{{ data.sse_clients === 1 ? '' : 's' }} · up {{ fmtUptime(data.uptime_seconds) }} · {{ updatedLabel }}</span>
    </button>

    <div v-if="expanded" class="border-t border-border p-[14px_20px_16px]">
      <div v-if="error" class="text-[12px] text-text-muted pb-2">failed to refresh: {{ error }}</div>
      <template v-if="data">
        <div class="grid grid-cols-1 min-[760px]:grid-cols-3 gap-6">
          <!-- OBS requests -->
          <div>
            <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">OBS requests</div>
            <div class="text-[22px] font-extrabold tabular-nums leading-none">{{ fmt(data.obs.total) }}</div>
            <div class="text-[11px] text-text-muted mt-1">
              total since start · <b class="text-text-secondary">{{ data.obs.req_per_s.toFixed(1) }}</b> req/s last minute
            </div>
            <div class="mt-2">
              <svg v-if="sparkPoints" width="180" height="34" viewBox="0 0 180 34" role="img" aria-label="OBS requests per 5 minutes over the last 24 hours">
                <polyline fill="none" stroke="var(--brand-purple)" stroke-width="1.5" :points="sparkPoints" />
              </svg>
              <div class="text-[9.5px] text-text-muted mt-1">requests per 5 min · last 24h</div>
            </div>
          </div>
          <!-- Rate limiter -->
          <div>
            <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">Rate limiter</div>
            <template v-if="data.limiter.enabled">
              <div class="text-[22px] font-extrabold tabular-nums leading-none">
                {{ data.limiter.remaining }}<span class="text-[13px] text-text-muted font-semibold"> / {{ data.limiter.budget }}</span>
              </div>
              <div class="text-[11px] text-text-muted mt-1">
                remaining this minute · <b class="text-text-secondary">{{ fmt(data.limiter.waits) }}</b> waits total
              </div>
              <div class="h-[6px] bg-bg-muted rounded-[4px] overflow-hidden mt-2">
                <div
                  class="h-full rounded-[4px]"
                  :style="{ width: `${(data.limiter.remaining / Math.max(1, data.limiter.budget)) * 100}%`, background: 'var(--brand-purple)' }"
                />
              </div>
            </template>
            <div v-else class="text-[12.5px] text-text-muted">disabled</div>
          </div>
          <!-- Working set -->
          <div>
            <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">Working set</div>
            <div class="text-[22px] font-extrabold tabular-nums leading-none">{{ fmt(data.working_set.packages) }}</div>
            <div class="text-[11px] text-text-muted mt-1">
              packages · <b class="text-text-secondary">{{ data.working_set.inflight }}</b> in flight
            </div>
            <div class="flex gap-[5px] flex-wrap mt-2">
              <span
                v-for="[state, n] in states"
                :key="state"
                class="text-[10.5px] font-bold px-2 py-[2px] rounded-[6px] bg-bg-muted text-text-secondary tabular-nums"
              >{{ fmt(n) }} {{ state }}</span>
            </div>
          </div>
        </div>
        <!-- Window tiles -->
        <div class="flex gap-2 mt-4 flex-wrap">
          <div
            v-for="tile in tiles"
            :key="tile.key"
            class="flex-1 min-w-[96px] bg-bg-muted border border-border rounded-[8px] px-3 py-2"
          >
            <div class="text-[9.5px] font-bold uppercase tracking-[0.05em] text-text-muted">last {{ tile.key }}</div>
            <div class="text-[15px] font-extrabold tabular-nums mt-[2px]">{{ fmt(tile.count) }}</div>
            <div class="text-[10.5px] font-bold mt-[2px]">
              <span v-if="tile.delta === null" class="text-text-muted font-normal">—</span>
              <span v-else-if="tile.delta > 0" :style="{ color: 'var(--ok)' }">▲ {{ tile.delta }}%</span>
              <span v-else-if="tile.delta < 0" :style="{ color: 'var(--fail)' }">▼ {{ Math.abs(tile.delta) }}%</span>
              <span v-else class="text-text-secondary">0%</span>
            </div>
          </div>
        </div>
        <!-- Endpoint table -->
        <div class="mt-4">
          <div class="text-[10.5px] font-bold uppercase tracking-[0.06em] text-text-muted mb-2">Requests by endpoint</div>
          <div v-if="endpoints.length" class="max-w-[560px]" style="columns: 2; column-gap: 32px;">
            <div v-for="[op, n] in endpoints" :key="op" class="flex justify-between font-mono text-[12px] py-[1px] break-inside-avoid">
              <span class="text-text-muted">{{ op }}</span>
              <span class="tabular-nums">{{ fmt(n) }}</span>
            </div>
          </div>
          <div v-else class="text-[12px] text-text-muted">no requests yet</div>
        </div>
      </template>
      <div v-else-if="!error" class="text-[12px] text-text-muted">loading…</div>
    </div>
  </div>
</template>
