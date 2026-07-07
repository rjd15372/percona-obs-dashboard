import { ref, computed, watch, onMounted, onUnmounted, type Ref } from 'vue'
import type { OverviewSnapshot, OverviewCount, WindowKey } from '../types/overview'

export interface RebuildBar {
  project: string
  count: number
  pct: number // 0-100, normalized to the max bar
}

export function useOverviewData(window: Ref<WindowKey>) {
  const snapshot = ref<OverviewSnapshot | null>(null)
  const loading = ref(true)
  const error = ref<string | null>(null)

  async function fetchSnapshot() {
    try {
      const res = await fetch(`/api/overview?window=${window.value}`)
      if (!res.ok) throw new Error(res.statusText)
      snapshot.value = await res.json() as OverviewSnapshot
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'failed to load overview'
    } finally {
      loading.value = false
    }
  }

  // Realtime: any activity on the app's global stream schedules a debounced
  // refetch. We open our own EventSource on the shared endpoint (decision:
  // no dedicated overview stream).
  let es: EventSource | null = null
  let debounce: ReturnType<typeof setTimeout> | null = null
  function onStreamMessage() {
    if (debounce) clearTimeout(debounce)
    debounce = setTimeout(fetchSnapshot, 2000)
  }
  onMounted(() => {
    fetchSnapshot()
    es = new EventSource('/api/stream')
    es.onmessage = onStreamMessage
  })
  onUnmounted(() => {
    es?.close()
    if (debounce) clearTimeout(debounce)
  })
  watch(window, () => { loading.value = true; fetchSnapshot() })

  const projects = computed(() => snapshot.value?.projects ?? [])
  const allImages = computed(() => projects.value.flatMap(p => p.images))

  const totalRebuilds = computed(() => projects.value.reduce((s, p) => s + p.rebuilds, 0))
  const rebuildDeltaPct = computed(() => {
    const prev = snapshot.value?.previous_window_rebuild_total ?? 0
    if (prev === 0) return 0
    return Math.round((totalRebuilds.value - prev) / prev * 100)
  })
  const topPackage = computed<{ name: string; count: number; project: string } | null>(() => {
    let best: { name: string; count: number; project: string } | null = null
    for (const p of projects.value) {
      if (p.top_package && (!best || p.top_package.count > best.count)) {
        best = { ...p.top_package, project: p.project }
      }
    }
    return best
  })
  const topRepo = computed<OverviewCount | null>(() => snapshot.value?.top_repo ?? null)
  const totalCritical = computed(() => allImages.value.reduce((s, i) => s + i.critical, 0))
  const totalHigh = computed(() => allImages.value.reduce((s, i) => s + i.high, 0))
  const affectedImageCount = computed(() =>
    allImages.value.filter(i => i.critical + i.high > 0).length)
  const avgFixDays = computed(() => {
    const days = allImages.value
      .filter(i => i.critical + i.high > 0 && i.avg_fix_days > 0)
      .map(i => i.avg_fix_days)
    if (days.length === 0) return 0
    return Math.round(days.reduce((a, b) => a + b) / days.length)
  })
  const oldestOpenDays = computed(() =>
    allImages.value.reduce((m, i) => Math.max(m, i.oldest_open_days), 0))
  const rebuildBars = computed<RebuildBar[]>(() => {
    const withRebuilds = projects.value.filter(p => p.rebuilds > 0)
    const max = Math.max(1, ...withRebuilds.map(p => p.rebuilds))
    return withRebuilds
      .slice()
      .sort((a, b) => b.rebuilds - a.rebuilds)
      .map(p => ({ project: p.project, count: p.rebuilds, pct: Math.round(p.rebuilds / max * 100) }))
  })

  return {
    snapshot, loading, error,
    totalRebuilds, rebuildDeltaPct, topPackage, topRepo,
    totalCritical, totalHigh, affectedImageCount, avgFixDays, oldestOpenDays,
    rebuildBars, projects,
  }
}
