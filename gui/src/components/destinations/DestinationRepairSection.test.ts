import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle } from '@/test/mount'
import { api } from '@/services/api'
import { Select } from '@/components/ui/select'
import type { Destination } from '@/types'
import DestinationRepairSection from './DestinationRepairSection.vue'

vi.mock('@/services/api', () => ({
  api: vi.fn(),
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
}))

const pack = '0606cc8ce3912abf19fecc7546fb74a5ce00651b833fb00a35a8db471aad1bc0'

const destination = {
  id: 'd1',
  name: 'garage',
  type: 's3',
  retention_agent_id: 'a1',
  retention_agent_name: 'icarus',
  repair_available: true,
  maintenance: null,
  locks: [],
} as unknown as Destination

// jobStatus is what GET /jobs/{id} answers, so a test decides when the
// tracked job ends.
let jobStatus = 'running'

function mockApi() {
  vi.mocked(api).mockImplementation(async (url: string) => {
    if (url.startsWith('/api/v1/jobs/')) return { data: { id: 'j1', status: jobStatus } }
    if (url.endsWith('/repair')) return { data: { job_id: 'j1', maintenance_started: true } }
    return { data: { enabled: true } }
  })
}

async function mount(dest: Destination = destination) {
  const { wrapper } = await mountPage(DestinationRepairSection, { props: { destination: dest }, stubs: { RouterLink: true } })
  return wrapper
}

function postedRepairs() {
  return vi.mocked(api).mock.calls
    .filter(([url]) => (url as string).endsWith('/repair'))
    .map(([, opts]) => (opts as { body: unknown }).body)
}

describe('DestinationRepairSection', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
    jobStatus = 'running'
    mockApi()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('starts a maintenance', async () => {
    const wrapper = await mount()
    await wrapper.find('[data-testid="start-maintenance"]').trigger('click')
    await settle()
    expect(api).toHaveBeenCalledWith('/api/v1/destinations/d1/maintenance', { method: 'PUT', body: { enabled: true } })
    expect(wrapper.emitted('changed')).toBeTruthy()
  })

  it('keeps a maintenance a repair started instead of ending it', async () => {
    const wrapper = await mount({
      ...destination,
      maintenance: { since: '2026-11-21T10:00:00Z', by: 'jane@example.com', repair_job_id: 'j0', effective: true },
    })
    expect(wrapper.find('[data-testid="end-maintenance"]').exists()).toBe(false)
    await wrapper.find('[data-testid="keep-maintenance"]').trigger('click')
    await settle()
    expect(api).toHaveBeenCalledWith('/api/v1/destinations/d1/maintenance', { method: 'PUT', body: { enabled: true } })
  })

  it('runs a non-destructive action directly', async () => {
    const wrapper = await mount()
    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    expect(postedRepairs()).toEqual([{ action: 'check' }])
    expect(wrapper.find('[data-testid="repair-job"]').text()).toContain('Pending')
  })

  it('disables repairs when the maintenance agent cannot run them', async () => {
    const wrapper = await mount({ ...destination, repair_available: false })
    expect(wrapper.find('[data-testid="repair-blocked"]').text()).toContain('icarus does not support repairs')
    expect(wrapper.find('[data-testid="repair-run"]').attributes('disabled')).toBeDefined()
  })

  it('waits for a maintenance to become effective', async () => {
    const wrapper = await mount({
      ...destination,
      maintenance: { since: '2026-11-21T10:00:00Z', by: 'jane@example.com', repair_job_id: '', effective: false },
    })
    expect(wrapper.find('[data-testid="repair-blocked"]').text()).toContain('once the jobs running on this destination end')
    expect(wrapper.find('[data-testid="repair-run"]').attributes('disabled')).toBeDefined()
  })

  it('previews repair-packs, then needs the destination name to apply it', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = await mount()
    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'repair-packs')
    await settle()
    await wrapper.find('[data-testid="repair-pack-ids"]').setValue(pack)

    const apply = () => wrapper.find('[data-testid="repair-apply"]')
    expect(apply().attributes('disabled')).toBeDefined()

    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    expect(postedRepairs()).toEqual([{ action: 'find-packs', pack_ids: [pack] }])
    expect(apply().attributes('disabled')).toBeDefined()

    // The preview succeeds: applying the same request is offered.
    jobStatus = 'succeeded'
    await vi.advanceTimersByTimeAsync(2100)
    await settle()
    expect(apply().attributes('disabled')).toBeUndefined()

    await apply().trigger('click')
    await settle()
    // Not in a maintenance an admin started: the dialog advises one.
    expect(wrapper.find('[data-testid="repair-maintenance-advice"]').exists()).toBe(true)
    const confirm = () => wrapper.find('[data-testid="repair-confirm"]')
    await wrapper.find('[data-testid="repair-confirm-name"]').setValue('Garage')
    await confirm().trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Type the destination name exactly')
    expect(postedRepairs()).toHaveLength(1)

    await wrapper.find('[data-testid="repair-confirm-name"]').setValue('garage')
    await confirm().trigger('click')
    await settle()
    expect(postedRepairs()[1]).toEqual({ action: 'repair-packs', pack_ids: [pack], confirm: 'garage' })
  })

  it('does not advise a maintenance an admin already started', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = await mount({
      ...destination,
      maintenance: { since: '2026-11-21T10:00:00Z', by: 'jane@example.com', repair_job_id: '', effective: true },
    })
    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'repair-packs')
    await settle()
    await wrapper.find('[data-testid="repair-pack-ids"]').setValue(pack)
    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    jobStatus = 'succeeded'
    await vi.advanceTimersByTimeAsync(2100)
    await settle()
    await wrapper.find('[data-testid="repair-apply"]').trigger('click')
    await settle()
    expect(wrapper.find('[data-testid="repair-confirm-name"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="repair-maintenance-advice"]').exists()).toBe(false)
  })

  it('refuses pack IDs that are not full lowercase hexadecimal IDs', async () => {
    const wrapper = await mount()
    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'repair-packs')
    await settle()
    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Enter at least one pack ID')

    await wrapper.find('[data-testid="repair-pack-ids"]').setValue('0606CC8C')
    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Invalid pack ID "0606CC8C"')
    expect(postedRepairs()).toEqual([])
  })

  it('refuses a host restic would read differently', async () => {
    const wrapper = await mount()
    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'repair-snapshots')
    await settle()
    await wrapper.find('#repair-hosts').setValue('-x')
    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Invalid host "-x"')
    expect(postedRepairs()).toEqual([])
  })

  it('asks for a new preview when the request changes', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = await mount()
    wrapper.findComponent(Select).vm.$emit('update:modelValue', 'repair-snapshots')
    await settle()
    await wrapper.find('#repair-hosts').setValue('helios')
    await wrapper.find('[data-testid="repair-run"]').trigger('click')
    await settle()
    jobStatus = 'succeeded'
    await vi.advanceTimersByTimeAsync(2100)
    await settle()
    expect(postedRepairs()[0]).toEqual({ action: 'repair-snapshots', hosts: ['helios'] })
    expect(wrapper.find('[data-testid="repair-apply"]').attributes('disabled')).toBeUndefined()

    await wrapper.find('#repair-hosts').setValue('selene')
    expect(wrapper.find('[data-testid="repair-apply"]').attributes('disabled')).toBeDefined()
  })
})
