import { describe, expect, it } from 'vitest'
import { calculatePlanPackagePeriod } from '@/utils/planPackageSchedule'

describe('calculatePlanPackagePeriod', () => {
  it('starts a same-plan purchase immediately while extending the existing expiry', () => {
    const now = new Date('2026-08-22T00:00:00+08:00')
    const result = calculatePlanPackagePeriod({
      now,
      months: 1,
      existingExpiresAt: [new Date('2026-09-15T00:00:00+08:00')]
    })

    expect(result.startsAt.toISOString()).toBe(now.toISOString())
    expect(result.expiresAt.toISOString()).toBe(new Date('2026-10-15T00:00:00+08:00').toISOString())
    expect(result.extendsExistingPeriod).toBe(true)
  })

  it('starts and expires from now when no same-plan period remains', () => {
    const now = new Date('2026-08-22T00:00:00+08:00')
    const result = calculatePlanPackagePeriod({
      now,
      months: 1,
      existingExpiresAt: [new Date('2026-08-15T00:00:00+08:00')]
    })

    expect(result.startsAt.toISOString()).toBe(now.toISOString())
    expect(result.expiresAt.toISOString()).toBe(new Date('2026-09-22T00:00:00+08:00').toISOString())
    expect(result.extendsExistingPeriod).toBe(false)
  })
})
