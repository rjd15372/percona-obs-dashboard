import { ref, computed, toValue } from 'vue'
import type { MaybeRef, ComputedRef } from 'vue'
import type { Package } from '../types/api'

const SEVERITY: Record<string, number> = {
  broken: 5,
  failed: 4,
  unresolvable: 3,
  blocked: 2,
  building: 1,
  finished: 1,
  scheduled: 1,
  succeeded: 0,
  published: -1,
}

// matchesVersion returns true if pkg belongs to the selected version.
// An empty version string means "all versions" — every package passes.
// A package is a "common" package (always shown) when the segment at prefixDepth
// in its project path is absent or not a known version number.
function matchesVersion(
  pkg: Package,
  version: string,
  prefixDepth: number,
  knownVersions: Set<string>,
): boolean {
  if (!version) return true
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
      // Backend ignores the version segment (filters by product prefix only).
      // Use "_" as a placeholder when version is "" (all-versions mode).
      const res = await fetch(`${base}/${v || '_'}/packages`)
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
      .filter(pkg => !pkg.is_release && matchesVersion(pkg, ver, depth, knownVersions))
      .sort((a, b) => (SEVERITY[b.rollup_state] ?? 0) - (SEVERITY[a.rollup_state] ?? 0))
  })

  function filterByTags(tags: string[]): Package[] {
    if (tags.length === 0) return sorted.value
    return sorted.value.filter(p =>
      tags.every(t => (p.tags ?? []).includes(t))
    )
  }

  return { data: sorted, rawData: data, availableVersions, loading, error, refresh, filterByTags }
}
