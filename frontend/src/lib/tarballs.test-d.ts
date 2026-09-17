import { isTarballRepo, isTarballTarget, tarballRepoOrder, tarballDownloadUrl } from './tarballs'

// Pins the helper signatures.
const a: boolean = isTarballRepo('ssl3')
const b: boolean = isTarballTarget('isv:percona:ppg:staging:18:tarballs', 'ssl3')
const c: number = tarballRepoOrder('ssl1.1', 'ssl3')
const d: string = tarballDownloadUrl('isv:percona:ppg:staging:18:tarballs', 'ssl1.1', 'percona-postgresql-tarball', '18.6-3', 'x86_64')
void a
void b
void c
void d
