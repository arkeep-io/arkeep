import { flushPromises, mount, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { createPinia, type Pinia } from 'pinia'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { h, type Component } from 'vue'
import { vi } from 'vitest'

// InPlaceTeleport replaces <Teleport> and renders its content where it is
// declared, inside the wrapper.
const InPlaceTeleport = {
  setup: (_: unknown, { slots }: { slots: { default?: () => unknown } }) => () => h('div', slots.default?.() as never),
}

// mountPage mounts a page or component with a fresh Pinia store and an
// in-memory router at path. router.push is stubbed, so tests can assert
// navigation to named routes without declaring the app's route table.
export async function mountPage(
  component: Component,
  options: {
    path?: string
    props?: Record<string, unknown>
    attachTo?: HTMLElement
    // stubs replaces child components by name, e.g. a combobox that loads
    // its options from the API.
    stubs?: Record<string, boolean>
    // pinia lets a test fill a store (e.g. the signed-in user) before mount.
    pinia?: Pinia
  } = {},
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  })
  await router.push(options.path ?? '/')
  await router.isReady()
  vi.spyOn(router, 'push').mockResolvedValue(undefined)

  const wrapper = mount(component, {
    props: options.props,
    attachTo: options.attachTo,
    // Sheets and dialogs teleport to <body>: render them in place so the
    // wrapper can find their fields.
    global: { plugins: [router, options.pinia ?? createPinia()], stubs: { teleport: InPlaceTeleport, ...options.stubs } },
  })
  await flushPromises()
  return { wrapper, router }
}

// settle waits for vee-validate, whose validation and submit handlers chain
// promises across several macrotasks, and for the resulting re-render.
export async function settle(): Promise<void> {
  for (let i = 0; i < 5; i++) {
    await flushPromises()
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
}

// submitForm submits the first form of wrapper (or form, a selector or an
// element found in it) and waits for the validation and submit handlers to
// settle.
export async function submitForm(wrapper: VueWrapper, form: string | DOMWrapper<Element> = 'form'): Promise<void> {
  await (typeof form === 'string' ? wrapper.find(form) : form).trigger('submit')
  await settle()
}
