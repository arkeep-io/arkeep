import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { router } from '@/router'
import { ArkeepWebSocketClient, liveUpdatesUnavailable, UNAVAILABLE_AFTER_MS } from './websocket'

vi.mock('@/router', () => ({ router: { push: vi.fn() } }))

// FakeSocket stands in for the browser WebSocket: tests open, close and feed
// it by hand. Every instance is kept in sockets, the last one being current.
class FakeSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  readyState = FakeSocket.CONNECTING
  onopen: (() => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onerror: (() => void) | null = null
  url: string
  constructor(url: string) {
    this.url = url
    sockets.push(this)
  }
  send() {}
  close() { this.readyState = FakeSocket.CLOSED }
  open() { this.readyState = FakeSocket.OPEN; this.onopen?.() }
  drop() { this.readyState = FakeSocket.CLOSED; this.onclose?.({ code: 1006 }) }
  receive(msg: unknown) { this.onmessage?.({ data: JSON.stringify(msg) }) }
}
let sockets: FakeSocket[] = []
const current = () => sockets.at(-1)!

describe('ArkeepWebSocketClient', () => {
  let client: ArkeepWebSocketClient

  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('WebSocket', FakeSocket)
    setActivePinia(createPinia())
    sockets = []
    liveUpdatesUnavailable.value = false
    client = new ArkeepWebSocketClient()
  })

  afterEach(() => {
    client.disconnect()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('never puts the changes topic in the URL, so subscribing to it does not reconnect', async () => {
    client.subscribe('job:j1', () => {})
    current().open()
    const opened = sockets.length

    client.subscribe('changes', () => {})
    await Promise.resolve()

    expect(sockets.length).toBe(opened)
    expect(new URL(current().url).searchParams.get('topics')).toBe('job:j1')
  })

  it('delivers change messages to the changes topic', () => {
    const handler = vi.fn()
    client.subscribe('changes', handler)
    current().open()
    current().receive({ type: 'change', topic: 'changes', payload: { type: 'jobs', id: 'j1' } })

    expect(handler).toHaveBeenCalledWith(expect.objectContaining({ payload: { type: 'jobs', id: 'j1' } }))
  })

  it('calls the reconnect listeners after a lost socket comes back, not on the first connection', () => {
    const reconnected = vi.fn()
    client.onReconnect(reconnected)
    client.subscribe('changes', () => {})
    current().open()
    expect(reconnected).not.toHaveBeenCalled()

    current().drop()
    vi.advanceTimersByTime(1_000)
    current().open()
    expect(reconnected).toHaveBeenCalledTimes(1)
  })

  it('does not call them when it reopens the socket itself to add a topic', async () => {
    const reconnected = vi.fn()
    client.onReconnect(reconnected)
    client.subscribe('changes', () => {})
    current().open()

    client.subscribe('job:j1', () => {})
    await Promise.resolve()
    current().open()

    expect(sockets.length).toBe(2)
    expect(reconnected).not.toHaveBeenCalled()
  })

  it('stops calling a removed listener', () => {
    const reconnected = vi.fn()
    const remove = client.onReconnect(reconnected)
    client.subscribe('changes', () => {})
    current().open()
    remove()
    current().drop()
    vi.advanceTimersByTime(1_000)
    current().open()

    expect(reconnected).not.toHaveBeenCalled()
  })

  it('reports live updates unavailable only after the socket stayed down for a while', () => {
    client.subscribe('changes', () => {})
    current().open()
    current().drop()

    // A normal reconnect: no warning.
    vi.advanceTimersByTime(1_000)
    current().open()
    vi.advanceTimersByTime(UNAVAILABLE_AFTER_MS)
    expect(liveUpdatesUnavailable.value).toBe(false)

    // The socket keeps failing.
    current().drop()
    for (let elapsed = 0; elapsed < UNAVAILABLE_AFTER_MS; elapsed += 1_000) {
      vi.advanceTimersByTime(1_000)
      if (current().readyState === FakeSocket.CONNECTING) current().drop()
    }
    expect(liveUpdatesUnavailable.value).toBe(true)

    // It comes back.
    vi.advanceTimersByTime(30_000)
    current().open()
    expect(liveUpdatesUnavailable.value).toBe(false)
  })

  it('counts a first connection that never opened as a failure', () => {
    client.subscribe('changes', () => {})
    current().drop()
    vi.advanceTimersByTime(UNAVAILABLE_AFTER_MS)
    expect(liveUpdatesUnavailable.value).toBe(true)
  })

  // After a server restart the access token may be unknown to it: the
  // handshake fails before the socket opens, and nothing else renews the
  // token on a page that only listens.
  it('renews the access token before retrying after a failed handshake', async () => {
    const auth = useAuthStore()
    auth.accessToken = 'old'
    const refresh = vi.fn(async () => { auth.accessToken = 'new'; return true })
    auth.refresh = refresh
    client.subscribe('changes', () => {})
    current().open()

    current().drop() // an open socket is lost: reconnect as is
    await vi.advanceTimersByTimeAsync(1_000)
    expect(refresh).not.toHaveBeenCalled()

    current().drop() // the retry fails before opening
    await vi.advanceTimersByTimeAsync(2_000)
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(new URL(current().url).searchParams.get('token')).toBe('new')
  })

  it('keeps retrying when the renewal fails while the server is down', async () => {
    const auth = useAuthStore()
    auth.accessToken = 'old'
    const refresh = vi.fn().mockRejectedValue(new Error('unreachable'))
    auth.refresh = refresh
    client.subscribe('changes', () => {})
    current().drop()
    await vi.advanceTimersByTimeAsync(1_000)
    const attempts = sockets.length
    current().drop()
    await vi.advanceTimersByTimeAsync(2_000)

    expect(sockets.length).toBe(attempts + 1)
    // A failed renewal changed nothing: the next attempt tries again, so the
    // token is renewed once the server is back.
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  // Behind a proxy that does not pass WebSocket connections, every handshake
  // fails while the server answers: renewing the token before each attempt
  // would rotate the refresh token every 30 s for as long as the tab is open.
  it('renews the token once per outage, not before every attempt', async () => {
    const auth = useAuthStore()
    auth.accessToken = 'old'
    const refresh = vi.fn(async () => { auth.accessToken = 'new'; return true })
    auth.refresh = refresh
    client.subscribe('changes', () => {})

    current().drop()
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(30_000)
      current().drop()
    }
    expect(refresh).toHaveBeenCalledTimes(1)

    // Once the socket opened again, a new outage may renew it again.
    await vi.advanceTimersByTimeAsync(30_000)
    current().open()
    current().drop()
    await vi.advanceTimersByTimeAsync(1_000)
    current().drop()
    await vi.advanceTimersByTimeAsync(2_000)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  // A refused renewal ended the session: on a page that only listens nothing
  // else would notice, so the socket sends the user to the login page and
  // stops retrying without a token.
  it('goes to the login page when the renewal ends the session', async () => {
    const auth = useAuthStore()
    auth.accessToken = 'old'
    auth.refresh = vi.fn(async () => { auth.accessToken = null; return false })
    client.subscribe('changes', () => {})
    current().drop()
    const attempts = sockets.length
    await vi.advanceTimersByTimeAsync(60_000)

    expect(router.push).toHaveBeenCalledWith('/login')
    expect(sockets.length).toBe(attempts)
  })

  it('clears the warning on disconnect', () => {
    client.subscribe('changes', () => {})
    current().drop()
    vi.advanceTimersByTime(UNAVAILABLE_AFTER_MS)
    client.disconnect()
    expect(liveUpdatesUnavailable.value).toBe(false)
  })
})
