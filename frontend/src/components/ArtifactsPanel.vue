<template>
  <div class="artifacts-panel">
    <ArtifactsVersionBar
      :version="localVersion"
      :available-versions="availableVersions"
      :contexts="artifactsContexts"
      :selected-context="selectedContext"
      :active-tab="artifactsTab"
      @update:version="onVersionChange"
      @update:tab="artifactsTab = $event"
      @update:context="onContextChange"
    />

    <PackagesSubTab
      v-if="artifactsTab === 'packages'"
      :package-rows="packageRows"
      :repos="repos"
      :selected-repo="selectedRepo"
      :version="localVersion"
      :art-arch="artArch"
      :copied-key="copiedKey"
      @update:art-repo="artRepoObs = $event"
      @update:art-arch="artArch = $event as 'x86_64' | 'aarch64'"
      @copy="onCopy"
    />

    <ContainersSubTab
      v-else
      :container-images="containerImages"
      :copied-key="copiedKey"
      @copy="onCopy"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import type { Context, PRGroup } from '../types/api'
import type { RepoInfo } from '../composables/useArtifacts'
import { useArtifacts } from '../composables/useArtifacts'
import ArtifactsVersionBar from './ArtifactsVersionBar.vue'
import PackagesSubTab from './PackagesSubTab.vue'
import ContainersSubTab from './ContainersSubTab.vue'

const props = defineProps<{
  prGroups: PRGroup[]
}>()

// Fixed contexts
const PPG_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
}

const RELEASES_CONTEXT: Context = {
  label: 'Releases',
  apiBase: '/api/releases/ppg',
  prefix: 'isv:percona:ppg:releases',
}

// Derive PR contexts from prGroups
const artifactsContexts = computed<Context[]>(() => {
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of props.prGroups) {
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

  return [PPG_CONTEXT, RELEASES_CONTEXT, ...prContexts]
})

// Context state
const selectedContext = ref<Context>(PPG_CONTEXT)

// Package state (self-fetched)
const artifactsPackages = ref<import('../types/api').Package[]>([])
const artifactsLoading = ref(false)

// Version derived from fetched packages
const availableVersions = computed<string[]>(() => {
  const depth = selectedContext.value.prefix.split(':').length
  const versions = new Set<string>()
  for (const pkg of artifactsPackages.value) {
    const parts = pkg.project.split(':')
    const seg = parts[depth]
    if (seg && /^\d+$/.test(seg)) {
      versions.add(seg)
    }
  }
  return [...versions].sort((a, b) => parseInt(b) - parseInt(a))
})

const localVersion = ref('17')
const artifactsTab = ref<'packages' | 'containers'>('packages')
const artRepoObs = ref<string>('')
const artArch = ref<'x86_64' | 'aarch64'>('x86_64')
const copiedKey = ref<string | null>(null)
const repos = ref<RepoInfo[]>([])

const contextPrefix = computed(() => selectedContext.value.prefix)

const selectedRepo = computed<RepoInfo | null>(
  () => repos.value.find(r => r.obs === artRepoObs.value) ?? null,
)

async function fetchPackages(ctx: Context) {
  artifactsLoading.value = true
  try {
    const url = `${ctx.apiBase}/${localVersion.value}/packages`
    const res = await fetch(url)
    const data = await res.json()
    artifactsPackages.value = Array.isArray(data) ? data : (data.packages ?? [])
  } catch {
    artifactsPackages.value = []
  } finally {
    artifactsLoading.value = false
  }
}

async function fetchRepos(version: string) {
  const ctx = selectedContext.value
  let url: string
  if (ctx.apiBase.startsWith('/api/products/')) {
    url = `/api/products/ppg/${version}/repos`
  } else if (ctx.apiBase.startsWith('/api/releases/')) {
    url = `/api/releases/ppg/${version}/repos`
  } else {
    url = `${ctx.apiBase}/${version}/repos`
  }
  try {
    const res = await fetch(url)
    const data = await res.json() as { rpm: { obs: string; name: string }[]; deb: { obs: string; name: string }[] }
    const next: RepoInfo[] = [
      ...data.rpm.map(r => ({ ...r, type: 'rpm' as const })),
      ...data.deb.map(r => ({ ...r, type: 'deb' as const })),
    ]
    repos.value = next
    if (next.length > 0 && !next.find(r => r.obs === artRepoObs.value)) {
      artRepoObs.value = next.find(r => r.type === 'rpm')?.obs ?? next[0].obs
    }
  } catch {
    repos.value = []
  }
}

// When available versions change, snap localVersion to the first one
watch(availableVersions, (versions) => {
  if (versions.length > 0) {
    localVersion.value = versions[0]
  }
})

// Re-fetch repos when version changes
watch(localVersion, (v) => {
  fetchRepos(v)
})

onMounted(() => {
  fetchPackages(selectedContext.value)
})

async function onContextChange(ctx: Context) {
  selectedContext.value = ctx
  await fetchPackages(ctx)
}

function onVersionChange(v: string) {
  localVersion.value = v
}

const { packageRows, containerImages } = useArtifacts(
  artifactsPackages,
  localVersion,
  selectedRepo,
  artArch,
  contextPrefix,
)

let copyTimer: ReturnType<typeof setTimeout> | null = null
function onCopy(key: string, text: string) {
  navigator.clipboard.writeText(text).catch(() => {})
  copiedKey.value = key
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copiedKey.value = null
    copyTimer = null
  }, 2000)
}
</script>

<style scoped>
.artifacts-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
</style>
