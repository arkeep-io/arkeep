import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { Agent } from '@/types'
import AgentSheet from './AgentSheet.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const agent = { id: 'a1', name: 'old-name', hostname: 'host-1' } as Agent

describe('AgentSheet', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: agent })
  })

  it('prefills the current name', async () => {
    const { wrapper } = await mountPage(AgentSheet, { props: { agent, open: true } })

    expect((wrapper.find('#agent-name').element as HTMLInputElement).value).toBe('old-name')
  })

  it('requires a name and sends nothing when empty', async () => {
    const { wrapper } = await mountPage(AgentSheet, { props: { agent, open: true } })
    await wrapper.find('#agent-name').setValue('')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Name is required')
    expect(api).not.toHaveBeenCalled()
  })

  it('renames the agent and closes', async () => {
    const { wrapper } = await mountPage(AgentSheet, { props: { agent, open: true } })
    await wrapper.find('#agent-name').setValue('new-name')
    await submitForm(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/agents/a1', { method: 'PATCH', body: { name: 'new-name' } })
    expect(wrapper.emitted('saved')).toHaveLength(1)
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })
})
