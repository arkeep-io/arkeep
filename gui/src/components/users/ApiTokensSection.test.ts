import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle } from '@/test/mount'
import { api } from '@/services/api'
import type { ApiToken } from '@/types'
import ApiTokensSection from './ApiTokensSection.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const existing: ApiToken = {
  id: 't1',
  name: 'Home Assistant',
  hint: 'ark_Ab3x',
  scope: 'read',
  expires_at: null,
  last_used_at: null,
  created_at: '2026-11-23T10:00:00Z',
}

// openSection mounts the section with the given tokens; creating a token
// answers with a new one.
async function openSection(tokens: ApiToken[]) {
  vi.mocked(api).mockImplementation((url: string, opts?: any) => {
    if (url === '/api/v1/users/me/api-tokens' && opts?.method === 'POST') {
      return Promise.resolve({ data: { ...existing, id: 't2', name: opts.body.name, token: 'ark_secret-token-value' } })
    }
    if (url === '/api/v1/users/me/api-tokens') return Promise.resolve({ data: tokens })
    return Promise.resolve(undefined)
  })
  const { wrapper } = await mountPage(ApiTokensSection, { attachTo: document.body })
  return wrapper
}

const click = async (wrapper: Awaited<ReturnType<typeof openSection>>, label: string) => {
  await wrapper.findAll('button').filter((b) => b.text() === label).at(-1)!.trigger('click')
  await settle()
}

describe('ApiTokensSection', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('lists the tokens without their value', async () => {
    const wrapper = await openSection([existing])

    expect(wrapper.text()).toContain('Home Assistant')
    expect(wrapper.text()).toContain('ark_Ab3x…')
    expect(wrapper.text()).toContain('No expiry')
    expect(wrapper.text()).toContain('Last used never')
  })

  it('requires a name', async () => {
    const wrapper = await openSection([])
    await click(wrapper, 'Create token')
    await click(wrapper, 'Create')

    expect(wrapper.text()).toContain('Name is required')
    expect(vi.mocked(api).mock.calls.some(([, o]) => (o as any)?.method === 'POST')).toBe(false)
  })

  it('creates a token expiring in a year and shows it once', async () => {
    const wrapper = await openSection([])
    await click(wrapper, 'Create token')
    await wrapper.find('#api-token-name').setValue('  HA  ')
    await click(wrapper, 'Create')

    expect(api).toHaveBeenCalledWith('/api/v1/users/me/api-tokens', {
      method: 'POST',
      body: { name: 'HA', expires_in_days: 365 },
    })
    expect(wrapper.find('[data-testid="created-api-token"]').text()).toBe('ark_secret-token-value')

    await click(wrapper, 'Done')
    expect(wrapper.find('[data-testid="created-api-token"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('ark_secret-token-value')
  })

  it('revokes a token after confirmation', async () => {
    const wrapper = await openSection([existing])
    await click(wrapper, 'Revoke')
    expect(wrapper.text()).toContain('Revoke API token?')
    await click(wrapper, 'Revoke')

    expect(api).toHaveBeenCalledWith('/api/v1/users/me/api-tokens/t1', { method: 'DELETE' })
  })
})
