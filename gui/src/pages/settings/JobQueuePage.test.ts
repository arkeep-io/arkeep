import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mountPage, settle, submitForm } from '@/test/mount'
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

  describe('unsaved changes bar', () => {
    const bar = (wrapper: Awaited<ReturnType<typeof openPage>>) => wrapper.find('[data-testid="unsaved-changes-bar"]')
    const click = async (wrapper: Awaited<ReturnType<typeof openPage>>, text: string) => {
      await bar(wrapper).findAll('button').find((b) => b.text() === text)!.trigger('click')
      await settle()
    }

    it('appears only once a value changes', async () => {
      const wrapper = await openPage()
      expect(bar(wrapper).exists()).toBe(false)

      await wrapper.find('#timeout_minutes').setValue('90')
      await settle()
      expect(bar(wrapper).text()).toContain('You have unsaved changes.')
    })

    it('discards the changes back to the saved values', async () => {
      const wrapper = await openPage()
      await wrapper.find('#timeout_minutes').setValue('90')
      await settle()

      await click(wrapper, 'Discard')
      expect((wrapper.find('#timeout_minutes').element as HTMLInputElement).value).toBe('60')
      expect(bar(wrapper).exists()).toBe(false)
      expect(putBody()).toBeUndefined()
    })

    it('saves from the bar, which then goes away', async () => {
      const wrapper = await openPage()
      await wrapper.find('#timeout_minutes').setValue('90')
      await settle()

      await click(wrapper, 'Save')
      expect(putBody()).toEqual({ timeout_minutes: 90 })
      expect(bar(wrapper).exists()).toBe(false)
      expect(wrapper.text()).toContain('Job queue settings saved.')
    })

    it('keeps the changes and shows the error when the save fails', async () => {
      const wrapper = await openPage()
      vi.mocked(api).mockImplementation((_url: string, opts?: { method?: string }) =>
        opts?.method ? Promise.reject(new Error('database is locked')) : Promise.resolve({ data: { timeout_minutes: 60 } }))
      await wrapper.find('#timeout_minutes').setValue('90')
      await settle()

      await click(wrapper, 'Save')
      expect(bar(wrapper).text()).toContain('database is locked')
      expect((wrapper.find('#timeout_minutes').element as HTMLInputElement).value).toBe('90')
    })
  })
})
