import { onMounted, onUnmounted, watch, type Ref } from 'vue'

const BEAT_INTERVAL_MS = 60_000

// Sends a presence heartbeat while the browser tab is visible and an
// eligible main tab (Builds/Artifacts) is selected, so the backend can
// pause OBS polling when nobody is watching live build data.
export function usePresenceHeartbeat(eligible: Ref<boolean>) {
  function shouldBeat(): boolean {
    return document.visibilityState === 'visible' && eligible.value
  }

  function beat() {
    fetch('/api/presence', { method: 'POST' }).catch(() => {
      // A missed beat is harmless; the next interval retries.
    })
  }

  function beatIfWatching() {
    if (shouldBeat()) beat()
  }

  let timer: ReturnType<typeof setInterval> | null = null
  const stopWatch = watch(eligible, (on) => {
    if (on) beatIfWatching()
  })

  onMounted(() => {
    beatIfWatching()
    timer = setInterval(beatIfWatching, BEAT_INTERVAL_MS)
    document.addEventListener('visibilitychange', beatIfWatching)
  })
  onUnmounted(() => {
    if (timer) clearInterval(timer)
    document.removeEventListener('visibilitychange', beatIfWatching)
    stopWatch()
  })
}
