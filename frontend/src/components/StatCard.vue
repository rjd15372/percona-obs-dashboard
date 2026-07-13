<script setup lang="ts">
defineProps<{
  label: string
  tint: 'brand' | 'info' | 'crit' | 'ok' | 'high'
}>()

const CHIP_CLASSES: Record<'brand' | 'info' | 'crit' | 'ok' | 'high', string> = {
  brand: 'bg-brand-purple-tint text-brand-purple',
  info: 'bg-info-tint text-info',
  crit: 'bg-crit-tint text-crit',
  ok: 'bg-ok-tint text-ok',
  high: 'bg-high-tint text-high',
}
</script>

<template>
  <div class="bg-bg-card border border-border rounded-[14px] p-[16px_18px] flex flex-col gap-2.5">
    <div class="flex items-center gap-[10px]">
      <div class="w-[30px] h-[30px] rounded-[8px] grid place-items-center shrink-0" :class="CHIP_CLASSES[tint]">
        <slot name="icon" />
      </div>
      <span class="text-[11.5px] font-bold uppercase tracking-[0.05em] text-text-muted">{{ label }}</span>
    </div>
    <slot name="value" />
    <slot />
    <div class="text-[12px] text-text-muted">
      <slot name="footnote" />
    </div>
  </div>
</template>
