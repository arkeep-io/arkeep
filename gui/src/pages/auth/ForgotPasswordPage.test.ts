import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import ForgotPasswordPage from './ForgotPasswordPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

describe('ForgotPasswordPage', () => {
  beforeEach(() => {
    // Password reset is available: the form is shown.
    vi.mocked(api).mockReset().mockResolvedValue({ data: { smtp_configured: true, base_url_configured: true } })
  })

  it('rejects an invalid email and sends no request', async () => {
    const { wrapper } = await mountPage(ForgotPasswordPage)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Enter a valid email address')
    expect(api).not.toHaveBeenCalledWith('/api/v1/auth/password-reset/request', expect.anything())
  })

  it('requests a reset link for a valid email', async () => {
    const { wrapper } = await mountPage(ForgotPasswordPage)
    await wrapper.find('#email').setValue('user@example.com')
    await submitForm(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/auth/password-reset/request', {
      method: 'POST',
      body: { email: 'user@example.com' },
    })
  })
})
