import { ref, onUnmounted } from 'vue'

export function useRebuild() {
  const loadingMap = ref(new Map<string, boolean>())
  const errorMap = ref(new Map<string, string>())
  const timers = new Set<ReturnType<typeof setTimeout>>()

  onUnmounted(() => {
    timers.forEach(clearTimeout)
    timers.clear()
  })

  function key(repo: string, arch: string): string {
    return `${repo}/${arch}`
  }

  function scheduleErrorClear(k: string) {
    const t = setTimeout(() => {
      const m = new Map(errorMap.value)
      m.delete(k)
      errorMap.value = m
      timers.delete(t)
    }, 4000)
    timers.add(t)
  }

  async function trigger(project: string, pkg: string, repo: string, arch: string): Promise<void> {
    const k = key(repo, arch)
    loadingMap.value = new Map(loadingMap.value).set(k, true)
    const cleared = new Map(errorMap.value)
    cleared.delete(k)
    errorMap.value = cleared

    try {
      const res = await fetch('/api/rebuild', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project, repo, arch, package: pkg }),
      })
      if (!res.ok) {
        const msg = (await res.text()).trim() || `HTTP ${res.status}`
        const m = new Map(errorMap.value)
        m.set(k, msg)
        errorMap.value = m
        scheduleErrorClear(k)
      }
    } catch {
      const m = new Map(errorMap.value)
      m.set(k, 'Network error')
      errorMap.value = m
      scheduleErrorClear(k)
    } finally {
      const m = new Map(loadingMap.value)
      m.set(k, false)
      loadingMap.value = m
    }
  }

  function isLoading(repo: string, arch: string): boolean {
    return loadingMap.value.get(key(repo, arch)) ?? false
  }

  function errorFor(repo: string, arch: string): string | null {
    return errorMap.value.get(key(repo, arch)) ?? null
  }

  return { trigger, isLoading, errorFor }
}
