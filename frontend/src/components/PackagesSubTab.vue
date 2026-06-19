<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { ArtifactBinary, PackageRow, RepoInfo } from '../composables/useArtifacts'
import { distroGroup } from '../composables/useArtifacts'

const props = defineProps<{
  packageRows: PackageRow[]
  repos: RepoInfo[]
  selectedRepo: RepoInfo | null
  version: string
  artArch: string
  copiedKey: string | null
}>()

const emit = defineEmits<{
  'update:art-repo': [obs: string]
  'update:art-arch': [arch: string]
  'copy': [key: string, text: string]
}>()

const rhelRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'RHEL')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const opensuseRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'openSUSE')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const ubuntuRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'Ubuntu')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const debianRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'Debian')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const otherRepos = computed(() =>
  props.repos.filter(r => distroGroup(r) === 'Other')
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
)
const showPackageList = computed(() => props.selectedRepo !== null || props.packageRows.length > 0)

// ----- snippet -----

const snippet = computed(() => {
  const repo = props.selectedRepo
  if (!repo) return ''
  const obsProject = props.packageRows[0]?.project ?? `isv:percona:ppg:${props.version}`
  const obsProjectUrl = obsProject.split(':').join(':/')
  const baseUrl = `https://download.opensuse.org/repositories/${obsProjectUrl}/${repo.obs}/`
  const projectId = obsProject.split(':').join('_')

  if (repo.obs.startsWith('openSUSE')) {
    return `zypper addrepo \\
  ${baseUrl} \\
  ${obsProject}
zypper --gpg-auto-import-keys refresh`
  }

  if (repo.type === 'rpm') {
    return `rpm --import ${baseUrl}repodata/repomd.xml.key
tee /etc/yum.repos.d/${projectId}.repo << 'EOF'
[${obsProject}]
name=${obsProject} - ${repo.obs}
baseurl=${baseUrl}
enabled=1
gpgcheck=0
EOF`
  }

  return `echo 'deb ${baseUrl} /' \\
  | tee /etc/apt/sources.list.d/${obsProject}.list
curl -fsSL ${baseUrl}Release.key \\
  | gpg --dearmor | tee /etc/apt/trusted.gpg.d/${projectId}.gpg > /dev/null
apt update`
})

// ----- binary expansion -----

type BinaryState = ArtifactBinary[] | 'loading' | 'error'
const binaryCache = reactive<Record<string, BinaryState>>({})
const expanded = reactive<Record<string, boolean>>({})

function rowKey(row: PackageRow): string {
  return `${row.project}/${row.repo.obs}/${row.arch}/${row.name}`
}

async function toggleRow(row: PackageRow) {
  const key = rowKey(row)
  expanded[key] = !expanded[key]
  if (!expanded[key] || binaryCache[key] !== undefined) return

  if (row.binaries) {
    binaryCache[key] = row.binaries
    return
  }

  binaryCache[key] = 'loading'
  try {
    const params = new URLSearchParams({
      project: row.project,
      repo: row.repo.obs,
      arch: row.arch,
      package: row.name,
    })
    const res = await fetch(`/api/binaries?${params}`)
    if (!res.ok) throw new Error(res.statusText)
    const data = await res.json() as { binaries: string[] }
    binaryCache[key] = data.binaries.map(filename => ({ filename }))
  } catch {
    binaryCache[key] = 'error'
  }
}

function downloadUrl(row: PackageRow, filename: string): string {
  const obsProjectUrl = row.project.split(':').join(':/')
  return `https://download.opensuse.org/repositories/${obsProjectUrl}/${row.repo.obs}/${row.arch}/${filename}`
}

function formatArtifactTime(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

// ----- labels -----


const STATE_LABELS: Record<string, string> = {
  succeeded: 'Built',
  building:  'Building',
  scheduled: 'Scheduled',
  blocked:   'Blocked',
  failed:    'Failed',
  disabled:  'Disabled',
  excluded:  'Excluded',
  broken:    'Broken',
  unresolvable: 'Unresolvable',
}

function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? state
}

function stateClass(state: string): string {
  if (state === 'succeeded') return 'status-built'
  if (state === 'building' || state === 'scheduled') return 'status-building'
  if (state === 'failed' || state === 'broken' || state === 'unresolvable') return 'status-failed'
  return 'status-other'
}

</script>

<template>
  <div class="packages-subtab">
    <!-- Sidebar -->
    <div class="sidebar">
      <template v-if="rhelRepos.length > 0">
        <div class="group-label">RHEL</div>
        <button
          v-for="repo in rhelRepos"
          :key="repo.obs"
          class="sidebar-row"
          :class="{ active: selectedRepo?.obs === repo.obs }"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="opensuseRepos.length > 0">
        <div class="group-label">openSUSE</div>
        <button
          v-for="repo in opensuseRepos"
          :key="repo.obs"
          class="sidebar-row"
          :class="{ active: selectedRepo?.obs === repo.obs }"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="ubuntuRepos.length > 0">
        <div class="group-label">Ubuntu</div>
        <button
          v-for="repo in ubuntuRepos"
          :key="repo.obs"
          class="sidebar-row"
          :class="{ active: selectedRepo?.obs === repo.obs }"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="debianRepos.length > 0">
        <div class="group-label">Debian</div>
        <button
          v-for="repo in debianRepos"
          :key="repo.obs"
          class="sidebar-row"
          :class="{ active: selectedRepo?.obs === repo.obs }"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <template v-if="otherRepos.length > 0">
        <div class="group-label">Other</div>
        <button
          v-for="repo in otherRepos"
          :key="repo.obs"
          class="sidebar-row"
          :class="{ active: selectedRepo?.obs === repo.obs }"
          @click="emit('update:art-repo', repo.obs)"
        >{{ repo.name }}</button>
      </template>

      <div v-if="repos.length === 0" class="sidebar-empty">
        {{ packageRows.length > 0 ? 'No build repos' : 'Loading…' }}
      </div>
    </div>

    <!-- Main content -->
    <div class="content">
      <!-- Combined repo header + snippet card -->
      <div class="repo-card" v-if="selectedRepo">
        <div class="repo-header">
          <div class="repo-header-left">
            <span class="repo-title">{{ selectedRepo.name }}</span>
            <code class="repo-obs-path">{{ selectedRepo.obs }}</code>
          </div>
          <div class="arch-selector">
            <button
              class="arch-pill"
              :class="{ active: artArch === 'x86_64' }"
              @click="emit('update:art-arch', 'x86_64')"
            >x86_64</button>
            <button
              class="arch-pill"
              :class="{ active: artArch === 'aarch64' }"
              @click="emit('update:art-arch', 'aarch64')"
            >aarch64</button>
          </div>
        </div>
        <div class="snippet-section">
          <div class="snippet-header">
            <span class="snippet-label">Repository setup</span>
            <button
              class="copy-btn"
              :class="{ copied: copiedKey === 'repo-config' }"
              @click="emit('copy', 'repo-config', snippet)"
            >
              {{ copiedKey === 'repo-config' ? '✓ Copied' : 'Copy' }}
            </button>
          </div>
          <pre class="snippet-pre"><code>{{ snippet }}</code></pre>
        </div>
      </div>

      <!-- Package list card -->
      <div class="pkg-card" v-if="showPackageList">
        <div class="pkg-card-header">
          <span class="pkg-card-title">Packages</span>
          <span class="pkg-card-subtitle">
            {{ packageRows.length }} available
            <template v-if="selectedRepo"> · {{ selectedRepo.name }} / {{ artArch }}</template>
          </span>
        </div>
        <div class="pkg-list">
          <div
            v-for="row in packageRows"
            :key="rowKey(row)"
            class="pkg-group"
          >
            <!-- Package header row (click to expand) -->
            <button
              class="pkg-row"
              :class="{ expanded: expanded[rowKey(row)] }"
              @click="row.binariesAvailable && row.state === 'succeeded' ? toggleRow(row) : undefined"
              :disabled="!row.binariesAvailable || row.state !== 'succeeded'"
              :title="!row.binariesAvailable ? 'No target binaries available' : (row.state !== 'succeeded' ? 'Not built' : 'Click to show binaries')"
            >
              <span class="expand-glyph">{{ row.binariesAvailable ? (expanded[rowKey(row)] ? '▼' : '▶') : '' }}</span>
              <code class="pkg-name">{{ row.name }}</code>
              <code v-if="row.version" class="pkg-version">{{ row.version }}</code>
              <span v-if="row.builtAt" class="pkg-built-at">{{ formatArtifactTime(row.builtAt) }}</span>
              <span v-if="row.isRebuilding" class="status-badge status-rebuilding">Rebuilding</span>
              <span class="status-badge" :class="row.published ? 'status-published' : stateClass(row.state)">
                {{ row.published ? 'Published' : stateLabel(row.state) }}
              </span>
            </button>

            <!-- Binary list (expanded) -->
            <div v-if="expanded[rowKey(row)]" class="binary-list">
              <div v-if="binaryCache[rowKey(row)] === 'loading'" class="binary-loading">
                Loading…
              </div>
              <div v-else-if="binaryCache[rowKey(row)] === 'error'" class="binary-error">
                Failed to load binaries.
              </div>
              <template v-else-if="Array.isArray(binaryCache[rowKey(row)])">
                <div
                  v-for="binary in (binaryCache[rowKey(row)] as ArtifactBinary[])"
                  :key="binary.filename"
                  class="binary-row"
                >
                  <div class="binary-details">
                    <code class="binary-name">{{ binary.filename }}</code>
                    <span v-if="binary.built_at" class="binary-built-at">{{ formatArtifactTime(binary.built_at) }}</span>
                  </div>
                  <a
                    class="download-btn"
                    :href="downloadUrl(row, binary.filename)"
                    target="_blank"
                  >&#x2193; Download</a>
                </div>
                <div v-if="(binaryCache[rowKey(row)] as ArtifactBinary[]).length === 0" class="binary-empty">
                  No distributable binaries.
                </div>
              </template>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.packages-subtab {
  display: flex;
  gap: 16px;
  padding: 16px;
  height: 100%;
  min-height: 0;
}

/* --- Sidebar --- */

.sidebar {
  width: 220px;
  flex-shrink: 0;
  align-self: flex-start;
  background: var(--bg-card);
  border-radius: 12px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.sidebar-empty {
  padding: 12px 16px;
  font-size: 12px;
  color: var(--text-muted);
}

.group-label {
  padding: 8px 16px;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
  border-top: 1px solid var(--border);
  border-bottom: 1px solid var(--border);
}

.sidebar > .group-label:first-child {
  border-top: none;
}

.sidebar-row {
  width: 100%;
  padding: 9px 16px;
  text-align: left;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  font-weight: 500;
  font-size: 13.5px;
  cursor: pointer;
}

.sidebar-row.active {
  background: var(--brand-purple-tint);
  color: var(--brand-purple);
  font-weight: 700;
}

/* --- Main content --- */

.content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  overflow-y: auto;
}

/* --- Repo card --- */

.repo-card {
  background: var(--bg-card);
  border-radius: 14px;
  overflow: hidden;
  flex-shrink: 0;
}

.repo-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
}

.repo-header-left {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.repo-title {
  font-size: 17px;
  font-weight: 700;
}

.repo-obs-path {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-muted);
}

.arch-selector {
  display: flex;
  gap: 2px;
  background: var(--bg-muted);
  padding: 3px;
  border-radius: 9px;
}

.arch-pill {
  padding: 4px 12px;
  border-radius: 7px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  border: none;
  background: transparent;
  color: var(--text-muted);
}

.arch-pill.active {
  background: var(--bg-card);
  color: var(--brand-purple);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.10);
}

.snippet-section {
  padding: 0 18px 18px;
}

.snippet-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.snippet-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}

.copy-btn {
  font-size: 12px;
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  cursor: pointer;
}

.copy-btn.copied {
  color: var(--success);
  border-color: var(--success);
}

.snippet-pre {
  background: var(--bg-card-2);
  padding: 14px 16px;
  border-radius: 8px;
  font-family: var(--font-mono);
  font-size: 12px;
  white-space: pre;
  overflow-x: auto;
  margin: 0;
}

/* --- Package list card --- */

.pkg-card {
  background: var(--bg-card);
  border-radius: 12px;
  overflow: hidden;
  flex-shrink: 0;
}

.pkg-card-header {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}

.pkg-card-title {
  font-size: 15px;
  font-weight: 700;
}

.pkg-card-subtitle {
  font-size: 12px;
  color: var(--text-muted);
}

.pkg-list {
  overflow-y: auto;
}

.pkg-group {
  border-bottom: 1px solid var(--border);
}

.pkg-group:last-child {
  border-bottom: none;
}

/* Package header row — acts as a button */
.pkg-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 18px;
  width: 100%;
  text-align: left;
  background: transparent;
  border: none;
  cursor: pointer;
}

.pkg-row:hover:not(:disabled) {
  background: var(--bg-muted);
}

.pkg-row:disabled {
  cursor: default;
  opacity: 0.7;
}

.pkg-row.expanded {
  background: var(--bg-muted);
}

.expand-glyph {
  font-size: 9px;
  color: var(--text-muted);
  width: 10px;
  flex-shrink: 0;
}

.pkg-name {
  font-family: var(--font-mono);
  font-size: 13px;
  font-weight: 700;
  flex: 1;
  min-width: 0;
  text-align: left;
}

.scope-badge {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 2px 7px;
  border-radius: 10px;
  background: var(--bg-muted);
  color: var(--text-secondary);
  white-space: nowrap;
}

.scope-badge.scope-version {
  background: var(--brand-purple-tint);
  color: var(--brand-purple);
}

.pkg-version {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-muted);
  white-space: nowrap;
  flex-shrink: 0;
}

.pkg-built-at {
  font-size: 11px;
  color: var(--text-muted);
  white-space: nowrap;
  flex-shrink: 0;
}

.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-published {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}

.status-built {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}

.status-building,
.status-rebuilding {
  background: #fef9c3;
  color: #a16207;
}

.status-failed {
  background: #fee2e2;
  color: var(--danger, #dc2626);
}

.status-other {
  background: var(--bg-muted);
  color: var(--text-muted);
}

/* Binary rows */
.binary-list {
  background: var(--bg-card-2);
  border-top: 1px solid var(--border);
  padding: 6px 0;
}

.binary-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 18px 6px 38px;
  gap: 12px;
}

.binary-row:hover {
  background: var(--bg-muted);
}

.binary-details {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.binary-name {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-secondary);
  flex: 1;
  min-width: 0;
  word-break: break-all;
}

.binary-built-at {
  font-size: 11px;
  color: var(--text-muted);
}

.download-btn {
  font-size: 12px;
  padding: 3px 10px;
  border-radius: 6px;
  background: var(--brand-purple);
  color: #fff;
  text-decoration: none;
  white-space: nowrap;
  font-weight: 500;
  flex-shrink: 0;
}

.binary-loading,
.binary-error,
.binary-empty {
  padding: 8px 18px 8px 38px;
  font-size: 12px;
  color: var(--text-muted);
}

.binary-error {
  color: var(--danger, #dc2626);
}
</style>
