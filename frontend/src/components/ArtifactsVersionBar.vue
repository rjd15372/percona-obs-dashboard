<script setup lang="ts">
import type { Context } from '../types/api'

defineProps<{
  version: string
  availableVersions: string[]
  activeTab: 'packages' | 'containers'
  contexts: Context[]
  selectedContext: Context
}>()

const emit = defineEmits<{
  'update:version': [v: string]
  'update:tab': [tab: 'packages' | 'containers']
  'update:context': [ctx: Context]
}>()
</script>

<template>
  <div class="version-bar">
    <div class="top-row">
      <!-- PostgreSQL badge -->
      <span class="pg-badge">PostgreSQL</span>

      <!-- Context: plain badge when only one context, dropdown when multiple -->
      <code v-if="contexts.length <= 1" class="obs-badge">
        {{ selectedContext.prefix }}:{{ version }}
      </code>
      <select
        v-else
        class="context-select"
        :value="selectedContext.apiBase"
        @change="emit('update:context', contexts.find(c => c.apiBase === ($event.target as HTMLSelectElement).value)!)"
      >
        <option
          v-for="ctx in contexts"
          :key="ctx.apiBase"
          :value="ctx.apiBase"
        >{{ ctx.label }}</option>
      </select>

      <!-- Version segment control -->
      <div v-if="availableVersions.length > 0" class="inline-group">
        <span class="row-label">Version</span>
        <div class="segment">
          <button
            v-for="v in availableVersions"
            :key="v"
            class="seg-btn"
            :class="{ active: v === version }"
            @click="emit('update:version', v)"
          >{{ v }}</button>
        </div>
      </div>

      <!-- Tab switcher -->
      <div class="inline-group" style="margin-left: auto;">
        <div class="segment">
          <button
            class="seg-btn"
            :class="{ active: activeTab === 'packages' }"
            @click="emit('update:tab', 'packages')"
          >Packages</button>
          <button
            class="seg-btn"
            :class="{ active: activeTab === 'containers' }"
            @click="emit('update:tab', 'containers')"
          >Container Images</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.version-bar {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 14px 18px;
  margin: 12px 16px 0;
  flex-shrink: 0;
}

.top-row {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.pg-badge {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 5px 12px;
  border-radius: 8px;
  background: var(--tint-postgres);
  color: var(--tech-postgres);
  font-size: 12px;
  font-weight: 700;
  border: 1px solid rgba(0, 94, 214, 0.15);
}

.obs-badge {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text-secondary);
  background: var(--bg-muted);
  padding: 5px 10px;
  border-radius: 7px;
}

.context-select {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text-secondary);
  background: var(--bg-muted);
  padding: 5px 10px;
  border-radius: 7px;
  border: none;
  cursor: pointer;
  appearance: auto;
}

.inline-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.row-label {
  font-size: 11px;
  color: var(--text-muted);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin-right: 2px;
}

.segment {
  display: flex;
  gap: 3px;
  background: var(--bg-muted);
  padding: 3px;
  border-radius: 9px;
  border: 1px solid var(--border);
}

.seg-btn {
  background: transparent;
  color: var(--text-muted);
  font-weight: 500;
  padding: 4px 12px;
  border-radius: 7px;
  border: 1px solid transparent;
  font-size: 13px;
  cursor: pointer;
  font-family: inherit;
}

.seg-btn.active {
  background: var(--bg-card);
  color: var(--text-primary);
  font-weight: 700;
  border-color: var(--border-strong);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12);
}
</style>
