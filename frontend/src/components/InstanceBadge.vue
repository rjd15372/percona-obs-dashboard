<script setup lang="ts">
import { computed } from 'vue'
import { useInstances } from '../composables/useInstances'

const props = defineProps<{ slug?: string }>()
const { multi, instanceFor } = useInstances()

const inst = computed(() => instanceFor(props.slug))

function ago(iso?: string): string {
  if (!iso) return 'a while'
  const m = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 60000))
  if (m < 60) return `${m}m`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

const stale = computed(() => inst.value !== undefined && !inst.value.health.ok)
const title = computed(() => stale.value && inst.value
  ? `${inst.value.name} unreachable — last update ${ago(inst.value.health.last_success)} ago`
  : inst.value?.name)
</script>

<template>
  <span
    v-if="multi && inst"
    class="inline-flex items-center gap-[4px] font-mono text-[9.5px] font-bold py-[2px] px-[6px] rounded-[5px] border whitespace-nowrap flex-shrink-0"
    :style="stale
      ? { color: 'var(--warn)', background: 'var(--warn-tint)', borderColor: 'var(--warn)' }
      : { color: 'var(--text-secondary)', background: 'var(--bg-muted, var(--blocked-tint))', borderColor: 'var(--border)' }"
    :title="title"
  >{{ inst.name }}<template v-if="stale"> · stale</template></span>
</template>
