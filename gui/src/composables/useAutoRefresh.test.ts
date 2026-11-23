import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { autoRefreshStorageKey, useAutoRefresh } from './useAutoRefresh'

// Only the interval timer is faked: flushPromises relies on the real setTimeout.
beforeEach(() => vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] }))
afterEach(() => {
  vi.useRealTimers()
  localStorage.clear()
  setVisibility('visible')
})

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
  document.dispatchEvent(new Event('visibilitychange'))
}

function mountAutoRefresh(refresh: () => unknown, busy = ref(false)) {
  let api!: ReturnType<typeof useAutoRefresh>
  const wrapper = mount(defineComponent({
    setup() {
      api = useAutoRefresh('agents', refresh, { busy })
      return () => h('div')
    },
  }))
  return { wrapper, api }
}

async function elapse(ms: number) {
  vi.advanceTimersByTime(ms)
  await flushPromises()
}

describe('useAutoRefresh', () => {
  it('is off until an interval is chosen', async () => {
    const refresh = vi.fn()
    const { api } = mountAutoRefresh(refresh)
    expect(api.interval.value).toBe(0)
    await elapse(600_000)
    expect(refresh).not.toHaveBeenCalled()
  })

  it('refreshes at the chosen interval and remembers it for the page', async () => {
    const refresh = vi.fn()
    const { api } = mountAutoRefresh(refresh)
    api.interval.value = 5
    await nextTick()
    expect(localStorage.getItem(autoRefreshStorageKey('agents'))).toBe('5')
    await elapse(5_000)
    expect(refresh).toHaveBeenCalledTimes(1)
    await elapse(5_000)
    await elapse(5_000)
    expect(refresh).toHaveBeenCalledTimes(3)
  })

  it('starts from the interval remembered for the page', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '10')
    const refresh = vi.fn()
    const { api } = mountAutoRefresh(refresh)
    expect(api.enabled.value).toBe(true)
    await elapse(10_000)
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it.each(['7', '-5', 'abc'])('treats the remembered value %s as off', async (value) => {
    localStorage.setItem(autoRefreshStorageKey('agents'), value)
    const refresh = vi.fn()
    const { api } = mountAutoRefresh(refresh)
    expect(api.interval.value).toBe(0)
    await elapse(600_000)
    expect(refresh).not.toHaveBeenCalled()
  })

  it('stops when turned off', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '5')
    const refresh = vi.fn()
    const { api } = mountAutoRefresh(refresh)
    api.interval.value = 0
    await nextTick()
    await elapse(60_000)
    expect(refresh).not.toHaveBeenCalled()
  })

  it('never starts a refresh while the previous one runs', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '5')
    let finish!: () => void
    const refresh = vi.fn(() => new Promise<void>((resolve) => { finish = resolve }))
    const { api } = mountAutoRefresh(refresh)
    await elapse(5_000)
    expect(api.refreshing.value).toBe(true)
    await elapse(15_000)
    expect(refresh).toHaveBeenCalledTimes(1)
    finish()
    await flushPromises()
    expect(api.refreshing.value).toBe(false)
    await elapse(5_000)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('skips its tick while the page loads on its own', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '5')
    const refresh = vi.fn()
    const busy = ref(true)
    mountAutoRefresh(refresh, busy)
    await elapse(5_000)
    expect(refresh).not.toHaveBeenCalled()
    busy.value = false
    await elapse(5_000)
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('keeps going after a failed refresh', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '5')
    const refresh = vi.fn().mockRejectedValueOnce(new Error('offline'))
    const { api } = mountAutoRefresh(refresh)
    await elapse(5_000)
    expect(api.refreshing.value).toBe(false)
    await elapse(5_000)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('pauses while the tab is hidden and refreshes when it is shown again', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '5')
    const refresh = vi.fn()
    mountAutoRefresh(refresh)
    setVisibility('hidden')
    await flushPromises()
    await elapse(60_000)
    expect(refresh).not.toHaveBeenCalled()
    setVisibility('visible')
    await flushPromises()
    expect(refresh).toHaveBeenCalledTimes(1)
    await elapse(5_000)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('stops when the page is left', async () => {
    localStorage.setItem(autoRefreshStorageKey('agents'), '5')
    const refresh = vi.fn()
    const { wrapper } = mountAutoRefresh(refresh)
    wrapper.unmount()
    await elapse(60_000)
    expect(refresh).not.toHaveBeenCalled()
  })
})
