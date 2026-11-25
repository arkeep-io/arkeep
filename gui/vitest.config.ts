import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, mergeConfig, type Plugin } from 'vitest/config'
import viteConfig from './vite.config.ts'

const publicDir = fileURLToPath(new URL('./public/', import.meta.url))

// The Vue compiler turns `<img src="/icons/x.png">` into an import of
// "/icons/x.png". Vite serves that from public/, but on Windows Vitest's module
// runner then fails to load it ("file:///icons/x.png" is not an absolute path
// there), so the whole test file never runs. Resolve public-dir assets to a
// virtual module that exports the URL, as Vite does for them in a build.
function publicAssets(): Plugin {
  const prefix = '\0public-asset:'
  return {
    name: 'test-public-assets',
    enforce: 'pre',
    resolveId(id) {
      if (id.startsWith('/') && !id.startsWith('//') && existsSync(publicDir + id.slice(1))) {
        return prefix + id
      }
    },
    load(id) {
      if (id.startsWith(prefix)) {
        return `export default ${JSON.stringify(id.slice(prefix.length))}`
      }
    },
  }
}

// Tests reuse the app's Vite config (Vue plugin, @ alias) and run in jsdom.
// Test files live next to the code they test, as *.test.ts.
export default mergeConfig(viteConfig, defineConfig({
  plugins: [publicAssets()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    setupFiles: ['src/test/setup.ts'],
    // The first test of a form's file mounts the whole form (vee-validate, zod,
    // shadcn-vue) on a cold module cache: about 1 s alone, but over 5 s (the
    // default) when the suite runs in parallel on a loaded machine.
    testTimeout: 15000,
    // pnpm test:coverage prints the coverage percentages in the terminal.
    // Every source file counts, tested or not. shadcn-vue's components
    // (components/ui) are generated, and the tests' helpers and type
    // declarations hold no logic to cover.
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,vue}'],
      exclude: ['src/**/*.test.ts', 'src/test/**', 'src/components/ui/**', 'src/**/*.d.ts'],
      reporter: ['text-summary'],
    },
  },
}))
