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

export const SCOPE_STYLE: Record<string, string> = {
  version:   'background: var(--brand-purple-tint); color: var(--brand-purple);',
  container: 'background: var(--info-tint); color: var(--info);',
  release:   'background: var(--ok-tint); color: var(--ok);',
  common:    'background: var(--blocked-tint); color: var(--blocked);',
  ppgcommon: 'background: var(--blocked-tint); color: var(--blocked);',
  pr:        'background: var(--warn-tint); color: var(--warn);',
}

export const SCOPE_LABEL: Record<string, string> = {
  version: 'PPG', ppgcommon: 'PPG Common', common: 'Common',
  container: 'Container', release: 'Release', pr: 'PR',
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
