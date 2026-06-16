<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Package, Target } from '../types/api'

const props = defineProps<{ pkg: Package }>()

const SKIP_STATES = new Set(['disabled', 'excluded', 'locked'])
const IN_PROGRESS_STATES = new Set(['scheduled', 'building', 'finished'])

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
  succeeded: 'Succeeded', failed: 'Failed', unresolvable: 'Unresolvable',
  broken: 'Broken', blocked: 'Blocked', building: 'Building',
  finished: 'Finishing', scheduled: 'Scheduled',
}

const SCOPE_LABEL: Record<string, string> = {
  common: 'Common', ppgcommon: 'PPG Common', version: 'PPG',
  container: 'Container', release: 'Release',
}

const TARGET_SEVERITY: Record<string, number> = {
  broken: 6, unresolvable: 5, failed: 4, blocked: 3, building: 2, finished: 1, scheduled: 1, succeeded: 0,
}

const INITIAL_VISIBLE = 3

const showAll = ref(false)
const expandedTargets = ref(new Set<string>())

function targetKey(t: Target): string {
  return `${t.repo}/${t.arch}`
}

function toggleTarget(t: Target) {
  if (!hasDetail(t)) return
  const key = targetKey(t)
  const next = new Set(expandedTargets.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expandedTargets.value = next
}

function isExpanded(t: Target): boolean {
  return expandedTargets.value.has(targetKey(t))
}

function hasDetail(t: Target): boolean {
  if (t.build_reason) return true
  if (t.state === 'blocked' && t.blocked_by) return true
  if ((t.state === 'finished' || t.state === 'unresolvable' || t.state === 'broken') && t.details) return true
  return false
}

function finishedOutcome(t: Target): 'ok' | 'fail' | 'unknown' {
  if (t.state !== 'finished') return 'unknown'
  if (t.details === 'succeeded' || t.details === 'unchanged') return 'ok'
  if (t.details === 'failed') return 'fail'
  return 'unknown'
}

function targetDotColor(t: Target): string {
  if (t.state === 'finished') {
    const o = finishedOutcome(t)
    if (o === 'ok') return 'var(--ok)'
    if (o === 'fail') return 'var(--fail)'
  }
  return STATE_COLOR[t.state] ?? 'var(--blocked)'
}


function targetBg(t: Target): string {
  if (t.state === 'finished') {
    const o = finishedOutcome(t)
    if (o === 'ok') return 'var(--ok-tint)'
    if (o === 'fail') return 'var(--fail-tint)'
  }
  return STATE_BG[t.state] ?? 'var(--blocked-tint)'
}

function buildReasonText(t: Target): string {
  if (!t.build_reason) return ''
  if (t.build_reason_packages?.length) return `${t.build_reason}: ${t.build_reason_packages.join(', ')}`
  return t.build_reason
}

function stateDetailLabel(t: Target): string {
  if (t.state === 'blocked') return 'WAITING FOR'
  if (t.state === 'finished') return 'BUILD OUTCOME'
  if (t.state === 'unresolvable') return 'UNRESOLVABLE'
  if (t.state === 'broken') return 'BROKEN'
  return ''
}

function stateDetailValue(t: Target): string {
  if (t.state === 'blocked') return t.blocked_by ?? ''
  if (t.state === 'finished') return t.details ?? ''
  if (t.state === 'unresolvable') return t.details ?? ''
  if (t.state === 'broken') return t.details ?? ''
  return ''
}

function stateDetailColor(t: Target): string {
  if (t.state === 'blocked') return 'var(--warn)'
  if (t.state === 'finished') {
    const o = finishedOutcome(t)
    if (o === 'ok') return 'var(--ok)'
    if (o === 'fail') return 'var(--fail)'
  }
  if (t.state === 'unresolvable') return 'var(--brand-purple)'
  if (t.state === 'broken') return 'var(--broken)'
  return 'var(--text-muted)'
}

const failingTargets = computed(() =>
  props.pkg.targets
    .filter(t => !SKIP_STATES.has(t.state) && t.state !== 'succeeded')
    .sort((a, b) => (TARGET_SEVERITY[b.state] ?? 0) - (TARGET_SEVERITY[a.state] ?? 0))
)
const visibleFailing = computed(() =>
  showAll.value ? failingTargets.value : failingTargets.value.slice(0, INITIAL_VISIBLE)
)
const hiddenCount = computed(() => Math.max(0, failingTargets.value.length - INITIAL_VISIBLE))

const rollupColor = computed(() => STATE_COLOR[props.pkg.rollup_state] ?? 'var(--text-muted)')
const rollupBg = computed(() => STATE_BG[props.pkg.rollup_state] ?? 'var(--blocked-tint)')
const obsUrl = computed(() => `https://build.opensuse.org/package/show/${props.pkg.project}/${props.pkg.name}`)

const stateAge = computed((): string | null => {
  if (!IN_PROGRESS_STATES.has(props.pkg.rollup_state)) return null
  if (!props.pkg.state_changed_at) return null
  const ms = Date.now() - new Date(props.pkg.state_changed_at).getTime()
  if (!Number.isFinite(ms) || ms < 0) return null
  const m = Math.floor(ms / 60000)
  if (m < 1) return 'for <1m'
  if (m < 60) return `for ${m}m`
  return `for ${Math.floor(m / 60)}h ${m % 60}m`
})

function logUrl(repo: string, arch: string): string {
  return `https://build.opensuse.org/package/live_build_log/${props.pkg.project}/${props.pkg.name}/${repo}/${arch}`
}
</script>

<template>
  <div :style="{
    background: 'var(--bg-card)',
    border: '1px solid var(--border)',
    borderLeft: `4px solid ${rollupColor}`,
    borderRadius: '12px',
    padding: '15px',
    display: 'flex',
    flexDirection: 'column',
    gap: '11px',
  }">
    <!-- Row 1: state pill + name + duration + OBS link -->
    <div style="display: flex; align-items: center; gap: 9px;">
      <span :style="{
        fontSize: '10.5px', fontWeight: '700', textTransform: 'uppercase',
        letterSpacing: '0.04em', padding: '3px 9px', borderRadius: '6px',
        color: rollupColor, background: rollupBg,
      }">{{ STATE_LABEL[pkg.rollup_state] ?? pkg.rollup_state }}</span>
      <code style="font-family: var(--font-mono); font-size: 13.5px; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ pkg.name }}</code>
      <span v-if="stateAge" style="margin-left: auto; font-size: 10.5px; color: var(--text-muted); font-family: var(--font-mono); white-space: nowrap; flex-shrink: 0;">{{ stateAge }}</span>
      <a :href="obsUrl" target="_blank" rel="noopener" :style="{ marginLeft: stateAge ? '0' : 'auto', fontSize: '11.5px', fontWeight: '700', color: 'var(--brand-purple)', textDecoration: 'none', whiteSpace: 'nowrap', flexShrink: '0' }">OBS ↗</a>
    </div>

    <!-- Row 2: scope tag + project path -->
    <div style="display: flex; align-items: center; gap: 7px;">
      <span style="font-size: 9.5px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; padding: 2px 7px; border-radius: 5px; background: var(--blocked-tint); color: var(--blocked);">{{ SCOPE_LABEL[pkg.scope] ?? pkg.scope }}</span>
      <code style="font-family: var(--font-mono); font-size: 10.5px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ pkg.project }}</code>
    </div>

    <!-- Row 3: failing targets -->
    <div v-if="failingTargets.length > 0" style="display: flex; flex-direction: column; gap: 6px;">
      <span style="font-size: 10.5px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em;">
        {{ failingTargets.length }} failing target{{ failingTargets.length !== 1 ? 's' : '' }}
      </span>
      <div style="display: flex; flex-direction: column; gap: 5px;">
        <div
          v-for="t in visibleFailing"
          :key="targetKey(t)"
          :style="{
            borderRadius: '7px',
            overflow: 'hidden',
            background: targetBg(t),
          }"
        >
          <!-- Target header row -->
          <div
            :style="{
              display: 'flex', alignItems: 'center', gap: '9px',
              padding: '5px 9px',
              cursor: hasDetail(t) ? 'pointer' : 'default',
              userSelect: 'none',
            }"
            @click="toggleTarget(t)"
          >
            <span :style="{ width: '8px', height: '8px', borderRadius: '2px', background: targetDotColor(t), flexShrink: '0' }"></span>
            <code style="font-family: var(--font-mono); font-size: 11.5px; color: var(--text-primary); flex-shrink: 0;">{{ t.repo }}/{{ t.arch }}</code>
            <span :style="{ fontSize: '11px', color: targetDotColor(t), marginLeft: 'auto', fontWeight: '600', flexShrink: '0' }">{{ STATE_LABEL[t.state] ?? t.state }}</span>
            <a
              :href="logUrl(t.repo, t.arch)"
              target="_blank"
              rel="noopener"
              style="font-size: 10.5px; color: var(--brand-purple); font-weight: 700; flex-shrink: 0; text-decoration: none;"
              @click.stop
            >log ↗</a>
            <span
              v-if="hasDetail(t)"
              style="font-size: 10px; color: var(--text-muted); flex-shrink: 0; width: 12px; text-align: center;"
            >{{ isExpanded(t) ? '▾' : '▸' }}</span>
          </div>

          <!-- Target body (collapsible) -->
          <div
            v-show="isExpanded(t)"
            style="padding: 0 9px 8px calc(9px + 8px + 9px); display: flex; flex-direction: column; gap: 5px;"
          >
            <hr style="border: none; border-top: 1px solid var(--border); margin: 0 0 3px;" />
            <div v-if="buildReasonText(t)" style="display: flex; flex-direction: column; gap: 1px;">
              <span style="font-size: 9px; text-transform: uppercase; letter-spacing: 0.07em; color: var(--text-muted); font-weight: 700;">Build reason</span>
              <span style="font-family: var(--font-mono); font-size: 10.5px; color: var(--text-secondary); line-height: 1.4;">{{ buildReasonText(t) }}</span>
            </div>
            <div v-if="stateDetailValue(t)" style="display: flex; flex-direction: column; gap: 1px;">
              <span style="font-size: 9px; text-transform: uppercase; letter-spacing: 0.07em; color: var(--text-muted); font-weight: 700;">{{ stateDetailLabel(t) }}</span>
              <span :style="{ fontFamily: 'var(--font-mono)', fontSize: '10.5px', color: stateDetailColor(t), fontWeight: '600', lineHeight: '1.4' }">{{ stateDetailValue(t) }}</span>
            </div>
          </div>
        </div>

        <button
          v-if="!showAll && hiddenCount > 0"
          @click="showAll = true"
          style="font-size: 11px; color: var(--brand-purple); font-weight: 600; padding: 4px 9px; border: none; background: transparent; cursor: pointer; text-align: left; font-family: inherit;"
        >+ {{ hiddenCount }} more</button>
        <button
          v-if="showAll && hiddenCount > 0"
          @click="showAll = false"
          style="font-size: 11px; color: var(--text-muted); font-weight: 600; padding: 4px 9px; border: none; background: transparent; cursor: pointer; text-align: left; font-family: inherit;"
        >Show less</button>
      </div>
    </div>

    <!-- Row 5: ok targets count -->
    <div style="font-size: 11px; color: var(--text-muted);">{{ pkg.ok_targets }}/{{ pkg.total_targets }} targets ok</div>
  </div>
</template>
