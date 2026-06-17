<script setup lang="ts">
defineProps<{
  theme: 'light' | 'dark'
  mainTab: 'board' | 'artifacts'
}>()
const emit = defineEmits<{
  'toggle-theme': []
  'update:main-tab': [tab: 'board' | 'artifacts']
}>()
</script>

<template>
  <header style="display: flex; align-items: center; justify-content: space-between; gap: 20px;">
    <div style="display: flex; align-items: center; gap: 12px;">
<div style="display: flex; flex-direction: column; gap: 1px;">
        <h1 style="margin: 0; font-size: 21px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-primary);">PPG Build Board</h1>
        <span style="font-size: 12.5px; color: var(--text-muted);">Failure-first build monitor across every subproject of a product</span>
      </div>
    </div>
    <div class="tab-switcher">
      <button
        class="tab-pill"
        :class="{ active: mainTab === 'board' }"
        @click="emit('update:main-tab', 'board')"
      >
        Board
      </button>
      <button
        class="tab-pill"
        :class="{ active: mainTab === 'artifacts' }"
        @click="emit('update:main-tab', 'artifacts')"
      >
        Artifacts
      </button>
    </div>
    <button
      @click="emit('toggle-theme')"
      style="flex-shrink: 0; display: inline-flex; align-items: center; gap: 8px; padding: 8px 14px; border-radius: 10px; border: 1px solid var(--border); background: var(--bg-card); color: var(--text-secondary); font-family: inherit; font-size: 13px; font-weight: 600; cursor: pointer;"
    >
      <span style="width: 8px; height: 8px; border-radius: 99px; background: var(--brand-purple);"></span>
      {{ theme === 'dark' ? 'Dark' : 'Light' }} mode
    </button>
  </header>
</template>

<style scoped>
.tab-switcher {
  display: flex;
  gap: 2px;
  background: var(--bg-muted);
  padding: 3px;
  border-radius: 11px;
}

.tab-pill {
  padding: 5px 14px;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  border: none;
  background: transparent;
  color: var(--text-muted);
  transition: background 0.15s, color 0.15s, box-shadow 0.15s;
}

.tab-pill.active {
  background: var(--bg-card);
  color: var(--brand-purple);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12);
}
</style>
