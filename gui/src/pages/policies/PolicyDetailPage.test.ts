import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/services/api'
import type { Policy, User } from '@/types'
import PolicyDetailPage from './PolicyDetailPage.vue'

vi.mock('@/services/api', () => ({
  api: vi.fn(),
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))

// mountWithSources mounts the page as a read-only user for a policy with the
// given sources JSON, as the API returns it to that role.
async function mountWithSources(sources: string) {
  const policy = {
    id: 'p1', name: 'db-backup', agent_id: 'a1', agent_name: 'host', schedule: '0 3 * * *', enabled: true,
    sources, hook_pre_backup: '', hook_post_backup: '', exclude_patterns: '[]', healthcheck_url: '',
    destinations: [], created_at: '2026-10-01T00:00:00Z',
  } as unknown as Policy
  vi.mocked(api).mockImplementation(async (url: string) =>
    url.includes('/jobs') ? { data: { items: [], total: 0 } } : { data: policy })
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role: 'user' } as User
  const { wrapper } = await mountPage(PolicyDetailPage, { pinia, stubs: { PolicySheet: true, RouterLink: true } })
  return wrapper
}

describe('PolicyDetailPage', () => {
  it('says a command source command is hidden when the server withholds it', async () => {
    const wrapper = await mountWithSources('[{"type":"command","path":"","label":"app-db"},{"type":"directory","path":"/data"}]')
    expect(wrapper.find('[data-testid="command-hidden"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('app-db')
    expect(wrapper.text()).toContain('/data')
  })
})
