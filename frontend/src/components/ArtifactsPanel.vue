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
      :loading="isLoading"
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
import type { ArtifactBinary, ContainerImage, PackageRow, RepoInfo } from '../composables/useArtifacts'
import { useArtifacts } from '../composables/useArtifacts'
import { useArtifactMetadata } from '../composables/useArtifactMetadata'
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
const artifactsLoading = ref(true)

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
const releaseArtifacts = ref<ReleaseArtifactsResponse | null>(null)

const contextPrefix = computed(() => selectedContext.value.prefix)
const isReleaseContext = computed(() => selectedContext.value.apiBase.startsWith('/api/releases/'))

const selectedRepo = computed<RepoInfo | null>(
  () => repos.value.find(r => r.obs === artRepoObs.value) ?? null,
)

async function fetchPackages(ctx: Context) {
  artifactsLoading.value = true
  try {
    // Fetch all versions for the selected context so the version selector can
    // be derived from the complete package corpus. The tab content filters by
    // localVersion client-side.
    const url = `${ctx.apiBase}/_/packages`
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
    if (repos.value.length > 0 && !repos.value.find(r => r.obs === artRepoObs.value)) {
      artRepoObs.value = repos.value.find(r => r.type === 'rpm')?.obs ?? repos.value[0].obs
    }
  } catch {
    repos.value = []
  }
}

async function fetchReleaseArtifacts(version: string) {
  try {
    const res = await fetch(`/api/releases/ppg/${version}/artifacts`)
    if (!res.ok) throw new Error(res.statusText)
    const data = await res.json() as ReleaseArtifactsResponse
    releaseArtifacts.value = data
    repos.value = reposFromReleaseArtifacts(data)
    if (repos.value.length > 0 && !repos.value.find(r => r.obs === artRepoObs.value)) {
      artRepoObs.value = repos.value.find(r => r.type === 'rpm')?.obs ?? repos.value[0].obs
    }
  } catch {
    releaseArtifacts.value = null
    repos.value = []
  }
}

// When available versions change, snap localVersion to the first one
watch(availableVersions, (versions) => {
  if (versions.length > 0) {
    localVersion.value = versions[0]
  }
})

// Re-fetch repos when version changes; immediate so repos load on mount
// even when localVersion default ('17') matches the first available version
watch(localVersion, (v) => {
  if (isReleaseContext.value) {
    fetchReleaseArtifacts(v)
  } else {
    fetchRepos(v)
  }
}, { immediate: true })

onMounted(() => {
  fetchPackages(selectedContext.value)
})

async function onContextChange(ctx: Context) {
  selectedContext.value = ctx
  artRepoObs.value = ''  // clear stale repo so new context auto-selects its first repo
  releaseArtifacts.value = null
  await fetchPackages(ctx)
  // availableVersions watcher only fires fetchRepos when localVersion actually changes.
  // If the version stays the same (e.g. PPG and Releases both have '17'), the watcher
  // is silent and repos for the new context are never fetched — so we call explicitly.
  const versions = availableVersions.value
  if (versions.length > 0 && localVersion.value !== versions[0]) {
    localVersion.value = versions[0]  // watcher fires fetchRepos/fetchReleaseArtifacts
  } else if (isReleaseContext.value) {
    await fetchReleaseArtifacts(localVersion.value)
  } else {
    await fetchRepos(localVersion.value)
  }
}

function onVersionChange(v: string) {
  localVersion.value = v
}

const { packageRows: livePackageRows, containerImages: liveContainerImages } = useArtifacts(
  artifactsPackages,
  localVersion,
  selectedRepo,
  artArch,
  contextPrefix,
)

const { enrichedPackageRows, enrichedContainerImages, isLoading: metadataLoading } = useArtifactMetadata(
  livePackageRows,
  liveContainerImages,
  computed(() => !isReleaseContext.value),
)

const isLoading = computed(() => artifactsLoading.value || metadataLoading.value)

const packageRows = computed<PackageRow[]>(() => {
  if (!isReleaseContext.value) return enrichedPackageRows.value.filter(row => row.binaries && row.binaries.length > 0)
  const repo = selectedRepo.value
  if (!repo || !releaseArtifacts.value) return []
  return releaseArtifacts.value.packages
    .filter(pkg => pkg.repo === repo.obs && pkg.arch === artArch.value)
    .map(pkg => ({
      project: pkg.project,
      name: pkg.name,
      version: '',
      tags: ['release'],
      state: 'succeeded',
      published: true,
      repo: { obs: pkg.repo, name: pkg.repo_name, type: pkg.repo_type as 'rpm' | 'deb' },
      arch: pkg.arch,
      binaries: pkg.binaries,
      builtAt: pkg.built_at,
    }))
})

const containerImages = computed<ContainerImage[]>(() => {
  if (!isReleaseContext.value) return enrichedContainerImages.value
  if (!releaseArtifacts.value) return []
  return releaseArtifacts.value.container_images.map(img => ({
    id: `${img.project}/${img.image_name}`,
    project: img.project,
    imageName: img.image_name,
    baseOs: img.base_os,
    registry: img.registry,
    tags: img.tags,
    pullCmd: img.pull_cmd,
    rollupState: 'succeeded',
    published: true,
    mtime: img.mtime,
    builtAt: img.built_at,
  }))
})

function reposFromReleaseArtifacts(data: ReleaseArtifactsResponse): RepoInfo[] {
  const byObs = new Map<string, RepoInfo>()
  for (const pkg of data.packages) {
    if (!byObs.has(pkg.repo)) {
      byObs.set(pkg.repo, {
        obs: pkg.repo,
        name: pkg.repo_name,
        type: pkg.repo_type,
      })
    }
  }
  return [...byObs.values()].sort((a, b) => {
    if (a.type !== b.type) return a.type === 'rpm' ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

interface ReleasePackageArtifact {
  project: string
  name: string
  repo: string
  repo_name: string
  repo_type: 'rpm' | 'deb'
  arch: string
  binaries: ArtifactBinary[]
  built_at: string
}

interface ReleaseContainerArtifact {
  project: string
  image_name: string
  base_os: string
  registry: string
  tags: string[]
  pull_cmd: string
  mtime: number
  built_at: string
}

interface ReleaseArtifactsResponse {
  version: string
  refreshed_at: string
  packages: ReleasePackageArtifact[]
  container_images: ReleaseContainerArtifact[]
}

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
