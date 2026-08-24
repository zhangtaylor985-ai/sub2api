export interface PlanPackagePeriodInput {
  now: Date
  months: number
  existingExpiresAt: Date[]
}

export interface PlanPackagePeriod {
  startsAt: Date
  expiresAt: Date
  extendsExistingPeriod: boolean
}

export const addCalendarMonthsClamped = (value: Date, months: number) => {
  const result = new Date(value.getTime())
  const originalDay = result.getDate()
  result.setDate(1)
  result.setMonth(result.getMonth() + months)
  const lastDay = new Date(result.getFullYear(), result.getMonth() + 1, 0).getDate()
  result.setDate(Math.min(originalDay, lastDay))
  return result
}

export const calculatePlanPackagePeriod = ({
  now,
  months,
  existingExpiresAt
}: PlanPackagePeriodInput): PlanPackagePeriod => {
  const validFutureExpiries = existingExpiresAt.filter(
    (date) => !Number.isNaN(date.getTime()) && date.getTime() > now.getTime()
  )
  const expiryBase = validFutureExpiries.length > 0
    ? new Date(Math.max(...validFutureExpiries.map((date) => date.getTime())))
    : now

  return {
    startsAt: now,
    expiresAt: addCalendarMonthsClamped(expiryBase, months),
    extendsExistingPeriod: expiryBase.getTime() > now.getTime()
  }
}
