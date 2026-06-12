<script setup lang="ts">
import type { PRGroup, Package } from '../types/api'

defineProps<{ groups: PRGroup[] }>()

const STATE_COLOR: Record<string, string> = {
  succeeded: 'var(--ok)',
  failed: 'var(--fail)',
  unresolvable: 'var(--warn)',
  broken: 'var(--broken)',
  blocked: 'var(--blocked)',
  building: 'var(--info)',
  scheduled: 'var(--info)',
}

const STATE_BG: Record<string, string> = {
  succeeded: 'var(--ok-tint)',
  failed: 'var(--fail-tint)',
  unresolvable: 'var(--warn-tint)',
  broken: 'var(--broken-tint)',
  blocked: 'var(--blocked-tint)',
  building: 'var(--info-tint)',
  scheduled: 'var(--info-tint)',
}

const STATE_LABEL: Record<string, string> = {
  succeeded: 'OK', failed: 'Failed', unresolvable: 'Unresolvable',
  broken: 'Broken', blocked: 'Blocked', building: 'Building', scheduled: 'Scheduled',
}

const SKIP_STATES = new Set(['disabled', 'excluded', 'locked'])

function failingTargets(pkg: Package) {
  return pkg.targets.filter(t => !SKIP_STATES.has(t.state) && t.state !== 'succeeded')
}

function subprojectLabel(project: string): string {
  // "isv:percona:PR:pr-42:ppg17" → "ppg17"
  const parts = project.split(':')
  // Find "PR" segment index and take everything after pr-<N>
  const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (prIdx >= 0 && prIdx + 2 < parts.length) {
    return parts.slice(prIdx + 2).join(':')
  }
  return parts[parts.length - 1]
}

function obsUrl(pkg: Package): string {
  return `https://build.opensuse.org/package/show/${pkg.project}/${pkg.name}`
}

function prProjectUrl(pr: string): string {
  return `https://build.opensuse.org/project/show/isv:percona:PR:pr-${pr}`
}
</script>

<template>
  <div v-if="groups.length > 0" style="display: flex; flex-direction: column; gap: 14px;">
    <!-- Section header -->
    <div style="display: flex; align-items: center; gap: 10px;">
      <h2 style="margin: 0; font-size: 15px; font-weight: 700; color: var(--text-primary);">PR builds</h2>
      <span style="font-size: 12.5px; color: var(--text-muted);">{{ groups.length }} pull request{{ groups.length !== 1 ? 's' : '' }}</span>
    </div>

    <!-- PR group cards -->
    <div style="display: flex; flex-direction: column; gap: 12px;">
      <div
        v-for="group in groups"
        :key="group.pr"
        :style="{
          background: 'var(--bg-card)',
          border: '1px solid var(--border)',
          borderLeft: `4px solid ${STATE_COLOR[group.rollup_state] ?? 'var(--text-muted)'}`,
          borderRadius: '12px',
          padding: '15px',
          display: 'flex',
          flexDirection: 'column',
          gap: '12px',
        }"
      >
        <!-- PR header -->
        <div style="display: flex; align-items: center; gap: 10px;">
          <span :style="{
            fontSize: '10.5px', fontWeight: '700', textTransform: 'uppercase',
            letterSpacing: '0.04em', padding: '3px 9px', borderRadius: '6px',
            color: STATE_COLOR[group.rollup_state] ?? 'var(--text-muted)',
            background: STATE_BG[group.rollup_state] ?? 'var(--blocked-tint)',
          }">{{ STATE_LABEL[group.rollup_state] ?? group.rollup_state }}</span>

          <span style="font-size: 14px; font-weight: 700; color: var(--text-primary);">PR #{{ group.pr }}</span>

          <span style="font-size: 12px; color: var(--text-muted);">
            {{ group.packages.filter(p => p.rollup_state === 'succeeded').length }}/{{ group.packages.length }} packages green
          </span>

          <a
            :href="prProjectUrl(group.pr)"
            target="_blank"
            rel="noopener"
            style="margin-left: auto; font-size: 11.5px; font-weight: 700; color: var(--brand-purple); text-decoration: none; white-space: nowrap; flex-shrink: 0;"
          >OBS ↗</a>
        </div>

        <!-- Package rows -->
        <div style="display: flex; flex-direction: column; gap: 6px;">
          <div
            v-for="pkg in group.packages"
            :key="`${pkg.project}/${pkg.name}`"
            style="display: flex; align-items: center; gap: 10px; padding: 7px 10px; border-radius: 8px; background: var(--bg-card-2);"
          >
            <!-- State dot -->
            <span :style="{
              width: '8px', height: '8px', borderRadius: '2px',
              background: STATE_COLOR[pkg.rollup_state] ?? 'var(--text-muted)',
              flexShrink: '0',
            }"></span>

            <!-- Package name -->
            <code style="font-family: var(--font-mono); font-size: 12.5px; font-weight: 600; color: var(--text-primary);">{{ pkg.name }}</code>

            <!-- Subproject label -->
            <span style="font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono);">{{ subprojectLabel(pkg.project) }}</span>

            <!-- State label -->
            <span :style="{
              marginLeft: 'auto',
              fontSize: '10.5px', fontWeight: '700', textTransform: 'uppercase',
              letterSpacing: '0.04em', padding: '2px 7px', borderRadius: '5px',
              color: STATE_COLOR[pkg.rollup_state] ?? 'var(--text-muted)',
              background: STATE_BG[pkg.rollup_state] ?? 'var(--blocked-tint)',
              flexShrink: '0',
            }">{{ STATE_LABEL[pkg.rollup_state] ?? pkg.rollup_state }}</span>

            <!-- Failing targets summary -->
            <span
              v-if="failingTargets(pkg).length > 0"
              style="font-size: 10.5px; color: var(--text-muted); flex-shrink: 0;"
            >{{ failingTargets(pkg).length }} failing</span>

            <!-- OBS link -->
            <a
              :href="obsUrl(pkg)"
              target="_blank"
              rel="noopener"
              style="font-size: 10.5px; font-weight: 700; color: var(--brand-purple); text-decoration: none; flex-shrink: 0;"
            >↗</a>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
