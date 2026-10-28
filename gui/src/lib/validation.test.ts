import { describe, expect, it } from 'vitest'
import { integerInRange, isHttpURL, MIN_PASSWORD_LENGTH, passwordSchema } from './validation'

// firstMessage returns the first validation error of a failed parse, or
// undefined when the value is valid.
function firstMessage(result: { success: boolean; error?: { issues: { message: string }[] } }) {
  return result.success ? undefined : result.error?.issues[0]?.message
}

describe('passwordSchema', () => {
  it.each([
    ['too short', 'a'.repeat(MIN_PASSWORD_LENGTH - 1), 'Password must be at least 8 characters'],
    ['minimum length', 'a'.repeat(MIN_PASSWORD_LENGTH), undefined],
  ])('%s', (_, value, want) => {
    expect(firstMessage(passwordSchema.safeParse(value))).toBe(want)
  })
})

describe('integerInRange', () => {
  const schema = integerInRange(0, 720)
  const outOfRange = 'Must be a whole number between 0 and 720'

  it.each([
    ['empty input is required', '', 'A value is required'],
    ['undefined is required', undefined, 'A value is required'],
    ['zero is accepted', '0', undefined],
    ['number from a number input', 24, undefined],
    ['upper bound', '720', undefined],
    ['above the range', '721', outOfRange],
    ['below the range', '-1', outOfRange],
    ['decimal', '1.5', outOfRange],
    ['not a number', 'abc', outOfRange],
  ])('%s', (_, value, want) => {
    expect(firstMessage(schema.safeParse(value))).toBe(want)
  })

  it('returns the value as a number', () => {
    expect(schema.parse('24')).toBe(24)
  })
})

describe('isHttpURL', () => {
  it.each([
    ['https://hooks.example.com/arkeep', true],
    ['http://10.0.0.1:8080/hook', true],
    ['ftp://example.com', false],
    ['hooks.example.com', false],
    ['', false],
  ])('%s', (value, want) => {
    expect(isHttpURL(value)).toBe(want)
  })
})
