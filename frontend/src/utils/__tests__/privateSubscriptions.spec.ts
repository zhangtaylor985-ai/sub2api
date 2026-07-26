import { describe, expect, it } from 'vitest'
import {
  formatCNYFromCents,
  parseCNYToCents,
  PRIVATE_SUBSCRIPTION_MAX_AMOUNT_CENTS
} from '../privateSubscriptions'

describe('private subscription amount helpers', () => {
  it.each([
    ['0', 0],
    ['0.5', 50],
    ['.75', 75],
    ['12.30', 1230],
    ['999999999.99', 99_999_999_999]
  ])('parses %s yuan without floating-point drift', (input, expected) => {
    expect(parseCNYToCents(input)).toBe(expected)
  })

  it.each(['', '-1', '01', '1.234', '1,000', 'abc', '1000000000'])(
    'rejects invalid amount %s',
    (input) => {
      expect(parseCNYToCents(input)).toBeNull()
    }
  )

  it('keeps the frontend limit aligned with the backend', () => {
    expect(PRIVATE_SUBSCRIPTION_MAX_AMOUNT_CENTS).toBe(99_999_999_999)
  })

  it('formats integer cents as CNY', () => {
    expect(formatCNYFromCents(123_456)).toBe('¥1,234.56')
  })
})
