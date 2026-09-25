import type { ObsInstance } from '../types/api'
import { obsProject, packageUrl, downloadBase, registryRef, liveLogUrl, projectUrl } from './instances'

const inst: ObsInstance = {
  name: 'Percona', slug: 'percona', root: 'percona',
  web_url: 'https://obs.example.com', download_url: 'https://dl.example.com/repositories',
  registry: 'registry.example.com',
  health: { ok: true, consecutive_failures: 0, mq_connected: true },
}
const a: string = obsProject(inst, 'ppg:17')                            // 'percona:ppg:17'
const b: string = packageUrl(inst, 'ppg:17', 'pg')                      // https://obs.example.com/package/show/percona:ppg:17/pg
const c: string = downloadBase(inst, 'ppg:17', 'RHEL_9')                // https://dl.example.com/repositories/percona:/ppg:/17/RHEL_9/
const d: string = registryRef(inst, 'ppg:17:containers', 'ubi9', 'pg')  // registry.example.com/percona/ppg/17/containers/ubi9/pg
const e: string = liveLogUrl(undefined, 'ppg:17', 'pg', 'R', 'x')       // '' while instances load
const f: string = projectUrl(inst, 'ppg:17')
void a; void b; void c; void d; void e; void f
