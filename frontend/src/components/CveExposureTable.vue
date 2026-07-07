<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { OverviewProject } from '../types/overview'
import { ageColor, ageLabel } from '../lib/overview'

const props = defineProps<{
  projects: OverviewProject[]
  accentOf: (project: string) => string
}>()

const projectsWithImages = computed(() => props.projects.filter(p => p.images.length > 0))

// Keyed by project path (not array index) so expansion survives snapshot
// replacement from SSE updates. Multiple rows may be open at once.
const expanded = reactive<Record<string, boolean>>({})

function toggle(project: string) {
  expanded[project] = !expanded[project]
}

function regionId(project: string): string {
  return `cve-imgs-${project.replace(/[^a-z0-9]/gi, '-')}`
}

interface Aggregate {
  critical: number
  high: number
  oldest: number
  avgFix: number | null
}

function computeAggregate(p: OverviewProject): Aggregate {
  const critical = p.images.reduce((s, i) => s + i.critical, 0)
  const high = p.images.reduce((s, i) => s + i.high, 0)
  const oldest = p.images.reduce((m, i) => Math.max(m, i.oldest_open_days), 0)
  const fixes = p.images
    .filter(i => i.critical + i.high > 0 && i.avg_fix_days > 0)
    .map(i => i.avg_fix_days)
  const avgFix = fixes.length > 0 ? Math.round(fixes.reduce((a, b) => a + b, 0) / fixes.length) : null
  return { critical, high, oldest, avgFix }
}

// Cached per-project aggregates keyed by path so template repeats don't
// recompute the same reduce()s on every access within a render.
const aggregates = computed<Record<string, Aggregate>>(() => {
  const map: Record<string, Aggregate> = {}
  for (const p of projectsWithImages.value) map[p.project] = computeAggregate(p)
  return map
})

function aggregate(project: string): Aggregate {
  return aggregates.value[project] ?? { critical: 0, high: 0, oldest: 0, avgFix: null }
}

// Names that appear more than once within a project row's images, so we can
// disambiguate them with a muted project-remainder suffix in the template.
const dupNamesByProject = computed<Record<string, Set<string>>>(() => {
  const map: Record<string, Set<string>> = {}
  for (const p of projectsWithImages.value) {
    const counts = new Map<string, number>()
    for (const img of p.images) counts.set(img.name, (counts.get(img.name) ?? 0) + 1)
    map[p.project] = new Set([...counts.entries()].filter(([, n]) => n > 1).map(([name]) => name))
  }
  return map
})

function dupSuffix(rowProject: string, img: { project: string; name: string }): string | null {
  if (!dupNamesByProject.value[rowProject]?.has(img.name)) return null
  const prefix = rowProject + ':'
  return img.project.startsWith(prefix) ? img.project.slice(prefix.length) : img.project
}

function badgeClass(n: number, kind: 'crit' | 'high'): string {
  if (n > 0) {
    return kind === 'crit' ? 'text-crit bg-crit-tint' : 'text-high bg-high-tint'
  }
  return 'text-text-muted bg-bg-muted'
}
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] overflow-hidden">
    <div class="p-[15px_20px] border-b border-border flex items-baseline gap-[10px]">
      <h2 class="m-0 text-[14px] font-bold">CVE exposure by project</h2>
      <span class="text-[12px] text-text-muted">click a project to see per-image breakdown</span>
    </div>

    <div class="grid grid-cols-[1fr_90px_90px_130px_130px] gap-3 items-center p-[9px_20px] bg-bg-card-2 border-b border-border text-[10.5px] font-bold uppercase tracking-[0.05em] text-text-muted">
      <span>Project</span>
      <span class="text-center">Critical</span>
      <span class="text-center">High</span>
      <span class="text-right">Oldest open</span>
      <span class="text-right">Avg fix time</span>
    </div>

    <template v-for="p in projectsWithImages" :key="p.project">
      <button
        type="button"
        class="w-full text-left p-[11px_20px] border-b border-border grid grid-cols-[1fr_90px_90px_130px_130px] gap-3 items-center"
        :class="expanded[p.project] ? 'bg-bg-card-2' : ''"
        :aria-expanded="!!expanded[p.project]"
        :aria-controls="regionId(p.project)"
        @click="toggle(p.project)"
      >
        <span class="flex items-center gap-2 min-w-0">
          <span
            class="text-text-muted text-[11px] transition-transform inline-block"
            :class="expanded[p.project] ? 'rotate-90' : ''"
          >▸</span>
          <span class="w-[9px] h-[9px] rounded-[3px] shrink-0" :style="{ background: accentOf(p.project) }" />
          <span class="font-mono text-[13.5px] font-semibold truncate">{{ p.project }}</span>
          <span class="text-[11px] text-text-muted whitespace-nowrap">{{ p.images.length }} images</span>
        </span>
        <span
          class="min-w-[30px] text-center text-[13px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
          :class="badgeClass(aggregate(p.project).critical, 'crit')"
        >{{ aggregate(p.project).critical }}</span>
        <span
          class="min-w-[30px] text-center text-[13px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
          :class="badgeClass(aggregate(p.project).high, 'high')"
        >{{ aggregate(p.project).high }}</span>
        <span
          class="font-mono font-semibold justify-self-end"
          :style="{ color: aggregate(p.project).oldest ? ageColor(aggregate(p.project).oldest) : 'var(--text-muted)' }"
        >{{ ageLabel(aggregate(p.project).oldest) }}</span>
        <span class="font-mono text-text-secondary justify-self-end">
          {{ aggregate(p.project).avgFix === null ? '—' : `${aggregate(p.project).avgFix}d` }}
        </span>
      </button>

      <div
        v-show="expanded[p.project]"
        :id="regionId(p.project)"
        class="bg-bg-card-2 border-b border-border"
      >
        <div
          v-for="(img, idx) in p.images"
          :key="img.project + '/' + img.name"
          class="pl-10 p-[9px_20px] grid grid-cols-[1fr_90px_90px_130px_130px] gap-3 items-center"
          :class="idx > 0 ? 'border-t border-border' : ''"
        >
          <span class="font-mono text-[12px] text-text-secondary truncate">
            {{ img.name }}
            <span v-if="dupSuffix(p.project, img)" class="text-text-muted text-[10.5px]"> · {{ dupSuffix(p.project, img) }}</span>
          </span>
          <span
            class="min-w-[30px] text-center text-[12.5px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
            :class="badgeClass(img.critical, 'crit')"
          >{{ img.critical }}</span>
          <span
            class="min-w-[30px] text-center text-[12.5px] font-bold px-2 py-0.5 rounded-md justify-self-center font-mono"
            :class="badgeClass(img.high, 'high')"
          >{{ img.high }}</span>
          <span
            class="font-mono text-[12.5px] font-semibold justify-self-end"
            :style="{ color: img.oldest_open_days ? ageColor(img.oldest_open_days) : 'var(--text-muted)' }"
          >{{ ageLabel(img.oldest_open_days) }}</span>
          <span class="font-mono text-[12.5px] text-text-muted justify-self-end">
            {{ img.avg_fix_days === 0 ? '—' : `${img.avg_fix_days}d` }}
          </span>
        </div>
      </div>
    </template>
  </div>
</template>
