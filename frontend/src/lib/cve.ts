import type { CveScan } from '../types/api'

// formatArtifactTime renders an ISO timestamp in the browser locale, falling
// back to the raw value when unparsable.
export function formatArtifactTime(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

// cveDuration renders how long ago `since` was, in the dashboard's compact
// day/week vocabulary ("< 1d", "3d", "2w 4d").
export function cveDuration(since: string): string {
  const diffMs = Date.now() - new Date(since).getTime()
  const days = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (days < 1) return '< 1d'
  if (days < 7) return `${days}d`
  const weeks = Math.floor(days / 7)
  const remainder = days % 7
  return remainder === 0 ? `${weeks}w` : `${weeks}w ${remainder}d`
}

// latestScanTime formats the most recent scanned_at across a set of scans.
export function latestScanTime(scans: CveScan[]): string {
  if (scans.length === 0) return ''
  const latest = scans.reduce((a, b) => (a.scanned_at > b.scanned_at ? a : b))
  return formatArtifactTime(latest.scanned_at)
}
