import { describe, expect, it } from 'vitest'
import { buildRequest, changeSummary, emptyRepairForm, formatParams, isDestructive, parseList, previewKey, repairFormSchema } from './repair'

const pack = '0606cc8ce3912abf19fecc7546fb74a5ce00651b833fb00a35a8db471aad1bc0'

describe('parseList', () => {
  it('splits on spaces, commas and new lines', () => {
    expect(parseList(' a, b\nc  d,,')).toEqual(['a', 'b', 'c', 'd'])
    expect(parseList('')).toEqual([])
  })
})

describe('buildRequest', () => {
  it('previews repair-snapshots as a dry run and applies it only when asked', () => {
    const form = { ...emptyRepairForm('repair-snapshots'), forget: true, hosts: 'helios selene', snapshotIds: 'fea2c19e' }
    expect(buildRequest(form, false)).toEqual({ action: 'repair-snapshots', hosts: ['helios', 'selene'], snapshot_ids: ['fea2c19e'] })
    expect(buildRequest(form, true)).toEqual({
      action: 'repair-snapshots', apply: true, forget: true, hosts: ['helios', 'selene'], snapshot_ids: ['fea2c19e'],
    })
  })

  it('previews repair-packs with find-packs on the same packs', () => {
    const form = { ...emptyRepairForm('repair-packs'), packIds: pack }
    expect(buildRequest(form, false)).toEqual({ action: 'find-packs', pack_ids: [pack] })
    expect(buildRequest(form, true)).toEqual({ action: 'repair-packs', pack_ids: [pack] })
  })

  it('only sends the options of the chosen action', () => {
    const form = { ...emptyRepairForm('check'), readAllPacks: true, hosts: 'helios', readData: true }
    expect(buildRequest(form, true)).toEqual({ action: 'check', read_data: true })
    expect(buildRequest({ ...form, action: 'repair-index' }, true)).toEqual({ action: 'repair-index', read_all_packs: true })
    expect(buildRequest({ ...form, action: 'unlock' }, true)).toEqual({ action: 'unlock' })
  })
})

describe('isDestructive', () => {
  it('matches the server: repair-snapshots with forget, and repair-packs', () => {
    expect(isDestructive({ action: 'repair-snapshots' })).toBe(false)
    expect(isDestructive({ action: 'repair-snapshots', apply: true })).toBe(false)
    expect(isDestructive({ action: 'repair-snapshots', apply: true, forget: true })).toBe(true)
    expect(isDestructive({ action: 'repair-packs', pack_ids: [pack] })).toBe(true)
    expect(isDestructive({ action: 'find-packs', pack_ids: [pack] })).toBe(false)
    expect(isDestructive({ action: 'check', read_data: true })).toBe(false)
  })
})

describe('previewKey', () => {
  it('changes when the request changes, not with options the preview ignores', () => {
    const form = { ...emptyRepairForm('repair-snapshots'), hosts: 'helios' }
    expect(previewKey({ ...form, forget: true })).toBe(previewKey(form))
    expect(previewKey({ ...form, hosts: 'selene' })).not.toBe(previewKey(form))
  })
})

describe('changeSummary and formatParams', () => {
  it('explains what applying changes', () => {
    expect(changeSummary({ action: 'repair-snapshots', apply: true, forget: true })).toContain('original snapshots are removed')
    expect(changeSummary({ action: 'repair-packs', pack_ids: [pack] })).toContain('replaced')
  })

  it('lists the parameters of a repair job', () => {
    expect(formatParams({ action: 'repair-snapshots', hosts: ['helios'] })).toEqual(['Action: repair-snapshots', 'Dry run', 'Hosts: helios'])
  })
})

describe('repairFormSchema', () => {
  const valid = (form: object) => repairFormSchema.safeParse(form).success

  it('mirrors the server rules on IDs, hosts and tags', () => {
    expect(valid({ ...emptyRepairForm('repair-packs'), packIds: pack })).toBe(true)
    expect(valid({ ...emptyRepairForm('repair-packs'), packIds: '' })).toBe(false)
    expect(valid({ ...emptyRepairForm('repair-packs'), packIds: pack.toUpperCase() })).toBe(false)
    expect(valid({ ...emptyRepairForm('repair-snapshots'), snapshotIds: 'fea2c19e', hosts: 'helios', tags: 'policy:1' })).toBe(true)
    expect(valid({ ...emptyRepairForm('repair-snapshots'), snapshotIds: 'fea2c19' })).toBe(false)
    expect(valid({ ...emptyRepairForm('repair-snapshots'), hosts: '-x' })).toBe(false)
    expect(valid({ ...emptyRepairForm('repair-snapshots'), tags: 'a,b' })).toBe(true) // two tags once split
    expect(valid({ ...emptyRepairForm('repair-snapshots'), tags: 'a;b' })).toBe(false)
    expect(valid({ ...emptyRepairForm('repair-snapshots'), hosts: Array(21).fill('h').join(' ') })).toBe(false)
  })
})
