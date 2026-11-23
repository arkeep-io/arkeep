import cronstrue from 'cronstrue'

// The server parses schedules with robfig/cron's standard parser
// (server/internal/api/policies.go): five fields, the @-descriptors and an
// optional CRON_TZ=/TZ= prefix. cronstrue is more permissive (seconds and
// year fields, Quartz L/W/#), so describeCron rejects what the server would,
// rather than describing a schedule that can never be saved.

const DESCRIPTORS: Record<string, string> = {
  '@yearly': '0 0 1 1 *',
  '@annually': '0 0 1 1 *',
  '@monthly': '0 0 1 * *',
  '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *',
  '@midnight': '0 0 * * *',
  '@hourly': '0 * * * *',
}

const TZ_PREFIX = /^(?:CRON_TZ|TZ)=(\S+)\s+/

/**
 * Returns a human-readable description of a cron expression ("At 02:00, only
 * on Sunday"), or null when the expression is empty or not one the server
 * accepts.
 */
export function describeCron(expression: string): string | null {
  let expr = expression.trim()
  if (!expr) return null

  let zone = ''
  const tz = TZ_PREFIX.exec(expr)
  if (tz) {
    zone = tz[1]!
    expr = expr.slice(tz[0].length)
  }
  const suffix = zone ? ` (${zone})` : ''

  const every = /^@every\s+(\S+)$/.exec(expr)
  if (every) return `Every ${every[1]}${suffix}`

  if (expr.startsWith('@')) {
    const mapped = DESCRIPTORS[expr]
    if (!mapped) return null
    expr = mapped
  }

  const fields = expr.split(/\s+/)
  if (fields.length !== 5) return null
  const [, , dom, , dow] = fields
  if (expr.includes('#') || /[LW]/i.test(dom!) || /L/i.test(dow!)) return null

  try {
    return cronstrue.toString(expr, { use24HourTimeFormat: true }) + suffix
  } catch {
    return null
  }
}

/** describeCron with the raw expression as fallback, for read-only labels. */
export function scheduleLabel(expression: string): string {
  return describeCron(expression) ?? expression
}
