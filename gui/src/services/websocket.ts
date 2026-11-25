// websocket.ts — WebSocket client with automatic reconnection and topic pub/sub.
//
// The server sends messages shaped as { type: string, topic: string, payload: any }.
// Consumers subscribe to named topics; the client handles connection lifecycle
// transparently. A single WebSocket connection is shared across all subscribers.

import { ref } from 'vue'
import { router } from '@/router'
import { useAuthStore } from '@/stores/auth'

// ─── Types ────────────────────────────────────────────────────────────────────

type MessageType =
  | 'job.status'
  | 'job.log'
  | 'agent.status'
  | 'agent.metrics'
  | 'notification'
  | 'change'
  | 'ping'

interface WSMessage<T = unknown> {
  type: MessageType
  topic: string
  payload: T
}

type MessageHandler<T = unknown> = (message: WSMessage<T>) => void

// ─── Constants ────────────────────────────────────────────────────────────────

const INITIAL_BACKOFF_MS = 1_000
const MAX_BACKOFF_MS = 30_000
const BACKOFF_MULTIPLIER = 2
const PING_INTERVAL_MS = 25_000
// UNAVAILABLE_AFTER_MS is how long the socket must stay down before
// liveUpdatesUnavailable turns true: a normal reconnect (server restart,
// laptop waking up) takes a few seconds and should not flash a warning.
export const UNAVAILABLE_AFTER_MS = 30_000

// CHANGES_TOPIC carries "something changed" events ({ type, id }). The server
// subscribes every connection to it, like notifications:<user_id>.
export const CHANGES_TOPIC = 'changes'

// liveUpdatesUnavailable is true while the socket has been down for longer
// than UNAVAILABLE_AFTER_MS, e.g. behind a proxy that does not upgrade
// WebSocket connections: pages then only change when refreshed by hand.
export const liveUpdatesUnavailable = ref(false)

// ─── WebSocket Client ─────────────────────────────────────────────────────────

export class ArkeepWebSocketClient {
  private ws: WebSocket | null = null
  private subscriptions: Map<string, Set<MessageHandler>> = new Map()
  private backoffMs = INITIAL_BACKOFF_MS
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private pingTimer: ReturnType<typeof setInterval> | null = null
  private explicitTopics: string[] = []
  private destroyed = false
  // openedTopics is the topics parameter the current socket was opened with.
  private openedTopics = ''
  private resyncQueued = false
  // lost is true once a socket closed without being asked to: the next open
  // is a reconnect, and events may have been missed meanwhile.
  private lost = false
  private reconnectListeners: Set<() => void> = new Set()
  // handshakeFailed is true when the last socket closed without ever
  // opening. The browser hides why; after a server restart it is a 401 on an
  // access token the server no longer knows (its signing keys may live in
  // memory), so the next attempt renews the token first.
  private handshakeFailed = false
  // tokenRenewed is true once the socket renewed the token since it last
  // opened. A handshake that keeps failing with a fresh token is not about
  // the token (e.g. a proxy that does not pass WebSocket connections), so it
  // is not renewed again until the socket opens.
  private tokenRenewed = false
  private unavailableTimer: ReturnType<typeof setTimeout> | null = null

  // connect opens the WebSocket connection. It is called automatically when
  // the first subscription is added if the socket is not yet open.
  //
  // topics: additional topics to subscribe to beyond the automatic
  //   notifications:<user_id> topic that the server adds from JWT claims.
  connect(topics: string[] = []): void {
    if (this.ws?.readyState === WebSocket.OPEN) return

    this.explicitTopics = topics
    this._open()
  }

  // disconnect closes the connection and prevents automatic reconnection.
  disconnect(): void {
    this.destroyed = true
    this._clearTimers()
    this._clearUnavailable()
    this.lost = false
    this.ws?.close()
    this.ws = null
  }

  // onReconnect registers a listener called each time the socket opens again
  // after it was lost (not on the first connection, nor when the client
  // reopens it itself to change its topics). Events published while it was
  // down are gone: listeners refetch what they show. Returns a function that
  // removes the listener.
  onReconnect(listener: () => void): () => void {
    this.reconnectListeners.add(listener)
    return () => {
      this.reconnectListeners.delete(listener)
    }
  }

  // subscribe registers a handler for messages on the given topic.
  // Returns an unsubscribe function.
  subscribe<T = unknown>(topic: string, handler: MessageHandler<T>): () => void {
    const isNewTopic = !this.subscriptions.has(topic)

    if (isNewTopic) {
      this.subscriptions.set(topic, new Set())
    }
    this.subscriptions.get(topic)!.add(handler as MessageHandler)

    if (!this.ws || this.ws.readyState === WebSocket.CLOSED) {
      // No active connection: open fresh with all current topics.
      this.destroyed = false
      this._open()
    } else if (isNewTopic) {
      // The server only learns about topics from the URL at connection time,
      // so a new topic needs a reconnect, also while the socket is still
      // connecting. Subscriptions made in the same tick (a list page
      // subscribing once per row) share a single reconnect.
      this._queueResync()
    }

    return () => {
      const handlers = this.subscriptions.get(topic)
      if (!handlers) return
      handlers.delete(handler as MessageHandler)
      // Remove the topic entry entirely when the last subscriber leaves so
      // it is not included in future WebSocket connection URLs.
      if (handlers.size === 0) {
        this.subscriptions.delete(topic)
      }
    }
  }

  // ─── Private ───────────────────────────────────────────────────────────────

  private _buildURL(): string {
    const auth = useAuthStore()
    const token = auth.accessToken

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.host

    const url = new URL(`${protocol}//${host}/api/v1/ws`)
    if (token) url.searchParams.set('token', token)

    const topics = this._topicsParam()
    if (topics) url.searchParams.set('topics', topics)

    return url.toString()
  }

  // _topicsParam merges explicitly-requested topics with topics derived from
  // active subscriptions, as sent in the connection URL.
  private _topicsParam(): string {
    const allTopics = [
      ...this.explicitTopics,
      ...this.subscriptions.keys(),
    ].filter(
      // Filter out the topics the server adds automatically, so subscribing to
      // them never reopens the socket.
      (t) => !t.startsWith('notifications:') && t !== CHANGES_TOPIC,
    )
    return [...new Set(allTopics)].join(',')
  }

  private _queueResync(): void {
    if (this.resyncQueued) return
    this.resyncQueued = true
    queueMicrotask(() => {
      this.resyncQueued = false
      this._resync()
    })
  }

  // _resync reopens a connecting or open socket whose URL lacks the current
  // topics. A closing or closed socket is left alone: its reconnect builds the
  // URL from the subscriptions at that time.
  private _resync(): void {
    const stale = this.ws
    if (!stale || stale.readyState === WebSocket.CLOSING || stale.readyState === WebSocket.CLOSED) return
    if (this._topicsParam() === this.openedTopics) return

    // Detach the close handler first to prevent the reconnect scheduler from
    // firing (we call _open() directly for an immediate reconnect).
    this.ws = null
    stale.onclose = null
    stale.close()
    this._open()
  }

  private _open(): void {
    if (this.destroyed) return
    if (this.ws?.readyState === WebSocket.CONNECTING) return

    let opened = false
    try {
      this.openedTopics = this._topicsParam()
      this.ws = new WebSocket(this._buildURL())
    } catch {
      this._scheduleReconnect()
      return
    }

    this.ws.onopen = () => {
      opened = true
      this.tokenRenewed = false
      console.debug('[WS] Connected')
      this.backoffMs = INITIAL_BACKOFF_MS
      this._startPing()
      this._clearUnavailable()
      if (this.lost) {
        this.lost = false
        for (const listener of this.reconnectListeners) listener()
      }
    }

    this.ws.onmessage = (event: MessageEvent<string>) => {
      this._handleMessage(event.data)
    }

    this.ws.onerror = (err) => {
      console.warn('[WS] Error', err)
    }

    this.ws.onclose = (event) => {
      console.debug(`[WS] Closed (code=${event.code})`)
      this._clearTimers()
      if (!this.destroyed) {
        this.lost = true
        this.handshakeFailed = !opened
        this._startUnavailableTimer()
        this._scheduleReconnect()
      }
    }
  }

  private _handleMessage(raw: string): void {
    let msg: WSMessage
    try {
      msg = JSON.parse(raw) as WSMessage
    } catch {
      console.warn('[WS] Invalid JSON message', raw)
      return
    }

    // Dispatch to all handlers subscribed to this topic
    const handlers = this.subscriptions.get(msg.topic)
    if (handlers) {
      for (const handler of handlers) {
        handler(msg)
      }
    }

    // Also dispatch to wildcard handlers registered for the message type
    // (topic = '*' is a convention for "all messages")
    const wildcardHandlers = this.subscriptions.get('*')
    if (wildcardHandlers) {
      for (const handler of wildcardHandlers) {
        handler(msg)
      }
    }
  }

  private _scheduleReconnect(): void {
    if (this.destroyed || this.reconnectTimer !== null) return

    const delay = this.backoffMs
    console.debug(`[WS] Reconnecting in ${delay}ms`)

    this.reconnectTimer = setTimeout(async () => {
      this.reconnectTimer = null
      if (this.handshakeFailed && !this.tokenRenewed) {
        this.handshakeFailed = false
        // Pages that only update from this socket send no request that would
        // renew the token on a 401, so the socket renews it itself, once per
        // outage. While the server is down the renewal fails like the
        // connection and is tried again on the next attempt. A refused
        // refresh token ends the session: nothing else would notice on a
        // page that only listens, so the socket sends the user to the login
        // page and stops; the next subscription after signing in reopens it.
        const auth = useAuthStore()
        if (auth.accessToken) {
          try {
            if (!(await auth.refresh())) {
              router.push('/login')
              return
            }
            this.tokenRenewed = true
          } catch { /* unreachable: the next attempt tries again */ }
        }
      }
      this._open()
    }, delay)

    // Exponential backoff capped at MAX_BACKOFF_MS
    this.backoffMs = Math.min(this.backoffMs * BACKOFF_MULTIPLIER, MAX_BACKOFF_MS)
  }

  // _startUnavailableTimer turns liveUpdatesUnavailable on if the socket is
  // still down after UNAVAILABLE_AFTER_MS. The first failure starts the
  // countdown; the next ones while it runs do not restart it.
  private _startUnavailableTimer(): void {
    if (this.unavailableTimer !== null || liveUpdatesUnavailable.value) return
    this.unavailableTimer = setTimeout(() => {
      this.unavailableTimer = null
      liveUpdatesUnavailable.value = true
    }, UNAVAILABLE_AFTER_MS)
  }

  private _clearUnavailable(): void {
    if (this.unavailableTimer !== null) {
      clearTimeout(this.unavailableTimer)
      this.unavailableTimer = null
    }
    liveUpdatesUnavailable.value = false
  }

  private _startPing(): void {
    this._clearPing()
    this.pingTimer = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        // Send a lightweight ping to keep the connection alive through proxies/load balancers
        this.ws.send(JSON.stringify({ type: 'ping' }))
      }
    }, PING_INTERVAL_MS)
  }

  private _clearTimers(): void {
    this._clearPing()
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }

  private _clearPing(): void {
    if (this.pingTimer !== null) {
      clearInterval(this.pingTimer)
      this.pingTimer = null
    }
  }
}

// Singleton — one WS connection shared across the entire app
export const wsClient = new ArkeepWebSocketClient()

// ─── Composable helper ────────────────────────────────────────────────────────

// useWebSocket is a Vue composable that automatically cleans up the subscription
// on component unmount.
import { onUnmounted } from 'vue'

export function useWebSocket<T = unknown>(
  topic: string,
  handler: MessageHandler<T>,
): void {
  const unsubscribe = wsClient.subscribe<T>(topic, handler)
  onUnmounted(unsubscribe)
}