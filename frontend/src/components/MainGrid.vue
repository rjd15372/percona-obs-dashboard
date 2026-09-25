<script setup lang="ts">
import FailureBoard from './FailureBoard.vue'
import EventLog from './EventLog.vue'
import type { Package, Event } from '../types/api'

defineProps<{
  packages: Package[]
  events: Event[]
  windowMin: number
  customFrom: string | null
  customTo: string | null
  spotlightStates: string[]
  activeInstances: string[]
}>()

const emit = defineEmits<{
  'update:windowMin': [min: number]
  'update:customFrom': [date: string]
  'update:customTo': [date: string]
}>()
</script>

<template>
  <div class="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_440px] gap-[18px] items-start">
    <FailureBoard :packages="packages" :spotlight-states="spotlightStates" :active-instances="activeInstances" />
    <EventLog
      :events="events"
      :window-min="windowMin"
      :custom-from="customFrom"
      :custom-to="customTo"
      @update:window-min="emit('update:windowMin', $event)"
      @update:custom-from="emit('update:customFrom', $event)"
      @update:custom-to="emit('update:customTo', $event)"
    />
  </div>
</template>
