import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import NotificationsPage from './NotificationsPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const events = { job_success: false, job_failure: true, agent_offline: true, agent_online: false, email_enabled: true }

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

  it('turns email notifications off and keeps the webhook on', async () => {
    const wrapper = await openPage({ url: 'https://hooks.example.com/arkeep', enabled: true })
    // The email switch comes right before the webhook one, the last.
    const toggle = wrapper.findAllComponents({ name: 'Switch' }).at(-2)!
    toggle.vm.$emit('update:modelValue', false)
    await wrapper.vm.$nextTick()
    await submitForm(wrapper)

    expect(putBody('/api/v1/settings/notifications')).toEqual({ ...events, email_enabled: false })
    expect(putBody('/api/v1/settings/webhook')).toEqual({
      url: 'https://hooks.example.com/arkeep', secret: '', enabled: true,
    })
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

  describe('send test webhook', () => {
    async function clickTest(wrapper: Awaited<ReturnType<typeof openPage>>, answer: unknown) {
      const base = vi.mocked(api).getMockImplementation()!
      vi.mocked(api).mockImplementation((url: string, opts?: { method?: string }) => {
        if (url === '/api/v1/settings/webhook/test') return answer instanceof Error ? Promise.reject(answer) : Promise.resolve(answer)
        return base(url, opts)
      })
      await wrapper.findAll('button').find((b) => b.text() === 'Send test webhook')!.trigger('click')
      await settle()
    }
    const testBody = () => vi.mocked(api).mock.calls.find(([u]) => u === '/api/v1/settings/webhook/test')?.[1]?.body

    it('posts the URL and secret of the form', async () => {
      const wrapper = await openPage({ url: 'https://hooks.example.com/saved', enabled: true })
      await wrapper.find('#webhook-url').setValue(' https://hooks.example.com/new ')
      await clickTest(wrapper, { data: { ok: true } })

      expect(testBody()).toEqual({ url: 'https://hooks.example.com/new', secret: '' })
      expect(wrapper.find('[data-testid="webhook-test-result"]').text()).toBe('Test webhook delivered.')
    })

    it('shows why the test failed', async () => {
      const wrapper = await openPage({ url: 'https://hooks.example.com/saved', enabled: true })
      const refusal = Object.assign(new Error('422'), { data: { error: { message: 'webhook returned non-2xx status 500' } } })
      await clickTest(wrapper, refusal)

      expect(wrapper.find('[data-testid="webhook-test-result"]').text()).toBe('webhook returned non-2xx status 500')
    })

    it('does not test an invalid URL', async () => {
      const wrapper = await openPage(null)
      await enableWebhook(wrapper)
      await wrapper.find('#webhook-url').setValue('ftp://hooks.example.com')
      await clickTest(wrapper, { data: { ok: true } })

      expect(wrapper.text()).toContain('Must be an http(s) URL')
      expect(testBody()).toBeUndefined()
    })
  })
})
