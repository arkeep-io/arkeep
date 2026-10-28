import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { OIDCProvider } from '@/types'
import OIDCPage from './OIDCPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const provider = {
  id: 'p1', name: 'Keycloak', issuer: 'https://idp.example.com/realms/main', client_id: 'arkeep',
  callback_url: 'http://localhost/api/v1/auth/oidc/callback', scopes: 'openid email profile', enabled: true,
  groups_claim: 'groups', allowed_groups: 'arkeep-users', admin_groups: '', session_max_hours: 8,
} as OIDCProvider

// openSheet mounts the page with the stored providers and opens the sheet,
// to add a provider or to edit the first one.
async function openSheet(mode: 'create' | 'edit') {
  vi.mocked(api).mockImplementation((_url: string, opts?: { method?: string }) =>
    Promise.resolve({ data: opts?.method ? {} : [provider] }))
  const { wrapper } = await mountPage(OIDCPage)
  const label = mode === 'create' ? 'Add provider' : 'Edit'
  await wrapper.findAll('button').find((b) => b.text() === label || b.attributes('aria-label') === label)!.trigger('click')
  return wrapper
}

// savedBody returns the body of the create or update request, if any.
function savedBody() {
  return vi.mocked(api).mock.calls.find(([, opts]) => opts?.method === 'POST' || opts?.method === 'PUT')?.[1]?.body
}

describe('OIDCPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('shows every missing field of a new provider and sends nothing', async () => {
    const wrapper = await openSheet('create')
    await submitForm(wrapper, 'form')

    expect(wrapper.text()).toContain('Name is required')
    expect(wrapper.text()).toContain('Must be a valid URL')
    expect(wrapper.text()).toContain('Client ID is required')
    expect(wrapper.text()).toContain('Client secret is required')
    expect(savedBody()).toBeUndefined()
  })

  it('requires a groups claim and a session duration in range', async () => {
    const wrapper = await openSheet('edit')
    await wrapper.find('#form-groups-claim').setValue('')
    await wrapper.find('#form-session-max-hours').setValue('200')
    await submitForm(wrapper, 'form')

    expect(wrapper.text()).toContain('Groups claim is required when allowed or admin groups are set')
    expect(wrapper.text()).toContain('Must be a whole number between 1 and 168')
    expect(savedBody()).toBeUndefined()
  })

  it('updates a provider keeping the stored secret', async () => {
    const wrapper = await openSheet('edit')
    await wrapper.find('#form-admin-groups').setValue('arkeep-admins')
    await submitForm(wrapper, 'form')

    expect(api).toHaveBeenCalledWith('/api/v1/settings/oidc/p1', {
      method: 'PUT',
      body: {
        name: 'Keycloak', issuer: 'https://idp.example.com/realms/main', client_id: 'arkeep',
        scopes: 'openid email profile', enabled: true, groups_claim: 'groups',
        allowed_groups: 'arkeep-users', admin_groups: 'arkeep-admins', session_max_hours: 8,
      },
    })
  })

  it('creates a provider with its secret', async () => {
    const wrapper = await openSheet('create')
    await wrapper.find('#form-name').setValue('Authentik')
    await wrapper.find('#form-issuer').setValue('https://auth.example.com')
    await wrapper.find('#form-client-id').setValue('arkeep')
    await wrapper.find('#form-client-secret').setValue('secret')
    await submitForm(wrapper, 'form')

    expect(savedBody()).toMatchObject({ name: 'Authentik', client_secret: 'secret', session_max_hours: 12 })
  })
})
