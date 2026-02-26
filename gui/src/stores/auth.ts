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
// Login flow:
//   1. POST /api/v1/auth/login  → receives { access_token, expires_in }
//   2. GET  /api/v1/users/me    → fetches user profile with the new token
//
// OIDC flow:
//   1. window.location.href = /api/v1/auth/oidc/login  (server-side redirect)
//   2. Server completes OAuth exchange, redirects to /?token=<access_token>
//   3. OIDCCallbackPage reads token from URL, calls setTokenAndFetchUser()

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ofetch } from 'ofetch'
import type { User, ApiResponse, TokenResponse } from '@/types'

export const useAuthStore = defineStore('auth', () => {
  // ─── State ──────────────────────────────────────────────────────────────────

  const accessToken = ref<string | null>(null)
  const user = ref<User | null>(null)

  // isInitialized becomes true after the first initialize() call completes.
  // The router guard waits for this before making allow/redirect decisions,
  // preventing a flash-redirect to /login on hard reload when a valid
  // refresh token cookie exists.
  const isInitialized = ref(false)

  // Single in-flight initialize promise — prevents concurrent calls during
  // rapid navigation before the first refresh completes.
  let initPromise: Promise<void> | null = null
  let refreshTimer: ReturnType<typeof setTimeout> | null = null

  // ─── Getters ─────────────────────────────────────────────────────────────────

  const isAuthenticated = computed(() => accessToken.value !== null)
  const isAdmin = computed(() => user.value?.role === 'admin')

  // ─── Actions ──────────────────────────────────────────────────────────────────

  // login exchanges email/password for an access token, then fetches the
  // user profile. Throws on invalid credentials (HTTP 401).
  async function login(email: string, password: string): Promise<void> {
    const res = await ofetch<ApiResponse<TokenResponse>>('/api/v1/auth/login', {
      method: 'POST',
      body: { email, password },
    })
    await setTokenAndFetchUser(res.data.access_token, 900)
  }

  // logout invalidates the refresh token server-side and clears local state.
  async function logout(): Promise<void> {
    try {
      await ofetch('/api/v1/auth/logout', {
        method: 'POST',
        credentials: 'include',
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
      const res = await ofetch<ApiResponse<TokenResponse>>('/api/v1/auth/refresh', {
        method: 'POST',
        credentials: 'include',
      })
      await setTokenAndFetchUser(res.data.access_token, 900)
      return true
    } catch {
      _clearSession()
      return false
    }
  }

  // initialize is called once on app boot by the router navigation guard.
  // Guards against concurrent calls during rapid navigation.
  async function initialize(): Promise<void> {
    if (isInitialized.value) return
    if (initPromise) return initPromise

    initPromise = refresh().then(() => {}).finally(() => {
      isInitialized.value = true
      initPromise = null
    })

    return initPromise
  }

  // setTokenAndFetchUser stores the access token in memory and fetches the
  // current user profile. Exported so OIDCCallbackPage can call it directly.
  async function setTokenAndFetchUser(token: string, expiresIn: number): Promise<void> {
    accessToken.value = token
    const res = await ofetch<ApiResponse<User>>('/api/v1/users/me', {
      headers: { Authorization: `Bearer ${token}` },
    })
    user.value = res.data
    _scheduleRefresh(expiresIn)
  }

  // ─── Private ──────────────────────────────────────────────────────────────────

  function _scheduleRefresh(expiresInSeconds: number): void {
    _cancelRefresh()
    const delayMs = Math.max((expiresInSeconds - 60) * 1000, 0)
    refreshTimer = setTimeout(async () => {
      const ok = await refresh()
      if (!ok) {
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

  function _clearSession(): void {
    accessToken.value = null
    user.value = null
    _cancelRefresh()
  }

  function _authHeader(): Record<string, string> {
    return accessToken.value
      ? { Authorization: `Bearer ${accessToken.value}` }
      : {}
  }

  return {
    accessToken,
    user,
    isInitialized,
    isAuthenticated,
    isAdmin,
    login,
    logout,
    refresh,
    initialize,
    setTokenAndFetchUser,
  }
})