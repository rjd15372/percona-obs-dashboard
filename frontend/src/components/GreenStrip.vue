<script setup lang="ts">
import { computed } from 'vue'
import type { Package } from '../types/api'

const props = defineProps<{ packages: Package[] }>()

const groups = computed(() => {
  const map = new Map<string, Package[]>()
  for (const pkg of props.packages) {
    const list = map.get(pkg.project) ?? []
    list.push(pkg)
    map.set(pkg.project, list)
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([project, pkgs]) => ({ project, pkgs }))
})

function projectUrl(project: string): string {
  return `https://build.opensuse.org/project/show/${project}`
}

function packageUrl(project: string, name: string): string {
  return `https://build.opensuse.org/package/show/${project}/${name}`
}
</script>

<template>
  <div style="background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px; padding: 15px; display: flex; flex-direction: column; gap: 14px;">
    <!-- Summary header -->
    <div style="display: flex; align-items: center; gap: 9px;">
      <span style="width: 10px; height: 10px; border-radius: 3px; background: var(--ok);"></span>
      <span style="font-size: 13px; font-weight: 700; color: var(--text-primary);">All clear · {{ packages.length }} package{{ packages.length !== 1 ? 's' : '' }} fully built</span>
    </div>
    <!-- Per-project groups -->
    <div
      v-for="(group, index) in groups"
      :key="group.project"
      :style="`display: flex; flex-direction: column; gap: 7px;${index > 0 ? ' border-top: 1px solid var(--border); padding-top: 10px;' : ''}`"
    >
      <!-- Group header: full OBS project path linking to project page -->
      <a
        :href="projectUrl(group.project)"
        target="_blank"
        rel="noopener"
        class="project-link"
        style="font-family: var(--font-mono); font-size: 11px; color: var(--text-muted); text-decoration: none; display: inline-flex; align-items: center; gap: 3px;"
      >{{ group.project }} ↗</a>
      <!-- Package pills linking to individual OBS package pages -->
      <div style="display: flex; gap: 7px; flex-wrap: wrap;">
        <a
          v-for="pkg in group.pkgs"
          :key="pkg.name"
          :href="packageUrl(group.project, pkg.name)"
          target="_blank"
          rel="noopener"
          class="pkg-pill"
          style="display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border-radius: 7px; background: var(--ok-tint); text-decoration: none;"
        >
          <span style="width: 6px; height: 6px; border-radius: 99px; background: var(--ok); flex-shrink: 0;"></span>
          <code style="font-family: var(--font-mono); font-size: 11px; color: var(--text-secondary);">{{ pkg.name }}</code>
        </a>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pkg-pill:hover {
  opacity: 0.75;
}
.project-link:hover {
  color: var(--text-secondary);
}
</style>
