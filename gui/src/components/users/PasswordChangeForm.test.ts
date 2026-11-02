import { describe, expect, it, vi } from 'vitest'
import { mountPage, settle, submitForm } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import PasswordChangeForm from './PasswordChangeForm.vue'

// mountForm mounts the form for challenge "p" with completePasswordChange stubbed.
async function mountForm(outcome: 'ok' | Error = 'ok') {
  const { wrapper } = await mountPage(PasswordChangeForm, { props: { challengeToken: 'p' } })
  const complete = vi.spyOn(useAuthStore(), 'completePasswordChange')
  if (outcome === 'ok') complete.mockResolvedValue()
  else complete.mockRejectedValue(outcome)
  return { wrapper, complete }
}

describe('PasswordChangeForm', () => {
  it('checks the length and the confirmation before sending', async () => {
    const { wrapper, complete } = await mountForm()
    await wrapper.find('#new-password').setValue('short')
    await submitForm(wrapper)
    expect(wrapper.text()).toContain('Password must be at least 8 characters')

    await wrapper.find('#new-password').setValue('long-enough-1')
    await wrapper.find('#confirm-password').setValue('long-enough-2')
    await submitForm(wrapper)
    expect(wrapper.text()).toContain('Passwords do not match')
    expect(complete).not.toHaveBeenCalled()
  })

  it('sets the new password and completes the login', async () => {
    const { wrapper, complete } = await mountForm()
    await wrapper.find('#new-password').setValue('long-enough-1')
    await wrapper.find('#confirm-password').setValue('long-enough-1')
    await submitForm(wrapper)

    expect(complete).toHaveBeenCalledWith('p', 'long-enough-1')
    expect(wrapper.emitted('verified')).toHaveLength(1)
  })

  it('shows the server refusal and stays on the form', async () => {
    const refusal = Object.assign(new Error('400'), {
      data: { error: { message: 'choose a password different from the one you were given' } },
    })
    const { wrapper } = await mountForm(refusal)
    await wrapper.find('#new-password').setValue('long-enough-1')
    await wrapper.find('#confirm-password').setValue('long-enough-1')
    await submitForm(wrapper)
    await settle()

    expect(wrapper.text()).toContain('choose a password different from the one you were given')
    expect(wrapper.emitted('verified')).toBeUndefined()
    expect(wrapper.find('button[type=submit]').attributes('disabled')).toBeUndefined()
  })
})
