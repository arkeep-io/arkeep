import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import type { Snapshot } from '@/types'
import RestoreSheet from './RestoreSheet.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

const snapshot = {
  id: 's1',
  agent_id: 'a1',
  agent_name: 'host-1',
  restic_snapshot_id: 'abcdef1234',
  destination_type: 's3',
  destination_deleted: false,
  repo_password_required: false,
} as Snapshot

// openSheet mounts the sheet closed and opens it, as the parent does, so the
// open watcher prefills the form. The agent combobox, which loads agents from
// the API, is stubbed.
async function openSheet(overrides: Partial<Snapshot> = {}) {
  const { wrapper, router } = await mountPage(RestoreSheet, {
    props: { open: false, snapshot: { ...snapshot, ...overrides } },
    stubs: { AsyncCombobox: true },
  })
  await wrapper.setProps({ open: true })
  return { wrapper, router }
}

// restoreBody returns the parsed body of the restore request.
function restoreBody() {
  const call = vi.mocked(api).mock.calls.find(([url]) => url === '/api/v1/snapshots/s1/restore')
  return call ? JSON.parse(call[1]!.body as string) : undefined
}

describe('RestoreSheet', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset().mockResolvedValue({ data: { job_id: 'j1' } })
  })

  it('restores to the original agent and default path', async () => {
    const { wrapper, router } = await openSheet()
    await submitForm(wrapper)

    expect(restoreBody()).toEqual({ agent_id: 'a1', target_path: '/tmp/arkeep-restore' })
    expect(router.push).toHaveBeenCalledWith({ name: 'job-detail', params: { id: 'j1' } })
  })

  it('requires a target agent', async () => {
    const { wrapper } = await openSheet({ agent_id: '' })
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Please select a target agent.')
    expect(restoreBody()).toBeUndefined()
  })

  it('requires a target path in custom mode', async () => {
    const { wrapper } = await openSheet()
    await wrapper.find('#target-path').setValue('  ')
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Target path is required.')
    expect(restoreBody()).toBeUndefined()
  })

  it('requires the credentials of a deleted destination', async () => {
    const { wrapper } = await openSheet({ destination_deleted: true, repo_password_required: true })
    await submitForm(wrapper)

    expect(wrapper.text()).toContain('Access key is required.')
    expect(wrapper.text()).toContain('Secret key is required.')
    expect(wrapper.text()).toContain('Repository password is required.')
    expect(restoreBody()).toBeUndefined()
  })

  it('sends the credentials of a deleted destination', async () => {
    const { wrapper } = await openSheet({ destination_deleted: true, repo_password_required: true })
    await wrapper.find('#restore-access-key').setValue('AK')
    await wrapper.find('#restore-secret-key').setValue('SK')
    await wrapper.find('#restore-repo-password').setValue('repo-pw')
    await submitForm(wrapper)

    expect(restoreBody()).toEqual({
      agent_id: 'a1',
      target_path: '/tmp/arkeep-restore',
      credentials: { access_key: 'AK', secret_key: 'SK' },
      repo_password: 'repo-pw',
    })
  })
})
