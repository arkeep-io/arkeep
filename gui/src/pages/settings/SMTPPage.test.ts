import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import SMTPPage from './SMTPPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const saved = {
  host: 'smtp.example.com', port: 465, tls: true, username: 'mailer',
  from: 'arkeep@example.com', from_name: 'Arkeep', recipients: ['ops@example.com'],
}

// openPage mounts the page with the stored SMTP settings, or none (404).
async function openPage(settings: typeof saved | null) {
  vi.mocked(api).mockReset().mockImplementation((_url: string, opts?: { method?: string }) => {
    if (opts?.method === 'PUT') return Promise.resolve({ data: {} })
    if (!settings) return Promise.reject({ status: 404 })
    return Promise.resolve({ data: settings })
  })
  const { wrapper } = await mountPage(SMTPPage)
  return wrapper
}

// putBody returns the body of the save request, if any.
function putBody() {
  return vi.mocked(api).mock.calls.find(([, opts]) => opts?.method === 'PUT')?.[1]?.body
}

describe('SMTPPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('requires the host and sender and sends nothing', async () => {
    const wrapper = await openPage(null)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Host is required')
    expect(wrapper.text()).toContain('Must be a valid email address')
    expect(putBody()).toBeUndefined()
  })

  it.each([
    ['empty', '', 'A value is required'],
    ['out of range', '70000', 'Must be a whole number between 1 and 65535'],
  ])('rejects an %s port', async (_, port, message) => {
    const wrapper = await openPage(saved)
    await wrapper.find('#smtp-port').setValue(port)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain(message)
    expect(putBody()).toBeUndefined()
  })

  it.each([
    ['an invalid address', 'not-an-email', 'Invalid email address'],
    ['a duplicate', 'ops@example.com', 'Already in the list'],
  ])('refuses to add %s as recipient', async (_, address, message) => {
    const wrapper = await openPage(saved)
    await wrapper.find('#smtp-recipient-input').setValue(address)
    await wrapper.find('#smtp-recipient-input').trigger('keydown', { key: 'Enter' })

    expect(wrapper.text()).toContain(message)
  })

  it('saves the stored settings with an added recipient', async () => {
    const wrapper = await openPage(saved)
    await wrapper.find('#smtp-recipient-input').setValue('dev@example.com')
    await wrapper.find('#smtp-recipient-input').trigger('keydown', { key: 'Enter' })
    await wrapper.find('#smtp-password').setValue('secret')
    await submitForm(wrapper)

    expect(putBody()).toEqual({
      host: 'smtp.example.com', port: 465, tls: true, username: 'mailer', password: 'secret',
      from: 'arkeep@example.com', from_name: 'Arkeep', recipients: ['ops@example.com', 'dev@example.com'],
    })
  })
})
