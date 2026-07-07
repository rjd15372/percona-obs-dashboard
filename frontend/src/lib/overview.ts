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

// Category grouping for the overview tables: logical projects are grouped
// into Devel / Releases / PRs sections, in that order.
export type ProjectCategory = 'Devel' | 'Releases' | 'PRs'

export const CATEGORY_ORDER: ProjectCategory[] = ['Devel', 'Releases', 'PRs']

export function categoryOf(project: string): ProjectCategory {
  if (project.includes(':PR:')) return 'PRs'
  if (project.endsWith(':releases')) return 'Releases'
  return 'Devel'
}

// Splits items into non-empty category sections, preserving item order.
export function groupByCategory<T>(
  items: T[],
  projectOf: (item: T) => string,
): { category: ProjectCategory; items: T[] }[] {
  return CATEGORY_ORDER
    .map(category => ({
      category,
      items: items.filter(i => categoryOf(projectOf(i)) === category),
    }))
    .filter(g => g.items.length > 0)
}
