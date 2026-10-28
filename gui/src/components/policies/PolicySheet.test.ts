import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'
import PolicySheet from './PolicySheet.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

// The API serves one enabled destination; every other call succeeds empty.
function mockApi() {
  vi.mocked(api).mockReset().mockImplementation(async (url: string) => {
    if (url.startsWith('/api/v1/destinations?')) {
      return { data: { items: [{ id: 'd1', name: 'Dest 1', type: 'local', enabled: true }], total: 1 } }
    }
    return { data: {} }
  })
}

// openCreate mounts the sheet for a new policy, with the agent combobox
// (which loads agents from the API) stubbed.
async function openCreate() {
  const { wrapper } = await mountPage(PolicySheet, {
    props: { open: true, policy: null },
    stubs: { AsyncCombobox: true },
  })
  return wrapper
}

// createCall returns the body of the policy creation request, if any.
function createBody() {
  const call = vi.mocked(api).mock.calls.find(([url, opts]) => url === '/api/v1/policies' && opts?.method === 'POST')
  return call?.[1]?.body as Record<string, unknown> | undefined
}

describe('PolicySheet', () => {
  beforeEach(mockApi)

  it('shows every missing field of a new policy and sends nothing', async () => {
    const wrapper = await openCreate()
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Name is required')
    expect(wrapper.text()).toContain('Agent is required')
    expect(wrapper.text()).toContain('Path is required')
    expect(wrapper.text()).toContain('At least one destination is required')
    expect(wrapper.text()).toContain('Repository password must be at least 8 characters')
    expect(createBody()).toBeUndefined()
  })

  it('rejects a repository password confirmation that does not match', async () => {
    const wrapper = await openCreate()
    await wrapper.find('#repo_password').setValue('long-enough')
    await wrapper.find('#repo_password_confirm').setValue('different')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Passwords do not match')
    expect(createBody()).toBeUndefined()
  })

  it('creates the policy with valid values', async () => {
    const wrapper = await openCreate()
    await wrapper.find('#policy-name').setValue('Nightly')
    wrapper.findComponent({ name: 'AsyncCombobox' }).vm.$emit('update:model-value', 'a1')
    await wrapper.find('#source-path-0').setValue('/var/lib/data')
    const dest = wrapper.findAll('div.cursor-pointer').find((d) => d.text().includes('Dest 1'))!
    await dest.trigger('click')
    await wrapper.find('#repo_password').setValue('long-enough')
    await wrapper.find('#repo_password_confirm').setValue('long-enough')
    await submitForm(wrapper)

    const body = createBody()
    expect(body).toMatchObject({
      name: 'Nightly',
      agent_id: 'a1',
      schedule: '0 2 * * *',
      repo_password: 'long-enough',
      destinations: [{ destination_id: 'd1', priority: 1 }],
    })
    expect(JSON.parse(body!.sources as string)).toEqual([{ type: 'directory', path: '/var/lib/data', label: '' }])
  })

  it('rejects a repository password shorter than the server minimum', async () => {
    const wrapper = await openCreate()
    await wrapper.find('#repo_password').setValue('1234567')
    await wrapper.find('#repo_password_confirm').setValue('1234567')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Repository password must be at least 8 characters')
  })

  describe('docker volume source', () => {
    // asDockerVolume switches source row 0 to the docker-volume type, as the
    // type selector does. The agent has no Docker, so only the manual name
    // input is offered.
    async function asDockerVolume(wrapper: Awaited<ReturnType<typeof openCreate>>) {
      wrapper.findAllComponents({ name: 'Select' })[0]!.vm.$emit('update:modelValue', 'docker-volume')
      await wrapper.vm.$nextTick()
    }

    it('requires a ticked volume or a typed name', async () => {
      const wrapper = await openCreate()
      await asDockerVolume(wrapper)
      await submitForm(wrapper)

      expect(wrapper.text()).toContain('Select at least one volume or enter its name')
    })

    it('accepts a volume name typed by hand', async () => {
      const wrapper = await openCreate()
      await asDockerVolume(wrapper)
      await wrapper.find('#source-path-0').setValue('postgres_data')
      await submitForm(wrapper)

      expect(wrapper.text()).not.toContain('Select at least one volume or enter its name')
    })
  })

  describe('hook timeout', () => {
    // enablePreHook opens the hooks section and switches the pre-backup hook
    // on, as an admin does.
    async function enablePreHook(wrapper: Awaited<ReturnType<typeof openCreate>>) {
      useAuthStore().user = { role: 'admin' } as User
      await wrapper.findAll('button').find((b) => b.text().startsWith('Hooks'))!.trigger('click')
      const preHook = wrapper.findAllComponents({ name: 'Switch' })
        .find((s) => s.element.parentElement?.textContent?.includes('Pre-backup'))!
      preHook.vm.$emit('update:modelValue', true)
      await wrapper.vm.$nextTick()
    }

    it.each([
      ['empty', '', 'A value is required'],
      ['above 24 hours', '86401', 'Must be a whole number between 0 and 86400'],
    ])('rejects an %s timeout', async (_, value, message) => {
      const wrapper = await openCreate()
      await enablePreHook(wrapper)
      await wrapper.find('#hook-pre-timeout').setValue(value)
      await submitForm(wrapper)

      expect(wrapper.text()).toContain(message)
    })

    it('ignores the timeout of a disabled hook', async () => {
      const wrapper = await openCreate()
      await enablePreHook(wrapper)
      await wrapper.find('#hook-pre-timeout').setValue('')
      const preHook = wrapper.findAllComponents({ name: 'Switch' })
        .find((s) => s.element.parentElement?.textContent?.includes('Pre-backup'))!
      preHook.vm.$emit('update:modelValue', false)
      await submitForm(wrapper)

      expect(wrapper.text()).not.toContain('A value is required')
    })
  })
})
