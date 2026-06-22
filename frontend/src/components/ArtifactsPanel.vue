<template>
  <div class="artifacts-panel">
    <ArtifactsVersionBar
      :version="props.artifactsVersion"
      :available-versions="availableVersions"
      :contexts="props.artifactsContexts"
      :selected-context="props.artifactsContext"
      :active-tab="props.artifactsTab"
      @update:version="onVersionChange"
      @update:tab="emit('update:artifactsTab', $event)"
      @update:context="onContextChange"
    />

    <PackagesSubTab
      v-if="props.artifactsTab === 'packages'"
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
import { ref, computed, watch, onMounted, nextTick } from 'vue'
import type { Context } from '../types/api'
import type { ArtifactBinary, ContainerImage, PackageRow, RepoInfo } from '../composables/useArtifacts'
import { useArtifacts } from '../composables/useArtifacts'
import { useArtifactMetadata } from '../composables/useArtifactMetadata'
import ArtifactsVersionBar from './ArtifactsVersionBar.vue'
import PackagesSubTab from './PackagesSubTab.vue'
import ContainersSubTab from './ContainersSubTab.vue'

const props = defineProps<{
  artifactsContexts: Context[]
  artifactsVersion: string
  artifactsContext: Context
  artifactsTab: 'packages' | 'containers'
}>()

const emit = defineEmits<{
  'update:artifactsVersion': [v: string]
  'update:artifactsContext': [ctx: Context]
  'update:artifactsTab': [tab: 'packages' | 'containers']
}>()

// Computed aliases so the rest of the component body can use them unchanged
const localVersion = computed(() => props.artifactsVersion)
const contextPrefix = computed(() => props.artifactsContext.prefix)
const isReleaseContext = computed(() => props.artifactsContext.apiBase.startsWith('/api/releases/'))

// Package state (self-fetched)
const artifactsPackages = ref<import('../types/api').Package[]>([])
const pendingFetches = ref(0)

// Version derived from fetched packages
const availableVersions = computed<string[]>(() => {
  const depth = props.artifactsContext.prefix.split(':').length
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

const artRepoObs = ref<string>('')
const artArch = ref<'x86_64' | 'aarch64'>('x86_64')
const copiedKey = ref<string | null>(null)
const repos = ref<RepoInfo[]>([])
const releaseArtifacts = ref<ReleaseArtifactsResponse | null>(null)

const selectedRepo = computed<RepoInfo | null>(
  () => repos.value.find(r => r.obs === artRepoObs.value) ?? null,
)

async function fetchPackages(ctx: Context) {
  pendingFetches.value++
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
    pendingFetches.value--
  }
}

async function fetchRepos(version: string) {
  const ctx = props.artifactsContext
  let url: string
  if (ctx.apiBase.startsWith('/api/products/')) {
    url = `/api/products/ppg/${version}/repos`
  } else {
    url = `${ctx.apiBase}/${version}/repos`
  }
  pendingFetches.value++
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
  } finally {
    pendingFetches.value--
  }
}

async function fetchReleaseArtifacts(version: string) {
  pendingFetches.value++
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
  } finally {
    pendingFetches.value--
  }
}

// When available versions change, snap to first if current version is absent
watch(availableVersions, (versions) => {
  if (versions.length > 0 && (props.artifactsVersion === '' || !versions.includes(props.artifactsVersion))) {
    emit('update:artifactsVersion', versions[0])
  }
})

// Re-fetch repos when version changes; immediate so repos load on mount.
// Guard against empty string — artifactsVersion starts as '' until URL is
// hydrated or availableVersions snaps to the latest version.
watch(() => props.artifactsVersion, (v) => {
  if (!v) return
  if (isReleaseContext.value) {
    fetchReleaseArtifacts(v)
  } else {
    fetchRepos(v)
  }
}, { immediate: true })

onMounted(() => {
  fetchPackages(props.artifactsContext)
})

async function onContextChange(ctx: Context) {
  emit('update:artifactsContext', ctx)
  artRepoObs.value = ''  // clear stale repo so new context auto-selects its first repo
  releaseArtifacts.value = null
  await fetchPackages(ctx)
  // Wait for Vue to flush the context prop so that availableVersions (which uses
  // props.artifactsContext.prefix depth) and fetchRepos (which reads
  // props.artifactsContext) both see the newly-selected context, not the old one.
  await nextTick()
  const versions = availableVersions.value
  if (versions.length > 0 && props.artifactsVersion !== versions[0]) {
    emit('update:artifactsVersion', versions[0])
  } else if (ctx.apiBase.startsWith('/api/releases/')) {
    await fetchReleaseArtifacts(props.artifactsVersion)
  } else {
    await fetchRepos(props.artifactsVersion)
  }
}

function onVersionChange(v: string) {
  emit('update:artifactsVersion', v)
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

const isLoading = computed(() => pendingFetches.value > 0 || metadataLoading.value)

const packageRows = computed<PackageRow[]>(() => {
  if (!isReleaseContext.value) return enrichedPackageRows.value.filter(row => row.binaries && row.binaries.length > 0)
  const repo = selectedRepo.value
  if (!repo || !releaseArtifacts.value) return []
  return releaseArtifacts.value.packages
    .filter(pkg => pkg.repo === repo.obs && pkg.arch === artArch.value)
    .map(pkg => ({
      project: pkg.project,
      name: pkg.name,
      version: pkg.version ?? '',
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
  version?: string
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
