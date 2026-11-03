import type { DestinationLock } from '@/types'

// A destination is held exclusively by a retention sweep or integrity check,
// or shared by any number of backups (issue #319).
function isExclusive(locks: DestinationLock[]): boolean {
  return locks.some((l) => l.mode === 'exclusive')
}

// lockLabel is the badge text for a destination's locks.
export function lockLabel(locks: DestinationLock[]): string {
  if (isExclusive(locks)) return 'Busy'
  return locks.length === 1 ? '1 backup running' : `${locks.length} backups running`
}

// lockExplanation says what the locks mean for other jobs on the destination.
export function lockExplanation(locks: DestinationLock[]): string {
  return isExclusive(locks)
    ? 'other jobs on this destination wait until it finishes'
    : 'more backups can run alongside, retention sweeps and integrity checks wait until they finish'
}
