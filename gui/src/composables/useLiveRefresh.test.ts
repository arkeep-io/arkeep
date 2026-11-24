import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { useLiveRefresh, type ChangeFilter, LIVE_REFRESH_DELAY_MS } from './useLiveRefresh'

// The WebSocket client is replaced: tests deliver change messages and
// reconnections by hand.
const ws = vi.hoisted(() => ({
  handlers: new Set<(msg: unknown) => void>(),
  reconnect: new Set<() => void>(),
}))
vi.mock('@/services/websocket', async () => {
  const { ref } = await import('vue')
  return {
    CHANGES_TOPIC: 'changes',
    liveUpdatesUnavailable: ref(false),
    wsClient: {
      subscribe: (topic: string, handler: (msg: unknown) => void) => {
        if (topic === 'changes') ws.handlers.add(handler)
        return () => ws.handlers.delete(handler)
      },
      onReconnect: (listener: () => void) => {
        ws.reconnect.add(listener)
        return () => ws.reconnect.delete(listener)
      },
    },
  }
})

function change(type: string, id = '') {
  for (const handler of ws.handlers) handler({ type: 'change', topic: 'changes', payload: { type, id } })
}

function reconnect() {
  for (const listener of ws.reconnect) listener()
}

// withPage mounts a component that uses useLiveRefresh with refresh.
function withPage(concerns: ChangeFilter, refresh: (o: { quiet?: boolean }) => unknown) {
  return mount(defineComponent({
    setup() {
      useLiveRefresh(concerns, refresh)
      return () => h('div')
    },
  }))
}

describe('useLiveRefresh', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    ws.handlers.clear()
    ws.reconnect.clear()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('refreshes quietly, once, after a burst of changes of its type', async () => {
    const refresh = vi.fn()
    withPage('jobs', refresh)

    change('jobs', 'a')
    change('jobs', 'b')
    change('jobs')
    expect(refresh).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(refresh).toHaveBeenCalledWith({ quiet: true })
  })

  it('ignores the changes it does not follow', async () => {
    const refresh = vi.fn()
    withPage((c) => c.type === 'jobs' && (c.id === 'mine' || c.id === ''), refresh)

    change('policies', 'mine')
    change('jobs', 'other')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).not.toHaveBeenCalled()

    change('jobs', 'mine')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    change('jobs')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('refreshes once more when a change arrives during a refresh', async () => {
    let finish!: () => void
    const refresh = vi.fn(() => new Promise<void>((resolve) => { finish = resolve }))
    withPage('jobs', refresh)

    change('jobs', 'a')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).toHaveBeenCalledTimes(1)

    change('jobs', 'b')
    change('jobs', 'c')
    finish()
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('refreshes after a reconnect, debounced with the changes', async () => {
    const refresh = vi.fn()
    withPage('jobs', refresh)

    reconnect()
    change('jobs', 'a')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('keeps going after a failed refresh', async () => {
    const refresh = vi.fn().mockRejectedValueOnce(new Error('down')).mockResolvedValue(undefined)
    withPage('jobs', refresh)

    change('jobs')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    change('jobs')
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('stops when the page is left', async () => {
    const refresh = vi.fn()
    const page = withPage('jobs', refresh)

    change('jobs')
    page.unmount()
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)
    change('jobs')
    reconnect()
    await vi.advanceTimersByTimeAsync(LIVE_REFRESH_DELAY_MS)

    expect(refresh).not.toHaveBeenCalled()
    expect(ws.handlers.size).toBe(0)
    expect(ws.reconnect.size).toBe(0)
  })
})
