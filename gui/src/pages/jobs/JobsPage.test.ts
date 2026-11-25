import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountPage } from '@/test/mount'
import JobsPage from './JobsPage.vue'

const { api, live } = vi.hoisted(() => ({
  api: vi.fn(),
  live: { refresh: null as null | ((o: { quiet?: boolean }) => unknown) },
}))
vi.mock('@/services/api', () => ({
  api,
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))
// The live refresh is captured: tests run it by hand, as a change event would.
vi.mock('@/composables/useLiveRefresh', () => ({
  useLiveRefresh: (_concerns: unknown, refresh: (o: { quiet?: boolean }) => unknown) => {
    live.refresh = refresh
  },
}))

function jobsResponse(name: string) {
  return {
    data: {
      items: [{ id: name, policy_name: name, status: 'succeeded', type: 'backup', created_at: '2026-01-01T00:00:00Z' }],
      total: 120,
    },
  }
}

describe('JobsPage', () => {
  beforeEach(() => {
    api.mockReset()
    live.refresh = null
  })

  it('ignores a live refresh that answers after the page the user moved to', async () => {
    api.mockResolvedValueOnce(jobsResponse('page-1'))
    const { wrapper } = await mountPage(JobsPage, { path: '/jobs' })
    expect(wrapper.text()).toContain('page-1')

    // A live refresh of page 1 is still running when the user moves to page 2.
    let answerLive!: (v: unknown) => void
    api.mockReturnValueOnce(new Promise((resolve) => { answerLive = resolve }))
    void live.refresh!({ quiet: true })
    api.mockResolvedValueOnce(jobsResponse('page-2'))
    await wrapper.findAll('button').find((b) => b.text() === 'Next')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('page-2')

    answerLive(jobsResponse('stale-page-1'))
    await flushPromises()
    expect(wrapper.text()).toContain('page-2')
    expect(wrapper.text()).not.toContain('stale-page-1')
  })

  it('keeps an earlier answer when a later request fails', async () => {
    api.mockResolvedValueOnce(jobsResponse('first'))
    const { wrapper } = await mountPage(JobsPage, { path: '/jobs' })

    let answerFirst!: (v: unknown) => void
    api.mockReturnValueOnce(new Promise((resolve) => { answerFirst = resolve }))
    void live.refresh!({ quiet: true })
    api.mockRejectedValueOnce(new Error('down'))
    void live.refresh!({ quiet: true })
    await flushPromises()
    answerFirst(jobsResponse('second'))
    await flushPromises()

    expect(wrapper.text()).toContain('second')
    expect(wrapper.text()).not.toContain('Failed to load jobs.')
  })
})
