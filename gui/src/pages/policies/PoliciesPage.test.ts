import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { config } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/services/api'
import type { Policy, PolicyRecentJob, User } from '@/types'
import PoliciesPage from './PoliciesPage.vue'

vi.mock('@/services/api', () => ({
  api: vi.fn(),
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))

function run(id: string, status: PolicyRecentJob['status'], at: string): PolicyRecentJob {
  return { id, status, created_at: at, started_at: at, ended_at: null }
}

function policy(id: string, name: string, recent_jobs?: PolicyRecentJob[]): Policy {
  return {
    id, name, agent_id: 'a1', agent_name: 'host', schedule: '0 2 * * *', enabled: true,
    sources: '[]', destinations: [], created_at: '2026-10-01T00:00:00Z', recent_jobs,
  } as unknown as Policy
}

// mountWith mounts the list as a read-only user with the given policies, as
// the API returns them (recent runs newest first).
async function mountWith(policies: Policy[]) {
  vi.mocked(api).mockReset()
  vi.mocked(api).mockResolvedValue({ data: { items: policies, total: policies.length } })
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role: 'user' } as User
  return mountPage(PoliciesPage, { pinia, stubs: { PolicySheet: true, RouterLink: true, Tooltip: true, TooltipTrigger: true, TooltipContent: true } })
}

describe('PoliciesPage', () => {
  // The run dots sit in the slot of the stubbed tooltip trigger.
  beforeAll(() => { config.global.renderStubDefaultSlot = true })
  afterAll(() => { config.global.renderStubDefaultSlot = false })

  it('shows the recent runs oldest to newest, padded to five', async () => {
    const { wrapper } = await mountWith([policy('p1', 'home', [
      run('j3', 'pending', '2026-10-12T10:00:00Z'),
      run('j2', 'failed', '2026-10-11T10:00:00Z'),
      run('j1', 'succeeded', '2026-10-10T10:00:00Z'),
    ])])

    const dots = wrapper.findAll('[data-testid="run-dot"]')
    expect(dots.map((d) => d.attributes('data-status'))).toEqual(['succeeded', 'failed', 'pending'])
    expect(wrapper.findAll('[data-testid="run-dot-empty"]')).toHaveLength(2)
    expect(wrapper.find('[data-testid="last-run"]').text()).not.toBe('Never')
  })

  it('says Never and shows five empty slots for a policy that has not run', async () => {
    const { wrapper } = await mountWith([policy('p1', 'idle')])

    expect(wrapper.find('[data-testid="last-run"]').text()).toBe('Never')
    expect(wrapper.findAll('[data-testid="run-dot"]')).toHaveLength(0)
    expect(wrapper.findAll('[data-testid="run-dot-empty"]')).toHaveLength(5)
  })

  it('opens the job from a run dot and the policy from the rest of the cell', async () => {
    const { wrapper, router } = await mountWith([policy('p1', 'home', [run('j1', 'failed', '2026-10-10T10:00:00Z')])])
    const dot = wrapper.get('[data-testid="run-dot"]')

    await dot.trigger('click')
    await dot.trigger('keyup', { key: 'Enter' })
    expect(router.push).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="run-dot-empty"]').trigger('click')
    expect(router.push).toHaveBeenCalledWith('/policies/p1')
  })
})
