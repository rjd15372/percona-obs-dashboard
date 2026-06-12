import { ref, computed, toValue } from 'vue'
import type { MaybeRef, ComputedRef } from 'vue'
import type { Package } from '../types/api'

const SEVERITY: Record<string, number> = {
  broken: 5,
  unresolvable: 4,
  failed: 3,
  blocked: 2,
  building: 1,
  finished: 1,
  scheduled: 1,
  succeeded: 0,
}

// matchesVersion returns true if pkg belongs to the selected version.
// A package is a "common" package (always shown) when the segment at prefixDepth
// in its project path is absent or not a known version number.
function matchesVersion(
  pkg: Package,
  version: string,
  prefixDepth: number,
  knownVersions: Set<string>,
): boolean {
  const seg = pkg.project.split(':')[prefixDepth]
  if (!seg || !knownVersions.has(seg)) return true
  return seg === version
}

export function usePackages(
  apiBase: MaybeRef<string>,
  version: MaybeRef<string>,
  prefixDepth: MaybeRef<number>,
) {
  const data = ref<Package[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh() {
    const base = toValue(apiBase)
    const v = toValue(version)
    loading.value = true
    error.value = null
    try {
      const res = await fetch(`${base}/${v}/packages`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  // availableVersions: unique version segments found at prefixDepth in project paths,
  // sorted descending (newest first). Purely numeric segments are versions; anything
  // else (e.g. "common", "containers") is not.
  const availableVersions: ComputedRef<string[]> = computed(() => {
    const depth = toValue(prefixDepth)
    const found = new Set<string>()
    for (const pkg of data.value) {
      const seg = pkg.project.split(':')[depth]
      if (seg && /^\d+$/.test(seg)) found.add(seg)
    }
    return [...found].sort((a, b) => parseInt(b) - parseInt(a))
  })

  const sorted = computed(() => {
    const ver = toValue(version)
    const depth = toValue(prefixDepth)
    const knownVersions = new Set(availableVersions.value)
    return [...data.value]
      .filter(pkg => matchesVersion(pkg, ver, depth, knownVersions))
      .sort((a, b) => (SEVERITY[b.rollup_state] ?? 0) - (SEVERITY[a.rollup_state] ?? 0))
  })

  function filterByScope(scopes: string[]) {
    if (scopes.length === 0) return sorted.value
    return sorted.value.filter(p => scopes.includes(p.scope))
  }

  return { data: sorted, rawData: data, availableVersions, loading, error, refresh, filterByScope }
}
