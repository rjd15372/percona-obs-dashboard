<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import ContextBar from './components/ContextBar.vue'
import HealthHeader from './components/HealthHeader.vue'
import MainGrid from './components/MainGrid.vue'
import ArtifactsPanel from './components/ArtifactsPanel.vue'
import type { Context } from './types/api'
import { usePackages } from './composables/usePackages'
import { useEvents } from './composables/useEvents'
import { usePRPackages } from './composables/usePRPackages'
import { useRealtimeStream } from './composables/useRealtimeStream'

// Main tab
const mainTab = ref<'board' | 'artifacts'>('board')

// Theme
const theme = ref<'light' | 'dark'>('light')
watch(theme, (val) => {
  document.documentElement.setAttribute('data-theme', val === 'dark' ? 'dark' : '')
}, { immediate: true })

function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light'
}

// Context
const DEFAULT_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
}
const selectedContext = ref<Context>(DEFAULT_CONTEXT)
const prefixDepth = computed(() => selectedContext.value.prefix.split(':').length)

// Navigation state
const version = ref('')
const activeScopes = ref<string[]>([])

function toggleScope(scope: string) {
  if (scope === 'all') {
    activeScopes.value = []
    return
  }
  const idx = activeScopes.value.indexOf(scope)
  if (idx >= 0) {
    activeScopes.value = activeScopes.value.filter(s => s !== scope)
  } else {
    activeScopes.value = [...activeScopes.value, scope]
  }
}

function selectContext(ctx: Context) {
  selectedContext.value = ctx
  activeScopes.value = []
  // Fetch immediately so packages update before availableVersions watcher fires.
  // The version watcher may fire a second request if version resets, but the
  // server ignores the version URL param so both return the same data.
  refresh()
}

// Event window state
const windowMin = ref(1440)
const customFrom = ref<string | null>(null)
const customTo = ref<string | null>(null)

// Data fetching
const apiBase = computed(() => selectedContext.value.apiBase)
const { data: allPackages, rawData: rawPackages, availableVersions, refresh: refreshPackages, filterByScope } = usePackages(apiBase, version, prefixDepth)
const { data: events, refresh: refreshEvents, filterEvents } = useEvents(apiBase, version)
const { data: prGroups, refresh: refreshPR } = usePRPackages()

// Reset to highest available only when a specific version is selected but no longer exists.
// When version is '' (All), leave it alone — All is always valid.
watch(availableVersions, (vers) => {
  if (vers.length > 0 && version.value !== '' && !vers.includes(version.value)) {
    version.value = vers[0]
  }
})

// Derive available contexts from PR groups data
const contexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 2 >= parts.length) continue
      const prSegment = parts[prIdx + 1]   // "pr-92"
      const subproject = parts[prIdx + 2]  // "ppg"
      const key = `${prSegment}:${subproject}`
      if (seen.has(key)) continue
      seen.add(key)
      const prNum = prSegment.replace(/^pr-/i, '')
      prContexts.push({
        label: `PR #${prNum} · ${subproject}`,
        apiBase: `/api/pr/${prSegment}/${subproject}`,
        prefix: `isv:percona:PR:${prSegment}:${subproject}`,
      })
    }
  }

  prContexts.sort((a, b) => {
    const na = parseInt(a.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    return nb - na
  })

  return [DEFAULT_CONTEXT, ...prContexts]
})

const filteredPackages = computed(() => filterByScope(activeScopes.value))
const filteredEvents = computed(() => filterEvents(activeScopes.value, version.value, prefixDepth.value, selectedContext.value.prefix))
const updatedAt = ref<string | null>(null)

async function refresh() {
  const isCustom = windowMin.value === -1
  const hasCustomRange = customFrom.value != null && customTo.value != null
  // Skip fetch when Custom is active but dates aren't set yet — window=-1 returns 400.
  const eventsOpts = isCustom
    ? (hasCustomRange ? { from: customFrom.value!, to: customTo.value! } : null)
    : { window: windowMin.value }
  await Promise.all([
    refreshPackages(),
    eventsOpts ? refreshEvents(eventsOpts) : Promise.resolve(),
    refreshPR(),
  ])
  updatedAt.value = new Date().toISOString()
}

// Initial fetch + SSE real-time stream
onMounted(() => {
  refresh()
})
useRealtimeStream(rawPackages, events, prGroups, refresh, refreshPR)

// Re-fetch on version change
watch(version, () => refresh())

// Re-fetch on window change
watch([windowMin, customFrom, customTo], () => refresh())
</script>

<template>
  <div class="min-h-screen bg-bg-app" style="padding: 24px 28px 60px;">
    <div style="max-width: 1360px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px;">
      <AppHeader :theme="theme" :main-tab="mainTab" @toggle-theme="toggleTheme" @update:main-tab="mainTab = $event" />
      <template v-if="mainTab === 'board'">
        <ContextBar
          :version="version"
          :updated-at="updatedAt"
          :active-scopes="activeScopes"
          :contexts="contexts"
          :selected-context="selectedContext"
          :available-versions="availableVersions"
          @update:version="version = $event"
          @toggle-scope="toggleScope"
          @update:context="selectContext"
        />
        <HealthHeader :packages="allPackages" />
        <MainGrid
          :packages="filteredPackages"
          :events="filteredEvents"
          :window-min="windowMin"
          :custom-from="customFrom"
          :custom-to="customTo"
          @update:window-min="windowMin = $event"
          @update:custom-from="customFrom = $event"
          @update:custom-to="customTo = $event"
        />
      </template>
      <ArtifactsPanel
        v-else
        :packages="rawPackages"
        :available-versions="availableVersions"
        :initial-version="version || availableVersions[0] || '17'"
      />
    </div>
  </div>
</template>
