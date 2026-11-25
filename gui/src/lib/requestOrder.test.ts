import { describe, expect, it } from 'vitest'
import { requestOrder } from './requestOrder'

describe('requestOrder', () => {
  it('drops an answer older than the one shown', () => {
    const beginRequest = requestOrder()
    const first = beginRequest()
    const second = beginRequest()

    expect(second.show()).toBe(true)
    expect(first.stale).toBe(true)
    expect(first.show()).toBe(false)
  })

  it('shows answers that arrive in order', () => {
    const beginRequest = requestOrder()
    const first = beginRequest()
    const second = beginRequest()

    expect(first.show()).toBe(true)
    expect(second.stale).toBe(false)
    expect(second.show()).toBe(true)
  })

  it('keeps an earlier answer when a later request fails', () => {
    const beginRequest = requestOrder()
    const first = beginRequest()
    const second = beginRequest()

    // second failed: it never calls show(), so first is not stale.
    expect(second.stale).toBe(false)
    expect(first.stale).toBe(false)
    expect(first.show()).toBe(true)
  })
})
