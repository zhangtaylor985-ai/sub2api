import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({ apiClient: client }))

import { resetRateLimitWindow } from '@/api/admin/apiKeys'

describe('admin API key rate-limit window reset API', () => {
  beforeEach(() => {
    client.post.mockReset()
  })

  it.each(['1d', '7d'] as const)('uses the dedicated %s reset endpoint', async (window) => {
    client.post.mockResolvedValue({ data: { api_key: { id: 42 } } })

    await resetRateLimitWindow(42, window)

    expect(client.post).toHaveBeenCalledWith(`/admin/api-keys/42/rate-limit-windows/${window}/reset`)
  })
})
