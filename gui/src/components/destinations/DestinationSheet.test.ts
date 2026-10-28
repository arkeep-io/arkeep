import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { Destination } from '@/types'
import DestinationSheet from './DestinationSheet.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

type Wrapper = Awaited<ReturnType<typeof mountPage>>['wrapper']

// openSheet mounts the sheet, for a new destination or to edit destination.
// The agent comboboxes, which load agents from the API, are stubbed, and so
// are the tooltips, which need the provider the app sets up at its root.
async function openSheet(destination: Destination | null = null) {
  const { wrapper } = await mountPage(DestinationSheet, {
    props: { open: true, destination },
    stubs: { AsyncCombobox: true, Tooltip: true, TooltipTrigger: true, TooltipContent: true },
  })
  return wrapper
}

// The toggles of a new destination, in template order.
const TOGGLES = ['Append-only', 'Enable retention', 'Integrity check', 'Import existing repository'] as const

// toggle switches a toggle of a new destination on or off.
async function toggle(wrapper: Wrapper, label: typeof TOGGLES[number], on: boolean) {
  wrapper.findAllComponents({ name: 'Switch' })[TOGGLES.indexOf(label)]!.vm.$emit('update:modelValue', on)
  await wrapper.vm.$nextTick()
}

// selectType picks a destination type, as the type selector does.
async function selectType(wrapper: Wrapper, type: string) {
  wrapper.findAllComponents({ name: 'Select' })[0]!.vm.$emit('update:modelValue', type)
  await wrapper.vm.$nextTick()
}

// pickMaintenanceAgent selects the agent that runs retention and checks.
async function pickMaintenanceAgent(wrapper: Wrapper) {
  wrapper.findAllComponents({ name: 'AsyncCombobox' })[0]!.vm.$emit('update:model-value', 'a1')
  await wrapper.vm.$nextTick()
}

// savedBody returns the body of the create or update request, if any.
function savedBody() {
  return vi.mocked(api).mock.calls.find(([, opts]) => opts?.method === 'POST' || opts?.method === 'PATCH')?.[1]
    ?.body as Record<string, unknown> | undefined
}

const s3Destination = {
  id: 'd1', name: 'Offsite', type: 's3', enabled: true,
  config: JSON.stringify({ bucket: 'backups', endpoint: 's3.example.com', region: '', prefix: '' }),
  append_only: false, retention_enabled: false, retention_agent_id: 'a1', retention_schedule: '0 2 * * *',
  retention_last: 0, retention_hourly: 0, retention_daily: 7, retention_weekly: 4, retention_monthly: 6, retention_yearly: 1,
  retention_needs_review: false,
  check_enabled: true, check_schedule: '0 3 * * 0', check_mode: 'subset', check_subset_percent: 5,
} as unknown as Destination

describe('DestinationSheet', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: { id: 'new' } })
  })

  it('shows every missing field of a new local destination and sends nothing', async () => {
    const wrapper = await openSheet()
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Name is required')
    expect(wrapper.text()).toContain('Path is required')
    expect(wrapper.text()).toContain('An agent is required to run retention sweeps and integrity checks.')
    expect(savedBody()).toBeUndefined()
  })

  it('checks the config and credentials of the selected type', async () => {
    const wrapper = await openSheet()
    await selectType(wrapper, 's3')
    await submitForm(wrapper)

    for (const message of ['Bucket is required', 'Endpoint is required', 'Access key is required', 'Secret key is required']) {
      expect(wrapper.text()).toContain(message)
    }
    expect(wrapper.text()).not.toContain('Path is required')
  })

  it('shows an invalid SFTP port', async () => {
    const wrapper = await openSheet()
    await selectType(wrapper, 'sftp')
    await wrapper.find('#sftp-port').setValue('70000')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Port must be between 1 and 65535')
  })

  it('creates a local destination with valid values', async () => {
    const wrapper = await openSheet()
    await wrapper.find('#dest-name').setValue('NAS')
    await wrapper.find('#local-path').setValue('/mnt/backups')
    await pickMaintenanceAgent(wrapper)
    await submitForm(wrapper)

    expect(savedBody()).toMatchObject({
      name: 'NAS', type: 'local', config: JSON.stringify({ path: '/mnt/backups' }), credentials: '{}',
      retention_agent_id: 'a1', check_enabled: true, check_subset_percent: 5, retention_daily: 7,
    })
  })

  describe('retention', () => {
    // withRetention opens a valid new local destination with retention on.
    async function withRetention() {
      const wrapper = await openSheet()
      await wrapper.find('#dest-name').setValue('NAS')
      await wrapper.find('#local-path').setValue('/mnt/backups')
      await pickMaintenanceAgent(wrapper)
      await toggle(wrapper, 'Enable retention', true)
      return wrapper
    }

    it('requires at least one keep value above 0', async () => {
      const wrapper = await withRetention()
      for (const id of ['ret-last', 'ret-hourly', 'ret-daily', 'ret-weekly', 'ret-monthly', 'ret-yearly']) {
        await wrapper.find(`#${id}`).setValue('0')
      }
      await submitForm(wrapper)

      expect(wrapper.text()).toContain('Set at least one keep value above 0.')
      expect(savedBody()).toBeUndefined()
    })

    it.each([
      ['empty', '', 'A value is required'],
      ['above the maximum', '1001', 'Must be a whole number between 0 and 1000'],
    ])('rejects an %s keep value', async (_, value, message) => {
      const wrapper = await withRetention()
      await wrapper.find('#ret-daily').setValue(value)
      await submitForm(wrapper)

      expect(wrapper.text()).toContain(message)
      expect(savedBody()).toBeUndefined()
    })
  })

  it('requires the agent and password of an import', async () => {
    const wrapper = await openSheet()
    await wrapper.find('#dest-name').setValue('NAS')
    await wrapper.find('#local-path').setValue('/mnt/backups')
    await pickMaintenanceAgent(wrapper)
    await toggle(wrapper, 'Import existing repository', true)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Please select an agent.')
    expect(wrapper.text()).toContain('Repository password is required.')
    expect(savedBody()).toBeUndefined()
  })

  it('updates a destination keeping the stored credentials when left blank', async () => {
    const wrapper = await openSheet(s3Destination)
    await wrapper.find('#dest-name').setValue('Offsite 2')
    await submitForm(wrapper)

    const body = savedBody()
    expect(body).toMatchObject({ name: 'Offsite 2', retention_agent_id: 'a1' })
    expect(body).not.toHaveProperty('credentials')
  })
})
