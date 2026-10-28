import { describe, expect, it, vi } from 'vitest'
import { mountPage, settle, submitForm } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import { PinInput } from '@/components/ui/pin-input'
import TwoFactorCodeForm from './TwoFactorCodeForm.vue'

// mountForm mounts the form for challenge "c" with completeTwoFactor stubbed.
async function mountForm(outcome: 'ok' | 'rejected' = 'ok') {
  // Attached to the document so focus can be asserted.
  const { wrapper } = await mountPage(TwoFactorCodeForm, { props: { challengeToken: 'c' }, attachTo: document.body })
  const complete = vi.spyOn(useAuthStore(), 'completeTwoFactor')
  if (outcome === 'ok') complete.mockResolvedValue()
  else complete.mockRejectedValue(new Error('400'))
  return { wrapper, complete }
}

const typePin = async (wrapper: Awaited<ReturnType<typeof mountForm>>['wrapper'], digits: string) => {
  wrapper.findComponent(PinInput).vm.$emit('update:modelValue', digits.split(''))
  await wrapper.vm.$nextTick()
}

const useRecoveryCode = (wrapper: Awaited<ReturnType<typeof mountForm>>['wrapper']) =>
  wrapper.findAll('button').find((b) => b.text() === 'Use a recovery code instead')!.trigger('click')

describe('TwoFactorCodeForm', () => {
  it('asks for the code when nothing was entered', async () => {
    const { wrapper, complete } = await mountForm()
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Enter the 6-digit code')
    expect(complete).not.toHaveBeenCalled()
  })

  it('asks for all 6 digits of a partial code', async () => {
    const { wrapper, complete } = await mountForm()
    await typePin(wrapper, '123')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Enter all 6 digits')
    expect(complete).not.toHaveBeenCalled()
  })

  it('completes the login as soon as the code is complete', async () => {
    const { wrapper, complete } = await mountForm()
    await typePin(wrapper, '123456')
    await settle()

    expect(complete).toHaveBeenCalledWith('c', '123456')
    expect(wrapper.emitted('verified')).toHaveLength(1)
    // The form stays busy while the parent navigates away.
    const submit = wrapper.find('button[type=submit]')
    expect(submit.attributes('disabled')).toBeDefined()
    expect(submit.text()).toBe('Verifying…')
  })

  it('asks for the recovery code when empty', async () => {
    const { wrapper, complete } = await mountForm()
    await useRecoveryCode(wrapper)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Enter your recovery code')
    expect(complete).not.toHaveBeenCalled()
  })

  it('completes the login with a trimmed recovery code', async () => {
    const { wrapper, complete } = await mountForm()
    await useRecoveryCode(wrapper)
    await wrapper.find('#recovery-code').setValue('  AAAA-BBBB  ')
    await submitForm(wrapper)

    expect(complete).toHaveBeenCalledWith('c', 'AAAA-BBBB')
  })

  it('shows the server refusal and stays on the form', async () => {
    const { wrapper } = await mountForm('rejected')
    await typePin(wrapper, '123456')
    await settle()

    expect(wrapper.text()).toContain('Invalid or expired code')
    expect(wrapper.emitted('verified')).toBeUndefined()
    expect(wrapper.find('[data-slot="field-error"]').exists()).toBe(false)
    // The rejected code is cleared and the cursor is back on the first digit.
    const slots = wrapper.findAll<HTMLInputElement>('[data-slot="pin-input-slot"]')
    expect(slots.every((s) => s.element.value === '')).toBe(true)
    expect(document.activeElement).toBe(slots[0]!.element)
    expect(wrapper.find('button[type=submit]').attributes('disabled')).toBeUndefined()
  })
})
