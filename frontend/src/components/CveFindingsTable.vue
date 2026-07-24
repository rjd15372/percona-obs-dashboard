<script setup lang="ts">
import type { CveScan } from '../types/api'
import { cveDuration } from '../lib/cve'

defineProps<{
  scans: CveScan[]
}>()
</script>

<template>
  <div class="flex flex-col gap-3">
    <div v-for="scan in scans" :key="scan.repo + '/' + scan.arch" class="cve-arch-block">
      <div class="text-[11px] font-bold text-text-muted uppercase [letter-spacing:0.06em] mb-[6px]">{{ scan.repo }} · {{ scan.arch }}</div>
      <div v-if="scan.cve_since" class="text-[11px] text-warn mb-[6px]">
        CVEs present for {{ cveDuration(scan.cve_since) }}
      </div>
      <div v-if="(scan.findings ?? []).length === 0" class="text-[12px] text-ok py-1">No fixable CVEs found</div>
      <div v-else class="overflow-x-auto">
        <table class="cve-table hidden sm:table w-full border-collapse text-[11px]">
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
        <div class="sm:hidden flex flex-col gap-2">
          <div
            v-for="f in (scan.findings ?? [])"
            :key="f.id"
            class="border border-border rounded-lg p-2.5 flex flex-col gap-1"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-mono text-[11px] font-bold text-text-primary">{{ f.id }}</span>
              <span
                class="text-[10px] font-bold uppercase whitespace-nowrap"
                :class="{ 'sev-critical': f.severity === 'CRITICAL', 'sev-high': f.severity === 'HIGH' }"
              >{{ f.severity }}</span>
            </div>
            <div class="font-mono text-[11px] text-text-secondary">{{ f.pkg }}</div>
            <div class="font-mono text-[11px] text-text-muted">{{ f.installed }} → {{ f.fixed }}</div>
            <div class="text-[11px] text-text-secondary">{{ f.title }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
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
