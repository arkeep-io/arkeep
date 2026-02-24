// auth.ts — Authentication Pinia store.
//
// Security model:
//   - Access token (JWT, 15 min TTL): stored in memory only, never in
//     localStorage/sessionStorage. Lost on page refresh — handled by silent
//     refresh on app boot.
//   - Refresh token (7 day TTL): stored as httpOnly cookie by the server,
//     never accessible from JS. The browser sends it automatically with
//     requests to /api/v1/auth/refresh.
//
// Refresh flow:
//   1. On app boot, attempt a silent refresh to restore the session.
//   2. A timer fires 60s before the access token expires to refresh proactively.
//   3. If any API call returns 401, the interceptor calls refresh() and retries.

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ofetch } from 'ofetch'

// ─── Types ────────────────────────────────────────────────────────────────────

interface User {
  id: string
  email: string
  name: string
  role: 'admin' | 'operator' | 'viewer'
  is_active: boolean
}

interface TokenPair {
  access_token: string
  expires_in: number // seconds until expiry
}

// ─── Store ────────────────────────────────────────────────────────────────────

export const useAuthStore = defineStore('auth', () => {
  // ─── State ─────────────────────────────────────────────────────────────────

  const accessToken = ref<string | null>(null)
  const user = ref<User | null>(null)
  const isInitialized = ref(false) // true after the boot refresh attempt

  // Timer ID for the proactive refresh scheduled before token expiry
  let refreshTimer: ReturnType<typeof setTimeout> | null = null

  // ─── Getters ───────────────────────────────────────────────────────────────

  const isAuthenticated = computed(() => accessToken.value !== null)
  const isAdmin = computed(() => user.value?.role === 'admin')

  // ─── Actions ───────────────────────────────────────────────────────────────

  // login exchanges credentials for a token pair. The refresh token is set
  // as an httpOnly cookie by the server — we only handle the access token.
  async function login(email: string, password: string): Promise<void> {
    const data = await ofetch<TokenPair & { user: User }>('/api/v1/auth/login', {
      method: 'POST',
      body: { email, password },
    })

    _setSession(data.access_token, data.expires_in, data.user)
  }

  // logout invalidates the session server-side and clears local state.
  async function logout(): Promise<void> {
    try {
      await ofetch('/api/v1/auth/logout', {
        method: 'POST',
        headers: _authHeader(),
      })
    } catch {
      // Best effort — clear local state regardless of server response
    } finally {
      _clearSession()
    }
  }

  // refresh silently exchanges the httpOnly refresh token cookie for a new
  // access token. Returns true on success, false if the session has expired.
  async function refresh(): Promise<boolean> {
    try {
      const data = await ofetch<TokenPair & { user: User }>(
        '/api/v1/auth/refresh',
        {
          method: 'POST',
          // credentials: 'include' is required for the httpOnly cookie to be
          // sent cross-origin in dev (proxy doesn't always forward cookies)
          credentials: 'include',
        },
      )

      _setSession(data.access_token, data.expires_in, data.user)
      return true
    } catch {
      _clearSession()
      return false
    }
  }

  // initialize is called once on app boot. It attempts a silent token refresh
  // to restore the session from the httpOnly cookie. The app should wait for
  // this to complete before rendering protected routes.
  async function initialize(): Promise<void> {
    await refresh()
    isInitialized.value = true
  }

  // authHeader returns the Authorization header for use in ofetch calls.
  // Use the exported helper below for convenience.
  function authHeader(): Record<string, string> {
    return _authHeader()
  }

  // ─── Private helpers ───────────────────────────────────────────────────────

  function _setSession(token: string, expiresIn: number, userData: User): void {
    accessToken.value = token
    user.value = userData

    // Schedule a proactive refresh 60s before expiry so API calls never
    // encounter a 401 due to an expired token in normal usage.
    _scheduleRefresh(expiresIn)
  }

  function _clearSession(): void {
    accessToken.value = null
    user.value = null
    _cancelRefresh()
  }

  function _scheduleRefresh(expiresInSeconds: number): void {
    _cancelRefresh()
    const delayMs = Math.max((expiresInSeconds - 60) * 1000, 0)
    refreshTimer = setTimeout(async () => {
      const ok = await refresh()
      if (!ok) {
        // Session expired — redirect to login. We import the router lazily
        // to avoid a circular dependency between the store and the router.
        const { router } = await import('@/router')
        router.push('/login')
      }
    }, delayMs)
  }

  function _cancelRefresh(): void {
    if (refreshTimer !== null) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
  }

  function _authHeader(): Record<string, string> {
    return accessToken.value
      ? { Authorization: `Bearer ${accessToken.value}` }
      : {}
  }

  return {
    // State (readonly from outside)
    accessToken,
    user,
    isInitialized,
    // Getters
    isAuthenticated,
    isAdmin,
    // Actions
    login,
    logout,
    refresh,
    initialize,
    authHeader,
  }
})