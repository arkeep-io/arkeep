import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'
import ProfilePage from './ProfilePage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

// openProfile mounts the page signed in as a local account named Jane. The
// two-factor section, which loads its own status, is stubbed.
async function openProfile() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role: 'user', is_oidc: false } as User
  const { wrapper } = await mountPage(ProfilePage, { pinia, stubs: { TwoFactorSection: true } })
  return wrapper
}

// passwordCall returns the body of the password change request, if any.
function passwordBody() {
  const call = vi.mocked(api).mock.calls.find(([, opts]) => (opts?.body as Record<string, unknown>)?.password !== undefined)
  return call?.[1]?.body
}

describe('ProfilePage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: { display_name: 'Jane Doe' } })
  })

  describe('name form', () => {
    it('prefills and requires the display name', async () => {
      const wrapper = await openProfile()
      expect((wrapper.find('#display-name').element as HTMLInputElement).value).toBe('Jane')

      await wrapper.find('#display-name').setValue('')
      await submitForm(wrapper)

      expect(wrapper.text()).toContain('Display name is required')
      expect(api).not.toHaveBeenCalled()
    })

    it('saves a new display name', async () => {
      const wrapper = await openProfile()
      await wrapper.find('#display-name').setValue('Jane Doe')
      await submitForm(wrapper)

      expect(api).toHaveBeenCalledWith('/api/v1/users/me', { method: 'PATCH', body: { display_name: 'Jane Doe' } })
      expect(useAuthStore().user?.display_name).toBe('Jane Doe')
    })
  })

  describe('password form', () => {
    // The password form is the second form of the page.
    const passwordForm = (wrapper: Awaited<ReturnType<typeof openProfile>>) => wrapper.findAll('form')[1]!

    it('shows every missing field and leaves the name form alone', async () => {
      const wrapper = await openProfile()
      await submitForm(wrapper, passwordForm(wrapper))

      expect(wrapper.text()).toContain('Current password is required')
      expect(wrapper.text()).toContain('Password must be at least 8 characters')
      expect(wrapper.text()).not.toContain('Display name is required')
      expect(passwordBody()).toBeUndefined()
    })

    it('rejects a confirmation that does not match', async () => {
      const wrapper = await openProfile()
      await wrapper.find('#current-password').setValue('old-password')
      await wrapper.find('#new-password').setValue('new-password')
      await wrapper.find('#confirm-password').setValue('different')
      await submitForm(wrapper, passwordForm(wrapper))

      expect(wrapper.text()).toContain('Passwords do not match')
      expect(passwordBody()).toBeUndefined()
    })

    it('changes the password and clears the fields', async () => {
      const wrapper = await openProfile()
      await wrapper.find('#current-password').setValue('old-password')
      await wrapper.find('#new-password').setValue('new-password')
      await wrapper.find('#confirm-password').setValue('new-password')
      await submitForm(wrapper, passwordForm(wrapper))

      expect(passwordBody()).toEqual({ password: 'new-password', current_password: 'old-password' })
      expect((wrapper.find('#new-password').element as HTMLInputElement).value).toBe('')
    })
  })
})
