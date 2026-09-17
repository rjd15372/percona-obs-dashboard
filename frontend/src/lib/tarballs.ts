// Detection helpers for tarball build artifacts. A tarball is a build target
// in a `…:tarballs` subproject whose repo is an OpenSSL-versioned repo
// (ssl1.1 / ssl3 / ssl3.5 / …). RockyLinux-built packages inside a :tarballs
// subproject (e.g. percona-psql) are NOT tarballs and stay in the Packages view.

export function isTarballRepo(repo: string): boolean {
  return /^ssl/i.test(repo)
}

export function isTarballTarget(project: string, repo: string): boolean {
  return project.endsWith(':tarballs') && isTarballRepo(repo)
}

// tarballRepoOrder sorts ssl repos naturally: ssl1.1 < ssl3 < ssl3.5.
export function tarballRepoOrder(a: string, b: string): number {
  return a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' })
}

// tarballDownloadUrl builds the direct download.opensuse.org URL for a tarball
// artifact of a given arch. The OBS project maps to the repo path with each ':'
// becoming ':/', and the published filename follows the stable convention
//   <name-without--tarball>-<upstreamVersion>-<repo>-linux-<arch>.tar.gz
// where upstreamVersion is pkg.version with the Percona build suffix dropped
// (e.g. "18.6-3" -> "18.6"). Verified against ssl1.1/ssl3/ssl3.5 for PG 16/17/18.
// Callers only build this when version is present, so the URL is never partial.
export function tarballDownloadUrl(
  project: string,
  repo: string,
  name: string,
  version: string,
  arch: string,
): string {
  const base =
    'https://download.opensuse.org/repositories/' +
    project.split(':').join(':/') +
    '/' +
    repo +
    '/'
  const prefix = name.replace(/-tarball$/, '')
  const upstream = version.split('-')[0]
  return `${base}${prefix}-${upstream}-${repo}-linux-${arch}.tar.gz`
}
