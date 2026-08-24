import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ApiKeysView from '../ApiKeysView.vue'

const { listApiKeys, resetRateLimitWindow, getAllGroups, showSuccess, showError } = vi.hoisted(() => ({
  listApiKeys: vi.fn(),
  resetRateLimitWindow: vi.fn(),
  getAllGroups: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    apiKeys: {
      list: listApiKeys,
      resetRateLimitWindow
    },
    groups: {
      getAll: getAllGroups
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

vi.mock('@/composables/usePersistedPageSize', () => ({
  getPersistedPageSize: () => 20
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const apiKey = {
  id: 42,
  key: 'sk-local-reset-test',
  name: 'Local reset test',
  user_id: 1,
  group_id: 1,
  group: {
    id: 1,
    name: 'Local group',
    platform: 'openai',
    subscription_type: 'standard',
    rate_multiplier: 1
  },
  status: 'active',
  quota: 0,
  quota_used: 12,
  rate_multiplier: 1,
  token_package_required: false,
  rate_limit_5h: 10,
  rate_limit_1d: 20,
  rate_limit_7d: 30,
  usage_5h: 3,
  usage_1d: 7,
  usage_7d: 11,
  concurrency: 1,
  allow_claude_family: true,
  allow_gpt_family: true,
  allow_image_generation: true,
  expires_at: '2026-09-24T08:00:00Z',
  window_5h_start: '2026-08-24T06:00:00Z',
  window_1d_start: '2026-08-23T16:00:00Z',
  window_7d_start: '2026-08-20T08:00:00Z',
  created_at: '2026-08-01T08:00:00Z'
}

const DataTableStub = {
  props: ['data'],
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>'
}

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>'
}

describe('admin ApiKeysView rate-limit window reset shortcuts', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    listApiKeys.mockReset()
    resetRateLimitWindow.mockReset()
    getAllGroups.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    listApiKeys.mockResolvedValue({
      items: [{ ...apiKey }],
      total: 1,
      page: 1,
      page_size: 20
    })
    getAllGroups.mockResolvedValue([apiKey.group])
  })

  it('resets daily and weekly windows independently from the edit dialog', async () => {
    const confirm = vi.spyOn(window, 'confirm')
    const weeklyStart = '2026-08-24T08:30:00Z'
    resetRateLimitWindow
      .mockResolvedValueOnce({
        api_key: { ...apiKey, usage_1d: 0 }
      })
      .mockResolvedValueOnce({
        api_key: { ...apiKey, usage_1d: 0, usage_7d: 0, window_7d_start: weeklyStart }
      })

    const wrapper = mount(ApiKeysView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          BaseDialog: BaseDialogStub,
          Select: true,
          GroupBadge: true,
          Icon: true
        }
      }
    })

    await flushPromises()
    await wrapper.get('[title="admin.apiKeys.editPolicy"]').trigger('click')

    const findDialogButton = (text: string) => wrapper
      .findAll('[data-test="dialog"] button')
      .find((button) => button.text().includes(text))
    const dailyButton = findDialogButton('admin.apiKeys.resetDailyWindow')
    expect(dailyButton).toBeDefined()
    expect(wrapper.text()).toContain('admin.apiKeys.resetDailyWindow')
    expect(wrapper.text()).toContain('admin.apiKeys.resetWeeklyWindow')

    confirm.mockReturnValueOnce(false)
    await dailyButton!.trigger('click')
    expect(resetRateLimitWindow).not.toHaveBeenCalled()

    confirm.mockReturnValueOnce(true)
    await dailyButton!.trigger('click')
    await flushPromises()
    expect(resetRateLimitWindow).toHaveBeenNthCalledWith(1, 42, '1d')
    expect(wrapper.text()).toContain('admin.apiKeys.currentDailyUsage')

    confirm.mockReturnValueOnce(true)
    const weeklyButton = findDialogButton('admin.apiKeys.resetWeeklyWindow')
    expect(weeklyButton).toBeDefined()
    await weeklyButton!.trigger('click')
    await flushPromises()
    expect(resetRateLimitWindow).toHaveBeenNthCalledWith(2, 42, '7d')
    expect(showSuccess).toHaveBeenLastCalledWith('admin.apiKeys.weeklyWindowResetSuccess')
    const weeklyWindowInput = wrapper.findAll('input[type="datetime-local"]').at(-1)
    expect((weeklyWindowInput!.element as HTMLInputElement).value).toContain('2026-08-24T16:30')
    expect(showError).not.toHaveBeenCalled()
  })
})
