// api.ts — Typed fetch wrapper with transparent token refresh and retry.
//
// Why not ofetch.create + onResponseError?
// ofetch's onResponseError hook is fire-and-forget: its return value is
// ignored and it cannot replace the response seen by the original caller.
// A retry inside the hook succeeds in the background but the original await
// still throws the 401. We therefore wrap ofetch manually so we can catch,
// refresh, and re-await the same request with the new token.
//
// Refresh deduplication:
//   Multiple concurrent requests may 401 at the same time (e.g. page load
//   fires 3 API calls after the access token has expired). We keep a single
//   shared refreshPromise so only one refresh call hits the server regardless
//   of how many requests are waiting.
//
// Usage:
//   import { api } from '@/services/api'
//   const res = await api<ApiResponse<Agent[]>>('/api/v1/agents')

import { ofetch, type FetchOptions } from 'ofetch'
import { useAuthStore } from '@/stores/auth'

// Shared in-flight refresh promise — deduplicates concurrent refresh attempts.
let refreshPromise: Promise<boolean> | null = null

// Normalize any HeadersInit variant into a plain Record so we can safely
// spread and extend it with the Authorization header.
function headersToRecord(
  headers: HeadersInit | undefined,
): Record<string, string> {
  if (!headers) return {}
  if (headers instanceof Headers) {
    const result: Record<string, string> = {}
    headers.forEach((value, key) => { result[key] = value })
    return result
  }
  if (Array.isArray(headers)) return Object.fromEntries(headers)
  return headers as Record<string, string>
}

// buildHeaders returns the options headers merged with the current Bearer token.
function buildHeaders(
  headers: HeadersInit | undefined,
  token: string | null,
): Record<string, string> {
  const base = headersToRecord(headers)
  if (token) base['Authorization'] = `Bearer ${token}`
  return base
}

// api<T> is the single fetch entry point for all REST calls in the GUI.
// It injects the Authorization header automatically and handles the
// access-token expiry cycle transparently:
//
//  1. First attempt: inject current access token and call the endpoint.
//  2. On 401: trigger (or join) a shared token refresh against the server.
//  3. Retry: if refresh succeeded, repeat the request with the new token.
//  4. Give up: if refresh failed (refresh token also expired), redirect to /login.
export async function api<T = unknown>(
  url: string,
  options: FetchOptions<'json'> = {},
): Promise<T> {
  const auth = useAuthStore()

  // ── First attempt ──────────────────────────────────────────────────────────
  try {
    return await ofetch<T>(url, {
      ...options,
      credentials: 'include',
      headers: buildHeaders(options.headers, auth.accessToken),
    })
  } catch (err: any) {
    // Only intercept 401 — propagate everything else immediately.
    if (err?.status !== 401 && err?.response?.status !== 401) throw err
  }

  // ── Token refresh ──────────────────────────────────────────────────────────
  // Deduplicate: if another request already started a refresh, join it
  // instead of firing a second one.
  if (!refreshPromise) {
    refreshPromise = auth.refresh().finally(() => { refreshPromise = null })
  }

  const refreshed = await refreshPromise

  if (!refreshed || !auth.accessToken) {
    // Refresh token is also expired — the session is gone.
    const { router } = await import('@/router')
    router.push('/login')
    // Throw so the original caller's await rejects cleanly rather than
    // silently returning undefined.
    throw new Error('Session expired')
  }

  // ── Retry with new token ────────────────────────────────────────────────────
  // This is a direct ofetch call (not api()) to avoid recursion.
  return ofetch<T>(url, {
    ...options,
    credentials: 'include',
    headers: buildHeaders(options.headers, auth.accessToken),
  })
}