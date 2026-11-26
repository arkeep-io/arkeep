import { describe, expect, it } from 'vitest'
import { progressPercent } from './jobUtils'

describe('progressPercent', () => {
    it('converts restic fraction to a percentage', () => {
        expect(progressPercent(0)).toBe(0)
        expect(progressPercent(0.5)).toBe(50)
        expect(progressPercent(1)).toBe(100)
    })

    it('caps at 100 when restic reads more than it scanned (#494)', () => {
        expect(progressPercent(1.48)).toBe(100)
    })

    it('treats negative or non-finite values as 0', () => {
        expect(progressPercent(-0.1)).toBe(0)
        expect(progressPercent(Number.NaN)).toBe(0)
        expect(progressPercent(undefined)).toBe(0)
    })
})
