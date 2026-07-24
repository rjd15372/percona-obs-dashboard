import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Package, Target, CveScan, Context } from '../types/api'
import { matchesVersionKey } from '../lib/versions'

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

function baseOsFromRepo(repo?: string): string {
  switch (repo) {
    case 'ubi8': return 'UBI 8'
    case 'ubi9': return 'UBI 9'
    case 'noble': return 'Ubuntu 24.04 Noble'
    case 'bookworm': return 'Debian 12 Bookworm'
    default: return ''
  }
}

export function deriveBaseOs(project: string, repo?: string): string {
  const fromRepo = baseOsFromRepo(repo)
  if (fromRepo) return fromRepo
  const parts = project.split(':')
  const containerIdx = parts.lastIndexOf('containers')
  if (containerIdx >= 0 && containerIdx < parts.length - 1) {
    const suffix = parts[containerIdx + 1]
    return baseOsFromRepo(suffix) || suffix
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
  context: MaybeRef<Context>,
) {
  // matchesProject: does an OBS project belong to this context at the
  // selected version key? Plain keys own the version root + absorbed
  // subprojects; extension keys ("17:extras") own that subtree; contexts
  // without allowedSubprojects (PR, Releases) keep the historical catch-all.
  const matchesProject = (project: string, ver: string): boolean => {
    const ctx = toValue(context)
    return matchesVersionKey(project, ctx.prefix, ver, ctx.allowedSubprojects)
  }

  const packageRows = computed<PackageRow[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const repo = toValue(selectedRepo)
    const arch = toValue(artArch)

    if (!repo) return []

    const rows: PackageRow[] = []
    for (const pkg of pkgs) {
      // Confirmed container images (is_container: true) are excluded — they belong
      // in the Container Images tab, not here.
      if (!matchesProject(pkg.project, ver) || pkg.is_container === true) continue

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

    return pkgs
      .filter(pkg =>
        pkg.is_container === true &&
        matchesProject(pkg.project, ver)
      )
      .flatMap(pkg => {
        const tags = pkg.container_tags ?? []
        const pullTag = tags[tags.length - 1] ?? ''
        // Distinct build repos = base images. Old-layout containers built against
        // a single "images" repo (base OS in the project); new-layout containers
        // build against ubi8/ubi9/… — one row per repo.
        const targets = pkg.targets ?? []
        const repos = [...new Set(targets.map((t: Target) => t.repo))]
        const effectiveRepos = repos.length > 0 ? repos : ['images']
        return effectiveRepos.map(repo => {
          const baseOs = deriveBaseOs(pkg.project, repo)
          const registryPath = pkg.project.toLowerCase().split(':').join('/')
          const registry = `registry.opensuse.org/${registryPath}/${repo}/${pkg.name}`
          const pullCmd = pullTag
            ? `docker pull ${registry}:${pullTag}`
            : `docker pull ${registry}`
          const published = targets.some((t: Target) => t.repo === repo && t.published === true)
          return {
            id: pkg.project + '/' + pkg.name + '/' + repo,
            project: pkg.project,
            imageName: pkg.name,
            baseOs,
            registry,
            tags,
            pullCmd,
            rollupState: pkg.rollup_state ?? '',
            published,
            cveScans: (pkg.cve_scans ?? []).filter((s: CveScan) => s.repo === repo),
          }
        })
      })
  })

  return { packageRows, containerImages }
}
