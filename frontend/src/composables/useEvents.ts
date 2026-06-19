import { ref, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Event } from '../types/api'

export function useEvents(apiBase: MaybeRef<string>, version: MaybeRef<string>) {
  const data = ref<Event[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh(opts: { window?: number; from?: string; to?: string } = {}) {
    const base = toValue(apiBase)
    const v = toValue(version)
    loading.value = true
    error.value = null
    try {
      let qs = ''
      if (opts.from && opts.to) {
        qs = `?from=${encodeURIComponent(opts.from)}&to=${encodeURIComponent(opts.to)}`
      } else {
        qs = `?window=${opts.window ?? 60}`
      }
      const res = await fetch(`${base}/${v || 'all'}/events${qs}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  function matchesEventVersion(event: Event, version: string, prefixDepth: number): boolean {
    if (!version) return true
    const seg = event.project.split(':')[prefixDepth]
    // Non-numeric segment (common, ppgcommon, project events) always passes
    if (!seg || !/^\d+$/.test(seg)) return true
    return seg === version
  }

  function matchesContext(project: string, prefix: string): boolean {
    if (!prefix || project === prefix || project.startsWith(prefix + ':')) return true
    if (!prefix.includes(':PR:')) return false

    const parts = prefix.split(':')
    const commonPrefix = `${parts.slice(0, 4).join(':')}:common`
    return project === commonPrefix || project.startsWith(`${commonPrefix}:`)
  }

  function filterEvents(tags: string[], version: string, prefixDepth: number, prefix: string): Event[] {
    return data.value.filter(e => {
      if (!matchesContext(e.project, prefix)) return false
      if (tags.length > 0 && !tags.every(t => (e.tags ?? []).includes(t))) return false
      return matchesEventVersion(e, version, prefixDepth)
    })
  }

  return { data, loading, error, refresh, filterEvents }
}
