import { onMounted, onUnmounted, type Ref } from 'vue'
import type { Package, PRGroup, Event } from '../types/api'

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

export function useRealtimeStream(
  packages: Ref<Package[]>,
  events: Ref<Event[]>,
  prGroups: Ref<PRGroup[]>,
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

        if (prNum) {
          const group = prGroups.value.find(g => g.pr === prNum)
          if (!group) {
            refreshPR()
          } else {
            const pkgIdx = group.packages.findIndex(
              p => p.project === pkg.project && p.name === pkg.name,
            )
            if (pkgIdx >= 0) {
              group.packages[pkgIdx] = pkg
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
        } else {
          const idx = packages.value.findIndex(
            p => p.project === pkg.project && p.name === pkg.name,
          )
          if (idx >= 0) {
            packages.value.splice(idx, 1, pkg)
          } else {
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
