// api.ts — Configured ofetch instance with auth interceptor.
//
// Every API call goes through this instance. The interceptor:
//   1. Injects the Authorization header from the auth store.
//   2. On 401, attempts a token refresh and retries the request once.
//   3. On second 401, clears the session and redirects to /login.
//
// Usage:
//   import { api } from '@/services/api'
//   const agents = await api<Agent[]>('/api/v1/agents')

import { ofetch, type FetchOptions } from 'ofetch'
import { useAuthStore } from '@/stores/auth'

// Track in-flight refresh promises to avoid concurrent refresh calls
// when multiple requests 401 at the same time.
let refreshPromise: Promise<boolean> | null = null

// Flag to prevent infinite retry loops: if the retried request also returns
// 401, we bail out instead of refreshing again.
let isRetrying = false

// Normalize any HeadersInit variant into a plain Record so we can safely
// spread and extend it with additional entries.
function headersToRecord(
  headers: HeadersInit | undefined,
): Record<string, string> {
  if (!headers) return {}
  if (headers instanceof Headers) {
    const result: Record<string, string> = {}
    headers.forEach((value, key) => {
      result[key] = value
    })
    return result
  }
  if (Array.isArray(headers)) {
    return Object.fromEntries(headers)
  }
  // Plain object — already the shape we want
  return headers as Record<string, string>
}

export const api = ofetch.create({
  // Base URL is empty — all paths are absolute (/api/v1/...) which works
  // both in production (same origin) and in dev (Vite proxy).
  baseURL: '',
  credentials: 'include',

  onRequest({ options }) {
    const auth = useAuthStore()
    if (auth.accessToken) {
      ;(options as FetchOptions).headers = {
        ...headersToRecord((options as FetchOptions).headers),
        Authorization: `Bearer ${auth.accessToken}`,
      }
    }
  },

  async onResponseError({ request, response, options }): Promise<void> {
    if (response.status !== 401) return
    if (isRetrying) return

    const auth = useAuthStore()

    // Prevent concurrent refresh attempts when multiple requests 401 together
    if (!refreshPromise) {
      refreshPromise = auth.refresh().finally(() => {
        refreshPromise = null
      })
    }

    const refreshed = await refreshPromise

    if (!refreshed || !auth.accessToken) {
      // Refresh token is also expired — send user back to login
      const { router } = await import('@/router')
      router.push('/login')
      return
    }

    // Retry the original request once with the new access token.
    isRetrying = true
    try {
      await ofetch(request as string, {
        ...(options as FetchOptions),
        headers: {
          ...headersToRecord((options as FetchOptions).headers),
          Authorization: `Bearer ${auth.accessToken}`,
        },
      })
    } finally {
      isRetrying = false
    }
  },
})