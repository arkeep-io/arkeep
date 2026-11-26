import { describe, expect, it } from 'vitest'
import { describeCron, scheduleLabel } from './cron'

describe('describeCron', () => {
  it.each([
    ['daily', '0 2 * * *', 'At 02:00'],
    ['hourly', '0 * * * *', 'Every hour'],
    ['weekly', '0 2 * * 0', 'At 02:00, only on Sunday'],
    ['monthly', '0 2 1 * *', 'At 02:00, on day 1 of the month'],
    ['step', '*/15 * * * *', 'Every 15 minutes'],
    ['day names', '30 4 * * MON-FRI', 'At 04:30, Monday through Friday'],
    ['surrounding blanks', '  0 2 * * *  ', 'At 02:00'],
    ['descriptor', '@daily', 'At 00:00'],
    ['@every', '@every 1h30m', 'Every 1h30m'],
    ['CRON_TZ prefix', 'CRON_TZ=Europe/Rome 0 2 * * *', 'At 02:00 (Europe/Rome)'],
    ['TZ prefix', 'TZ=UTC @every 6h', 'Every 6h (UTC)'],
  ])('describes %s', (_, expr, want) => {
    expect(describeCron(expr)).toBe(want)
  })

  // Everything cronstrue would describe but the server's parser rejects.
  it.each([
    ['empty', ''],
    ['blank', '   '],
    ['garbage', 'foo'],
    ['too few fields', '0 2 * *'],
    ['seconds field', '0 0 2 * * *'],
    ['unknown descriptor', '@fortnightly'],
    ['last day of month', '0 2 L * *'],
    ['nearest weekday', '0 2 15W * *'],
    ['last weekday', '0 2 * * 5L'],
    ['nth weekday', '0 2 * * 0#1'],
    ['out of range', '0 25 * * *'],
  ])('rejects %s', (_, expr) => {
    expect(describeCron(expr)).toBeNull()
  })
})

describe('scheduleLabel', () => {
  it('falls back to the raw expression', () => {
    expect(scheduleLabel('0 2 * * *')).toBe('At 02:00')
    expect(scheduleLabel('0 2 L * *')).toBe('0 2 L * *')
    expect(scheduleLabel('')).toBe('')
  })
})
