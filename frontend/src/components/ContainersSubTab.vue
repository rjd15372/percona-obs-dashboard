<template>
  <div class="containers-subtab">
    <div class="images-grid">
      <div
        v-for="image in containerImages"
        :key="image.id"
        class="image-card"
      >
        <!-- Section 1: Card header -->
        <div class="card-header">
          <div class="card-header-left">
            <div class="container-icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none"
                   stroke="currentColor" stroke-width="1.8"
                   stroke-linecap="round" stroke-linejoin="round">
                <rect x="2" y="7" width="20" height="14" rx="3"/>
                <path d="M7 7V5a2 2 0 012-2h6a2 2 0 012 2v2"/>
                <path d="M2 13h20"/>
              </svg>
            </div>
            <div class="card-title-block">
              <span class="image-name">{{ image.imageName }}</span>
              <span class="base-os">{{ image.baseOs }}</span>
            </div>
          </div>
          <span class="status-badge" :class="image.published ? 'published' : 'failing'">
            {{ image.published ? 'Published' : 'Build failing' }}
          </span>
        </div>

        <!-- Section 2: Registry -->
        <div class="registry-section">
          <div class="section-label">REGISTRY</div>
          <code class="registry-path">{{ image.registry }}</code>
        </div>

        <!-- Section 3: Available tags -->
        <div class="tags-section">
          <div class="section-label">AVAILABLE TAGS</div>
          <div class="tags-list">
            <code
              v-for="(tag, i) in image.tags"
              :key="tag"
              class="tag-chip"
              :class="{ 'tag-primary': i === 0 }"
            >{{ tag }}</code>
          </div>
        </div>

        <!-- Section 4: Docker pull -->
        <div class="pull-section">
          <div class="pull-header">
            <span class="section-label">DOCKER PULL</span>
            <button
              class="copy-btn"
              :class="{ copied: copiedKey === image.id }"
              @click="emit('copy', image.id, image.pullCmd)"
            >
              {{ copiedKey === image.id ? '✓ Copied' : 'Copy' }}
            </button>
          </div>
          <pre class="pull-code"><code>{{ image.pullCmd }}</code></pre>
        </div>
      </div>
    </div>

    <div v-if="containerImages.length === 0" class="empty-state">
      No container images for this version.
    </div>
  </div>
</template>

<script setup lang="ts">
import type { ContainerImage } from '../composables/useArtifacts'

defineProps<{
  containerImages: ContainerImage[]
  copiedKey: string | null
}>()

const emit = defineEmits<{
  'copy': [key: string, text: string]
}>()
</script>

<style scoped>
.containers-subtab {
  padding: 16px;
}

.images-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 16px;
}

.image-card {
  background: var(--bg-card);
  border-radius: 12px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

/* Section 1: Header */
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid var(--border);
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.container-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 8px;
  background: var(--info-tint, #dbeafe);
  color: var(--info, #3b82f6);
  flex-shrink: 0;
}

.card-title-block {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.image-name {
  font-size: 14px;
  font-weight: 700;
}

.base-os {
  font-size: 11.5px;
  color: var(--text-muted);
}

.status-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 3px 9px;
  border-radius: 10px;
  white-space: nowrap;
}

.status-badge.published {
  background: var(--success-tint, #d1fae5);
  color: var(--success, #16a34a);
}

.status-badge.failing {
  background: var(--danger-tint, #fee2e2);
  color: var(--danger, #dc2626);
}

/* Section 2: Registry */
.registry-section {
  background: var(--bg-card-2);
  padding: 10px 18px;
  border-bottom: 1px solid var(--border);
}

/* Section 3: Tags */
.tags-section {
  padding: 12px 18px;
  border-bottom: 1px solid var(--border);
}

.tags-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.tag-chip {
  font-family: var(--font-mono);
  font-size: 11px;
  padding: 3px 8px;
  border-radius: 6px;
  background: var(--bg-muted);
  color: var(--text-secondary);
}

.tag-chip.tag-primary {
  background: var(--brand-purple-tint);
  color: var(--brand-purple);
  font-weight: 700;
}

/* Section 4: Docker pull */
.pull-section {
  padding: 12px 18px;
  flex: 1;
}

.pull-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.section-label {
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}

.registry-path {
  display: block;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-secondary);
  word-break: break-all;
  margin-top: 4px;
}

.copy-btn {
  font-size: 12px;
  padding: 3px 10px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  cursor: pointer;
}

.copy-btn.copied {
  color: var(--success, #16a34a);
  border-color: var(--success, #16a34a);
}

.pull-code {
  background: var(--bg-card-2);
  padding: 10px 14px;
  border-radius: 8px;
  font-family: var(--font-mono);
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}

.empty-state {
  text-align: center;
  padding: 48px;
  color: var(--text-muted);
  font-size: 14px;
}
</style>
