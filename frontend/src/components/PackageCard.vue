<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Package, Target } from '../types/api'
import { displayVersion, TAG_LABEL } from '../composables/useEventDisplay'
import { useRebuild } from '../composables/useRebuild'

const props = defineProps<{ pkg: Package }>()

const { trigger: triggerRebuild, isLoading: isRebuildLoading, errorFor: rebuildErrorFor } = useRebuild()

const SKIP_STATES = new Set(['disabled', 'excluded', 'locked'])
const REBUILD_STATES = new Set(['failed', 'broken', 'unresolvable', 'blocked'])

const STATE_COLOR: Record<string, string> = {
  succeeded: 'var(--ok)',
  published: 'var(--ok)',
  failed: 'var(--fail)',
  unresolvable: 'var(--broken)',
  broken: 'var(--broken)',
  blocked: 'var(--blocked)',
  building: 'var(--info)',
  finished: 'var(--warn)',
  scheduled: 'var(--info)',
}

const STATE_BG: Record<string, string> = {
  succeeded: 'var(--ok-tint)',
  published: 'var(--ok-tint)',
  failed: 'var(--fail-tint)',
  unresolvable: 'var(--broken-tint)',
  broken: 'var(--broken-tint)',
  blocked: 'var(--blocked-tint)',
  building: 'var(--info-tint)',
  finished: 'var(--warn-tint)',
  scheduled: 'var(--info-tint)',
}

const STATE_LABEL: Record<string, string> = {
  succeeded: 'Succeeded', published: 'Published', failed: 'Failed', unresolvable: 'Unresolvable',
  broken: 'Broken', blocked: 'Blocked', building: 'Building',
  finished: 'Finishing', scheduled: 'Scheduled',
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
  if (t.state === 'unresolvable') return 'var(--broken)'
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
const versionLabel = computed(() => displayVersion(props.pkg.version, props.pkg.is_container ?? false))
const obsUrl = computed(() => `https://build.opensuse.org/package/show/${props.pkg.project}/${props.pkg.name}`)

function elapsedTime(iso: string | undefined): string | null {
  if (!iso) return null
  const ms = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(ms) || ms < 0) return null
  const m = Math.floor(ms / 60000)
  if (m < 1) return '<1m'
  if (m < 60) return `${m}m`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

const stateAge = computed((): string | null => {
  const startedTimes = props.pkg.targets
    .filter(t => t.state === props.pkg.rollup_state && t.started_at)
    .map(t => new Date(t.started_at!).getTime())
    .filter(n => Number.isFinite(n))
  if (startedTimes.length === 0) return null
  const oldest = new Date(Math.min(...startedTimes)).toISOString()
  const elapsed = elapsedTime(oldest)
  return elapsed ? `for ${elapsed}` : null
})

function logUrl(repo: string, arch: string): string {
  return `https://build.opensuse.org/package/live_build_log/${props.pkg.project}/${props.pkg.name}/${repo}/${arch}`
}

function targetAge(t: Target): string | null {
  return elapsedTime(t.started_at)
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

    <!-- Row 2: scope tags + version badge -->
    <div style="display: flex; align-items: center; gap: 7px;">
      <span
        v-for="tag in (pkg.tags ?? [])" :key="tag"
        style="font-size: 9.5px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; padding: 2px 7px; border-radius: 5px; background: var(--blocked-tint); color: var(--blocked);"
      >{{ TAG_LABEL[tag] ?? tag }}</span>
      <span
        v-if="versionLabel"
        :style="{
          fontFamily: 'var(--font-mono)',
          fontSize: '10px',
          fontWeight: '700',
          padding: '2px 7px',
          borderRadius: '5px',
          background: pkg.is_container ? 'var(--brand-purple-tint)' : 'var(--bg-muted, var(--blocked-tint))',
          color: pkg.is_container ? 'var(--brand-purple)' : 'var(--text-secondary)',
          border: '1px solid var(--border)',
          whiteSpace: 'nowrap',
          flexShrink: '0',
        }"
      >{{ versionLabel }}</span>
    </div>

    <!-- Row 3: project path -->
    <div style="display: flex;">
      <code style="font-family: var(--font-mono); font-size: 10.5px; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">{{ pkg.project }}</code>
    </div>

    <!-- Row 4: failing targets -->
    <div v-if="failingTargets.length > 0" style="display: flex; flex-direction: column; gap: 6px;">
      <span style="font-size: 10.5px; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em;">
        {{ failingTargets.length }} active target{{ failingTargets.length !== 1 ? 's' : '' }}
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
            <span :style="{ fontSize: '11px', color: targetDotColor(t), marginLeft: 'auto', fontWeight: '600', flexShrink: '0' }">
              {{ STATE_LABEL[t.state] ?? t.state }}<template v-if="targetAge(t)"> · {{ targetAge(t) }}</template>
            </span>
            <a
              :href="logUrl(t.repo, t.arch)"
              target="_blank"
              rel="noopener"
              style="font-size: 10.5px; color: var(--brand-purple); font-weight: 700; flex-shrink: 0; text-decoration: none;"
              @click.stop
            >log ↗</a>
            <button
              v-if="REBUILD_STATES.has(t.state)"
              :disabled="isRebuildLoading(t.repo, t.arch)"
              :style="{
                background: 'none',
                border: 'none',
                cursor: isRebuildLoading(t.repo, t.arch) ? 'default' : 'pointer',
                padding: '0 2px',
                fontSize: '13px',
                color: 'var(--text-muted)',
                flexShrink: '0',
                lineHeight: '1',
              }"
              title="Retrigger build"
              aria-label="Retrigger build"
              @click.stop="triggerRebuild(pkg.project, pkg.name, t.repo, t.arch)"
            >
              <span :class="{ 'rebuild-spinning': isRebuildLoading(t.repo, t.arch) }">↺</span>
            </button>
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

          <!-- Rebuild error -->
          <div
            v-if="rebuildErrorFor(t.repo, t.arch)"
            :style="{
              padding: '3px 9px 6px',
              fontSize: '10.5px',
              color: 'var(--fail)',
              fontFamily: 'var(--font-mono)',
            }"
          >{{ rebuildErrorFor(t.repo, t.arch) }}</div>
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

<style scoped>
@keyframes rebuild-spin {
  to { transform: rotate(360deg); }
}
.rebuild-spinning {
  display: inline-block;
  animation: rebuild-spin 0.7s linear infinite;
}
button:focus-visible {
  outline: 2px solid var(--brand-purple);
  outline-offset: 2px;
  border-radius: 2px;
}
</style>
