// websocket.ts — WebSocket client with automatic reconnection and topic pub/sub.
//
// The server sends messages shaped as { type: string, topic: string, payload: any }.
// Consumers subscribe to named topics; the client handles connection lifecycle
// transparently. A single WebSocket connection is shared across all subscribers.

import { useAuthStore } from '@/stores/auth'

// ─── Types ────────────────────────────────────────────────────────────────────

export type MessageType =
  | 'job.status'
  | 'job.log'
  | 'agent.status'
  | 'agent.metrics'
  | 'notification'
  | 'ping'

export interface WSMessage<T = unknown> {
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

// ─── WebSocket Client ─────────────────────────────────────────────────────────

class ArkeepWebSocketClient {
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
    this.ws?.close()
    this.ws = null
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
      // Filter out the notifications topic — the server adds it automatically
      (t) => !t.startsWith('notifications:'),
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

    try {
      this.openedTopics = this._topicsParam()
      this.ws = new WebSocket(this._buildURL())
    } catch {
      this._scheduleReconnect()
      return
    }

    this.ws.onopen = () => {
      console.debug('[WS] Connected')
      this.backoffMs = INITIAL_BACKOFF_MS
      this._startPing()
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

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this._open()
    }, delay)

    // Exponential backoff capped at MAX_BACKOFF_MS
    this.backoffMs = Math.min(this.backoffMs * BACKOFF_MULTIPLIER, MAX_BACKOFF_MS)
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