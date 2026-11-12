import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import TwoFactorCodeForm from '@/components/users/TwoFactorCodeForm.vue'
import PasswordChangeForm from '@/components/users/PasswordChangeForm.vue'
import LoginPage from './LoginPage.vue'

describe('LoginPage', () => {
  beforeEach(() => {
    // The page lists the OIDC providers on mount; none are configured.
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false }))
  })

  it('shows the field messages and does not log in when empty', async () => {
    const { wrapper } = await mountPage(LoginPage)
    const login = vi.spyOn(useAuthStore(), 'login')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Enter a valid email address')
    expect(wrapper.text()).toContain('Password is required')
    expect(login).not.toHaveBeenCalled()
  })

  it('logs in and goes to the dashboard with valid credentials', async () => {
    const { wrapper, router } = await mountPage(LoginPage)
    const login = vi.spyOn(useAuthStore(), 'login').mockResolvedValue({ twoFactorRequired: false })
    await wrapper.find('#email').setValue('admin@example.com')
    await wrapper.find('#password').setValue('secret')
    await submitForm(wrapper)

    expect(login).toHaveBeenCalledWith('admin@example.com', 'secret')
    expect(router.push).toHaveBeenCalledWith('/dashboard')
  })

  it('reports wrong credentials', async () => {
    const { wrapper, router } = await mountPage(LoginPage)
    vi.spyOn(useAuthStore(), 'login').mockRejectedValue(Object.assign(new Error('401'), { status: 401 }))
    await wrapper.find('#email').setValue('admin@example.com')
    await wrapper.find('#password').setValue('wrong')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Invalid email or password')
    expect(router.push).not.toHaveBeenCalled()
  })

  it('says so when there were too many failed attempts', async () => {
    const { wrapper, router } = await mountPage(LoginPage)
    vi.spyOn(useAuthStore(), 'login').mockRejectedValue(Object.assign(new Error('429'), { status: 429 }))
    await wrapper.find('#email').setValue('admin@example.com')
    await wrapper.find('#password').setValue('secret')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Too many failed attempts')
    expect(wrapper.text()).not.toContain('Invalid email or password')
    expect(router.push).not.toHaveBeenCalled()
  })

  it('moves to the code step for a two-factor account', async () => {
    const { wrapper, router } = await mountPage(LoginPage)
    vi.spyOn(useAuthStore(), 'login').mockResolvedValue({ twoFactorRequired: true, challengeToken: 'c' })
    await wrapper.find('#email').setValue('admin@example.com')
    await wrapper.find('#password').setValue('secret')
    await submitForm(wrapper)

    const codeForm = wrapper.findComponent(TwoFactorCodeForm)
    expect(codeForm.props('challengeToken')).toBe('c')
    expect(router.push).not.toHaveBeenCalled()

    codeForm.vm.$emit('verified')
    expect(router.push).toHaveBeenCalledWith('/dashboard')
  })

  it('asks for a new password when an admin required one', async () => {
    const { wrapper, router } = await mountPage(LoginPage)
    vi.spyOn(useAuthStore(), 'login').mockResolvedValue({ twoFactorRequired: false, passwordChangeRequired: true, challengeToken: 'p' })
    await wrapper.find('#email').setValue('jane@example.com')
    await wrapper.find('#password').setValue('temporary')
    await submitForm(wrapper)

    const form = wrapper.findComponent(PasswordChangeForm)
    expect(form.props('challengeToken')).toBe('p')
    expect(router.push).not.toHaveBeenCalled()

    form.vm.$emit('verified')
    expect(router.push).toHaveBeenCalledWith('/dashboard')
  })

  it('goes from the code to the new password on a two-factor account', async () => {
    const { wrapper } = await mountPage(LoginPage)
    vi.spyOn(useAuthStore(), 'login').mockResolvedValue({ twoFactorRequired: true, challengeToken: 'c' })
    await wrapper.find('#email').setValue('jane@example.com')
    await wrapper.find('#password').setValue('temporary')
    await submitForm(wrapper)

    wrapper.findComponent(TwoFactorCodeForm).vm.$emit('password-change', 'p')
    await wrapper.vm.$nextTick()
    expect(wrapper.findComponent(PasswordChangeForm).props('challengeToken')).toBe('p')
  })
})
