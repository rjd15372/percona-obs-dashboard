export type WindowKey = '24h' | '48h' | '7d'

export interface OverviewCount { name: string; count: number }

export interface OverviewInstance {
  instance: string
  ok: number
  failing: number
  building: number
  blocked: number
}

export interface OverviewImage {
  project: string
  name: string
  repo: string
  base_os: string
  critical: number
  high: number
  oldest_open_days: number   // 0 = none open / unknown
  avg_fix_hours: number      // mean of closed episodes; 0 = none yet
}

export interface OverviewProject {
  project: string
  rebuilds: number
  top_package?: OverviewCount
  images: OverviewImage[]
}

export interface OverviewFixStat {
  avg_hours: number // 0 when episodes === 0
  episodes: number
}

// CVE fix times by release stage; PR and devel images are excluded.
export interface OverviewFixTime {
  released: OverviewFixStat
  staging: OverviewFixStat
}

export interface OverviewSnapshot {
  window: WindowKey
  generated_at: string
  previous_window_rebuild_total: number
  top_repo?: OverviewCount
  projects: OverviewProject[]
  by_instance?: OverviewInstance[]
  fix_time?: OverviewFixTime
}
