import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const { configureMock, refreshMock, successMock, errorMock } = vi.hoisted(() => ({
  configureMock: vi.fn(),
  refreshMock: vi.fn(),
  successMock: vi.fn(),
  errorMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      configureExternalQuotaGate: configureMock,
      refreshExternalQuotaGate: refreshMock
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: successMock, showError: errorMock })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'zh-CN' }
    })
  }
})

import OpenAIExternalQuotaGatePanel from '../OpenAIExternalQuotaGatePanel.vue'

const buildAccount = (enabled = true) => ({
  id: 19,
  name: 'OpenAI OAuth',
  platform: 'openai',
  type: 'oauth',
  credentials: {},
  extra: enabled ? {
    openai_external_quota_gate_enabled: true,
    openai_external_quota_gate_state: {
      allowed: true,
      limit_reached: false,
      decision: 'external_decrease_detected',
      primary_window: { used_percent: 42.5, limit_window_seconds: 604800, reset_at: 1787204531 },
      external_delta_percent: 1.5,
      last_attempt_at: '2026-08-19T12:00:00Z',
      lease_until: '2026-08-19T12:10:00Z'
    }
  } : {},
  proxy_id: null,
  concurrency: 1,
  priority: 1,
  status: 'active',
  schedulable: enabled,
  error_message: null,
  last_used_at: null,
  expires_at: null,
  auto_pause_on_expired: false,
  created_at: '2026-08-19T00:00:00Z',
  updated_at: '2026-08-19T00:00:00Z',
  rate_limited_at: null,
  rate_limit_reset_at: null,
  overload_until: null,
  temp_unschedulable_until: null,
  temp_unschedulable_reason: null,
  session_window_start: null,
  session_window_end: null,
  session_window_status: null
} as any)

describe('OpenAIExternalQuotaGatePanel', () => {
  beforeEach(() => {
    configureMock.mockReset()
    refreshMock.mockReset()
    successMock.mockReset()
    errorMock.mockReset()
  })

  it('renders the persisted decision and upstream usage', () => {
    const wrapper = mount(OpenAIExternalQuotaGatePanel, { props: { account: buildAccount() } })

    expect(wrapper.text()).toContain('admin.accounts.externalQuotaGate.decisions.externalDecrease')
    expect(wrapper.text()).toContain('42.5%')
    expect(wrapper.text()).toContain('+1.5%')
  })

  it('refreshes immediately and emits the updated account', async () => {
    const updated = buildAccount()
    updated.extra.openai_external_quota_gate_state.primary_window.used_percent = 43
    refreshMock.mockResolvedValue(updated)
    const wrapper = mount(OpenAIExternalQuotaGatePanel, { props: { account: buildAccount() } })

    await wrapper.get('button.btn-secondary').trigger('click')
    await flushPromises()

    expect(refreshMock).toHaveBeenCalledWith(19)
    expect(wrapper.emitted('updated')?.[0]).toEqual([updated])
  })

  it('enables a disabled gate through the dedicated API', async () => {
    configureMock.mockResolvedValue(buildAccount())
    const wrapper = mount(OpenAIExternalQuotaGatePanel, { props: { account: buildAccount(false) } })

    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()

    expect(configureMock).toHaveBeenCalledWith(19, true)
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })
})
