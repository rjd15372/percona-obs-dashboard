<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ContainerImage } from '../composables/useArtifacts'
import type { CveScan } from '../types/api'

const props = defineProps<{
  containerImages: ContainerImage[]
  copiedKey: string | null
}>()

const emit = defineEmits<{
  'copy': [key: string, text: string]
}>()

// Group images by baseOs, preserving insertion order
const groups = computed(() => {
  const map = new Map<string, ContainerImage[]>()
  for (const img of props.containerImages) {
    const list = map.get(img.baseOs) ?? []
    list.push(img)
    map.set(img.baseOs, list)
  }
  return Array.from(map.entries()).map(([baseOs, images]) => ({ baseOs, images }))
})

const STATE_LABELS: Record<string, string> = {
  succeeded:    'Built',
  building:     'Building',
  scheduled:    'Scheduled',
  blocked:      'Blocked',
  failed:       'Failed',
  disabled:     'Disabled',
  excluded:     'Excluded',
  broken:       'Broken',
  unresolvable: 'Unresolvable',
}

function stateLabel(img: ContainerImage): string {
  if (img.published) return 'Published'
  return STATE_LABELS[img.rollupState] ?? img.rollupState
}

function stateClass(img: ContainerImage): string {
  if (img.published) return 'published'
  if (img.rollupState === 'succeeded') return 'built'
  if (img.rollupState === 'building' || img.rollupState === 'scheduled') return 'building'
  if (['failed', 'broken', 'unresolvable'].includes(img.rollupState)) return 'failed'
  return 'other'
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

function cveTotals(scans: CveScan[]): { critical: number; high: number } {
  return scans.reduce(
    (acc, s) => ({ critical: acc.critical + s.critical_count, high: acc.high + s.high_count }),
    { critical: 0, high: 0 }
  )
}

function cveBadgeText(scans: CveScan[]): string | null {
  if (scans.length === 0) return null
  const { critical, high } = cveTotals(scans)
  if (critical === 0 && high === 0) return 'No CVEs'
  const parts: string[] = []
  if (critical > 0) parts.push(`${critical} CRITICAL`)
  if (high > 0) parts.push(`${high} HIGH`)
  return parts.join(' · ')
}

function cveBadgeClass(scans: CveScan[]): string {
  if (scans.length === 0) return ''
  const { critical, high } = cveTotals(scans)
  if (critical === 0 && high === 0) return 'cve-clean'
  return 'cve-vuln'
}

function latestScanTime(scans: CveScan[]): string {
  if (scans.length === 0) return ''
  const latest = scans.reduce((a, b) => (a.scanned_at > b.scanned_at ? a : b))
  return formatArtifactTime(latest.scanned_at)
}

const openCvePanels = ref(new Set<string>())

function toggleCvePanel(imageId: string) {
  const next = new Set(openCvePanels.value)
  if (next.has(imageId)) {
    next.delete(imageId)
  } else {
    next.add(imageId)
  }
  openCvePanels.value = next
}
</script>

<template>
  <div class="containers-subtab">
    <template v-if="groups.length > 0">
      <div v-for="group in groups" :key="group.baseOs" class="os-group">
        <div class="os-group-header">{{ group.baseOs }}</div>
        <div class="images-grid">
          <div
            v-for="image in group.images"
            :key="image.id"
            class="image-card"
          >
            <!-- Card header -->
            <div class="card-header">
              <div class="card-header-left">
                <div class="container-icon">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none"
                       stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <rect x="2" y="7" width="20" height="14" rx="3"/>
                    <path d="M7 7V5a2 2 0 012-2h6a2 2 0 012 2v2"/>
                    <path d="M2 13h20"/>
                  </svg>
                </div>
                <span class="image-name">{{ image.imageName }}</span>
              </div>
              <span class="status-badge" :class="stateClass(image)">
                {{ stateLabel(image) }}
              </span>
              <span
                v-if="cveBadgeText(image.cveScans)"
                class="status-badge"
                :class="cveBadgeClass(image.cveScans)"
              >{{ cveBadgeText(image.cveScans) }}</span>
            </div>

            <!-- Registry -->
            <div class="registry-section">
              <div class="section-label">REGISTRY</div>
              <code class="registry-path">{{ image.registry }}</code>
            </div>

            <div v-if="image.builtAt" class="built-section">
              <div class="section-label">BUILT</div>
              <span class="built-time">{{ formatArtifactTime(image.builtAt) }}</span>
              <span v-if="image.isRebuilding" class="status-badge building">Rebuilding</span>
            </div>

            <!-- Tags -->
            <div class="tags-section">
              <div class="section-label">AVAILABLE TAGS</div>
              <div class="tags-list" v-if="image.tags.length > 0">
                <code
                  v-for="(tag, i) in image.tags"
                  :key="tag"
                  class="tag-chip"
                  :class="{ 'tag-primary': i === 0 }"
                >{{ tag }}</code>
              </div>
              <span v-else class="tags-empty">No tags yet</span>
            </div>

            <!-- Docker pull -->
            <div class="pull-section">
              <div class="pull-header">
                <span class="section-label">DOCKER PULL</span>
                <button
                  class="copy-btn"
                  :class="{ copied: copiedKey === image.id }"
                  @click="emit('copy', image.id, image.pullCmd)"
                >
                  {{ copiedKey === image.id ? '✓ Copied' : 'Copy' }}
                </button>
              </div>
              <pre class="pull-code"><code>{{ image.pullCmd }}</code></pre>
            </div>

            <!-- Security / CVE -->
            <div v-if="image.cveScans.length > 0" class="card-section cve-section">
              <div class="cve-header" @click="toggleCvePanel(image.id)">
                <span class="section-label">SECURITY</span>
                <span class="cve-scan-time">Scanned {{ latestScanTime(image.cveScans) }}</span>
                <span class="cve-chevron" :class="{ open: openCvePanels.has(image.id) }">›</span>
              </div>
              <div v-if="openCvePanels.has(image.id)" class="cve-body">
                <div v-for="scan in image.cveScans" :key="scan.arch" class="cve-arch-block">
                  <div class="cve-arch-label">{{ scan.arch }}</div>
                  <div v-if="scan.findings.length === 0" class="cve-clean-line">No fixable CVEs found</div>
                  <div v-else class="cve-table-wrap">
                    <table class="cve-table">
                      <thead>
                        <tr>
                          <th>Severity</th>
                          <th>CVE ID</th>
                          <th>Package</th>
                          <th>Installed → Fixed</th>
                          <th>Title</th>
                        </tr>
                      </thead>
                      <tbody>
                        <tr v-for="f in scan.findings" :key="f.id">
                          <td :class="f.severity === 'CRITICAL' ? 'sev-critical' : 'sev-high'">{{ f.severity }}</td>
                          <td class="mono">{{ f.id }}</td>
                          <td class="mono">{{ f.pkg }}</td>
                          <td class="mono">{{ f.installed }} → {{ f.fixed }}</td>
                          <td>{{ f.title }}</td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </template>

    <div v-else class="empty-state">
      No container images for this version.
    </div>
  </div>
</template>

<style scoped>
.containers-subtab {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 24px;
}

/* OS group */
.os-group-header {
  font-size: 13px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
  margin-bottom: 12px;
}

.images-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 16px;
}

/* Card */
.image-card {
  background: var(--bg-card);
  border-radius: 12px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.container-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 8px;
  background: var(--info-tint, #dbeafe);
  color: var(--info, #3b82f6);
  flex-shrink: 0;
}

.image-name {
  font-size: 14px;
  font-weight: 700;
}

.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-badge.published {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}

.status-badge.built {
  background: #dcfce7;
  color: #15803d;
}

.status-badge.building {
  background: #fef9c3;
  color: #a16207;
}

.status-badge.failed {
  background: #fee2e2;
  color: var(--danger, #dc2626);
}

.status-badge.other {
  background: var(--bg-muted);
  color: var(--text-muted);
}

/* Registry */
.registry-section {
  background: var(--bg-card-2);
  padding: 10px 18px;
  border-bottom: 1px solid var(--border);
}

.built-section {
  padding: 10px 18px;
  border-bottom: 1px solid var(--border);
}

.built-time {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: var(--text-secondary);
}

/* Tags */
.tags-section {
  padding: 12px 18px;
  border-bottom: 1px solid var(--border);
}

.tags-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.tag-chip {
  font-family: var(--font-mono);
  font-size: 11px;
  padding: 3px 8px;
  border-radius: 6px;
  background: var(--bg-muted);
  color: var(--text-secondary);
}

.tag-chip.tag-primary {
  background: var(--brand-purple-tint);
  color: var(--brand-purple);
  font-weight: 700;
}

.tags-empty {
  display: block;
  margin-top: 6px;
  font-size: 12px;
  color: var(--text-muted);
}

/* Docker pull */
.pull-section {
  padding: 12px 18px;
  flex: 1;
}

.pull-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.section-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

.registry-path {
  display: block;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-secondary);
  word-break: break-all;
  margin-top: 4px;
}

.copy-btn {
  font-size: 12px;
  padding: 3px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  cursor: pointer;
}

.copy-btn.copied {
  color: var(--success, #16a34a);
  border-color: var(--success, #16a34a);
}

.pull-code {
  background: var(--bg-card-2);
  padding: 10px 14px;
  border-radius: 8px;
  font-family: var(--font-mono);
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}

.empty-state {
  text-align: center;
  padding: 48px;
  color: var(--text-muted);
  font-size: 14px;
}

/* CVE section */
.cve-section {
  border-top: 1px solid var(--border);
}
.cve-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 18px;
  cursor: pointer;
  user-select: none;
}
.cve-scan-time {
  font-size: 11px;
  color: var(--text-muted);
  margin-left: auto;
}
.cve-chevron {
  font-size: 16px;
  color: var(--text-muted);
  transition: transform 0.15s;
  transform: rotate(0deg);
}
.cve-chevron.open {
  transform: rotate(90deg);
}
.cve-body {
  padding: 0 18px 12px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.cve-arch-label {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-bottom: 6px;
}
.cve-clean-line {
  font-size: 12px;
  color: var(--success, #16a34a);
  padding: 4px 0;
}
.cve-table-wrap {
  overflow-x: auto;
}
.cve-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
}
.cve-table th {
  text-align: left;
  font-weight: 600;
  color: var(--text-muted);
  padding: 4px 6px 4px 0;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
.cve-table td {
  padding: 4px 6px 4px 0;
  vertical-align: top;
  border-bottom: 1px solid var(--border);
  color: var(--text-secondary);
}
.sev-critical {
  color: var(--danger, #dc2626);
  font-weight: 700;
}
.sev-high {
  color: var(--warn, #d97706);
  font-weight: 700;
}
.mono {
  font-family: var(--font-mono);
}
.status-badge.cve-clean {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}
.status-badge.cve-vuln {
  background: #fee2e2;
  color: var(--danger, #dc2626);
}
</style>
