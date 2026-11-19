import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { NotificationProvider } from '@/types'
import NotificationProviders from './NotificationProviders.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const LIST = '/api/v1/settings/notification-providers'

const gotify: NotificationProvider = {
  id: 'p1',
  name: 'Home Gotify',
  type: 'gotify',
  enabled: true,
  config: { url: 'https://gotify.example.com' },
  secrets_set: ['token'],
  priorities: { job_success: '0', job_failure: '8', agent_offline: '', agent_online: '' },
  created_at: '2026-11-16T00:00:00Z',
  updated_at: '2026-11-16T00:00:00Z',
}

// open mounts the component with the stored providers; test answers the
// test endpoint.
async function open(stored: NotificationProvider[], test: unknown = { data: { ok: true } }) {
  vi.mocked(api).mockImplementation((url: string, opts?: { method?: string }) => {
    if (url === `${LIST}/test`) return test instanceof Error ? Promise.reject(test) : Promise.resolve(test)
    if (opts?.method) return Promise.resolve({ data: {} })
    return Promise.resolve({ data: stored })
  })
  const { wrapper } = await mountPage(NotificationProviders)
  return wrapper
}

function callBody(url: string, method: string) {
  return vi.mocked(api).mock.calls.find(([u, opts]) => u === url && opts?.method === method)?.[1]?.body
}

function clickButton(wrapper: Awaited<ReturnType<typeof open>>, text: string) {
  return wrapper.findAll('button').find((b) => b.text().includes(text))!.trigger('click')
}

describe('NotificationProviders', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('lists the stored providers', async () => {
    const wrapper = await open([gotify])
    const rows = wrapper.findAll('[data-testid="notification-provider"]')
    expect(rows).toHaveLength(1)
    expect(rows[0]!.text()).toContain('Home Gotify')
    expect(rows[0]!.text()).toContain('https://gotify.example.com')
  })

  it('lists the stored providers alphabetically by name', async () => {
    const named = (id: string, name: string): NotificationProvider => ({ ...gotify, id, name })
    const wrapper = await open([named('p1', 'ops'), named('p2', 'Backups'), named('p3', 'home')])
    const names = wrapper.findAll('[data-testid="notification-provider"]').map((r) => r.find('.font-medium').text())
    expect(names).toEqual(['Backups', 'home', 'ops'])
  })

  it('lists the services alphabetically, with the shoutrrr URL last', async () => {
    const wrapper = await open([])
    await clickButton(wrapper, 'Add provider')
    const types = wrapper.findAll('[data-testid^="provider-type-"]').map((b) => b.attributes('data-testid')!.slice('provider-type-'.length))
    expect(types).toEqual([
      'discord', 'googlechat', 'gotify', 'matrix', 'mattermost', 'teams', 'ntfy', 'pushover', 'slack', 'telegram', 'custom',
    ])
  })

  it('adds a Gotify provider from the service picker', async () => {
    const wrapper = await open([])
    await clickButton(wrapper, 'Add provider')
    await wrapper.find('[data-testid="provider-type-gotify"]').trigger('click')
    await wrapper.find('#provider-field-url').setValue('https://gotify.example.com')
    await wrapper.find('#provider-field-token').setValue('AbCdEfGhIjKlMnO')
    await wrapper.find('#provider-priority-job_failure').setValue('8')
    await submitForm(wrapper)

    expect(callBody(LIST, 'POST')).toEqual({
      name: 'Gotify',
      type: 'gotify',
      enabled: true,
      fields: { url: 'https://gotify.example.com', token: 'AbCdEfGhIjKlMnO' },
      priorities: { job_success: '', job_failure: '8', agent_offline: '', agent_online: '' },
      clear_secrets: [],
    })
  })

  it('requires the fields of the service', async () => {
    const wrapper = await open([])
    await clickButton(wrapper, 'Add provider')
    await wrapper.find('[data-testid="provider-type-telegram"]').trigger('click')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Bot token is required')
    expect(wrapper.text()).toContain('Chat IDs is required')
    expect(callBody(LIST, 'POST')).toBeUndefined()
  })

  it('sends no priority for a service without one', async () => {
    const wrapper = await open([])
    await clickButton(wrapper, 'Add provider')
    await wrapper.find('[data-testid="provider-type-discord"]').trigger('click')
    expect(wrapper.find('#provider-priority-job_failure').exists()).toBe(false)
    await wrapper.find('#provider-field-webhook_url').setValue('https://discord.com/api/webhooks/1/tok')
    await submitForm(wrapper)

    expect(callBody(LIST, 'POST')).toMatchObject({
      type: 'discord',
      fields: { webhook_url: 'https://discord.com/api/webhooks/1/tok' },
      priorities: { job_success: '', job_failure: '', agent_offline: '', agent_online: '' },
    })
  })

  it('edits a provider keeping the stored secret', async () => {
    const wrapper = await open([gotify])
    await wrapper.find('button[aria-label="Edit Home Gotify"]').trigger('click')
    await settle()
    expect((wrapper.find('#provider-field-url').element as HTMLInputElement).value).toBe('https://gotify.example.com')
    await submitForm(wrapper)

    expect(callBody(`${LIST}/p1`, 'PUT')).toEqual({
      name: 'Home Gotify',
      type: 'gotify',
      enabled: true,
      fields: { url: 'https://gotify.example.com', token: '' },
      priorities: { job_success: '0', job_failure: '8', agent_offline: '', agent_online: '' },
      clear_secrets: [],
    })
  })

  it('removes a saved optional secret', async () => {
    const ntfy: NotificationProvider = {
      ...gotify, id: 'p2', name: 'ntfy', type: 'ntfy',
      config: { url: 'https://ntfy.sh' }, secrets_set: ['token', 'topic'],
    }
    const wrapper = await open([ntfy])
    await wrapper.find('button[aria-label="Edit ntfy"]').trigger('click')
    await settle()
    await clickButton(wrapper, 'Remove the saved access token')
    await submitForm(wrapper)

    expect(callBody(`${LIST}/p2`, 'PUT')).toMatchObject({ clear_secrets: ['token'] })
  })

  it('tests a saved provider with its id and shows the outcome', async () => {
    const refusal = Object.assign(new Error('422'), { data: { error: { message: 'gotify: invalid token' } } })
    const wrapper = await open([gotify], refusal)
    await wrapper.find('button[aria-label="Edit Home Gotify"]').trigger('click')
    await settle()
    await clickButton(wrapper, 'Send test notification')
    await settle()

    expect(callBody(`${LIST}/test`, 'POST')).toMatchObject({ id: 'p1', type: 'gotify' })
    expect(wrapper.find('[data-testid="provider-test-result"]').text()).toBe('gotify: invalid token')
  })

  it('deletes a provider after confirmation', async () => {
    const wrapper = await open([gotify])
    await wrapper.find('button[aria-label="Delete Home Gotify"]').trigger('click')
    await settle()
    await wrapper.findAll('button').filter((b) => b.text() === 'Delete').at(-1)!.trigger('click')
    await settle()

    expect(vi.mocked(api).mock.calls.some(([u, o]) => u === `${LIST}/p1` && o?.method === 'DELETE')).toBe(true)
  })
})
