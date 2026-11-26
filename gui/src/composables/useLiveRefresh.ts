// useLiveRefresh — refresh a page when the objects it shows change on the
// server, instead of on a timer.
//
// The server announces every write on the "changes" WebSocket topic with a
// small { type, id } message (id is empty when a write changed several
// objects at once). The page says which changes concern it, and gets its
// fetch function called with { quiet: true }: it updates its data in place,
// without the loading skeletons. Changes that arrive together are coalesced
// into one refresh, run at most once per delay. The page also refreshes when
// the socket comes back after being lost, since the changes published
// meanwhile were missed (not on the first connection: the page has just
// loaded its data).
import { onUnmounted } from 'vue'
import { ADMIN_CHANGES_TOPIC, CHANGES_TOPIC, liveUpdatesUnavailable, wsClient } from '@/services/websocket'

// FetchOptions are the options of a page's fetch function. quiet: refresh the
// data in place, without the loading state (live updates); the refresh
// button and the first load leave it off.
export interface FetchOptions {
  quiet?: boolean
}

// Change is the payload of a "change" message.
export interface Change {
  type: string // "jobs", "agents", "policies", "destinations", "snapshots", "users", "audit"
  id: string // "" for a bulk change
}

// LIVE_REFRESH_DELAY_MS coalesces a burst of changes into one request.
export const LIVE_REFRESH_DELAY_MS = 1_000

// concerns is the type of changes a page follows ("jobs"), the types
// (["jobs", "policies"]), or a function for a finer choice, e.g. one job:
// (c) => c.type === 'jobs' && (c.id === id || c.id === '').
export type ChangeFilter = string | string[] | ((change: Change) => boolean)

export function useLiveRefresh(
  concerns: ChangeFilter,
  refresh: (options: FetchOptions) => unknown,
  { delayMs = LIVE_REFRESH_DELAY_MS }: { delayMs?: number } = {},
) {
  const matches = typeof concerns === 'function'
    ? concerns
    : (c: Change) => ([] as string[]).concat(concerns).includes(c.type)

  let timer: ReturnType<typeof setTimeout> | null = null
  let running = false
  // again is set when a change arrives while a refresh is running: that
  // refresh may have read the state before the change, so one more follows.
  let again = false
  let stopped = false

  async function run(): Promise<void> {
    timer = null
    if (stopped) return
    running = true
    try {
      await refresh({ quiet: true })
    } catch {
      // The page shows its own errors; a failed refresh waits for the next change.
    } finally {
      running = false
    }
    if (again) {
      again = false
      schedule()
    }
  }

  function schedule(): void {
    if (stopped) return
    if (running) {
      again = true
      return
    }
    if (timer === null) timer = setTimeout(run, delayMs)
  }

  // The users and audit log changes come on the admin topic, which the server
  // only fills for admins.
  const onChange = (msg: { type: string; payload?: Change }) => {
    if (msg.type === 'change' && msg.payload && matches(msg.payload)) schedule()
  }
  const unsubscribes = [CHANGES_TOPIC, ADMIN_CHANGES_TOPIC].map((topic) => wsClient.subscribe<Change>(topic, onChange))
  const stopReconnect = wsClient.onReconnect(schedule)

  onUnmounted(() => {
    stopped = true
    for (const unsubscribe of unsubscribes) unsubscribe()
    stopReconnect()
    if (timer !== null) clearTimeout(timer)
  })

  return { unavailable: liveUpdatesUnavailable }
}
