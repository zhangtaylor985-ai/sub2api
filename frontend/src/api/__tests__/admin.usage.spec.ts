import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post,
  },
}))

import { searchApiKeys } from '@/api/admin/usage'

describe('admin usage API key search', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('sends an exact API key in the request body instead of the URL', async () => {
    const exactKey = 'sk-test-exact-value-not-a-real-key'
    const response = [{ id: 496, name: 'Customer Key', user_id: 84 }]
    post.mockResolvedValue({ data: response })

    const result = await searchApiKeys(undefined, exactKey)

    expect(post).toHaveBeenCalledWith('/admin/usage/search-api-keys', {
      keyword: exactKey,
    })
    expect(result).toEqual(response)
  })
})
