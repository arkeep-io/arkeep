import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import SetupPage from './SetupPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

describe('SetupPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('shows every required field and sends nothing when empty', async () => {
    const { wrapper } = await mountPage(SetupPage)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Name is required')
    expect(wrapper.text()).toContain('Enter a valid email address')
    expect(wrapper.text()).toContain('Password must be at least 8 characters')
    expect(wrapper.text()).toContain('Please confirm your password')
    expect(api).not.toHaveBeenCalled()
  })

  it('rejects a confirmation that does not match', async () => {
    const { wrapper } = await mountPage(SetupPage)
    await wrapper.find('#name').setValue('Admin')
    await wrapper.find('#email').setValue('admin@example.com')
    await wrapper.find('#password').setValue('long-enough')
    await wrapper.find('#confirmPassword').setValue('different')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Passwords do not match')
    expect(api).not.toHaveBeenCalled()
  })

  it('creates the first admin with valid values', async () => {
    const { wrapper } = await mountPage(SetupPage)
    await wrapper.find('#name').setValue('Admin')
    await wrapper.find('#email').setValue('admin@example.com')
    await wrapper.find('#password').setValue('long-enough')
    await wrapper.find('#confirmPassword').setValue('long-enough')
    await submitForm(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/setup/complete', {
      method: 'POST',
      body: { name: 'Admin', email: 'admin@example.com', password: 'long-enough' },
    })
  })
})
