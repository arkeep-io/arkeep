import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage, settle } from '@/test/mount'
import { api } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { PinInput } from '@/components/ui/pin-input'
import type { User } from '@/types'
import TwoFactorSection from './TwoFactorSection.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))
// jsdom has no canvas to draw the QR code.
vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,') } }))

// openSection mounts the section for a local account whose two-factor
// authentication is enabled or not. verify answers the enrollment code.
async function openSection(enabled: boolean, verify: 'ok' | 'rejected' = 'ok') {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/api/v1/auth/2fa/status') return Promise.resolve({ data: { enabled, pending: false, recovery_codes_remaining: 10 } })
    if (url === '/api/v1/auth/2fa/setup') return Promise.resolve({ data: { secret: 'S', otpauth_url: 'otpauth://x' } })
    if (url === '/api/v1/auth/2fa/verify' && verify === 'rejected') return Promise.reject(new Error('400'))
    return Promise.resolve({ data: { recovery_codes: ['a', 'b'] } })
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', is_oidc: false } as User
  const { wrapper } = await mountPage(TwoFactorSection, { pinia, attachTo: document.body })
  return wrapper
}

const click = async (wrapper: Awaited<ReturnType<typeof openSection>>, label: string) => {
  await wrapper.findAll('button').find((b) => b.text() === label)!.trigger('click')
  await settle()
}

// called reports whether url was requested.
const called = (url: string) => vi.mocked(api).mock.calls.some(([u]) => u === url)

describe('TwoFactorSection', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  describe('enrollment', () => {
    // toVerifyStep starts enrollment and moves past the QR code.
    async function toVerifyStep(wrapper: Awaited<ReturnType<typeof openSection>>) {
      await click(wrapper, 'Enable two-factor authentication')
      await click(wrapper, 'Done')
    }

    it('asks for all 6 digits', async () => {
      const wrapper = await openSection(false)
      await toVerifyStep(wrapper)
      await click(wrapper, 'Verify')

      expect(wrapper.text()).toContain('Enter all 6 digits')
      expect(called('/api/v1/auth/2fa/verify')).toBe(false)
    })

    it('confirms a complete code and shows the recovery codes', async () => {
      const wrapper = await openSection(false)
      await toVerifyStep(wrapper)
      wrapper.findComponent(PinInput).vm.$emit('update:modelValue', '123456'.split(''))
      await settle()

      expect(api).toHaveBeenCalledWith('/api/v1/auth/2fa/verify', { method: 'POST', body: { code: '123456' } })
      expect(wrapper.text()).toContain('Save your recovery codes')
    })

    it('clears a rejected code and focuses the first digit', async () => {
      const wrapper = await openSection(false, 'rejected')
      await toVerifyStep(wrapper)
      wrapper.findComponent(PinInput).vm.$emit('update:modelValue', '123456'.split(''))
      await settle()

      const slots = wrapper.findAll<HTMLInputElement>('[data-slot="pin-input-slot"]')
      expect(slots.every((s) => s.element.value === '')).toBe(true)
      expect(document.activeElement).toBe(slots[0]!.element)
      expect(wrapper.find('[data-slot="field-error"]').exists()).toBe(false)
    })
  })

  describe.each([
    ['Disable', 'disable-password', '/api/v1/auth/2fa/disable'],
    ['Regenerate recovery codes', 'regenerate-password', '/api/v1/auth/2fa/recovery-codes/regenerate'],
  ])('%s dialog', (openLabel, inputId, url) => {
    const confirmLabel = openLabel === 'Disable' ? 'Disable' : 'Regenerate'

    // confirm presses the dialog's confirm button, the last with its label.
    const confirm = async (wrapper: Awaited<ReturnType<typeof openSection>>) => {
      await wrapper.findAll('button').filter((b) => b.text() === confirmLabel).at(-1)!.trigger('click')
      await settle()
    }

    it('requires the password', async () => {
      const wrapper = await openSection(true)
      await click(wrapper, openLabel)
      await confirm(wrapper)

      expect(wrapper.text()).toContain('Password is required')
      expect(called(url)).toBe(false)
    })

    it('sends the password', async () => {
      const wrapper = await openSection(true)
      await click(wrapper, openLabel)
      await wrapper.find(`#${inputId}`).setValue('my-password')
      await confirm(wrapper)

      expect(api).toHaveBeenCalledWith(url, { method: 'POST', body: { password: 'my-password' } })
    })
  })
})
