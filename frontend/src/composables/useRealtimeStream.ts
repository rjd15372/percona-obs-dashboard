import { onMounted, onUnmounted, type Ref } from 'vue'
import type { Package, PRGroup, Event, Target } from '../types/api'

const SEVERITY: Record<string, number> = {
  broken: 5, failed: 4, unresolvable: 3, blocked: 2,
  building: 1, finished: 1, scheduled: 1, succeeded: 0,
}

function prNumberFromProject(project: string): string {
  const parts = project.split(':')
  const idx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (idx >= 0 && idx + 1 < parts.length) {
    return parts[idx + 1].toLowerCase().replace(/^pr-/, '')
  }
  return ''
}

function projectInContext(project: string, prefix: string): boolean {
  if (project === prefix || project.startsWith(prefix + ':')) return true

  if (prefix.includes(':PR:')) {
    const parts = prefix.split(':')
    const commonPrefix = `${parts.slice(0, 4).join(':')}:common`
    return project === commonPrefix || project.startsWith(`${commonPrefix}:`)
  }

  // The main PPG board includes product packages plus product/global common
  // packages. Release contexts are exact subtrees.
  if (prefix.endsWith(':ppg') && !prefix.includes(':PR:') && !prefix.includes(':releases')) {
    const root = prefix.slice(0, -':ppg'.length)
    return project === `${prefix}:common` ||
      project.startsWith(`${prefix}:common:`) ||
      project === `${root}:common` ||
      project.startsWith(`${root}:common:`)
  }

  return false
}

function mergeTags(prev: string[] | undefined, next: string[] | undefined): string[] | undefined {
  if (!prev?.length) return next
  if (!next?.length) return prev
  return [...new Set([...next, ...prev])]
}

function mergeTarget(prev: Target | undefined, next: Target): Target {
  if (!prev || prev.state !== next.state) return next
  return {
    ...next,
    details: next.details || prev.details,
    blocked_by: next.blocked_by || prev.blocked_by,
    build_reason: next.build_reason || prev.build_reason,
    build_reason_packages: next.build_reason_packages?.length
      ? next.build_reason_packages
      : prev.build_reason_packages,
    published: next.published || prev.published,
  }
}

function mergePackage(prev: Package, next: Package): Package {
  if (prev.rollup_state !== next.rollup_state) return next

  const previousTargets = new Map(prev.targets.map(t => [`${t.repo}/${t.arch}`, t]))
  return {
    ...prev,
    ...next,
    tags: mergeTags(prev.tags, next.tags),
    is_container: next.is_container ?? prev.is_container,
    version: next.version || prev.version,
    container_tags: next.container_tags?.length ? next.container_tags : prev.container_tags,
    trigger: next.trigger ?? prev.trigger,
    state_changed_at: next.state_changed_at ?? prev.state_changed_at,
    targets: next.targets.map(t => mergeTarget(previousTargets.get(`${t.repo}/${t.arch}`), t)),
  }
}

function upsertPackage(packages: Ref<Package[]>, pkg: Package): boolean {
  const idx = packages.value.findIndex(
    p => p.project === pkg.project && p.name === pkg.name,
  )
  if (idx < 0) return false
  packages.value.splice(idx, 1, mergePackage(packages.value[idx], pkg))
  return true
}

export function useRealtimeStream(
  packages: Ref<Package[]>,
  events: Ref<Event[]>,
  prGroups: Ref<PRGroup[]>,
  selectedPrefix: Ref<string>,
  refresh: () => void,
  refreshPR: () => void,
): void {
  let es: EventSource | null = null
  let wasError = false

  function connect(): void {
    es = new EventSource('/api/stream')

    es.onopen = (): void => {
      if (wasError) {
        wasError = false
        refresh()
      }
    }

    es.onmessage = (e: MessageEvent): void => {
      const msg = JSON.parse(e.data as string) as { type: string; data: unknown }

      if (msg.type === 'package_update') {
        const pkg = msg.data as Package
        const prNum = prNumberFromProject(pkg.project)
        const inSelectedContext = projectInContext(pkg.project, selectedPrefix.value)

        if (prNum) {
          const group = prGroups.value.find(g => g.pr === prNum)
          if (!group) {
            refreshPR()
          } else {
            const pkgIdx = group.packages.findIndex(
              p => p.project === pkg.project && p.name === pkg.name,
            )
            if (pkgIdx >= 0) {
              group.packages[pkgIdx] = mergePackage(group.packages[pkgIdx], pkg)
            } else {
              group.packages.push(pkg)
            }
            const worst = group.packages.reduce((acc, p) => {
              return (SEVERITY[p.rollup_state] ?? 0) > (SEVERITY[acc] ?? 0)
                ? p.rollup_state
                : acc
            }, 'succeeded' as string)
            group.rollup_state = worst as PRGroup['rollup_state']
          }

          if (inSelectedContext && !upsertPackage(packages, pkg)) {
            packages.value.push(pkg)
          }
        } else {
          if (upsertPackage(packages, pkg)) {
            return
          }
          if (inSelectedContext) {
            refresh()
          }
        }
      } else if (msg.type === 'new_event') {
        events.value.unshift(msg.data as Event)
        if (events.value.length > 200) {
          events.value.length = 200
        }
      }
    }

    es.onerror = (): void => {
      wasError = true
    }
  }

  onMounted(connect)
  onUnmounted((): void => {
    es?.close()
  })
}
