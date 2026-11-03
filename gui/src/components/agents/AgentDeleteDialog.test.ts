import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle } from '@/test/mount'
import { api } from '@/services/api'
import AgentDeleteDialog from './AgentDeleteDialog.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const agent = { id: 'a1', name: 'nas' }
const free = { policies: [], destinations: [] }
const inUse = {
  policies: [{ id: 'p1', name: 'Laptop' }],
  destinations: [{ id: 'd1', name: 'Offsite' }],
}

// openDialog mounts the dialog closed and opens it, as the agents pages do.
async function openDialog() {
  const { wrapper } = await mountPage(AgentDeleteDialog, { props: { open: false, agent } })
  await wrapper.setProps({ open: true })
  await settle()
  return wrapper
}

const deleteButton = (wrapper: Awaited<ReturnType<typeof openDialog>>) =>
  wrapper.findAll('button').find((b) => b.text() === 'Delete')!

describe('AgentDeleteDialog', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it('deletes an agent nothing depends on', async () => {
    vi.mocked(api).mockResolvedValueOnce({ data: free }).mockResolvedValueOnce(undefined)
    const wrapper = await openDialog()

    expect(api).toHaveBeenCalledWith('/api/v1/agents/a1/usage')
    expect(deleteButton(wrapper).attributes('disabled')).toBeUndefined()
    await deleteButton(wrapper).trigger('click')
    await settle()

    expect(api).toHaveBeenCalledWith('/api/v1/agents/a1', { method: 'DELETE' })
    expect(wrapper.emitted('deleted')).toHaveLength(1)
  })

  it('lists what uses the agent and keeps Delete disabled', async () => {
    vi.mocked(api).mockResolvedValueOnce({ data: inUse })
    const wrapper = await openDialog()

    const usage = wrapper.find('[data-testid="agent-usage"]')
    expect(usage.text()).toContain('Laptop')
    expect(usage.text()).toContain('Offsite')
    expect(usage.find('a[href="/policies/p1"]').exists()).toBe(true)
    expect(usage.find('a[href="/destinations/d1"]').exists()).toBe(true)
    expect(deleteButton(wrapper).attributes('disabled')).toBeDefined()
  })

  it('shows the refusal when the agent came into use meanwhile', async () => {
    const conflict = Object.assign(new Error('409'), { data: { error: { message: 'the agent is still in use' } } })
    vi.mocked(api)
      .mockResolvedValueOnce({ data: free })
      .mockRejectedValueOnce(conflict)
      .mockResolvedValueOnce({ data: inUse })
    const wrapper = await openDialog()
    await deleteButton(wrapper).trigger('click')
    await settle()

    expect(wrapper.text()).toContain('the agent is still in use')
    expect(wrapper.find('[data-testid="agent-usage"]').text()).toContain('Laptop')
    expect(wrapper.emitted('deleted')).toBeUndefined()
  })
})
