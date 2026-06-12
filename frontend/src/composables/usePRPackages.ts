import { ref } from 'vue'
import type { PRGroup } from '../types/api'

export function usePRPackages() {
  const data = ref<PRGroup[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function refresh() {
    loading.value = true
    error.value = null
    try {
      const res = await fetch('/api/pr/packages')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      data.value = await res.json()
    } catch (e) {
      error.value = String(e)
    } finally {
      loading.value = false
    }
  }

  return { data, loading, error, refresh }
}
