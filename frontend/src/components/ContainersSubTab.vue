<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ContainerImage } from '../composables/useArtifacts'
import type { CveScan } from '../types/api'

const props = defineProps<{
  containerImages: ContainerImage[]
  copiedKey: string | null
  loading?: boolean
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
  building: 'Rebuilding',
  finished: 'Rebuilding',
  scheduled: 'Waiting to rebuild',
}

function rebuildBadge(img: ContainerImage): { label: string; cls: string } | null {
  const label = STATE_LABELS[img.rollupState]
  if (!label) return null
  return {
    label,
    cls: img.rollupState === 'scheduled' ? 'waiting' : 'building',
  }
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

function cveBadgeInfo(scans: CveScan[]): { text: string | null; cls: string } {
  if (scans.length === 0) return { text: null, cls: '' }
  const { critical, high } = cveTotals(scans)
  if (critical === 0 && high === 0) return { text: 'No CVEs', cls: 'cve-clean' }
  const parts: string[] = []
  if (critical > 0) parts.push(`${critical} CRITICAL`)
  if (high > 0) parts.push(`${high} HIGH`)
  return { text: parts.join(' · '), cls: 'cve-vuln' }
}

function latestScanTime(scans: CveScan[]): string {
  if (scans.length === 0) return ''
  const latest = scans.reduce((a, b) => (a.scanned_at > b.scanned_at ? a : b))
  return formatArtifactTime(latest.scanned_at)
}

function cveDuration(since: string): string {
  const diffMs = Date.now() - new Date(since).getTime()
  const days = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (days < 1) return '< 1d'
  if (days < 7) return `${days}d`
  const weeks = Math.floor(days / 7)
  const remainder = days % 7
  return remainder === 0 ? `${weeks}w` : `${weeks}w ${remainder}d`
}

function worstCveDuration(scans: CveScan[]): string | null {
  let oldest: Date | null = null
  for (const s of scans) {
    if (!s.cve_since) continue
    const d = new Date(s.cve_since)
    if (!oldest || d < oldest) oldest = d
  }
  if (!oldest) return null
  return cveDuration(oldest.toISOString())
}

function baseOsSubtitle(baseOs: string): string {
  if (baseOs.startsWith('UBI')) return 'Red Hat Universal Base Image'
  if (baseOs.startsWith('Ubuntu')) return 'Ubuntu container base'
  if (baseOs.startsWith('Debian')) return 'Debian container base'
  return 'Container base image'
}

const openCvePanels = ref(new Set<string>())

watch(() => props.containerImages, () => {
  openCvePanels.value = new Set()
})

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
  <div class="flex flex-col gap-6 p-4">
    <div
      v-if="loading"
      class="containers-loading flex flex-col items-center justify-center py-12 gap-3 text-text-muted"
      :class="{ compact: groups.length > 0 }"
    >
      <div class="spinner"></div>
      <span class="text-[13px] text-text-muted">Fetching container metadata…</span>
    </div>

    <template v-if="groups.length > 0">
      <div v-for="group in groups" :key="group.baseOs" class="flex flex-col gap-[14px]">
        <div class="flex items-center justify-between gap-4 px-[14px] py-[11px] bg-bg-card-2 border border-border border-l-4 border-l-brand-purple rounded-lg">
          <div class="flex flex-col gap-0.5 min-w-0">
            <h3 class="m-0 text-[15px] [font-weight:750] leading-[1.2] text-text-primary">{{ group.baseOs }}</h3>
            <span class="text-[11.5px] text-text-muted">{{ baseOsSubtitle(group.baseOs) }}</span>
          </div>
          <span class="shrink-0 px-2 py-[3px] rounded-[6px] bg-bg-card border border-border text-text-secondary text-[11px] font-bold whitespace-nowrap">
            {{ group.images.length }} image{{ group.images.length !== 1 ? 's' : '' }}
          </span>
        </div>
        <div class="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-4">
          <div
            v-for="image in group.images"
            :key="image.id"
            class="bg-bg-card rounded-[12px] overflow-hidden flex flex-col [transition:opacity_0.15s_ease,filter_0.15s_ease]"
            :class="{ 'opacity-[0.48] grayscale-[0.85]': loading }"
          >
            <!-- Card header -->
            <div class="flex items-center justify-between px-[18px] py-[14px] border-b border-border">
              <div class="flex items-center gap-[10px]">
                <div class="flex items-center justify-center w-9 h-9 rounded-lg bg-info-tint text-info shrink-0">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none"
                       stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <rect x="2" y="7" width="20" height="14" rx="3"/>
                    <path d="M7 7V5a2 2 0 012-2h6a2 2 0 012 2v2"/>
                    <path d="M2 13h20"/>
                  </svg>
                </div>
                <span class="text-[14px] font-bold">{{ image.imageName }}</span>
              </div>
              <template v-for="badge in [rebuildBadge(image)]" :key="'rebuild-badge'">
                <span v-if="badge" class="status-badge" :class="badge.cls">{{ badge.label }}</span>
              </template>
              <template v-for="badge in [cveBadgeInfo(image.cveScans)]" :key="'cve-badge'">
                <span v-if="badge.text" class="status-badge" :class="badge.cls">{{ badge.text }}</span>
              </template>
              <template v-for="dur in [worstCveDuration(image.cveScans)]" :key="'cve-age-badge'">
                <span v-if="dur" class="status-badge cve-age">CVEs for {{ dur }}</span>
              </template>
            </div>

            <!-- Registry -->
            <div class="bg-bg-card-2 px-[18px] py-[10px] border-b border-border">
              <div class="section-label">REGISTRY</div>
              <code class="block font-[var(--font-mono)] text-[12px] text-text-secondary break-all mt-1">{{ image.registry }}</code>
            </div>

            <div v-if="image.builtAt" class="px-[18px] py-[10px] border-b border-border">
              <div class="section-label">BUILT</div>
              <span class="block mt-1 text-[12px] text-text-secondary">{{ formatArtifactTime(image.builtAt) }}</span>
            </div>

            <!-- Tags -->
            <div class="px-[18px] py-3 border-b border-border">
              <div class="section-label">AVAILABLE TAGS</div>
              <div class="flex flex-wrap gap-[6px] mt-2" v-if="image.tags.length > 0">
                <code
                  v-for="(tag, i) in image.tags"
                  :key="tag"
                  class="font-[var(--font-mono)] text-[11px] px-2 py-[3px] rounded-[6px] bg-bg-muted text-text-secondary"
                  :class="{ 'bg-brand-purple-tint text-brand-purple font-bold': i === 0 }"
                >{{ tag }}</code>
              </div>
              <span v-else class="block mt-[6px] text-[12px] text-text-muted">No tags yet</span>
            </div>

            <!-- Docker pull -->
            <div class="px-[18px] py-3 flex-1">
              <div class="flex items-center justify-between mb-2">
                <span class="section-label">DOCKER PULL</span>
                <button
                  class="text-[12px] px-[10px] py-[3px] rounded-[6px] border border-border bg-bg-card text-text-secondary cursor-pointer"
                  :class="{ 'text-ok border-ok': copiedKey === image.id }"
                  @click="emit('copy', image.id, image.pullCmd)"
                >
                  {{ copiedKey === image.id ? '✓ Copied' : 'Copy' }}
                </button>
              </div>
              <pre class="bg-bg-card-2 px-[14px] py-[10px] rounded-lg font-[var(--font-mono)] text-[12px] whitespace-pre-wrap break-all m-0"><code>{{ image.pullCmd }}</code></pre>
            </div>

            <!-- Security / CVE -->
            <div v-if="image.cveScans.length > 0" class="border-t border-border">
              <div class="flex items-center gap-2 px-[18px] py-[10px] cursor-pointer select-none" @click="toggleCvePanel(image.id)">
                <span class="section-label">SECURITY</span>
                <span class="text-[11px] text-text-muted ml-auto">Scanned {{ latestScanTime(image.cveScans) }}</span>
                <span
                  class="text-[16px] text-text-muted [transition:transform_0.15s] rotate-0"
                  :class="{ 'rotate-90': openCvePanels.has(image.id) }"
                >›</span>
              </div>
              <div v-if="openCvePanels.has(image.id)" class="px-[18px] pb-3 flex flex-col gap-3">
                <div v-for="scan in image.cveScans" :key="scan.arch" class="cve-arch-block">
                  <div class="text-[11px] font-bold text-text-muted uppercase [letter-spacing:0.06em] mb-[6px]">{{ scan.arch }}</div>
                  <div v-if="scan.cve_since" class="text-[11px] text-warn mb-[6px]">
                    CVEs present for {{ cveDuration(scan.cve_since) }}
                  </div>
                  <div v-if="(scan.findings ?? []).length === 0" class="text-[12px] text-ok py-1">No fixable CVEs found</div>
                  <div v-else class="overflow-x-auto">
                    <table class="cve-table w-full border-collapse text-[11px]">
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
                        <tr v-for="f in (scan.findings ?? [])" :key="f.id">
                          <td :class="{ 'sev-critical': f.severity === 'CRITICAL', 'sev-high': f.severity === 'HIGH' }">{{ f.severity }}</td>
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

    <div v-else-if="!loading" class="text-center py-12 text-text-muted text-[14px]">
      No container images for this version.
    </div>
  </div>
</template>

<style scoped>
@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Loading states */
.containers-loading.compact {
  flex-direction: row;
  justify-content: flex-start;
  padding: 12px 16px;
  gap: 10px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
}

.spinner {
  width: 28px;
  height: 28px;
  border: 3px solid var(--border);
  border-top-color: var(--brand-purple);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.containers-loading.compact .spinner {
  width: 18px;
  height: 18px;
  border-width: 2px;
}

/* Section label shared utility */
.section-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

/* Status badges */
.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-badge.building {
  background: #fef9c3;
  color: #a16207;
}

.status-badge.waiting {
  background: var(--info-tint);
  color: var(--info);
}

.status-badge.cve-clean {
  background: var(--ok-tint, #d1fae5);
  color: var(--ok, #16a34a);
}

.status-badge.cve-vuln {
  background: var(--fail-tint, #fee2e2);
  color: var(--fail, #dc2626);
}

.status-badge.cve-age {
  background: var(--warn-tint, #fff3dc);
  color: var(--warn, #e08a00);
}

/* CVE table cell styles */
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
  color: var(--fail, #dc2626);
  font-weight: 700;
}

.sev-high {
  color: var(--warn, #d97706);
  font-weight: 700;
}

.mono {
  font-family: var(--font-mono);
}
</style>
