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
    openai_external_quota_gate_draining: false,
    openai_external_quota_gate_grant_minutes: 120,
    openai_external_quota_gate_state: {
      allowed: true,
      limit_reached: false,
      decision: 'external_decrease_detected',
      primary_window: { used_percent: 42.5, limit_window_seconds: 604800, reset_at: 1787204531 },
      baseline_primary_window: { used_percent: 41, limit_window_seconds: 604800, reset_at: 1787204531 },
      external_delta_percent: 1.5,
      last_attempt_at: '2026-08-19T12:00:00Z',
      external_detected_at: '2026-08-19T12:00:00Z',
      lease_until: '2026-08-19T13:00:00Z',
      last_lease_until: '2026-08-19T13:00:00Z',
      recent_events: [
        {
          occurred_at: '2026-08-19T12:00:00Z',
          decision: 'external_decrease_detected',
          schedulable: true,
          used_percent: 42.5,
          external_delta_percent: 1.5,
          lease_until: '2026-08-19T13:00:00Z'
        }
      ]
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
    expect(wrapper.text()).toContain('41.0%')
    expect(wrapper.text()).toContain('+1.5%')
    expect(wrapper.text()).toContain('admin.accounts.externalQuotaGate.policyLease')
    expect(wrapper.text()).toContain('admin.accounts.externalQuotaGate.recentEvents')
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

    expect(configureMock).toHaveBeenCalledWith(19, true, 120)
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it('updates the configurable grant duration without toggling the gate', async () => {
    const updated = buildAccount()
    updated.extra.openai_external_quota_gate_grant_minutes = 180
    configureMock.mockResolvedValue(updated)
    const wrapper = mount(OpenAIExternalQuotaGatePanel, { props: { account: buildAccount() } })

    await wrapper.get('[data-testid="external-quota-grant-minutes"]').setValue('180')
    await wrapper.get('[data-testid="save-external-quota-grant"]').trigger('click')
    await flushPromises()

    expect(configureMock).toHaveBeenCalledWith(19, true, 180)
    expect(wrapper.emitted('updated')?.[0]).toEqual([updated])
  })

  it('shows draining as existing-session-only with live counts', () => {
    const account = buildAccount()
    account.extra.openai_external_quota_gate_draining = true
    account.extra.openai_external_quota_gate_state.decision = 'draining'
    account.extra.openai_external_quota_gate_state.drain_started_at = '2026-08-19T13:00:00Z'
    account.extra.openai_external_quota_gate_state.drain_empty_checks = 0
    account.extra.openai_external_quota_gate_state.active_sticky_sessions = 3
    account.extra.openai_external_quota_gate_state.active_requests = 1
    account.extra.openai_external_quota_gate_state.waiting_requests = 2

    const wrapper = mount(OpenAIExternalQuotaGatePanel, { props: { account } })

    expect(wrapper.text()).toContain('admin.accounts.externalQuotaGate.drainingOnly')
    expect(wrapper.text()).toContain('admin.accounts.externalQuotaGate.decisions.draining')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('1 / 2')
  })
})
