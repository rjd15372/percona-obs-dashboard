import type { Context } from '../types/api'

export const PPG_CONTEXT: Context = {
  label: 'PPG',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
  // The distribution + its container images; sibling subprojects (extras, …)
  // are hidden by default and get their own selector entries.
  allowedSubprojects: ['containers'],
}

export const PPG_EXTRAS_CONTEXT: Context = {
  label: 'PPG Extras',
  apiBase: '/api/products/ppg',
  prefix: 'isv:percona:ppg',
  subproject: 'extras',
}

export const RELEASES_CONTEXT: Context = {
  label: 'Releases',
  apiBase: '/api/releases/ppg',
  prefix: 'isv:percona:ppg:releases',
}
