import type { BusinessDataQuality, BusinessLineItem } from '@/api/admin/business'

const BUSINESS_MAX_MINOR = 99_999_999_999
const BUSINESS_MAX_RATE_SCALED = 1_000_000_000_000

function parseBusinessFixedDecimal(
  value: string | number,
  decimalPlaces: number,
  maximum: number
): number | null {
  const normalized = String(value).trim()
  const match = normalized.match(new RegExp(`^(0|[1-9]\\d*)?(?:\\.(\\d{1,${decimalPlaces}}))?$`))
  if (!match || (!match[1] && !match[2])) return null
  const whole = match[1] || '0'
  const fraction = (match[2] || '').padEnd(decimalPlaces, '0')
  const scaled = BigInt(whole) * (10n ** BigInt(decimalPlaces)) + BigInt(fraction || '0')
  if (scaled > BigInt(maximum) || scaled > BigInt(Number.MAX_SAFE_INTEGER)) return null
  return Number(scaled)
}

export function parseBusinessMoneyToMinor(value: string | number): number | null {
  return parseBusinessFixedDecimal(value, 2, BUSINESS_MAX_MINOR)
}

export function parseBusinessRateToScaled(value: string | number): number | null {
  const scaled = parseBusinessFixedDecimal(value, 6, BUSINESS_MAX_RATE_SCALED)
  return scaled === null || scaled <= 0 ? null : scaled
}

export function formatBusinessCNY(cents: number | null | undefined): string {
  const value = Number.isFinite(Number(cents)) ? Number(cents) : 0
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    minimumFractionDigits: 0,
    maximumFractionDigits: 2
  }).format(value / 100)
}

export function formatBusinessPercent(bps: number | null | undefined): string {
  const value = Number.isFinite(Number(bps)) ? Number(bps) : 0
  return `${(value / 100).toFixed(1)}%`
}

export function businessMonthKey(value: string | Date): string {
  if (typeof value === 'string') {
    const match = value.match(/^(\d{4})-(\d{2})/)
    if (match) return `${match[1]}-${match[2]}`
  }
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
}

export function businessBeijingDateKey(value: Date = new Date()): string {
  if (Number.isNaN(value.getTime())) return ''
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }).formatToParts(value)
  const byType = Object.fromEntries(parts.map((part) => [part.type, part.value]))
  return `${byType.year}-${byType.month}-${byType.day}`
}

export function formatBusinessMonth(value: string | Date): string {
  const month = businessMonthKey(value)
  if (!month) return '—'
  const [year, monthNumber] = month.split('-')
  return `${year}年${Number(monthNumber)}月`
}

export function formatBusinessDate(value?: string): string {
  if (!value) return '—'
  const match = value.match(/^(\d{4})-(\d{2})-(\d{2})/)
  return match ? `${match[1]}-${match[2]}-${match[3]}` : value
}

export function businessQualityLabel(quality: BusinessDataQuality | string): string {
  const labels: Record<string, string> = {
    live: '实时',
    actual: '已锁账',
    estimated: '估算',
    manual: '人工修正'
  }
  return labels[quality] ?? quality
}

export function businessItemTypeLabel(type: string): string {
  const labels: Record<string, string> = {
    revenue_api_key: 'API Key 收入',
    revenue_token_package: '流量包销售收入',
    revenue_private_subscription: '客户订阅收入',
    excluded_api_key: '已排除 Key',
    cost_direct: '直接成本',
    cost_operating: '运营费用'
  }
  return labels[type] ?? type
}

export function businessLineAmount(item: BusinessLineItem): string {
  if (item.currency === 'CNY') return formatBusinessCNY(item.original_amount_minor)
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: item.currency,
    minimumFractionDigits: 0,
    maximumFractionDigits: 2
  }).format(item.original_amount_minor / 100)
}
