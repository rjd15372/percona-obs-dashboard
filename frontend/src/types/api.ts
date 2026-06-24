export type BuildState = 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'scheduled' | 'building' | 'finished' | 'published'
export type EventType = 'triggered' | 'started' | 'succeeded' | 'failed' | 'unresolvable' | 'broken' | 'blocked' | 'published' | 'created' | 'deleted' | 'build_started' | 'build_finished' | 'version_change' | 'updated' | 'cve_scan_started' | 'cve_scan_finished' | 'cve_scan_failed'

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
  started_at?: string
  details?: string
  blocked_by?: string
  build_reason?: string
  build_reason_packages?: string[]
  published?: boolean
}

export interface CveFinding {
  id: string
  pkg: string
  installed: string
  fixed: string
  severity: 'HIGH' | 'CRITICAL'
  title: string
}

export interface CveScan {
  arch: string
  image_ref: string
  scanned_at: string
  critical_count: number
  high_count: number
  findings?: CveFinding[]
}

export interface Package {
  project: string
  name: string
  tags?: string[]
  is_release?: boolean
  rollup_state: BuildState
  ok_targets: number
  total_targets: number
  is_container?: boolean
  version?: string
  trigger?: Trigger // optional
  targets: Target[]
  updated_at: string // ISO 8601
  state_changed_at?: string // ISO 8601; absent when NULL
  container_tags?: string[]
  cve_scans?: CveScan[]
}

export interface PRGroup {
  pr: string
  rollup_state: BuildState
  packages: Package[]
}

export interface Event {
  id: string
  type: EventType
  tags?: string[]
  project: string
  package: string
  repo?: string // optional
  arch?: string // optional
  what: string
  why: string
  version?: string
  url: string
  at: string // ISO 8601
}
