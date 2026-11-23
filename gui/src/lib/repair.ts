// Repository repairs (ADR 0026): what each action does, how its form maps to
// the request, and which requests change the repository for good. The server
// validates every request again (shared/repair).
import type { RepairAction, RepairParams } from '@/types'

export interface RepairActionInfo {
  action: RepairAction
  label: string
  description: string
  // previewFirst: the action changes the repository for good, so the GUI
  // runs a read-only preview of the same request before offering it.
  previewFirst: boolean
}

// The actions offered in the GUI, in the order of restic's procedure for a
// damaged repository. find-packs is not listed: it is the preview of
// repair-packs.
export const repairActions: RepairActionInfo[] = [
  {
    action: 'check',
    label: 'Check',
    description:
      'Check the repository without changing it, to find damaged or missing data. Reading all data verifies every pack file, which can take a long time on a large repository.',
    previewFirst: false,
  },
  {
    action: 'unlock',
    label: 'Remove stale locks',
    description:
      'Remove the locks left by a restic process that was killed or crashed. Locks of operations that are still running are kept.',
    previewFirst: false,
  },
  {
    action: 'repair-index',
    label: 'Repair index',
    description:
      'Rebuild the index from the pack files actually present, so it no longer refers to lost packs. Reading all packs also rebuilds the entries of damaged ones.',
    previewFirst: false,
  },
  {
    action: 'repair-snapshots',
    label: 'Repair snapshots',
    description:
      'Rewrite the snapshots that refer to lost data, leaving out the files that cannot be restored. A dry run lists what would change; applying it saves repaired copies of those snapshots.',
    previewFirst: true,
  },
  {
    action: 'repair-packs',
    label: 'Repair packs',
    description:
      'Salvage what can still be read from damaged pack files and replace them. The preview lists the files and snapshots that use those packs.',
    previewFirst: true,
  },
]

export function actionInfo(action: RepairAction): RepairActionInfo | undefined {
  return repairActions.find((a) => a.action === action)
}

// parseList splits what an admin typed or pasted (spaces, commas, new lines)
// into the values of a list field.
export function parseList(text: string): string[] {
  return text.split(/[\s,]+/).filter((v) => v !== '')
}

// The fields of the repair form, before they become a request.
export interface RepairForm {
  action: RepairAction
  readAllPacks: boolean
  readData: boolean
  forget: boolean
  hosts: string
  tags: string
  snapshotIds: string
  packIds: string
}

export function emptyRepairForm(action: RepairAction = 'check'): RepairForm {
  return { action, readAllPacks: false, readData: false, forget: false, hosts: '', tags: '', snapshotIds: '', packIds: '' }
}

// buildRequest turns the form into the request for the action itself (apply
// true) or for its read-only preview (apply false): repair-snapshots as a
// dry run, repair-packs as find-packs on the same packs.
export function buildRequest(form: RepairForm, apply: boolean): RepairParams {
  switch (form.action) {
    case 'repair-index':
      return form.readAllPacks ? { action: 'repair-index', read_all_packs: true } : { action: 'repair-index' }
    case 'check':
      return form.readData ? { action: 'check', read_data: true } : { action: 'check' }
    case 'repair-snapshots': {
      const p: RepairParams = { action: 'repair-snapshots' }
      if (apply) {
        p.apply = true
        if (form.forget) p.forget = true
      }
      const hosts = parseList(form.hosts)
      const tags = parseList(form.tags)
      const ids = parseList(form.snapshotIds)
      if (hosts.length) p.hosts = hosts
      if (tags.length) p.tags = tags
      if (ids.length) p.snapshot_ids = ids
      return p
    }
    case 'repair-packs':
    case 'find-packs':
      return { action: apply ? 'repair-packs' : 'find-packs', pack_ids: parseList(form.packIds) }
    default:
      return { action: form.action }
  }
}

// isDestructive mirrors shared/repair.Params.Destructive: the requests that
// need the destination's name to be confirmed.
export function isDestructive(p: RepairParams): boolean {
  return (p.action === 'repair-snapshots' && !!p.apply && !!p.forget) || p.action === 'repair-packs'
}

// previewKey identifies the request a preview was run for: applying is only
// offered after a successful preview of the very same request.
export function previewKey(form: RepairForm): string {
  return JSON.stringify(buildRequest(form, false))
}

// changeSummary says, for the confirmation dialog, what applying the request
// changes in the repository.
export function changeSummary(p: RepairParams): string {
  switch (p.action) {
    case 'repair-snapshots':
      return p.forget
        ? 'The snapshots that refer to lost data are replaced by repaired copies without the files that cannot be restored, and the original snapshots are removed. Those files cannot be restored from this repository afterwards.'
        : 'Repaired copies of the snapshots that refer to lost data are added, without the files that cannot be restored. The original snapshots are kept.'
    case 'repair-packs':
      return 'The damaged pack files are replaced by new ones holding the data that could still be read. Data that could not be read is gone for good: run Repair snapshots afterwards so the snapshots stop referring to it.'
    default:
      return ''
  }
}

// formatParams lists a repair's parameters on the job page.
export function formatParams(p: RepairParams): string[] {
  const out: string[] = [`Action: ${p.action}`]
  if (p.action === 'repair-snapshots') out.push(p.apply ? (p.forget ? 'Applied, originals removed (--forget)' : 'Applied') : 'Dry run')
  if (p.read_all_packs) out.push('Read all packs')
  if (p.read_data) out.push('Read all data')
  if (p.hosts?.length) out.push(`Hosts: ${p.hosts.join(', ')}`)
  if (p.tags?.length) out.push(`Tags: ${p.tags.join(', ')}`)
  if (p.snapshot_ids?.length) out.push(`Snapshots: ${p.snapshot_ids.join(', ')}`)
  if (p.pack_ids?.length) out.push(`Packs: ${p.pack_ids.join(', ')}`)
  return out
}
