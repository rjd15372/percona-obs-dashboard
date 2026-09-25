import type { ObsInstance } from '../types/api'

// Instance project name for a logical project on inst.
export function obsProject(inst: ObsInstance, project: string): string {
  return inst.root ? `${inst.root}:${project}` : project
}

export function projectUrl(inst: ObsInstance | undefined, project: string): string {
  return inst ? `${inst.web_url}/project/show/${obsProject(inst, project)}` : ''
}

export function packageUrl(inst: ObsInstance | undefined, project: string, name: string): string {
  return inst ? `${inst.web_url}/package/show/${obsProject(inst, project)}/${name}` : ''
}

export function liveLogUrl(inst: ObsInstance | undefined, project: string, name: string, repo: string, arch: string): string {
  return inst ? `${inst.web_url}/package/live_build_log/${obsProject(inst, project)}/${name}/${repo}/${arch}` : ''
}

// Repository base URL (trailing slash) for a project's repo on inst.
export function downloadBase(inst: ObsInstance | undefined, project: string, repo: string): string {
  return inst ? `${inst.download_url}/${obsProject(inst, project).split(':').join(':/')}/${repo}/` : ''
}

export function registryRef(inst: ObsInstance | undefined, project: string, repo: string, name: string): string {
  return inst ? `${inst.registry}/${obsProject(inst, project).toLowerCase().split(':').join('/')}/${repo}/${name}` : ''
}
