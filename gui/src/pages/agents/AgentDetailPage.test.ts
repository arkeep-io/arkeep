import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/services/api'
import type { Agent, User } from '@/types'
import AgentDetailPage from './AgentDetailPage.vue'

vi.mock('@/services/api', () => ({
  api: vi.fn(),
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))

vi.mock('@/services/websocket', () => ({
  wsClient: { subscribe: () => () => {} },
}))

const agent: Agent = {
  id: 'a1',
  name: 'nas',
  hostname: 'nas',
  os: 'linux',
  arch: 'amd64',
  status: 'online',
  version: '0.13.0',
  restic_version: '0.19.1',
  rclone_version: '1.76.0',
  docker_available: false,
  identity_bound: true,
  last_seen_at: '2026-11-23T09:00:00Z',
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-11-23T09:00:00Z',
}

// mountWith mounts the page for an admin, with the agent the API returns.
async function mountWith(a: Agent) {
  vi.mocked(api).mockImplementation(async (url: string) => {
    if (url.includes('/jobs')) return { data: { items: [], total: 0 } }
    return { data: a }
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role: 'admin' } as User
  const { wrapper } = await mountPage(AgentDetailPage, {
    pinia,
    stubs: { VisXYContainer: true, VisArea: true, VisAxis: true, AgentSheet: true, AgentDeleteDialog: true, RouterLink: true },
  })
  return wrapper
}

describe('AgentDetailPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('shows the versions of the embedded restic and rclone next to the agent version', async () => {
    const wrapper = await mountWith(agent)
    const indicator = wrapper.find('[data-testid="agent-tools-indicator"]')
    expect(indicator.exists()).toBe(true)
    expect(indicator.attributes('aria-label')).toBe('Embedded restic 0.19.1, rclone 1.76.0')
  })

  it('shows no indicator for an agent that does not report them', async () => {
    const wrapper = await mountWith({ ...agent, restic_version: '', rclone_version: '' })
    expect(wrapper.text()).toContain('0.13.0')
    expect(wrapper.find('[data-testid="agent-tools-indicator"]').exists()).toBe(false)
  })
})
