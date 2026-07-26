export const PRIVATE_SUBSCRIPTION_MAX_AMOUNT_CENTS = 99_999_999_999

export function parseCNYToCents(value: string): number | null {
  const normalized = value.trim()
  const match = /^(?:(0|[1-9]\d*)(?:\.(\d{1,2}))?|\.(\d{1,2}))$/.exec(normalized)
  if (!match) return null

  const whole = Number(match[1] ?? '0')
  const fractional = (match[2] ?? match[3] ?? '').padEnd(2, '0')
  const cents = whole * 100 + Number(fractional || '0')

  if (
    !Number.isSafeInteger(cents) ||
    cents < 0 ||
    cents > PRIVATE_SUBSCRIPTION_MAX_AMOUNT_CENTS
  ) {
    return null
  }
  return cents
}

export function formatCNYFromCents(cents: number): string {
  if (!Number.isSafeInteger(cents)) return '¥0.00'

  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    currencyDisplay: 'narrowSymbol',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  }).format(cents / 100)
}
