export type WindowKey = '24h' | '48h' | '7d'

export interface OverviewCount { name: string; count: number }

export interface OverviewImage {
  project: string
  name: string
  critical: number
  high: number
  oldest_open_days: number   // 0 = none open / unknown
  avg_fix_days: number       // 0 = no closed episodes yet
}

export interface OverviewProject {
  project: string
  rebuilds: number
  top_package?: OverviewCount
  images: OverviewImage[]
}

export interface OverviewSnapshot {
  window: WindowKey
  generated_at: string
  previous_window_rebuild_total: number
  top_repo?: OverviewCount
  projects: OverviewProject[]
}
