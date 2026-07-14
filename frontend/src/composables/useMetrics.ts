import { ref, watch, onUnmounted, type Ref } from 'vue'
import type { MetricsSnapshot } from '../types/metrics'

const REFRESH_MS = 30_000

// Polls /api/metrics while `active` is true: immediate fetch on activation,
// then every 30s. A failed fetch keeps the previous data (shown stale with
// its age) and retries on the next tick.
export function useMetrics(active: Ref<boolean>) {
  const data = ref<MetricsSnapshot | null>(null)
  const error = ref<string | null>(null)
  const fetchedAt = ref<Date | null>(null)

  async function refresh() {
    try {
      const res = await fetch('/api/metrics')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json() as MetricsSnapshot
      fetchedAt.value = new Date()
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'failed to fetch metrics'
    }
  }

  let timer: ReturnType<typeof setInterval> | null = null
  function stop() {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }
  watch(active, (on) => {
    stop()
    if (on) {
      refresh()
      timer = setInterval(refresh, REFRESH_MS)
    }
  }, { immediate: true })
  onUnmounted(stop)

  return { data, error, fetchedAt }
}
