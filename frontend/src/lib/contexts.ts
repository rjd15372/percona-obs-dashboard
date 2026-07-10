import type { Context } from '../types/api'

export const PPG_DEVEL_CONTEXT: Context = {
  label: 'PPG Devel',
  apiBase: '/api/products/ppg/devel',
  prefix: 'isv:percona:ppg:devel',
  // Subprojects absorbed into the plain version entry; every other
  // subproject (extras, tde, …) surfaces as a <version>:<sub> entry in
  // the version selector.
  allowedSubprojects: ['containers'],
}

export const PPG_STAGING_CONTEXT: Context = {
  label: 'PPG Staging',
  apiBase: '/api/products/ppg/staging',
  prefix: 'isv:percona:ppg:staging',
  allowedSubprojects: ['containers'],
}

export const RELEASES_CONTEXT: Context = {
  label: 'Releases',
  apiBase: '/api/releases/ppg',
  prefix: 'isv:percona:ppg:releases',
}
