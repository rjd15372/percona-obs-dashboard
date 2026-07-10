// Version-key model for the <version>[:<subproject>] selector: subprojects
// of a version (extras, tde, …) are "version extensions" with their own
// selector entries, while absorbed subprojects (containers) fold into the
// plain version entry. Shared by the board (usePackages), the artifacts
// panel, and the events filter so all three scope identically.

/** Split "17:extras" into ["17", "extras"]; "17" into ["17", undefined]. */
export function splitVersionKey(key: string): [string, string | undefined] {
  const idx = key.indexOf(':')
  return idx < 0 ? [key, undefined] : [key.slice(0, idx), key.slice(idx + 1)]
}

/** Derive selector keys from project paths. depth is the context prefix
 *  segment count; absorbed lists the subprojects folded into the plain
 *  version entry (undefined = catch-all context: plain numeric keys only).
 *  Order: numeric descending, plain key first, extensions alphabetical. */
export function deriveVersionKeys(
  projects: Iterable<string>,
  depth: number,
  absorbed: string[] | undefined,
): string[] {
  const plain = new Set<string>()
  const extensions = new Set<string>()
  for (const project of projects) {
    const parts = project.split(':')
    const ver = parts[depth]
    if (!ver || !/^\d+$/.test(ver)) continue
    const sub = parts[depth + 1]
    if (!sub || absorbed === undefined || absorbed.includes(sub)) {
      plain.add(ver)
    } else {
      extensions.add(`${ver}:${sub}`)
    }
  }
  const versions = new Set<string>(plain)
  for (const ext of extensions) versions.add(splitVersionKey(ext)[0])
  const keys: string[] = []
  for (const ver of [...versions].sort((a, b) => parseInt(b) - parseInt(a))) {
    if (plain.has(ver)) keys.push(ver)
    keys.push(...[...extensions].filter(e => e.startsWith(ver + ':')).sort())
  }
  return keys
}

/** Does a project belong under prefix at the selected version key?
 *  Plain key "17": the version root plus absorbed subprojects only (when
 *  absorbed is defined; catch-all contexts match the whole subtree).
 *  Extension key "17:extras": the prefix:17:extras subtree (catch-all,
 *  containers beneath included). */
export function matchesVersionKey(
  project: string,
  prefix: string,
  key: string,
  absorbed: string[] | undefined,
): boolean {
  const [ver, sub] = splitVersionKey(key)
  const base = sub ? `${prefix}:${ver}:${sub}` : `${prefix}:${ver}`
  if (project === base) return true
  if (!project.startsWith(base + ':')) return false
  if (sub || absorbed === undefined) return true
  const first = project.slice(base.length + 1).split(':')[0]
  return absorbed.includes(first)
}
