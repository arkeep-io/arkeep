import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle } from '@/test/mount'
import { api } from '@/services/api'
import type { Destination } from '@/types'
import ImportSnapshotsDialog from './ImportSnapshotsDialog.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const destination = { id: 'd1', name: 'Offsite' } as Destination

// openDialog mounts the dialog closed and opens it, as the parent does. The
// agent combobox, which loads agents from the API, is stubbed.
async function openDialog() {
  const { wrapper } = await mountPage(ImportSnapshotsDialog, {
    props: { open: false, destination },
    stubs: { AsyncCombobox: true },
  })
  await wrapper.setProps({ open: true })
  return wrapper
}

// clickImport presses the Import button, which sits outside the form.
async function clickImport(wrapper: Awaited<ReturnType<typeof openDialog>>) {
  await wrapper.findAll('button').find((b) => b.text() === 'Import')!.trigger('click')
  await settle()
}

describe('ImportSnapshotsDialog', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: { found: 2, imported: 2, skipped: 0, failed: 0 } })
  })

  it('requires an agent and the repository password', async () => {
    const wrapper = await openDialog()
    await clickImport(wrapper)

    expect(wrapper.text()).toContain('Please select an agent.')
    expect(wrapper.text()).toContain('Repository password is required.')
    expect(api).not.toHaveBeenCalled()
  })

  it('imports with the selected agent and password', async () => {
    const wrapper = await openDialog()
    wrapper.findComponent({ name: 'AsyncCombobox' }).vm.$emit('update:model-value', 'a1')
    await wrapper.find('#import-dialog-password').setValue('short')
    await clickImport(wrapper)

    expect(api).toHaveBeenCalledWith('/api/v1/destinations/d1/import', {
      method: 'POST',
      body: { agent_id: 'a1', repo_password: 'short' },
    })
    expect(wrapper.emitted('imported')).toHaveLength(1)
  })
})
