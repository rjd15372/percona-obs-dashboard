import { watch, watchEffect, onMounted, type Ref } from 'vue'
import type { Context } from '../types/api'
import { PPG_CONTEXT } from '../lib/contexts'

export function contextToKey(ctx: Context): string {
  const parts = ctx.prefix.split(':')
  const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
  if (prIdx >= 0) {
    return parts[prIdx + 1] // e.g. "pr-106"
  }
  return parts[parts.length - 1] // "ppg" or "releases"
}

export function keyToContext(key: string, contexts: Context[]): Context | undefined {
  return contexts.find(c => contextToKey(c) === key)
}

interface UrlStateOptions {
  mainTab: Ref<'board' | 'artifacts'>
  boardCtx: Ref<Context>
  version: Ref<string>
  activeTags: Ref<string[]>
  artifactsCtx: Ref<Context>
  artifactsVersion: Ref<string>
  artifactsTab: Ref<'packages' | 'containers'>
  boardContexts: Ref<Context[]>
  artifactsContexts: Ref<Context[]>
}

export function useUrlState(state: UrlStateOptions): void {
  const {
    mainTab, boardCtx, version, activeTags,
    artifactsCtx, artifactsVersion, artifactsTab,
    boardContexts, artifactsContexts,
  } = state

  // Pending raw URL keys awaiting context list population (PR contexts load async)
  let pendingBoardKey: string | null = null
  let pendingArtifactsKey: string | null = null
  let hydrated = false

  onMounted(() => {
    const params = new URLSearchParams(window.location.search)

    const tab = params.get('tab')
    if (tab === 'board' || tab === 'artifacts') mainTab.value = tab

    const ver = params.get('version')
    if (ver) version.value = ver

    const tags = params.get('tags')
    if (tags) activeTags.value = tags.split(',').filter(Boolean)

    const aver = params.get('aversion')
    if (aver) artifactsVersion.value = aver

    const sub = params.get('sub')
    if (sub === 'packages' || sub === 'containers') artifactsTab.value = sub

    const ctxKey = params.get('ctx')
    if (ctxKey) {
      const resolved = keyToContext(ctxKey, boardContexts.value)
      if (resolved) {
        boardCtx.value = resolved
      } else {
        pendingBoardKey = ctxKey
      }
    }

    const actxKey = params.get('actx')
    if (actxKey) {
      const resolved = keyToContext(actxKey, artifactsContexts.value)
      if (resolved) {
        artifactsCtx.value = resolved
      } else {
        pendingArtifactsKey = actxKey
      }
    }

    hydrated = true
  })

  // Resolve board ctx once context list loads (PR contexts come from prGroups async)
  watch(boardContexts, (contexts) => {
    if (!pendingBoardKey || contexts.length === 0) return
    const resolved = keyToContext(pendingBoardKey, contexts)
    boardCtx.value = resolved ?? PPG_CONTEXT
    pendingBoardKey = null
  }, { immediate: true })

  // Resolve artifacts ctx once context list loads
  watch(artifactsContexts, (contexts) => {
    if (!pendingArtifactsKey || contexts.length === 0) return
    const resolved = keyToContext(pendingArtifactsKey, contexts)
    artifactsCtx.value = resolved ?? PPG_CONTEXT
    pendingArtifactsKey = null
  }, { immediate: true })

  // Write URL whenever any state ref changes; omit default-value params
  watchEffect(() => {
    if (!hydrated) return
    const params = new URLSearchParams()

    if (mainTab.value !== 'board') params.set('tab', mainTab.value)

    const boardKey = contextToKey(boardCtx.value)
    if (boardKey !== 'ppg') params.set('ctx', boardKey)

    if (version.value) params.set('version', version.value)

    if (activeTags.value.length > 0) params.set('tags', activeTags.value.join(','))

    const artKey = contextToKey(artifactsCtx.value)
    if (artKey !== 'ppg') params.set('actx', artKey)

    if (artifactsVersion.value) params.set('aversion', artifactsVersion.value)

    if (artifactsTab.value !== 'packages') params.set('sub', artifactsTab.value)

    const search = params.toString()
    history.replaceState(null, '', search ? `?${search}` : window.location.pathname)
  })
}
