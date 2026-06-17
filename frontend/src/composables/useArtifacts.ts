import { computed, toValue } from 'vue'
import type { MaybeRef } from 'vue'
import type { Package, Target } from '../types/api'

export interface Repo {
  short: string
  name: string
  obs: string
  type: 'rpm' | 'deb'
}

export const REPOS: Repo[] = [
  { short: 'el9',    name: 'RHEL 9',       obs: 'RHEL_9',        type: 'rpm' },
  { short: 'el8',    name: 'RHEL 8',       obs: 'RHEL_8',        type: 'rpm' },
  { short: 'deb12',  name: 'Debian 12',    obs: 'Debian_12',     type: 'deb' },
  { short: 'deb11',  name: 'Debian 11',    obs: 'Debian_11',     type: 'deb' },
  { short: 'ub2404', name: 'Ubuntu 24.04', obs: 'xUbuntu_24.04', type: 'deb' },
  { short: 'ub2204', name: 'Ubuntu 22.04', obs: 'xUbuntu_22.04', type: 'deb' },
]

export interface PackageRow {
  project: string
  name: string
  scope: 'common' | 'ppgcommon' | 'version'
  state: string
  repo: Repo
  arch: string
}

export interface ContainerImage {
  id: string
  imageName: string
  baseOs: string
  registry: string
  tags: string[]
  pullCmd: string
  published: boolean
}

export function deriveBaseOs(project: string): string {
  // project ends with :containers:<suffix>
  // e.g. "isv:percona:ppg:17:containers:ubi9" -> "ubi9"
  const parts = project.split(':')
  const containerIdx = parts.lastIndexOf('containers')
  if (containerIdx >= 0 && containerIdx < parts.length - 1) {
    const suffix = parts[containerIdx + 1]
    const osMap: Record<string, string> = {
      'ubi9': 'Oracle Linux 9',
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
  artRepo: MaybeRef<string>,
  artArch: MaybeRef<string>,
) {
  const packageRows = computed<PackageRow[]>(() => {
    const pkgs = toValue(packages)
    const ver = toValue(version)
    const repoShort = toValue(artRepo)
    const arch = toValue(artArch)

    const repo = REPOS.find(r => r.short === repoShort)
    if (!repo) return []

    const rows: PackageRow[] = []
    for (const pkg of pkgs) {
      const scope = pkg.scope as string
      if (scope !== 'common' && scope !== 'ppgcommon' && scope !== 'version') continue

      // version-scoped packages must belong to the selected version's project
      if (scope === 'version' && !pkg.project.includes(':ppg:' + ver)) continue

      // find a matching target for the selected repo x arch
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
      .filter(pkg => pkg.scope === 'container')
      .map(pkg => {
        const tags = pkg.container_tags ?? []
        const baseOs = deriveBaseOs(pkg.project)
        const published = pkg.targets?.some((t: Target) => t.published === true) ?? false

        // pull command uses first tag
        const pullTag = tags[0] ?? ver
        const pullCmd = `docker pull percona/${pkg.name}:${pullTag}`

        return {
          id: pkg.project + '/' + pkg.name,
          imageName: pkg.name,
          baseOs,
          registry: `docker.io/percona/${pkg.name}`,
          tags,
          pullCmd,
          published,
        }
      })
  })

  return { packageRows, containerImages }
}
