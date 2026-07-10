const ROOT = 'isv:percona:'

/** Display-only: strip the constant root project prefix from an OBS project name. */
export function shortProject(name: string): string {
  return name.startsWith(ROOT) ? name.slice(ROOT.length) : name
}
