// useAutoRefresh.ts — Refresh a page's data on a timer chosen by the user.
//
// The interval is a preference of the browser, kept per page in localStorage
// under arkeep:auto-refresh:<page>, in seconds; 0 (the default) turns it off.
// Only the values of AUTO_REFRESH_INTERVALS are accepted, so a stale or edited
// value falls back to off rather than hammering the server.
//
// The timer pauses while the tab is hidden and refreshes once when it is shown
// again. A tick is skipped while the previous refresh, or one the page started
// itself (busy), is still running, so requests never pile up on a slow server.

import { computed, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import { tryOnScopeDispose, useDocumentVisibility, useLocalStorage } from '@vueuse/core'

// FetchOptions is what a page's fetch function receives from RefreshControl:
// quiet is set on the timer's refreshes, which update the data in place
// without loading skeletons.
export interface FetchOptions {
  quiet?: boolean
}

// The intervals offered, in seconds. 0 means off.
export const AUTO_REFRESH_INTERVALS = [0, 5, 10, 30, 60, 300] as const

// autoRefreshStorageKey is where the interval of page is remembered.
export function autoRefreshStorageKey(page: string): string {
  return `arkeep:auto-refresh:${page}`
}

function isInterval(v: unknown): v is number {
  return typeof v === 'number' && (AUTO_REFRESH_INTERVALS as readonly number[]).includes(v)
}

export function useAutoRefresh(
  page: string,
  refresh: () => unknown,
  options: { busy?: MaybeRefOrGetter<boolean> } = {},
) {
  const stored = useLocalStorage<number>(autoRefreshStorageKey(page), 0)

  // The interval in seconds, 0 when off.
  const interval = computed<number>({
    get: () => (isInterval(stored.value) ? stored.value : 0),
    set: (v) => {
      stored.value = isInterval(v) ? v : 0
    },
  })
  const enabled = computed(() => interval.value > 0)

  // True while a refresh started by the timer runs.
  const refreshing = ref(false)

  const visibility = useDocumentVisibility()
  let timer: ReturnType<typeof setInterval> | undefined

  async function tick() {
    if (refreshing.value || toValue(options.busy)) return
    refreshing.value = true
    try {
      await refresh()
    } catch {
      // The page reports its own errors; the next tick tries again.
    } finally {
      refreshing.value = false
    }
  }

  function stop() {
    if (timer !== undefined) {
      clearInterval(timer)
      timer = undefined
    }
  }

  function start() {
    stop()
    if (enabled.value && visibility.value !== 'hidden') {
      timer = setInterval(tick, interval.value * 1000)
    }
  }

  watch([interval, visibility], ([, vis], [, prevVis]) => {
    start()
    // Back on the tab: what it shows may be long out of date.
    if (enabled.value && vis === 'visible' && prevVis === 'hidden') void tick()
  })
  start()
  tryOnScopeDispose(stop)

  return { interval, enabled, refreshing }
}
