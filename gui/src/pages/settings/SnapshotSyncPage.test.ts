import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import SnapshotSyncPage from './SnapshotSyncPage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

// openPage mounts the page with the stored settings.
async function openPage() {
  vi.mocked(api).mockImplementation((_url: string, opts?: { method?: string }) =>
    Promise.resolve({ data: opts?.method ? {} : { interval_hours: 24 } }))
  const { wrapper } = await mountPage(SnapshotSyncPage)
  return wrapper
}

// putBody returns the body of the save request, if any.
function putBody() {
  return vi.mocked(api).mock.calls.find(([, opts]) => opts?.method === 'PUT')?.[1]?.body
}

describe('SnapshotSyncPage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it.each([
    ['empty', '', 'A value is required'],
    ['above the maximum', '721', 'Must be a whole number between 0 and 720'],
    ['negative', '-1', 'Must be a whole number between 0 and 720'],
  ])('rejects an %s value and sends nothing', async (_, value, message) => {
    const wrapper = await openPage()
    await wrapper.find('#interval_hours').setValue(value)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain(message)
    expect(putBody()).toBeUndefined()
  })

  it('saves 0, which disables the feature', async () => {
    const wrapper = await openPage()
    await wrapper.find('#interval_hours').setValue('0')
    await submitForm(wrapper)

    expect(putBody()).toEqual({ interval_hours: 0 })
  })

  it('saves the stored settings unchanged', async () => {
    const wrapper = await openPage()
    await submitForm(wrapper)

    expect(putBody()).toEqual({ interval_hours: 24 })
  })
})
