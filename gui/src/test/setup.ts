import { afterEach, vi } from 'vitest'
import { enableAutoUnmount } from '@vue/test-utils'

// jsdom has no matchMedia; pages read it for the theme and reduced motion.
// Every query answers "no match", as in a browser without the preference.
vi.stubGlobal('matchMedia', (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => {},
  removeEventListener: () => {},
  addListener: () => {},
  removeListener: () => {},
  dispatchEvent: () => false,
}))

// Unmount every component after each test, so pages attached to the
// document do not leak into the next one.
enableAutoUnmount(afterEach)
