import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import ResetPasswordPage from './ResetPasswordPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

describe('ResetPasswordPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('rejects a short password and sends nothing', async () => {
    const { wrapper } = await mountPage(ResetPasswordPage, { path: '/auth/reset-password?token=t' })
    await wrapper.find('#password').setValue('short')
    await wrapper.find('#confirmPassword').setValue('short')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Password must be at least 8 characters')
    expect(api).not.toHaveBeenCalled()
  })

  it('rejects a confirmation that does not match', async () => {
    const { wrapper } = await mountPage(ResetPasswordPage, { path: '/auth/reset-password?token=t' })
    await wrapper.find('#password').setValue('long-enough')
    await wrapper.find('#confirmPassword').setValue('different')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Passwords do not match')
    expect(api).not.toHaveBeenCalled()
  })

  it('sends the token from the link with the new password', async () => {
    const { wrapper } = await mountPage(ResetPasswordPage, { path: '/auth/reset-password?token=t' })
    await wrapper.find('#password').setValue('long-enough')
    await wrapper.find('#confirmPassword').setValue('long-enough')
    await submitForm(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/auth/password-reset/confirm', {
      method: 'POST',
      body: { token: 't', password: 'long-enough' },
    })
  })
})
