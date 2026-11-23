import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import RefreshControl from './RefreshControl.vue'
import { autoRefreshStorageKey } from '@/composables/useAutoRefresh'

beforeEach(() => vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] }))
afterEach(() => {
  vi.useRealTimers()
  localStorage.clear()
})

function mountControl(refresh = vi.fn(), loading = false) {
  const wrapper = mount(RefreshControl, { props: { page: 'jobs', refresh, loading } })
  return { wrapper, refresh }
}

describe('RefreshControl', () => {
  it('refreshes the page normally when its button is clicked', async () => {
    const { wrapper, refresh } = mountControl()
    await wrapper.get('button[aria-label="Refresh"]').trigger('click')
    expect(refresh).toHaveBeenCalledWith({ quiet: false })
  })

  it('shows auto-refresh as off by default', () => {
    const { wrapper } = mountControl()
    expect(wrapper.find('[data-test="auto-refresh-interval"]').exists()).toBe(false)
    expect(wrapper.find('button[aria-label="Auto-refresh: off"]').exists()).toBe(true)
  })

  it('shows the remembered interval and refreshes quietly at that pace', async () => {
    localStorage.setItem(autoRefreshStorageKey('jobs'), '60')
    const { wrapper, refresh } = mountControl()
    expect(wrapper.get('[data-test="auto-refresh-interval"]').text()).toBe('1m')
    expect(wrapper.find('button[aria-label="Auto-refresh: every minute"]').exists()).toBe(true)
    vi.advanceTimersByTime(60_000)
    await flushPromises()
    expect(refresh).toHaveBeenCalledWith({ quiet: true })
  })

  it('disables the button while the page loads', () => {
    const { wrapper } = mountControl(vi.fn(), true)
    expect(wrapper.get('button[aria-label="Refresh"]').attributes('disabled')).toBeDefined()
  })
})
