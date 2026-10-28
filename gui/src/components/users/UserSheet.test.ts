import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { User } from '@/types'
import UserSheet from './UserSheet.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role: 'user', is_active: true } as User

// openSheet mounts the sheet for user (null = new user) and resets it, as
// the users page does when it opens the sheet.
async function openSheet(forUser: User | null) {
  const { wrapper } = await mountPage(UserSheet, { props: { open: true, user: forUser } })
  ;(wrapper.vm as unknown as { reset: () => void }).reset()
  await wrapper.vm.$nextTick()
  return wrapper
}

describe('UserSheet', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: {} })
  })

  describe('create', () => {
    it('shows every missing field and sends nothing', async () => {
      const wrapper = await openSheet(null)
      await submitForm(wrapper)

      expect(wrapper.text()).toContain('Must be a valid email address')
      expect(wrapper.text()).toContain('Display name is required')
      expect(wrapper.text()).toContain('Password must be at least 8 characters')
      expect(api).not.toHaveBeenCalled()
    })

    it('creates the user with valid values', async () => {
      const wrapper = await openSheet(null)
      await wrapper.find('#user-email').setValue('new@example.com')
      await wrapper.find('#user-display-name').setValue('New User')
      await wrapper.find('#user-password').setValue('long-enough')
      await submitForm(wrapper)

      expect(api).toHaveBeenCalledWith('/api/v1/users', {
        method: 'POST',
        body: { email: 'new@example.com', display_name: 'New User', password: 'long-enough', role: 'user' },
      })
      expect(wrapper.emitted('saved')).toHaveLength(1)
    })
  })

  describe('edit', () => {
    it('requires a display name', async () => {
      const wrapper = await openSheet(user)
      await wrapper.find('#user-display-name').setValue('')
      await submitForm(wrapper)

      expect(wrapper.text()).toContain('Display name is required')
      expect(api).not.toHaveBeenCalled()
    })

    it('updates the user without asking for a password', async () => {
      const wrapper = await openSheet(user)
      await wrapper.find('#user-display-name').setValue('Jane Doe')
      await submitForm(wrapper)

      expect(api).toHaveBeenCalledWith('/api/v1/users/u1', {
        method: 'PATCH',
        body: { display_name: 'Jane Doe', role: 'user', is_active: true },
      })
    })
  })
})
