export interface MetricsSnapshot {
  obs: {
    total: number
    by_endpoint: Record<string, number>
    req_per_s: number
    windows: Record<string, number> // keys "6h" | "12h" | "24h" | "7d" | "30d"
    windows_prev: Record<string, number>
    series: number[]
    oldest_sample: string
  }
  limiter: { enabled: boolean; budget: number; remaining: number; waits: number }
  working_set: { packages: number; inflight: number; by_state: Record<string, number> }
  uptime_seconds: number
  sse_clients: number
  polling: string
}
