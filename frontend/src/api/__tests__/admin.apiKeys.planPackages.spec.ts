import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({ apiClient: client }))

import { addPlanPackage, listPlanPackages } from '@/api/admin/apiKeys'

describe('admin API key plan packages API', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.post.mockReset()
  })

  it('uses the API key plan schedule endpoint', async () => {
    client.get.mockResolvedValue({ data: { packages: [], summary: { managed: false } } })

    await listPlanPackages(42)

    expect(client.get).toHaveBeenCalledWith('/admin/api-keys/42/plan-packages')
  })

  it('sends the idempotency request ID with the selected plan and duration', async () => {
    client.post.mockResolvedValue({ data: { idempotent: false } })
    const payload = {
      group_id: 7,
      request_id: 'plan-purchase-123',
      months: 1,
      note: 'Paid offline'
    }

    await addPlanPackage(42, payload)

    expect(client.post).toHaveBeenCalledWith('/admin/api-keys/42/plan-packages', payload)
  })
})
