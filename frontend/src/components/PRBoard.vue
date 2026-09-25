<script setup lang="ts">
import type { PRGroup, Package } from '../types/api'
import { useInstances } from '../composables/useInstances'
import { packageUrl, projectUrl } from '../lib/instances'

defineProps<{ groups: PRGroup[] }>()

const { instances, instanceFor, packageInstances } = useInstances()

const STATE_COLOR: Record<string, string> = {
  succeeded: 'var(--ok)',
  failed: 'var(--fail)',
  unresolvable: 'var(--brand-purple)',
  broken: 'var(--broken)',
  blocked: 'var(--blocked)',
  building: 'var(--info)',
  finished: 'var(--warn)',
  scheduled: 'var(--info)',
}

const STATE_BG: Record<string, string> = {
  succeeded: 'var(--ok-tint)',
  failed: 'var(--fail-tint)',
  unresolvable: 'var(--brand-purple-tint)',
  broken: 'var(--broken-tint)',
  blocked: 'var(--blocked-tint)',
  building: 'var(--info-tint)',
  finished: 'var(--warn-tint)',
  scheduled: 'var(--info-tint)',
}

const STATE_LABEL: Record<string, string> = {
  succeeded: 'OK', failed: 'Failed', unresolvable: 'Unresolvable',
  broken: 'Broken', blocked: 'Blocked', building: 'Building',
  finished: 'Finishing', scheduled: 'Scheduled',
}

const SKIP_STATES = new Set(['disabled', 'excluded', 'locked'])
const BUILDING_STATES = new Set(['building', 'finished', 'scheduled'])

function activeTargets(pkg: Package) {
  return pkg.targets.filter(t => !SKIP_STATES.has(t.state) && t.state !== 'succeeded')
}

function targetSummary(pkg: Package): string {
  const targets = activeTargets(pkg)
  const building = targets.filter(t => BUILDING_STATES.has(t.state)).length
  const failing = targets.filter(t => !BUILDING_STATES.has(t.state)).length
  const parts = []
  if (failing > 0) parts.push(`${failing} failing`)
  if (building > 0) parts.push(`${building} building`)
  return parts.join(', ')
}

function subprojectLabel(project: string): string {
  // "PR:pr-42:ppg17" → "ppg17"
  const parts = project.split(':')
  // Find "PR" segment index and take everything after pr-<N>
  const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (prIdx >= 0 && prIdx + 2 < parts.length) {
    return parts.slice(prIdx + 2).join(':')
  }
  return parts[parts.length - 1]
}

function obsUrl(pkg: Package): string {
  return packageUrl(packageInstances(pkg)[0], pkg.project, pkg.name)
}

// The PR project page lives on the instance(s) building the group's
// packages: take the first, in config order, that any package builds on.
function prProjectUrl(group: PRGroup): string {
  const slugs = new Set(group.packages.flatMap(p => packageInstances(p).map(i => i.slug)))
  const inst = instances.value.find(i => slugs.has(i.slug)) ?? instanceFor()
  return projectUrl(inst, `PR:pr-${group.pr}`)
}
</script>

<template>
  <div v-if="groups.length > 0" class="flex flex-col gap-[14px]">
    <!-- Section header -->
    <div class="flex items-center gap-[10px]">
      <h2 class="m-0 text-[15px] font-bold text-text-primary">PR builds</h2>
      <span class="text-[12.5px] text-text-muted">{{ groups.length }} pull request{{ groups.length !== 1 ? 's' : '' }}</span>
    </div>

    <!-- PR group cards -->
    <div class="flex flex-col gap-[12px]">
      <div
        v-for="group in groups"
        :key="group.pr"
        class="flex flex-col gap-[12px] rounded-[12px] p-[15px]"
        :style="{
          background: 'var(--bg-card)',
          border: '1px solid var(--border)',
          borderLeft: `4px solid ${STATE_COLOR[group.rollup_state] ?? 'var(--text-muted)'}`,
        }"
      >
        <!-- PR header -->
        <div class="flex items-center gap-[10px]">
          <span
            class="text-[10.5px] font-bold uppercase tracking-[0.04em] py-[3px] px-[9px] rounded-[6px]"
            :style="{
              color: STATE_COLOR[group.rollup_state] ?? 'var(--text-muted)',
              background: STATE_BG[group.rollup_state] ?? 'var(--blocked-tint)',
            }"
          >{{ STATE_LABEL[group.rollup_state] ?? group.rollup_state }}</span>

          <span class="text-[14px] font-bold text-text-primary">PR #{{ group.pr }}</span>

          <span class="text-[12px] text-text-muted">
            {{ group.packages.filter(p => p.rollup_state === 'succeeded').length }}/{{ group.packages.length }} packages green
          </span>

          <a
            :href="prProjectUrl(group) || undefined"
            target="_blank"
            rel="noopener"
            class="ml-auto text-[11.5px] font-bold text-brand-purple no-underline whitespace-nowrap flex-shrink-0"
          >OBS ↗</a>
        </div>

        <!-- Package rows -->
        <div class="flex flex-col gap-[6px]">
          <div
            v-for="pkg in group.packages"
            :key="`${pkg.project}/${pkg.name}`"
            class="flex items-center gap-[10px] py-[7px] px-[10px] rounded-[8px] bg-bg-card-2"
          >
            <!-- State dot -->
            <span
              class="w-2 h-2 rounded-[2px] flex-shrink-0"
              :style="{ background: STATE_COLOR[pkg.rollup_state] ?? 'var(--text-muted)' }"
            ></span>

            <!-- Package name -->
            <code class="font-mono text-[12.5px] font-semibold text-text-primary">{{ pkg.name }}</code>

            <!-- Subproject label -->
            <span class="text-[10.5px] text-text-muted font-mono">{{ subprojectLabel(pkg.project) }}</span>

            <!-- State label -->
            <span
              class="ml-auto text-[10.5px] font-bold uppercase tracking-[0.04em] py-[2px] px-[7px] rounded-[5px] flex-shrink-0"
              :style="{
                color: STATE_COLOR[pkg.rollup_state] ?? 'var(--text-muted)',
                background: STATE_BG[pkg.rollup_state] ?? 'var(--blocked-tint)',
              }"
            >{{ STATE_LABEL[pkg.rollup_state] ?? pkg.rollup_state }}</span>

            <!-- Active targets summary -->
            <span
              v-if="activeTargets(pkg).length > 0"
              class="text-[10.5px] text-text-muted flex-shrink-0"
            >{{ targetSummary(pkg) }}</span>

            <!-- OBS link -->
            <a
              :href="obsUrl(pkg) || undefined"
              target="_blank"
              rel="noopener"
              class="text-[10.5px] font-bold text-brand-purple no-underline flex-shrink-0"
            >↗</a>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
