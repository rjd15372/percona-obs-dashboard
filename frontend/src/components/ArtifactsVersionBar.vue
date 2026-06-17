<template>
  <div class="version-bar">
    <div class="version-bar-left">
      <span class="pg-badge">PostgreSQL</span>
      <span class="version-label">VERSION</span>
      <div class="version-pills">
        <button
          v-for="v in availableVersions"
          :key="v"
          class="version-pill"
          :class="{ active: v === version }"
          @click="emit('update:version', v)"
        >
          {{ v }}
        </button>
      </div>
    </div>
    <div class="obs-chip">
      <span class="obs-label">OBS</span>
      <code class="obs-root">{{ obsRoot }}</code>
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  version: string
  availableVersions: string[]
  obsRoot: string
}>()

const emit = defineEmits<{
  'update:version': [v: string]
}>()
</script>

<style scoped>
.version-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 20px;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  flex-wrap: wrap;
  gap: 12px;
}

.version-bar-left {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.pg-badge {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  background: #336791;
  color: #fff;
  letter-spacing: 0.02em;
}

.version-label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-muted);
}

.version-pills {
  display: flex;
  gap: 6px;
}

.version-pill {
  padding: 4px 12px;
  border-radius: 20px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.version-pill.active {
  background: var(--brand-purple);
  color: #fff;
  border: 1px solid var(--brand-purple);
}

.obs-chip {
  display: flex;
  align-items: center;
  gap: 8px;
}

.obs-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

.obs-root {
  font-family: var(--font-mono);
  font-size: 12px;
  background: var(--bg-muted);
  padding: 3px 8px;
  border-radius: 6px;
  color: var(--text-secondary);
}
</style>
