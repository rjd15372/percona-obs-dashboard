export type BuildState = 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'scheduled' | 'building' | 'finished'
export type PackageScope = 'common' | 'ppgcommon' | 'version' | 'container' | 'release' | 'pr'
export type EventType = 'triggered' | 'started' | 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'published' | 'created' | 'deleted' | 'build_started' | 'build_finished' | 'version_change'

export interface Context {
  label: string
  apiBase: string  // e.g. "/api/products/ppg" or "/api/pr/pr-92/ppg"
  prefix: string   // e.g. "isv:percona:ppg" or "isv:percona:PR:pr-92:ppg"
}

export interface Trigger {
  what: string
  kind: string
  at: string // ISO 8601
}

export interface Target {
  repo: string
  arch: string
  state: BuildState
  blocked_by?: string
}

export interface Package {
  project: string
  name: string
  scope: PackageScope
  rollup_state: BuildState
  ok_targets: number
  total_targets: number
  trigger?: Trigger // optional
  targets: Target[]
  updated_at: string // ISO 8601
}

export interface PRGroup {
  pr: string
  rollup_state: BuildState
  packages: Package[]
}

export interface Event {
  id: string
  type: EventType
  scope: string
  project: string
  package: string
  repo?: string // optional
  arch?: string // optional
  what: string
  why: string
  url: string
  at: string // ISO 8601
}
