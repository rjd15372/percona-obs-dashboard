<template>
  <div class="artifacts-panel">
    <ArtifactsVersionBar
      :version="localVersion"
      :available-versions="availableVersions"
      :obs-root="obsRoot"
      :active-tab="artifactsTab"
      @update:version="onVersionChange"
      @update:tab="artifactsTab = $event"
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
import { ref, computed } from 'vue'
import type { Package } from '../types/api'
import type { RepoInfo } from '../composables/useArtifacts'
import { useArtifacts } from '../composables/useArtifacts'
import ArtifactsVersionBar from './ArtifactsVersionBar.vue'
import PackagesSubTab from './PackagesSubTab.vue'
import ContainersSubTab from './ContainersSubTab.vue'

const props = defineProps<{
  packages: Package[]
  initialVersion?: string
}>()

const availableVersions = ['17', '18', '16']
const localVersion = ref(props.initialVersion ?? '17')
const artifactsTab = ref<'packages' | 'containers'>('packages')
const artRepoObs = ref<string>('')
const artArch = ref<'x86_64' | 'aarch64'>('x86_64')
const copiedKey = ref<string | null>(null)
const repos = ref<RepoInfo[]>([])

const obsRoot = computed(() => `isv:percona:ppg:${localVersion.value}`)

const selectedRepo = computed<RepoInfo | null>(
  () => repos.value.find(r => r.obs === artRepoObs.value) ?? null,
)

async function fetchRepos(version: string) {
  try {
    const res = await fetch(`/api/products/ppg/${version}/repos`)
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

function onVersionChange(v: string) {
  localVersion.value = v
  fetchRepos(v)
}

fetchRepos(localVersion.value)

const packagesRef = computed(() => props.packages)

const { packageRows, containerImages } = useArtifacts(
  packagesRef,
  localVersion,
  selectedRepo,
  artArch,
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
