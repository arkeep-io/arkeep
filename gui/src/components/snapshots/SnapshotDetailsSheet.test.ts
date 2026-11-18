import { describe, expect, it } from 'vitest'
import { mountPage } from '@/test/mount'
import type { Snapshot } from '@/types'
import SnapshotDetailsSheet from './SnapshotDetailsSheet.vue'

const snapshot = {
  id: 's1',
  policy_name: 'Daily server backup',
  destination_name: 'NAS',
  restic_snapshot_id: '8725bff10a20eaab',
  size_bytes: 2048,
  created_at: '2026-11-17T08:47:37Z',
  summary: {
    backup_start: '2026-11-17T08:46:07Z',
    backup_end: '2026-11-17T08:47:37Z',
    files_new: 12,
    files_changed: 3,
    files_unmodified: 1500,
    total_files_processed: 1515,
    total_bytes_processed: 5 * 1024 * 1024,
    data_added: 3 * 1024,
  },
} as Snapshot

async function open(s: Snapshot) {
  const { wrapper } = await mountPage(SnapshotDetailsSheet, { props: { open: true, snapshot: s } })
  return wrapper
}

// rows returns the label/value pairs of the section titled title.
function rows(wrapper: Awaited<ReturnType<typeof open>>, title: string): Record<string, string> {
  const section = wrapper.findAll('section').find((s) => s.find('h3').text() === title)
  if (!section) return {}
  const labels = section.findAll('dt').map((d) => d.text())
  const values = section.findAll('dd').map((d) => d.text())
  return Object.fromEntries(labels.map((l, i) => [l, values[i]]))
}

describe('SnapshotDetailsSheet', () => {
  it('shows every figure of the restic summary', async () => {
    const wrapper = await open(snapshot)

    expect(rows(wrapper, 'Snapshot')['Snapshot ID']).toBe('8725bff10a20eaab')
    expect(rows(wrapper, 'Backup').Duration).toBe('1m 30s')
    expect(rows(wrapper, 'Data')).toEqual({
      Processed: '5.0 MB',
      Added: '3.0 KB',
      'Stored (compressed)': '2.0 KB',
    })
    expect(rows(wrapper, 'Files')).toEqual({
      Processed: (1515).toLocaleString(),
      New: '12',
      Changed: '3',
      Unmodified: (1500).toLocaleString(),
    })
    expect(wrapper.find('[data-testid="no-summary"]').exists()).toBe(false)
  })

  it('shows only the recorded size when the summary is unknown', async () => {
    const wrapper = await open({ ...snapshot, summary: null })

    expect(wrapper.find('[data-testid="no-summary"]').exists()).toBe(true)
    expect(rows(wrapper, 'Backup')).toEqual({})
    expect(rows(wrapper, 'Files')).toEqual({})
    expect(rows(wrapper, 'Data')).toEqual({ 'Stored (compressed)': '2.0 KB' })
  })
})
