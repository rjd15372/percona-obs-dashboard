<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import AppHeader from './components/AppHeader.vue'
import ContextBar from './components/ContextBar.vue'
import HealthHeader from './components/HealthHeader.vue'
import MainGrid from './components/MainGrid.vue'
import PRBoard from './components/PRBoard.vue'
import { usePackages } from './composables/usePackages'
import { useEvents } from './composables/useEvents'
import { usePRPackages } from './composables/usePRPackages'

// Theme
const theme = ref<'light' | 'dark'>('light')
watch(theme, (val) => {
  document.documentElement.setAttribute('data-theme', val === 'dark' ? 'dark' : '')
}, { immediate: true })

function toggleTheme() {
  theme.value = theme.value === 'light' ? 'dark' : 'light'
}

// Navigation state
const version = ref('17')
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

// Event window state
const windowMin = ref(1440)
const customFrom = ref<string | null>(null)
const customTo = ref<string | null>(null)

// Data fetching
const { data: allPackages, refresh: refreshPackages, filterByScope } = usePackages('ppg', version)
const { data: events, refresh: refreshEvents } = useEvents('ppg', version)
const { data: prGroups, refresh: refreshPR } = usePRPackages()

const filteredPackages = computed(() => filterByScope(activeScopes.value))
const updatedAt = ref<string | null>(null)

async function refresh() {
  await Promise.all([
    refreshPackages(),
    refreshEvents(
      windowMin.value === -1 && customFrom.value && customTo.value
        ? { from: customFrom.value, to: customTo.value }
        : { window: windowMin.value }
    ),
    refreshPR(),
  ])
  updatedAt.value = new Date().toISOString()
}

// Initial fetch + 5-min auto-refresh
onMounted(() => { refresh() })
const timer = setInterval(refresh, 5 * 60 * 1000)
onUnmounted(() => clearInterval(timer))

// Re-fetch on version change
watch(version, () => refresh())

// Re-fetch on window change
watch([windowMin, customFrom, customTo], () => refresh())
</script>

<template>
  <div class="min-h-screen bg-bg-app" style="padding: 24px 28px 60px;">
    <div style="max-width: 1360px; margin: 0 auto; display: flex; flex-direction: column; gap: 16px;">
      <AppHeader :theme="theme" @toggle-theme="toggleTheme" />
      <ContextBar
        :version="version"
        :updated-at="updatedAt"
        :active-scopes="activeScopes"
        @update:version="version = $event"
        @toggle-scope="toggleScope"
      />
      <HealthHeader :packages="allPackages" />
      <MainGrid
        :packages="filteredPackages"
        :events="events"
        :window-min="windowMin"
        :custom-from="customFrom"
        :custom-to="customTo"
        @update:window-min="windowMin = $event"
        @update:custom-from="customFrom = $event"
        @update:custom-to="customTo = $event"
      />
      <PRBoard :groups="prGroups" />
    </div>
  </div>
</template>
