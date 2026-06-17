import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Package, Target } from '../types/api'

export interface RepoInfo {
  obs: string
  name: string
  type: 'rpm' | 'deb'
}

export interface PackageRow {
  project: string
  name: string
  scope: 'common' | 'ppgcommon' | 'version'
  state: string
  repo: RepoInfo
  arch: string
}

export interface ContainerImage {
  id: string
  imageName: string
  baseOs: string
  registry: string
  tags: string[]
  pullCmd: string
  rollupState: string
  published: boolean
}

export function deriveBaseOs(project: string): string {
  const parts = project.split(':')
  const containerIdx = parts.lastIndexOf('containers')
  if (containerIdx >= 0 && containerIdx < parts.length - 1) {
    const suffix = parts[containerIdx + 1]
    const osMap: Record<string, string> = {
      'ubi8': 'UBI 8',
      'ubi9': 'UBI 9',
      'noble': 'Ubuntu 24.04 Noble',
      'bookworm': 'Debian 12 Bookworm',
    }
    return osMap[suffix] ?? suffix
  }
  return project
}

export function useArtifacts(
  packages: MaybeRef<Package[]>,
  version: MaybeRef<string>,
  selectedRepo: MaybeRef<RepoInfo | null>,
  artArch: MaybeRef<string>,
) {
  const packageRows = computed<PackageRow[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const repo = toValue(selectedRepo)
    const arch = toValue(artArch)

    if (!repo) return []

    const rows: PackageRow[] = []
    for (const pkg of pkgs) {
      const scope = pkg.scope as string
      if (scope !== 'common' && scope !== 'ppgcommon' && scope !== 'version') continue

      // version-scoped packages must belong to the selected version's project
      if (scope === 'version' && !pkg.project.includes(':ppg:' + ver)) continue

      // find a matching target for the selected repo × arch
      const target = pkg.targets?.find(
        (t: Target) => t.repo === repo.obs && t.arch === arch,
      )
      if (!target) continue

      rows.push({
        project: pkg.project,
        name: pkg.name,
        scope: scope as 'common' | 'ppgcommon' | 'version',
        state: target.state ?? '',
        repo,
        arch,
      })
    }
    return rows
  })

  const containerImages = computed<ContainerImage[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)

    return pkgs
      .filter(pkg => pkg.scope === 'container' && pkg.is_container !== false && pkg.project.includes(':ppg:' + ver + ':'))
      .map(pkg => {
        const tags = pkg.container_tags ?? []
        const baseOs = deriveBaseOs(pkg.project)
        const published = pkg.targets?.some((t: Target) => t.published === true) ?? false

        // project "isv:percona:ppg:17:containers:ubi8"
        // → registry path "isv/percona/ppg/17/containers/ubi8"
        const registryPath = pkg.project.split(':').join('/')
        const registry = `registry.opensuse.org/${registryPath}/images/${pkg.name}`

        const pullTag = tags[tags.length - 1] ?? ''
        const pullCmd = pullTag
          ? `docker pull ${registry}:${pullTag}`
          : `docker pull ${registry}`

        return {
          id: pkg.project + '/' + pkg.name,
          imageName: pkg.name,
          baseOs,
          registry,
          tags,
          pullCmd,
          rollupState: pkg.rollup_state ?? '',
          published,
        }
      })
  })

  return { packageRows, containerImages }
}
