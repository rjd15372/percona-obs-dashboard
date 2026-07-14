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

const WINDOW_KEYS = ['6h', '12h', '24h'] as const

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
            <div class="mt-2 max-w-[200px]">
              <div v-for="k in WINDOW_KEYS" :key="k" class="flex justify-between text-[12.5px] py-[2px]">
                <span class="text-text-muted">last {{ k }}</span>
                <span class="font-mono tabular-nums font-semibold">{{ fmt(data.obs.windows?.[k] ?? 0) }}</span>
              </div>
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
