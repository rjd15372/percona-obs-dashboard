// Shared helpers for the Overview panel components (StatCard consumers,
// RebuildBarChart, CveExposureTable). Kept in one place so the age-color
// escalation rule and project accent assignment can't drift between them.

export const PROJECT_ACCENTS = ['#6E3FF3', '#2A78D4', '#1F9D55', '#E08A00', '#B0203A']

export function ageColor(days: number): string {
  if (days >= 45) return 'var(--crit)'
  if (days >= 21) return 'var(--high)'
  return 'var(--text-secondary)'
}

export function ageLabel(days: number): string {
  return days === 0 ? '—' : `${days}d`
}
