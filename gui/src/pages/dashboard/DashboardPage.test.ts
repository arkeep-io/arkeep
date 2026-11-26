import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountPage } from '@/test/mount'
import DashboardPage from './DashboardPage.vue'

const { api, live } = vi.hoisted(() => ({
  api: vi.fn(),
  live: { refresh: null as null | ((o: { quiet?: boolean }) => unknown) },
}))
vi.mock('@/services/api', () => ({
  api,
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))
// The unovis charts measure text with SVG APIs jsdom lacks, and finish
// rendering after the test ends. Stubbing them by name does not work (their
// components are all named "index"): the module is replaced.
vi.mock('@unovis/vue', () => {
  const chart = { render: () => null }
  return { VisXYContainer: chart, VisArea: chart, VisAxis: chart, VisGroupedBar: chart }
})
// The live refresh is captured: tests run it by hand, as a change event would.
vi.mock('@/composables/useLiveRefresh', () => ({
  useLiveRefresh: (_concerns: unknown, refresh: (o: { quiet?: boolean }) => unknown) => {
    live.refresh = refresh
  },
}))

const days = Array.from({ length: 7 }, (_, i) => ({ date: `2026-01-0${i + 1}`, succeeded: 0, failed: 0, size_bytes: 0 }))
const dashboard = {
  data: {
    agents_total: 1, agents_online: 1, policies_total: 1, policies_active: 1,
    jobs_today_total: 0, jobs_today_succeeded: 0, jobs_today_failed: 0,
    snapshots_total: 0, snapshots_total_size: 0, checks_enabled: 0, checks_failed: 0,
    job_activity: days, size_activity: days,
  },
}

function recentJobs(name: string) {
  return {
    data: {
      items: [{ id: name, policy_name: name, status: 'succeeded', type: 'backup', created_at: '2026-01-01T00:00:00Z' }],
      total: 1,
    },
  }
}

// answer makes the API answer the dashboard at once and the recent jobs with
// jobs, a value or a promise.
function answer(jobs: unknown) {
  api.mockImplementationOnce(async () => dashboard)
  api.mockImplementationOnce(() => jobs)
}

describe('DashboardPage', () => {
  beforeEach(() => {
    api.mockReset()
    live.refresh = null
  })

  it('ignores a refresh that answers after a later one', async () => {
    answer(recentJobs('first'))
    const { wrapper } = await mountPage(DashboardPage, { path: '/' })
    expect(wrapper.text()).toContain('first')

    let answerSlow!: (v: unknown) => void
    answer(new Promise((resolve) => { answerSlow = resolve }))
    void live.refresh!({ quiet: true })
    answer(recentJobs('latest'))
    void live.refresh!({ quiet: true })
    await flushPromises()
    expect(wrapper.text()).toContain('latest')

    answerSlow(recentJobs('stale'))
    await flushPromises()
    expect(wrapper.text()).toContain('latest')
    expect(wrapper.text()).not.toContain('stale')
  })
})
