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
      <div style="width: 34px; height: 34px; border-radius: 9px; background: var(--tint-postgres); display: flex; align-items: center; justify-content: center; color: var(--tech-postgres); flex-shrink: 0;">
        <svg width="20" height="20" viewBox="0 0 1123.51 1123.51">
          <path fill="currentColor" fill-rule="evenodd" d="M1082.02,1010.89H41.49L561.75,109.77l520.26,901.12ZM69.93,994.47h983.65L561.75,142.61,69.93,994.47Z"/>
          <polygon fill="currentColor" points="681.51 899.1 625.22 899.1 567.09 798.9 609.3 726.62 620.68 791.4 681.51 899.1"/>
          <path fill="currentColor" d="M718.7,562.3h-14.61l-6.06,10.37-45.26,78.39-33.43,57.9-3.84,6.66h107.44l32.67,32.67v59.03h-52.4c-5.34,0-9.67,4.33-9.67,9.67v39.11h62.55c26.37,0,47.78-21.13,48.27-47.38h.02v-160.72c0-47.33-38.37-85.7-85.7-85.7ZM740.29,646.03c0,5.32-4.35,9.67-9.67,9.67h-8.99c-5.32,0-9.67-4.35-9.67-9.67v-8.99c0-5.32,4.35-9.67,9.67-9.67h8.99c5.32,0,9.67,4.35,9.67,9.67v8.99Z"/>
          <polygon fill="currentColor" points="305.31 827.37 328.46 787.35 378.35 787.35 442.63 898.29 498.94 898.29 433.67 787.35 548.29 787.45 573.7 743.44 516.78 645.54 473.31 569.38 374.65 570.25 305.94 688.19 305.88 704.68 279.05 752.33 279.05 799.28 306.72 752.18"/>
          <polygon fill="currentColor" points="586.76 720.72 689.75 542.35 483.78 542.35 586.76 720.72"/>
          <path fill="currentColor" d="M557.54,809.88h-30.45v79.55c0,5.34,4.33,9.67,9.67,9.67h46.53l-.23-44.84-25.52-44.38Z"/>
          <path fill="currentColor" d="M362.46,867.89l23.21-40.05-10.35-17.95h-43.93l-22.88,39.62v39.32c0,5.34,4.33,9.67,9.67,9.67h61.73l-17.45-30.6Z"/>
        </svg>
      </div>
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
