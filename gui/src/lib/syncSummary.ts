import type { SyncDestinationResponse } from '@/types'

const plural = (n: number) => (n === 1 ? 'snapshot' : 'snapshots')

// summariseSync turns one or more snapshot sync results into the line shown to
// the user, so a sync that changed nothing reads differently from one that
// removed or added snapshots, and failures are never hidden.
export function summariseSync(results: SyncDestinationResponse[]): string {
  const total = results.reduce(
    (acc, r) => ({
      found: acc.found + r.found,
      imported: acc.imported + r.imported,
      removed: acc.removed + r.removed,
      failed: acc.failed + r.failed,
    }),
    { found: 0, imported: 0, removed: 0, failed: 0 },
  )

  const changes: string[] = []
  if (total.removed > 0) changes.push(`${total.removed} ${plural(total.removed)} removed`)
  if (total.imported > 0) changes.push(`${total.imported} ${plural(total.imported)} added`)
  if (total.failed > 0) changes.push(`${total.failed} could not be recorded — check the server logs`)

  const found = `${total.found} ${plural(total.found)} in the repositor${results.length === 1 ? 'y' : 'ies'}`
  return changes.length > 0
    ? `Sync complete — ${changes.join(', ')}. ${found}.`
    : `Already up to date — ${found}.`
}
