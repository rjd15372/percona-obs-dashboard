import { ref, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Context, Event } from '../types/api'
import { matchesVersionKey } from '../lib/versions'

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

  function matchesEventVersion(event: Event, key: string, ctx: Context): boolean {
    if (!key) return true
    const seg = event.project.split(':')[ctx.prefix.split(':').length]
    // Non-numeric segment (common, project events) always passes.
    if (!seg || !/^\d+$/.test(seg)) return true
    return matchesVersionKey(event.project, ctx.prefix, key, ctx.allowedSubprojects)
  }

  function matchesContext(project: string, prefix: string): boolean {
    if (!prefix || project === prefix || project.startsWith(prefix + ':')) return true

    if (prefix.includes(':PR:')) {
      const parts = prefix.split(':')
      const commonPrefix = `${parts.slice(0, 4).join(':')}:common`
      return project === commonPrefix || project.startsWith(`${commonPrefix}:`)
    }
    if (prefix.includes(':releases')) return false

    // Product (devel/staging) boards include product-family and global
    // common events, mirroring the packages query (root:product:common +
    // root:common).
    const parts = prefix.split(':')
    const family = parts.slice(0, -1).join(':')
    const root = parts.slice(0, -2).join(':')
    return project === `${family}:common` || project.startsWith(`${family}:common:`) ||
      project === `${root}:common` || project.startsWith(`${root}:common:`)
  }

  function filterEvents(tags: string[], version: string, ctx: Context): Event[] {
    return data.value.filter(e => {
      if (!matchesContext(e.project, ctx.prefix)) return false
      if (tags.length > 0 && !tags.every(t => (e.tags ?? []).includes(t))) return false
      return matchesEventVersion(e, version, ctx)
    })
  }

  return { data, loading, error, refresh, filterEvents }
}
