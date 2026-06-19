import { ref, watch, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import type { PackageRow, ContainerImage, ArtifactBinary } from './useArtifacts'

interface ArtifactMetadataItem {
  project: string
  name: string
  repo: string
  arch: string
  kind: 'package' | 'container'
}

interface ArtifactMetadataResult {
  project: string
  name: string
  repo: string
  arch: string
  kind: string
  built_at?: string
  mtime?: number
  binaries?: ArtifactBinary[]
}

const REBUILDING_STATES = new Set(['building', 'scheduled', 'finished'])

function metaKey(project: string, name: string, repo: string, arch: string, kind: string): string {
  return `${project}/${name}/${repo}/${arch}/${kind}`
}

export function useArtifactMetadata(
  packageRows: Ref<PackageRow[]>,
  containerImages: Ref<ContainerImage[]>,
  isLiveContext: Ref<boolean>,
): {
  enrichedPackageRows: ComputedRef<PackageRow[]>
  enrichedContainerImages: ComputedRef<ContainerImage[]>
  isLoading: Ref<boolean>
} {
  const metadataMap = ref(new Map<string, ArtifactMetadataResult>())
  const isLoading = ref(false)
  let controller: AbortController | null = null

  async function fetchMetadata() {
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    isLoading.value = true

    try {
      if (!isLiveContext.value) {
        metadataMap.value = new Map()
        return
      }

      const items: ArtifactMetadataItem[] = [
        ...packageRows.value.map(row => ({
          project: row.project,
          name: row.name,
          repo: row.repo.obs,
          arch: row.arch,
          kind: 'package' as const,
        })),
        ...containerImages.value.map(img => ({
          project: img.project,
          name: img.imageName,
          repo: '',
          arch: '',
          kind: 'container' as const,
        })),
      ]

      if (items.length === 0) {
        metadataMap.value = new Map()
        return
      }

      const res = await fetch('/api/artifacts/metadata', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items }),
        signal,
      })
      if (!res.ok || signal.aborted) return
      const data = await res.json() as { items: ArtifactMetadataResult[] }
      const newMap = new Map<string, ArtifactMetadataResult>()
      for (const result of data.items) {
        newMap.set(metaKey(result.project, result.name, result.repo, result.arch, result.kind), result)
      }
      metadataMap.value = newMap
    } catch {
      // metadata is best-effort; silently ignore network/parse errors (including AbortError)
    } finally {
      isLoading.value = false
    }
  }

  watch([packageRows, containerImages, isLiveContext], fetchMetadata, { immediate: true })

  const enrichedPackageRows = computed<PackageRow[]>(() =>
    packageRows.value.map(row => {
      const meta = metadataMap.value.get(
        metaKey(row.project, row.name, row.repo.obs, row.arch, 'package'),
      )
      if (!meta?.built_at) return row
      return {
        ...row,
        builtAt: meta.built_at,
        mtime: meta.mtime,
        binaries: meta.binaries ?? row.binaries,
        isRebuilding: REBUILDING_STATES.has(row.state),
      }
    })
  )

  const enrichedContainerImages = computed<ContainerImage[]>(() =>
    containerImages.value.map(img => {
      const meta = metadataMap.value.get(
        metaKey(img.project, img.imageName, '', '', 'container'),
      )
      if (!meta?.built_at) return img
      return {
        ...img,
        builtAt: meta.built_at,
        mtime: meta.mtime,
        isRebuilding: REBUILDING_STATES.has(img.rollupState),
      }
    })
  )

  return { enrichedPackageRows, enrichedContainerImages, isLoading }
}
