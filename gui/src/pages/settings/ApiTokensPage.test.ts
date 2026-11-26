import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle } from '@/test/mount'
import { api } from '@/services/api'
import type { ApiToken } from '@/types'
import ApiTokensPage from './ApiTokensPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const token: ApiToken = {
  id: 't1',
  name: 'Home Assistant',
  hint: 'ark_Ab3x',
  scope: 'read',
  expires_at: '2020-01-01T00:00:00Z',
  last_used_at: '2026-11-23T10:00:00Z',
  created_at: '2019-11-23T10:00:00Z',
  user_id: 'u1',
  user_email: 'alice@example.com',
}

describe('ApiTokensPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
    vi.mocked(api).mockImplementation((url: string) =>
      Promise.resolve(url === '/api/v1/api-tokens' ? { data: [token] } : undefined))
  })

  it("lists every user's tokens and revokes one", async () => {
    const { wrapper } = await mountPage(ApiTokensPage, { attachTo: document.body })

    expect(wrapper.text()).toContain('alice@example.com')
    expect(wrapper.text()).toContain('ark_Ab3x…')
    expect(wrapper.text()).toContain('Expired')

    await wrapper.findAll('button').find((b) => b.text() === 'Revoke')!.trigger('click')
    await settle()
    expect(wrapper.text()).toContain('of alice@example.com')
    await wrapper.findAll('button').filter((b) => b.text() === 'Revoke').at(-1)!.trigger('click')
    await settle()

    expect(api).toHaveBeenCalledWith('/api/v1/api-tokens/t1', { method: 'DELETE' })
  })
})
