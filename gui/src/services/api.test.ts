import { describe, expect, it } from 'vitest'
import { FetchError } from 'ofetch'
import { apiErrorMessage } from './api'

// fetchError builds the error ofetch throws for an HTTP failure with status
// and body data (undefined status: no response at all).
function fetchError(status: number | undefined, data?: unknown): FetchError {
  return Object.assign(new FetchError('[POST] "/api/v1/destinations": failed'), { status, data })
}

describe('apiErrorMessage', () => {
  it("prefers the server's message", () => {
    const err = fetchError(503, { error: { message: 'agent did not respond in time' } })
    expect(apiErrorMessage(err, 'An error occurred')).toBe('agent did not respond in time')
  })

  // #448: a reverse proxy that times out answers with its own HTML page, and
  // the user only saw "An error occurred".
  it.each([502, 503, 504])('explains an HTTP %i that did not come from Arkeep', (status) => {
    const msg = apiErrorMessage(fetchError(status, '<html>Gateway Timeout</html>'), 'An error occurred')
    expect(msg).toContain(`HTTP ${status}`)
    expect(msg).toContain('reverse proxy')
  })

  it('uses the fallback for other failures without a server message', () => {
    expect(apiErrorMessage(fetchError(500), 'An error occurred')).toBe('An error occurred')
    expect(apiErrorMessage(fetchError(undefined), 'An error occurred')).toBe('An error occurred')
  })

  it('keeps the message of errors that are not HTTP failures', () => {
    expect(apiErrorMessage(new Error('Session expired'), 'An error occurred')).toBe('Session expired')
  })
})
