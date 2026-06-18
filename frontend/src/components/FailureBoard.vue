<script setup lang="ts">
import { computed } from 'vue'
import PackageCard from './PackageCard.vue'
import GreenStrip from './GreenStrip.vue'
import type { Package } from '../types/api'

const props = defineProps<{ packages: Package[] }>()

const failingPackages = computed(() => props.packages.filter(p => p.rollup_state !== 'succeeded' && p.rollup_state !== 'published'))
const okPackages = computed(() => props.packages.filter(p => p.rollup_state === 'succeeded' || p.rollup_state === 'published'))
const attentionCount = computed(() => failingPackages.value.length)
</script>

<template>
  <div style="display: flex; flex-direction: column; gap: 14px; min-width: 0;">
    <!-- Section header -->
    <div style="display: flex; align-items: center; gap: 10px;">
      <h2 style="margin: 0; font-size: 15px; font-weight: 700; color: var(--text-primary);">Active packages</h2>
      <span style="font-size: 12.5px; color: var(--text-muted);">{{ attentionCount }} package{{ attentionCount !== 1 ? 's' : '' }} · sorted by severity</span>
    </div>

    <!-- 2-column failure grid -->
    <div v-if="failingPackages.length > 0" style="display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px;">
      <PackageCard
        v-for="pkg in failingPackages"
        :key="`${pkg.project}/${pkg.name}`"
        :pkg="pkg"
      />
    </div>

    <!-- All green state -->
    <div v-if="failingPackages.length === 0 && packages.length > 0" style="background: var(--ok-tint); border: 1px solid var(--ok); border-radius: 12px; padding: 28px; display: flex; flex-direction: column; align-items: center; gap: 8px; text-align: center;">
      <span style="font-size: 26px; color: var(--ok); font-weight: 800;">✓</span>
      <span style="font-size: 15px; font-weight: 700; color: var(--text-primary);">All packages green</span>
    </div>

    <!-- Empty state -->
    <div v-if="packages.length === 0" style="text-align: center; color: var(--text-muted); padding: 32px 0; font-size: 14px;">
      No packages found
    </div>

    <!-- Green strip -->
    <GreenStrip v-if="okPackages.length > 0" :packages="okPackages" />
  </div>
</template>
