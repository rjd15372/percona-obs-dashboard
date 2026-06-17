<script setup lang="ts">
import { computed } from 'vue'
import { REPOS } from '../composables/useArtifacts'
import type { PackageRow, Repo } from '../composables/useArtifacts'

const props = defineProps<{
  packageRows: PackageRow[]
  selectedRepo: Repo | undefined
  version: string
  artArch: string
  copiedKey: string | null
}>()

const emit = defineEmits<{
  'update:art-repo': [short: string]
  'update:art-arch': [arch: string]
  'copy': [key: string, text: string]
}>()

const snippet = computed(() => {
  const repo = props.selectedRepo
  if (!repo) return ''
  const ver = props.version
  const obsRepo = repo.obs
  if (repo.type === 'rpm') {
    return `[percona-ppg${ver}]\nname=Percona PPG ${ver} — ${repo.name}\nbaseurl=https://download.opensuse.org/repositories/isv:percona:ppg:${ver}/${obsRepo}/\nenabled=1\ngpgcheck=0\n\n# Save to /etc/yum.repos.d/percona-ppg${ver}.repo, then:\ndnf makecache\ndnf install percona-postgresql${ver}-server`
  } else {
    return `# 1. Add repository\necho "deb https://download.opensuse.org/repositories/isv:percona:ppg:${ver}/${obsRepo}/ ./" \\\n  | sudo tee /etc/apt/sources.list.d/percona-ppg${ver}.list\n\n# 2. Import GPG key\nwget -qO- https://download.opensuse.org/repositories/isv:percona:ppg:${ver}/${obsRepo}/Release.key \\\n  | sudo apt-key add -\n\n# 3. Update and install\nsudo apt-get update\nsudo apt-get install percona-postgresql-${ver}`
  }
})

function scopeLabel(scope: string, version: string): string {
  if (scope === 'common') return 'Common'
  if (scope === 'ppgcommon') return 'PPG·Common'
  if (scope === 'version') return `PPG ${version}`
  return scope
}

function installCmd(name: string, repoType: 'rpm' | 'deb'): string {
  return repoType === 'rpm' ? `dnf install ${name}` : `apt-get install ${name}`
}

function downloadUrl(row: PackageRow): string {
  return `https://build.opensuse.org/package/binaries/${row.project}/${row.name}/${row.repo.obs}?arch=${row.arch}`
}
</script>

<template>
  <div class="packages-subtab">
    <!-- Sidebar -->
    <div class="sidebar">
      <div class="group-label rpm-label">RPM</div>
      <button
        v-for="repo in REPOS.filter(r => r.type === 'rpm')"
        :key="repo.short"
        class="sidebar-row"
        :class="{ active: selectedRepo?.short === repo.short }"
        @click="emit('update:art-repo', repo.short)"
      >{{ repo.name }}</button>

      <div class="group-label deb-label">DEB</div>
      <button
        v-for="repo in REPOS.filter(r => r.type === 'deb')"
        :key="repo.short"
        class="sidebar-row"
        :class="{ active: selectedRepo?.short === repo.short }"
        @click="emit('update:art-repo', repo.short)"
      >{{ repo.name }}</button>
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
      <div class="pkg-card" v-if="selectedRepo">
        <div class="pkg-card-header">
          <span class="pkg-card-title">Packages</span>
          <span class="pkg-card-subtitle">{{ packageRows.length }} available · {{ selectedRepo.name }} / {{ artArch }}</span>
        </div>
        <div class="pkg-list">
          <div
            v-for="row in packageRows"
            :key="row.project + '/' + row.name"
            class="pkg-row"
          >
            <code class="pkg-name">{{ row.name }}</code>
            <span class="scope-badge" :class="'scope-' + row.scope">{{ scopeLabel(row.scope, version) }}</span>
            <code class="install-cmd">{{ installCmd(row.name, row.repo.type) }}</code>
            <span class="status-badge" :class="row.state === 'succeeded' ? 'status-built' : 'status-other'">
              {{ row.state === 'succeeded' ? 'Built' : row.state }}
            </span>
            <a
              class="download-btn"
              :class="{ disabled: row.state !== 'succeeded' }"
              :href="row.state === 'succeeded' ? downloadUrl(row) : undefined"
              target="_blank"
              :style="row.state !== 'succeeded' ? 'pointer-events: none' : ''"
            >&#x2193; Download</a>
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

.sidebar {
  width: 220px;
  flex-shrink: 0;
  background: var(--bg-card);
  border-radius: 12px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.group-label {
  padding: 8px 16px;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}

.rpm-label {
  border-bottom: 1px solid var(--border);
}

.deb-label {
  border-top: 1px solid var(--border);
  border-bottom: 1px solid var(--border);
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

.content {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

.repo-card {
  background: var(--bg-card);
  border-radius: 14px;
  overflow: hidden;
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

.pkg-card {
  background: var(--bg-card);
  border-radius: 12px;
  overflow: hidden;
  flex: 1;
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

.pkg-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 18px;
  border-bottom: 1px solid var(--border);
}

.pkg-row:last-child {
  border-bottom: none;
}

.pkg-name {
  font-family: var(--font-mono);
  font-size: 13.5px;
  font-weight: 700;
  flex: 1;
  min-width: 0;
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

.install-cmd {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--text-muted);
  white-space: nowrap;
}

.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-built {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}

.status-other {
  background: var(--bg-muted);
  color: var(--text-muted);
}

.download-btn {
  font-size: 12px;
  padding: 4px 10px;
  border-radius: 6px;
  background: var(--brand-purple);
  color: #fff;
  text-decoration: none;
  white-space: nowrap;
  font-weight: 500;
}

.download-btn.disabled {
  background: var(--bg-muted);
  color: var(--text-muted);
  pointer-events: none;
}
</style>
