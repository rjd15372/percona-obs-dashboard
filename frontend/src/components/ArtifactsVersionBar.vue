<script setup lang="ts">
defineProps<{
  version: string
  availableVersions: string[]
  obsRoot: string
  activeTab: 'packages' | 'containers'
}>()

const emit = defineEmits<{
  'update:version': [v: string]
  'update:tab': [tab: 'packages' | 'containers']
}>()
</script>

<template>
  <div class="version-bar">
    <!-- Top row: badge + context + version + tab switcher -->
    <div class="top-row">
      <!-- PostgreSQL badge — matches ContextBar exactly -->
      <span class="pg-badge">PostgreSQL</span>

      <!-- OBS project context badge -->
      <code class="obs-badge">{{ obsRoot }}</code>

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

/* PostgreSQL badge — identical to ContextBar */
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

/* OBS context code badge */
.obs-badge {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text-secondary);
  background: var(--bg-muted);
  padding: 5px 10px;
  border-radius: 7px;
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

/* Segment control — identical pattern to ContextBar + AppHeader */
.segment {
  display: flex;
  gap: 3px;
  background: var(--bg-muted);
  padding: 3px;
  border-radius: 9px;
}

.seg-btn {
  background: transparent;
  color: var(--text-muted);
  font-weight: 500;
  padding: 4px 12px;
  border-radius: 7px;
  border: none;
  font-size: 13px;
  cursor: pointer;
  font-family: inherit;
}

.seg-btn.active {
  background: var(--bg-card);
  color: var(--text-primary);
  font-weight: 700;
}
</style>
