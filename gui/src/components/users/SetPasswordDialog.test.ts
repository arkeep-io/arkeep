import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { User } from '@/types'
import SetPasswordDialog from './SetPasswordDialog.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane' } as User

// openDialog mounts the dialog closed and opens it, as the users page does.
async function openDialog() {
  const { wrapper } = await mountPage(SetPasswordDialog, { props: { open: false, user } })
  await wrapper.setProps({ open: true })
  return wrapper
}

describe('SetPasswordDialog', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: {} })
  })

  it('checks the length and the confirmation before sending', async () => {
    const wrapper = await openDialog()
    await submitForm(wrapper)
    expect(wrapper.text()).toContain('Password must be at least 8 characters')

    await wrapper.find('#set-password').setValue('temporary-1')
    await wrapper.find('#set-password-confirm').setValue('temporary-2')
    await submitForm(wrapper)
    expect(wrapper.text()).toContain('Passwords do not match')
    expect(api).not.toHaveBeenCalled()
  })

  it('sets the password and requires a new one by default', async () => {
    const wrapper = await openDialog()
    await wrapper.find('#set-password').setValue('temporary-1')
    await wrapper.find('#set-password-confirm').setValue('temporary-1')
    await submitForm(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/users/u1/password', {
      method: 'POST',
      body: { password: 'temporary-1', must_change: true },
    })
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })

  it('can leave the password as final', async () => {
    const wrapper = await openDialog()
    await wrapper.find('#set-password').setValue('temporary-1')
    await wrapper.find('#set-password-confirm').setValue('temporary-1')
    wrapper.findComponent({ name: 'Switch' }).vm.$emit('update:modelValue', false)
    await submitForm(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/users/u1/password', {
      method: 'POST',
      body: { password: 'temporary-1', must_change: false },
    })
  })

  it('starts empty each time it opens', async () => {
    const wrapper = await openDialog()
    await wrapper.find('#set-password').setValue('temporary-1')
    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect((wrapper.find('#set-password').element as HTMLInputElement).value).toBe('')
  })
})
