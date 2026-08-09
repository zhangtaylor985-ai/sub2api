import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn()
}))

vi.mock('@/api/client', () => ({ apiClient: client }))

import {
  closeMonth,
  createCost,
  deleteCost,
  getCurrent,
  getHistory,
  getMonth,
  getReconciliation,
  getReferences,
  initializeDefaults,
  listCosts,
  listExchangeRates,
  listPricingRules,
  refreshExchangeRate,
  updateCost,
  upsertAPIKeyConfig,
  upsertExchangeRate,
  upsertPricingRule,
  type BusinessCostPayload
} from '@/api/admin/business'

describe('admin business API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.post.mockReset()
    client.put.mockReset()
    client.delete.mockReset()
  })

  it('uses the admin-only dashboard and reconciliation read endpoints', async () => {
    client.get.mockResolvedValue({ data: { marker: 'ok' } })

    await getCurrent()
    await getHistory()
    await getMonth('2026-07')
    await getReferences()
    await getReconciliation()
    await listCosts()
    await listPricingRules()
    await listExchangeRates('2026-08')

    expect(client.get.mock.calls.map(([path]) => path)).toEqual([
      '/admin/business/dashboard/current',
      '/admin/business/dashboard/history',
      '/admin/business/dashboard/months/2026-07',
      '/admin/business/references',
      '/admin/business/reconciliation',
      '/admin/business/costs',
      '/admin/business/pricing-rules',
      '/admin/business/exchange-rates/2026-08'
    ])
  })

  it('sends exact minor-unit cost, rate, pricing, refresh, initialization, and close payloads', async () => {
    client.post.mockResolvedValue({ data: {} })
    client.put.mockResolvedValue({ data: {} })
    client.delete.mockResolvedValue({ data: { message: 'ok' } })
    const cost: BusinessCostPayload = {
      name: 'Philippines account',
      cost_class: 'direct',
      category: 'subscription_account',
      amount_minor: 15_000,
      currency: 'USD',
      billing_cycle: 'monthly',
      starts_on: '2026-08-01',
      is_free: false,
      active: true
    }

    await createCost(cost)
    await updateCost(7, cost)
    await deleteCost(7)
    await upsertPricingRule({ group_id: 4, tier: 'quad', monthly_price_cents: 36_500, active: true })
    await upsertExchangeRate('2026-08', { currency: 'USD', rate_scaled: 6_750_000, source: 'confirmed' })
    await refreshExchangeRate()
    await upsertAPIKeyConfig(42, { revenue_excluded: false, override_amount_cents: 36_500 })
    await initializeDefaults()
    await closeMonth('2026-07', { data_quality: 'estimated', notes: 'Historical backfill' })

    expect(client.post).toHaveBeenNthCalledWith(1, '/admin/business/costs', cost)
    expect(client.put).toHaveBeenNthCalledWith(1, '/admin/business/costs/7', cost)
    expect(client.delete).toHaveBeenCalledWith('/admin/business/costs/7')
    expect(client.put).toHaveBeenNthCalledWith(2, '/admin/business/pricing-rules', {
      group_id: 4, tier: 'quad', monthly_price_cents: 36_500, active: true
    })
    expect(client.put).toHaveBeenNthCalledWith(3, '/admin/business/exchange-rates/2026-08', {
      currency: 'USD', rate_scaled: 6_750_000, source: 'confirmed'
    })
    expect(client.put).toHaveBeenNthCalledWith(4, '/admin/business/api-key-configs/42', {
      revenue_excluded: false, override_amount_cents: 36_500
    })
    expect(client.post).toHaveBeenNthCalledWith(2, '/admin/business/exchange-rates/refresh')
    expect(client.post).toHaveBeenNthCalledWith(3, '/admin/business/initialize')
    expect(client.post).toHaveBeenNthCalledWith(4, '/admin/business/snapshots/2026-07/close', {
      data_quality: 'estimated', notes: 'Historical backfill'
    })
  })
})
