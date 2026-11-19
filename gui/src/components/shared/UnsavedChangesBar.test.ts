import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { defineComponent, h, ref } from 'vue'
import { settle } from '@/test/mount'
import UnsavedChangesBar from './UnsavedChangesBar.vue'

// InPlaceTeleport renders the dialog where it is declared, inside the wrapper.
const InPlaceTeleport = {
  setup: (_: unknown, { slots }: { slots: { default?: () => unknown } }) => () => h('div', slots.default?.() as never),
}

// open mounts a settings page holding the bar, with changes or not, behind a
// RouterView, which the leave guard needs.
async function open(dirty: boolean) {
  const changed = ref(dirty)
  const Page = defineComponent({
    setup: () => () => h(UnsavedChangesBar, { dirty: changed.value, saving: false }),
  })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', component: Page },
      { path: '/other', component: { render: () => h('p', 'other page') } },
    ],
  })
  await router.push('/settings')
  await router.isReady()
  const wrapper = mount(RouterView, { global: { plugins: [router], stubs: { teleport: InPlaceTeleport } } })
  await flushPromises()
  return { wrapper, router, changed }
}

function clickButton(wrapper: Awaited<ReturnType<typeof open>>['wrapper'], text: string) {
  return wrapper.findAll('button').find((b) => b.text() === text)!.trigger('click')
}

describe('UnsavedChangesBar', () => {
  it('lets the page go when nothing changed', async () => {
    const { wrapper, router } = await open(false)
    await router.push('/other')
    await settle()

    expect(router.currentRoute.value.path).toBe('/other')
    expect(wrapper.text()).not.toContain('Leave without saving?')
  })

  it('asks before leaving with changes, and stays on Stay', async () => {
    const { wrapper, router } = await open(true)
    const navigation = router.push('/other')
    await settle()
    expect(wrapper.text()).toContain('Leave without saving?')

    await clickButton(wrapper, 'Stay on this page')
    await navigation
    await settle()
    expect(router.currentRoute.value.path).toBe('/settings')
  })

  it('leaves without saving when confirmed', async () => {
    const { wrapper, router } = await open(true)
    const navigation = router.push('/other')
    await settle()

    await clickButton(wrapper, 'Leave without saving')
    await navigation
    await settle()
    expect(router.currentRoute.value.path).toBe('/other')
  })

  it('has the browser confirm closing the tab only with changes', async () => {
    const { changed } = await open(true)
    const unload = () => {
      const e = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(e)
      return e.defaultPrevented
    }
    expect(unload()).toBe(true)

    changed.value = false
    await flushPromises()
    expect(unload()).toBe(false)
  })
})
