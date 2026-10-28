import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import NotificationsPage from './NotificationsPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const events = { job_success: false, job_failure: true, agent_offline: true, agent_online: false }

// openPage mounts the page with the stored events and webhook (null = none).
async function openPage(webhook: { url: string; enabled: boolean } | null) {
  vi.mocked(api).mockImplementation((url: string, opts?: { method?: string }) => {
    if (opts?.method === 'PUT') return Promise.resolve({ data: {} })
    if (url === '/api/v1/settings/notifications') return Promise.resolve({ data: events })
    return webhook ? Promise.resolve({ data: { ...webhook, secret: '***' } }) : Promise.reject({ status: 404 })
  })
  const { wrapper } = await mountPage(NotificationsPage)
  return wrapper
}

// putBody returns the body saved to url, if any.
function putBody(url: string) {
  return vi.mocked(api).mock.calls.find(([u, opts]) => u === url && opts?.method === 'PUT')?.[1]?.body
}

// enableWebhook switches the webhook on, as its toggle does.
async function enableWebhook(wrapper: Awaited<ReturnType<typeof openPage>>) {
  const toggle = wrapper.findAllComponents({ name: 'Switch' }).at(-1)!
  toggle.vm.$emit('update:modelValue', true)
  await wrapper.vm.$nextTick()
}

describe('NotificationsPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('saves the events with the webhook disabled and no URL', async () => {
    const wrapper = await openPage(null)
    await submitForm(wrapper)

    expect(putBody('/api/v1/settings/notifications')).toEqual(events)
    expect(putBody('/api/v1/settings/webhook')).toEqual({ url: '', secret: '', enabled: false })
  })

  it.each([
    ['empty', ''],
    ['not http(s)', 'ftp://hooks.example.com'],
  ])('rejects an %s URL on an enabled webhook', async (_, url) => {
    const wrapper = await openPage(null)
    await enableWebhook(wrapper)
    await wrapper.find('#webhook-url').setValue(url)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Must be an http(s) URL')
    expect(putBody('/api/v1/settings/webhook')).toBeUndefined()
  })

  it('saves an enabled webhook', async () => {
    const wrapper = await openPage({ url: 'https://hooks.example.com/arkeep', enabled: true })
    await wrapper.find('#webhook-secret').setValue('s3cret')
    await submitForm(wrapper)

    expect(putBody('/api/v1/settings/webhook')).toEqual({
      url: 'https://hooks.example.com/arkeep', secret: 's3cret', enabled: true,
    })
  })
})
