// useTheme.ts — Thin wrapper around VueUse's useColorMode.
//
// Centralizes the color mode configuration so all components use the same
// storage key and attribute. Import this composable instead of calling
// useColorMode() directly to ensure consistency.

import { useColorMode, usePreferredColorScheme } from '@vueuse/core'
import { computed, watch } from 'vue'

export type ColorModeValue = 'dark' | 'light'

export function useTheme() {
  const mode = useColorMode({
    // Persisted in localStorage under this key
    storageKey: 'arkeep:color-mode',
    modes: {
      dark: 'dark',
      light: 'light',
    },
  })

  const preferredScheme = usePreferredColorScheme()

  const isDark = computed(() =>
    mode.value === 'dark' ||
    (mode.value === 'auto' && preferredScheme.value === 'dark'),
  )

  // Manually toggle the 'dark' class on <html> instead of relying on
  // attribute: 'class' which overwrites all existing classes
  watch(isDark, (dark) => {
    document.documentElement.classList.toggle('dark', dark)
  }, { immediate: true })

  function cycle(): void {
    const next: Record<ColorModeValue, ColorModeValue> = {
      light: 'dark',
      dark: 'light',
    }
    mode.value = next[mode.value as ColorModeValue] ?? 'dark'
  }

  const modeLabel = computed(() => {
    if (mode.value === 'dark') return 'Dark'
    return 'Light'
  })

  return { mode, isDark, cycle, modeLabel }
}