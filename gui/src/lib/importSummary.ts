import type { ImportDestinationResponse } from '@/types'

export interface ImportSummary {
  // tone drives the icon: an error for snapshots that could not be recorded,
  // neutral for an empty repository, success otherwise.
  tone: 'success' | 'neutral' | 'error'
  headline: string
  detail: string
}

const plural = (n: number) => (n === 1 ? 'snapshot' : 'snapshots')

// summariseImport turns an import result into the text shown to the user.
// It is deliberately explicit about every case so the UI never claims snapshots
// were imported when none were: an empty repository, snapshots already known,
// and a failed insert all read differently.
export function summariseImport(r: ImportDestinationResponse): ImportSummary {
  if (r.failed > 0) {
    return {
      tone: 'error',
      headline: `${r.failed} ${plural(r.failed)} could not be imported`,
      detail: `${r.found} found, ${r.imported} imported, ${r.failed} failed. Check the server logs for the cause.`,
    }
  }
  if (r.found === 0) {
    return {
      tone: 'neutral',
      headline: 'Repository is empty',
      detail: 'No existing snapshots were found in this repository.',
    }
  }
  if (r.imported === 0) {
    return {
      tone: 'success',
      headline: 'Already up to date',
      detail: `${r.found} ${plural(r.found)} found, all of them already known for this destination.`,
    }
  }
  return {
    tone: 'success',
    headline: `${r.imported} ${plural(r.imported)} imported`,
    detail:
      r.skipped > 0
        ? `${r.found} found, ${r.imported} new, ${r.skipped} already known.`
        : `${r.found} found, ${r.imported} new.`,
  }
}
