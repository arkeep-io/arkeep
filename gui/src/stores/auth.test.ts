import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ofetch } from 'ofetch'
import { router } from '@/router'
import { useAuthStore } from './auth'

vi.mock('ofetch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('ofetch')>()),
  ofetch: vi.fn(),
}))
vi.mock('@/router', () => ({ router: { push: vi.fn() } }))

const fetchMock = vi.mocked(ofetch)
const httpError = (status: number) => Object.assign(new Error(String(status)), { status })

// answerRefresh makes /auth/refresh answer with the given outcomes in turn
// (a token, or an HTTP status to fail with) and /users/me with a user.
function answerRefresh(...outcomes: (string | number)[]) {
  fetchMock.mockImplementation((async (url: string) => {
    if (url === '/api/v1/users/me') return { data: { id: 'u', email: 'u@example.com', role: 'user' } }
    const next = outcomes.shift()
    if (typeof next === 'number') throw httpError(next)
    return { data: { access_token: next, expires_in: 900 } }
  }) as never)
}

const refreshCalls = () => fetchMock.mock.calls.filter(([url]) => url === '/api/v1/auth/refresh').length

describe('auth store refresh (G-01)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    fetchMock.mockReset()
    vi.mocked(router.push).mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('sends one request for concurrent callers', async () => {
    answerRefresh('t1')
    const auth = useAuthStore()

    const results = await Promise.all([auth.refresh(), auth.refresh(), auth.refresh()])

    expect(results).toEqual([true, true, true])
    expect(refreshCalls()).toBe(1)
    expect(auth.accessToken).toBe('t1')
  })

  it('tries once more after a 401, with the cookie another tab just rotated', async () => {
    answerRefresh(401, 't2')
    const auth = useAuthStore()

    const result = auth.refresh()
    await vi.advanceTimersByTimeAsync(1000)

    expect(await result).toBe(true)
    expect(refreshCalls()).toBe(2)
    expect(auth.accessToken).toBe('t2')
  })

  it('ends the session when the second try is refused too', async () => {
    answerRefresh('t1', 401, 401)
    const auth = useAuthStore()
    await auth.refresh()

    const result = auth.refresh()
    await vi.advanceTimersByTimeAsync(1000)

    expect(await result).toBe(false)
    expect(auth.accessToken).toBeNull()
    expect(auth.user).toBeNull()
  })

  it('keeps the session when the server is unreachable or failing', async () => {
    for (const status of [0, 429, 500, 503]) {
      answerRefresh('t1', status)
      const auth = useAuthStore()
      await auth.refresh()

      await expect(auth.refresh()).rejects.toThrow()
      expect(auth.accessToken).toBe('t1')
      expect(auth.user).not.toBeNull()
    }
  })

  it('retries a scheduled refresh that failed for a temporary reason, without signing out', async () => {
    // The token expires in 60 s, so the scheduled refresh fires at once.
    answerRefresh(503, 't2')
    const auth = useAuthStore()
    fetchMock.mockImplementationOnce((async () => ({ data: { id: 'u', email: 'u@example.com', role: 'user' } })) as never)
    await auth.setTokenAndFetchUser('t1', 60)

    await vi.advanceTimersByTimeAsync(0)
    expect(router.push).not.toHaveBeenCalled()
    expect(auth.accessToken).toBe('t1')

    await vi.advanceTimersByTimeAsync(30_000)
    expect(auth.accessToken).toBe('t2')
    expect(router.push).not.toHaveBeenCalled()
  })
})
