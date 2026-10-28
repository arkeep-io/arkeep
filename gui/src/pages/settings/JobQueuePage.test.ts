import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, submitForm } from '@/test/mount'
import { api } from '@/services/api'
import JobQueuePage from './JobQueuePage.vue'

vi.mock('@/services/api', async (importActual) => ({
  ...(await importActual<typeof import('@/services/api')>()),
  api: vi.fn(),
}))

// openPage mounts the page with the stored settings.
async function openPage() {
  vi.mocked(api).mockImplementation((_url: string, opts?: { method?: string }) =>
    Promise.resolve({ data: opts?.method ? {} : { timeout_minutes: 60 } }))
  const { wrapper } = await mountPage(JobQueuePage)
  return wrapper
}

// putBody returns the body of the save request, if any.
function putBody() {
  return vi.mocked(api).mock.calls.find(([, opts]) => opts?.method === 'PUT')?.[1]?.body
}

describe('JobQueuePage', () => {
  beforeEach(() => {
    vi.mocked(api).mockReset()
  })

  it.each([
    ['empty', '', 'A value is required'],
    ['above the maximum', '43201', 'Must be a whole number between 0 and 43200'],
    ['negative', '-1', 'Must be a whole number between 0 and 43200'],
  ])('rejects an %s value and sends nothing', async (_, value, message) => {
    const wrapper = await openPage()
    await wrapper.find('#timeout_minutes').setValue(value)
    await submitForm(wrapper)

    expect(wrapper.text()).toContain(message)
    expect(putBody()).toBeUndefined()
  })

  it('saves 0, which disables the feature', async () => {
    const wrapper = await openPage()
    await wrapper.find('#timeout_minutes').setValue('0')
    await submitForm(wrapper)

    expect(putBody()).toEqual({ timeout_minutes: 0 })
  })

  it('saves the stored settings unchanged', async () => {
    const wrapper = await openPage()
    await submitForm(wrapper)

    expect(putBody()).toEqual({ timeout_minutes: 60 })
  })
})
