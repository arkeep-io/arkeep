import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage, settle } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/services/api'
import type { Destination, User } from '@/types'
import DestinationDetailPage from './DestinationDetailPage.vue'

vi.mock('@/services/api', () => ({
  api: vi.fn(),
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))

const destination = {
  id: 'd1',
  name: 'usb-disk',
  type: 'local',
  config: '{"path":"/mnt/usb"}',
  enabled: true,
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  repo_size_bytes: 1024,
  repo_size_updated_at: '2026-11-01T03:00:00Z',
  repo_initialized_at: '2026-10-01T03:00:00Z',
  retention_enabled: false,
  append_only: false,
  retention_needs_review: false,
  retention_agent_id: '',
  policy_count: 1,
  locks: [],
} as unknown as Destination

// mountAs mounts the page for a signed-in user of the given role, with the
// destination the API returns.
async function mountAs(role: 'admin' | 'user', dest: Destination = destination) {
  vi.mocked(api).mockImplementation(async (url: string) => {
    if (url.includes('/jobs')) return { data: { items: [], total: 0 } }
    if (url.endsWith('/reset-repository')) return { data: { ...dest, repo_initialized_at: '' } }
    return { data: dest }
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role } as User
  const { wrapper } = await mountPage(DestinationDetailPage, { pinia, stubs: { DestinationSheet: true, RouterLink: true } })
  return wrapper
}

describe('DestinationDetailPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('lets an admin start a new repository after confirming', async () => {
    const wrapper = await mountAs('admin')

    await wrapper.find('[data-testid="reset-repository"]').trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Start a new repository?')

    await wrapper.find('[data-testid="confirm-reset-repository"]').trigger('click')
    await settle()

    expect(api).toHaveBeenCalledWith(expect.stringMatching(/\/reset-repository$/), { method: 'POST' })
    // The repository is no longer recorded, so the action has nothing left to do.
    expect(wrapper.find('[data-testid="reset-repository"]').exists()).toBe(false)
  })

  it('does not offer it before any backup reached the repository', async () => {
    const wrapper = await mountAs('admin', { ...destination, repo_initialized_at: '' })
    expect(wrapper.find('[data-testid="reset-repository"]').exists()).toBe(false)
  })

  it('does not offer it to the read-only user role', async () => {
    const wrapper = await mountAs('user')
    expect(wrapper.find('[data-testid="reset-repository"]').exists()).toBe(false)
  })
})
