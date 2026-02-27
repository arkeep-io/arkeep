import path from 'node:path'
import { defineConfig } from 'vite'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { VitePWA } from 'vite-plugin-pwa'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), tailwindcss(), VitePWA({
      // generateSW lets vite-plugin-pwa (via workbox-build) generate the
      // service worker automatically. No custom sw.ts needed, no extra
      // Workbox packages to install beyond workbox-window.
      strategies: 'generateSW',
      registerType: 'autoUpdate',

      workbox: {
        // ── Precache ────────────────────────────────────────────────────────
        // All assets emitted by Vite (JS chunks, CSS, fonts, icons) are
        // precached automatically. The generated SW handles cache busting
        // via the content hash in each filename.
        globPatterns: ['**/*.{js,css,html,ico,png,svg,woff2}'],

        // ── Runtime caching ─────────────────────────────────────────────────
        runtimeCaching: [
          {
            // API calls — network first.
            // Always try the network; fall back to cache if offline.
            // This ensures data freshness while allowing stale data display
            // when the server is unreachable.
            urlPattern: /^\/api\//,
            handler: 'NetworkFirst',
            options: {
              cacheName: 'api-cache',
              networkTimeoutSeconds: 10,
              cacheableResponse: { statuses: [200] },
              expiration: {
                maxEntries: 100,
                // 5 minute TTL — API data is stale quickly; we cache it only
                // to provide an offline fallback, not as a performance trick.
                maxAgeSeconds: 5 * 60,
              },
            },
          },
          {
            // External fonts and stylesheets — cache first, long TTL.
            urlPattern: /^https:\/\/fonts\.(googleapis|gstatic)\.com\//,
            handler: 'CacheFirst',
            options: {
              cacheName: 'google-fonts',
              cacheableResponse: { statuses: [0, 200] },
              expiration: {
                maxEntries: 20,
                maxAgeSeconds: 365 * 24 * 60 * 60,
              },
            },
          },
        ],

        // ── SPA fallback ────────────────────────────────────────────────────
        // Unmatched navigation requests (client-side routes) fall back to
        // index.html so Vue Router can handle them offline.
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [
          // Never intercept API calls or WebSocket upgrades
          /^\/api\//,
        ],

        // Skip waiting so the new SW activates immediately on deploy
        skipWaiting: true,
        clientsClaim: true,
      },

      manifest: {
        name: 'Arkeep',
        short_name: 'Arkeep',
        description: 'Centralized backup management',
        theme_color: '#0f172a',
        background_color: '#0f172a',
        display: 'standalone',
        start_url: '/',
        icons: [
          {
            src: 'icons/pwa-192x192.png',
            sizes: '192x192',
            type: 'image/png',
          },
          {
            src: 'icons/pwa-512x512.png',
            sizes: '512x512',
            type: 'image/png',
          },
          {
            src: 'icons/pwa-512x512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'any maskable',
          },
        ],
      },

      devOptions: {
        // Keep SW disabled in dev — it interferes with HMR and the Vite proxy.
        // Test offline behavior with a production build (task build → task serve).
        enabled: false,
      },
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    // Output to gui/dist — the Go server embeds this directory via embed.FS
    outDir: 'dist',
    emptyOutDir: true,
    // Increase chunk size warning threshold — we accept slightly larger bundles
    // in exchange for fewer HTTP round trips when loading the SPA
    chunkSizeWarningLimit: 600,
  },
  server: {
    port: 5173,
    proxy: {
      // Forward all API calls to the Go server in dev.
      // In production, both GUI and API are served from the same origin (port 8080).
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      // Proxy WebSocket connections to the Go server in dev
      '/api/v1/ws': {
        target: 'ws://localhost:8080',
        ws: true,
        changeOrigin: true,
      },
    },
  },
})