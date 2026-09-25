import { computed, ref } from 'vue'
import type { InstanceHealth, ObsInstance, Package } from '../types/api'

// Module-level singleton: the instance list is static for a page lifetime;
// only health changes (via SSE instance_health messages).
const instances = ref<ObsInstance[]>([])
let started = false

async function load(): Promise<void> {
  try {
    const res = await fetch('/api/instances')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    instances.value = await res.json() as ObsInstance[]
  } catch {
    started = false // retry on the next useInstances() call
  }
}

export function applyInstanceHealth(slug: string, health: InstanceHealth): void {
  const idx = instances.value.findIndex(i => i.slug === slug)
  if (idx >= 0) instances.value.splice(idx, 1, { ...instances.value[idx], health })
}

export function useInstances() {
  if (!started) {
    started = true
    void load()
  }
  const bySlug = computed(() => new Map(instances.value.map(i => [i.slug, i])))
  const multi = computed(() => instances.value.length > 1)

  function instanceFor(slug?: string): ObsInstance | undefined {
    return (slug ? bySlug.value.get(slug) : undefined) ?? instances.value[0]
  }

  // Distinct instances a package builds on, in config order (the first
  // instance when no target is stamped yet).
  function packageInstances(pkg: Package): ObsInstance[] {
    const slugs = new Set((pkg.targets ?? []).map(t => t.instance).filter((s): s is string => !!s))
    const list = instances.value.filter(i => slugs.has(i.slug))
    if (list.length > 0) return list
    const first = instances.value[0]
    return first ? [first] : []
  }

  return { instances, multi, instanceFor, packageInstances }
}
