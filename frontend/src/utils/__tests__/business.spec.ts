import { describe, expect, it } from 'vitest'
import {
  businessBeijingDateKey,
  businessItemTypeLabel,
  businessLineAmount,
  businessMonthKey,
  businessQualityLabel,
  formatBusinessCNY,
  formatBusinessDate,
  formatBusinessMonth,
  formatBusinessPercent,
  parseBusinessMoneyToMinor,
  parseBusinessRateToScaled
} from '../business'

describe('business presentation helpers', () => {
  it('formats exact minor units, basis points, months, and dates', () => {
    expect(formatBusinessCNY(2_408_500)).toBe('¥24,085')
    expect(formatBusinessPercent(9_160)).toBe('91.6%')
    expect(businessMonthKey('2026-08-08T12:00:00+08:00')).toBe('2026-08')
    expect(formatBusinessMonth('2026-08')).toBe('2026年8月')
    expect(formatBusinessDate('2026-08-31T00:00:00+08:00')).toBe('2026-08-31')
  })

  it('derives the operating date in Asia/Shanghai rather than UTC', () => {
    expect(businessBeijingDateKey(new Date('2026-08-08T16:30:00Z'))).toBe('2026-08-09')
  })

  it('parses money and exchange rates without floating-point accumulation', () => {
    expect(parseBusinessMoneyToMinor('150')).toBe(15_000)
    expect(parseBusinessMoneyToMinor(99.99)).toBe(9_999)
    expect(parseBusinessMoneyToMinor('.75')).toBe(75)
    expect(parseBusinessMoneyToMinor('1.005')).toBeNull()
    expect(parseBusinessMoneyToMinor('01')).toBeNull()
    expect(parseBusinessRateToScaled('6.75')).toBe(6_750_000)
    expect(parseBusinessRateToScaled(6.75)).toBe(6_750_000)
    expect(parseBusinessRateToScaled('6.750001')).toBe(6_750_001)
    expect(parseBusinessRateToScaled('0')).toBeNull()
  })

  it('labels data quality and independent revenue line items', () => {
    expect(businessQualityLabel('actual')).toBe('已锁账')
    expect(businessQualityLabel('estimated')).toBe('估算')
    expect(businessItemTypeLabel('revenue_private_subscription')).toBe('客户订阅收入')
    expect(businessItemTypeLabel('revenue_token_package')).toBe('流量包销售收入')
    expect(businessLineAmount({
      item_type: 'cost_direct', source_type: 'cost_item', name: 'Paid account',
      original_amount_minor: 15_000, currency: 'USD', rate_scaled: 6_750_000,
      amount_cny_cents: 101_250, included: true
    })).toBe('US$150')
  })
})
