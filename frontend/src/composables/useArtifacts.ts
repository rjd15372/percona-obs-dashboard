import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Package, Target, CveScan } from '../types/api'

export interface RepoInfo {
  obs: string
  name: string
  type: 'rpm' | 'deb'
}

export interface ArtifactBinary {
  filename: string
  size?: number
  mtime?: number
  built_at?: string
}

export interface PackageRow {
  project: string
  name: string
  version: string
  tags: string[]
  state: string
  published: boolean
  repo: RepoInfo
  arch: string
  binaries?: ArtifactBinary[]
  builtAt?: string
  mtime?: number
  isRebuilding?: boolean
}

export interface ContainerImage {
  id: string
  project: string
  imageName: string
  baseOs: string
  registry: string
  tags: string[]
  pullCmd: string
  rollupState: string
  published: boolean
  mtime?: number
  builtAt?: string
  isRebuilding?: boolean
  cveScans: CveScan[]
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

export function distroGroup(repo: RepoInfo): string {
  const name = repo.name.toLowerCase()
  if (/rhel|centos|rocky|oracle|ubi/.test(name)) return 'RHEL'
  if (/opensuse|suse/.test(name)) return 'openSUSE'
  if (/ubuntu/.test(name)) return 'Ubuntu'
  if (/debian/.test(name)) return 'Debian'
  return 'Other'
}

export function useArtifacts(
  packages: MaybeRef<Package[]>,
  version: MaybeRef<string>,
  selectedRepo: MaybeRef<RepoInfo | null>,
  artArch: MaybeRef<string>,
  contextPrefix: MaybeRef<string>,
) {
  const packageRows = computed<PackageRow[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const repo = toValue(selectedRepo)
    const arch = toValue(artArch)
    const prefix = toValue(contextPrefix)

    if (!repo) return []

    const exactProject = `${prefix}:${ver}`
    const rows: PackageRow[] = []
    for (const pkg of pkgs) {
      // Accept packages at the exact version project OR in sub-projects beneath it
      // (e.g. PR packages live at isv:percona:PR:pr-33:ppg:18:containers:ubi9).
      // Confirmed container images (is_container: true) are excluded — they belong
      // in the Container Images tab, not here.
      const inProject =
        pkg.project === exactProject ||
        pkg.project.startsWith(exactProject + ':')
      if (!inProject || pkg.is_container === true) continue

      const target = pkg.targets?.find(
        (t: Target) => t.repo === repo.obs && t.arch === arch,
      )
      if (!target) continue

      rows.push({
        project: pkg.project,
        name: pkg.name,
        version: pkg.version ?? '',
        tags: pkg.tags ?? [],
        state: target.state ?? '',
        published: target.published === true,
        repo,
        arch,
      })
    }
    return rows
  })

  const containerImages = computed<ContainerImage[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const prefix = toValue(contextPrefix)

    return pkgs
      .filter(pkg =>
        pkg.is_container === true &&
        pkg.project.startsWith(`${prefix}:${ver}:`)
      )
      .map(pkg => {
        const tags = pkg.container_tags ?? []
        const baseOs = deriveBaseOs(pkg.project)
        const published = pkg.targets?.some((t: Target) => t.published === true) ?? false

        const registryPath = pkg.project.toLowerCase().split(':').join('/')
        const registry = `registry.opensuse.org/${registryPath}/images/${pkg.name}`

        const pullTag = tags[tags.length - 1] ?? ''
        const pullCmd = pullTag
          ? `docker pull ${registry}:${pullTag}`
          : `docker pull ${registry}`

        return {
          id: pkg.project + '/' + pkg.name,
          project: pkg.project,
          imageName: pkg.name,
          baseOs,
          registry,
          tags,
          pullCmd,
          rollupState: pkg.rollup_state ?? '',
          published,
          cveScans: pkg.cve_scans ?? [],
        }
      })
  })

  return { packageRows, containerImages }
}
