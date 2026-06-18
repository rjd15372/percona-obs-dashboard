import type { Event, EventType } from '../types/api'

export const GLYPH: Record<EventType, string> = {
  succeeded: '✓', failed: '✗', broken: '✗', unresolvable: '⚠',
  blocked: '⊘', published: '↑', triggered: '↻', started: '▶',
  created: '+', deleted: '−', build_started: '▶', build_finished: '■',
  version_change: '↕', updated: '◉',
}

export const GLYPH_COLOR: Record<EventType, string> = {
  succeeded: 'var(--ok)', failed: 'var(--fail)', broken: 'var(--blocked)',
  unresolvable: 'var(--blocked)', blocked: 'var(--blocked)',
  published: 'var(--brand-purple)', triggered: 'var(--blocked)', started: 'var(--blocked)',
  created: 'var(--ok)', deleted: 'var(--fail)', build_started: 'var(--info)',
  build_finished: 'var(--blocked)', version_change: 'var(--blocked)', updated: 'var(--blocked)',
}

export const GLYPH_BG: Record<EventType, string> = {
  succeeded: 'var(--ok-tint)', failed: 'var(--fail-tint)', broken: 'var(--blocked-tint)',
  unresolvable: 'var(--blocked-tint)', blocked: 'var(--blocked-tint)',
  published: 'var(--brand-purple-tint)', triggered: 'var(--blocked-tint)', started: 'var(--blocked-tint)',
  created: 'var(--ok-tint)', deleted: 'var(--fail-tint)', build_started: 'var(--info-tint)',
  build_finished: 'var(--blocked-tint)', version_change: 'var(--blocked-tint)', updated: 'var(--blocked-tint)',
}

export const TAG_STYLE: Record<string, string> = {
  ppg:       'background: var(--brand-purple-tint); color: var(--brand-purple);',
  common:    'background: var(--blocked-tint); color: var(--blocked);',
  container: 'background: var(--info-tint); color: var(--info);',
  pr:        'background: var(--warn-tint); color: var(--warn);',
  release:   'background: var(--ok-tint); color: var(--ok);',
}

export const TAG_LABEL: Record<string, string> = {
  ppg: 'PPG', common: 'Common', container: 'Container', pr: 'PR', release: 'Release',
}

export function eventTitle(event: Event): string {
  if (event.repo && event.arch) {
    return event.what.replace(` on ${event.repo}/${event.arch}`, '')
  }
  return event.what
}

export function timeStr(iso: string): string {
  const d = new Date(iso)
  const diff = Date.now() - d.getTime()
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return d.toLocaleDateString()
}

export function showReason(event: Event): boolean {
  return (event.type === 'build_started' || event.type === 'failed') && !!event.why
}

// Returns the formatted version string for display, or null if unavailable.
// Containers: "Tag: 18.4-1-1.7"; RPMs: strips release suffix "17.5-1" → "17.5".
export function displayVersion(version: string | undefined, isContainer: boolean): string | null {
  if (!version) return null
  if (isContainer) return 'Tag: ' + version
  return version.replace(/-[^-]+$/, '')
}
