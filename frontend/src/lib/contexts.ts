import type { Context, PRGroup } from '../types/api'

export const PPG_DEVEL_CONTEXT: Context = {
  label: 'PPG Devel',
  apiBase: '/api/products/ppg/devel',
  prefix: 'isv:percona:ppg:devel',
  // Subprojects absorbed into the plain version entry; every other
  // subproject (extras, tde, …) surfaces as a <version>:<sub> entry in
  // the version selector.
  allowedSubprojects: ['containers'],
}

export const PPG_STAGING_CONTEXT: Context = {
  label: 'PPG Staging',
  apiBase: '/api/products/ppg/staging',
  prefix: 'isv:percona:ppg:staging',
  allowedSubprojects: ['containers'],
}

export const RELEASES_CONTEXT: Context = {
  label: 'Releases',
  apiBase: '/api/releases/ppg',
  prefix: 'isv:percona:ppg:releases',
}

const PR_TIERS = ['staging', 'devel'] as const

// prArtifactsContexts expands PR package groups into one Artifacts context per
// (PR, tier). PR projects follow the devel/staging-restructured layout
// isv:percona:PR:<pr>:ppg:<tier>:<version>[:<sub>], so each tier needs its own
// context whose prefix includes ppg:<tier> — that way the shared
// deriveVersionKeys/matchesVersionKey (which read the version at the prefix
// depth) resolve the numeric version, exactly as they do for the devel/staging
// contexts, and 'containers' folds into the plain version entry. PR projects
// without a versioned ppg:<tier> (e.g. common:deps) contribute no context.
export function prArtifactsContexts(groups: PRGroup[]): Context[] {
  const seen = new Set<string>()
  const contexts: Context[] = []
  for (const group of groups) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      // Expect: …:PR:<pr>:ppg:<tier>:<version>…
      if (prIdx < 0) continue
      const prSegment = parts[prIdx + 1]
      const tier = parts[prIdx + 3]
      const version = parts[prIdx + 4]
      if (!prSegment || parts[prIdx + 2] !== 'ppg') continue
      if (!PR_TIERS.includes(tier as (typeof PR_TIERS)[number])) continue
      if (!version || !/^\d+$/.test(version)) continue
      const key = `${prSegment}:${tier}`
      if (seen.has(key)) continue
      seen.add(key)
      const prNum = prSegment.replace(/^pr-/i, '')
      const tierLabel = tier.charAt(0).toUpperCase() + tier.slice(1)
      contexts.push({
        label: `PR #${prNum} · ${tierLabel}`,
        apiBase: `/api/pr/${prSegment}`,
        prefix: `isv:percona:PR:${prSegment}:ppg:${tier}`,
        allowedSubprojects: ['containers'],
      })
    }
  }
  contexts.sort((a, b) => {
    const na = parseInt(a.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    if (na !== nb) return nb - na // PR number descending
    const ta = a.prefix.split(':')[5] ?? ''
    const tb = b.prefix.split(':')[5] ?? ''
    if (ta === tb) return 0
    return ta === 'staging' ? -1 : 1 // staging before devel
  })
  return contexts
}
