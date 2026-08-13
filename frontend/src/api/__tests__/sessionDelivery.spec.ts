import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))

vi.mock('@/api/client', () => ({ apiClient: { get, put, post } }))

import {
  getSessionCapturePolicy,
  getSessionDeliveryOverview,
  setOnlySessionCaptureAPIKey,
  updateSessionCaptureAPIKey,
  updateSessionCaptureMode
} from '@/api/admin/sessionDelivery'

describe('Session delivery admin API', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
    get.mockResolvedValue({ data: {} })
    put.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: {} })
  })

  it('loads the overview and paginated capture policy', async () => {
    const controller = new AbortController()
    await getSessionDeliveryOverview(controller.signal)
    await getSessionCapturePolicy({ q: 'delivery', page: 2, page_size: 20 }, controller.signal)
    expect(get).toHaveBeenNthCalledWith(1, '/admin/session-delivery/overview', { signal: controller.signal })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/session-delivery/policy', {
      params: { q: 'delivery', page: 2, page_size: 20 },
      signal: controller.signal
    })
  })

  it('uses explicit endpoints for global, per-key, and only-this-key mutations', async () => {
    await updateSessionCaptureMode('disabled')
    await updateSessionCaptureAPIKey(42, 'exclude')
    await setOnlySessionCaptureAPIKey(42)
    expect(put).toHaveBeenNthCalledWith(1, '/admin/session-delivery/policy/mode', { mode: 'disabled' })
    expect(put).toHaveBeenNthCalledWith(2, '/admin/session-delivery/policy/api-keys/42', { policy: 'exclude' })
    expect(post).toHaveBeenCalledWith('/admin/session-delivery/policy/api-keys/42/only')
  })
})
