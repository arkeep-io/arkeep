import { describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mountPage } from '@/test/mount'
import { useAuthStore } from '@/stores/auth'
import type { User } from '@/types'
import AppLayout from './AppLayout.vue'

// mountAs mounts the layout for a signed-in user of the given role, with the
// sidebar and the bell stubbed (they load data of their own).
async function mountAs(role: 'admin' | 'user') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthStore().user = { id: 'u1', email: 'jane@example.com', display_name: 'Jane', role } as User
  const { wrapper } = await mountPage(AppLayout, {
    pinia,
    stubs: { AppSidebar: true, NotificationBell: true, SidebarProvider: false },
  })
  return wrapper
}

describe('AppLayout', () => {
  it('shows the notification bell to administrators', async () => {
    const wrapper = await mountAs('admin')
    expect(wrapper.findComponent({ name: 'NotificationBell' }).exists()).toBe(true)
  })

  it('hides it from the read-only user role, who never gets in-app notifications', async () => {
    const wrapper = await mountAs('user')
    expect(wrapper.findComponent({ name: 'NotificationBell' }).exists()).toBe(false)
  })
})
