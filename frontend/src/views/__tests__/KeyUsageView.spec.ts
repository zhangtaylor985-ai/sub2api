import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import KeyUsageView from '../KeyUsageView.vue'

const { showInfo, showSuccess, showError, fetchPublicSettings } = vi.hoisted(() => ({
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  fetchPublicSettings: vi.fn(),
}))

const messages: Record<string, string> = {
  'keyUsage.title': 'API Key Usage',
  'keyUsage.subtitle': 'Usage status',
  'keyUsage.placeholder': 'sk-test',
  'keyUsage.query': 'Query',
  'keyUsage.querying': 'Querying...',
  'keyUsage.privacyNote': 'Privacy note',
  'keyUsage.dateRange': 'Date Range:',
  'keyUsage.dateRangeToday': 'Today',
  'keyUsage.dateRange7d': '7 Days',
  'keyUsage.dateRange30d': '30 Days',
  'keyUsage.dateRange90d': '90 Days',
  'keyUsage.dateRangeCustom': 'Custom',
  'keyUsage.apply': 'Apply',
  'keyUsage.used': 'Used',
  'keyUsage.detailInfo': 'Detail Information',
  'keyUsage.tokenStats': 'Token Statistics',
  'keyUsage.dailyDetail': 'Daily Detail',
  'keyUsage.date': 'Date',
  'keyUsage.requests': 'Requests',
  'keyUsage.inputTokens': 'Input Tokens',
  'keyUsage.outputTokens': 'Output Tokens',
  'keyUsage.cacheReadTokens': 'Cache Read',
  'keyUsage.cacheWriteTokens': 'Cache Write',
  'keyUsage.cost': 'Cost',
  'keyUsage.quotaMode': 'Key Quota Mode',
  'keyUsage.walletBalance': 'Wallet Balance',
  'keyUsage.totalQuota': 'Total Quota',
  'keyUsage.limit5h': '5-Hour Limit',
  'keyUsage.limitDaily': 'Daily Limit',
  'keyUsage.limit7d': '7-Day Limit',
  'keyUsage.limitWeekly': 'Weekly Limit',
  'keyUsage.limitMonthly': 'Monthly Limit',
  'keyUsage.remainingQuota': 'Remaining Quota',
  'keyUsage.usedQuota': 'Used Quota',
  'keyUsage.windowPeriod': 'Period',
  'keyUsage.subscriptionType': 'Subscription Type',
  'keyUsage.todayRequests': 'Today Requests',
  'keyUsage.todayInputTokens': 'Today Input',
  'keyUsage.todayOutputTokens': 'Today Output',
  'keyUsage.todayTokens': 'Today Tokens',
  'keyUsage.todayCacheCreation': 'Today Cache Creation',
  'keyUsage.todayCacheRead': 'Today Cache Read',
  'keyUsage.todayCost': 'Today Cost',
  'keyUsage.rpmTpm': 'RPM / TPM',
  'keyUsage.totalRequests': 'Total Requests',
  'keyUsage.totalInputTokens': 'Total Input',
  'keyUsage.totalOutputTokens': 'Total Output',
  'keyUsage.totalTokensLabel': 'Total Tokens',
  'keyUsage.totalCacheCreation': 'Total Cache Creation',
  'keyUsage.totalCacheRead': 'Total Cache Read',
  'keyUsage.totalCost': 'Total Cost',
  'keyUsage.avgDuration': 'Avg Duration',
  'keyUsage.querySuccess': 'Query successful',
  'keyUsage.queryFailed': 'Query failed',
  'keyUsage.queryFailedRetry': 'Query failed, please try again later',
  'keyUsage.dedicatedUnlimitedMode': 'Dedicated Unlimited',
  'keyUsage.dedicatedUnlimitedStatus': 'Dedicated active',
	'keyUsage.stackedPlans': 'Stacked Plans',
	'keyUsage.stackedPlansTitle': '2 plans are currently active',
	'keyUsage.stackedPlansHint': 'Expired contributions are removed automatically',
	'keyUsage.overallExpiry': 'Overall API key expiry',
	'keyUsage.effectiveDailyLimit': 'Effective daily limit',
	'keyUsage.effectiveWeeklyLimit': 'Effective weekly limit',
	'keyUsage.effectiveConcurrency': 'Effective concurrency',
	'keyUsage.planActive': 'Active',
	'keyUsage.planUpcoming': 'Upcoming',
	'keyUsage.planExpired': 'Expired',
	'keyUsage.concurrentUnits': 'concurrent',
  'keyUsage.todayUsage': 'Today Usage',
  'keyUsage.totalUsage': 'Total Usage',
  'keyUsage.limitPolicy': 'Limit Policy',
  'keyUsage.concurrencyOnly': 'No amount or usage limits; concurrency still applies',
  'home.viewDocs': 'Docs',
  'home.switchToLight': 'Light',
  'home.switchToDark': 'Dark',
  'home.footer.allRightsReserved': 'All rights reserved.',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
      locale: { value: 'en' },
    }),
  }
})

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    cachedPublicSettings: null,
    siteName: 'Sub2API',
    siteLogo: '',
    docUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings,
    showInfo,
    showSuccess,
    showError,
  }),
}))

describe('KeyUsageView daily detail', () => {
  beforeEach(() => {
    showInfo.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    fetchPublicSettings.mockReset()
    localStorage.clear()

    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: vi.fn().mockReturnValue({ matches: false }),
    })
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => window.setTimeout(() => cb(0), 0))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        mode: 'quota_limited',
        isValid: true,
        status: 'active',
        quota: {
          limit: 10,
          used: 1,
          remaining: 9,
          unit: 'USD',
        },
        rate_limits: [
          {
            window: '7d',
            limit: 500,
            used: 125,
            remaining: 375,
            window_start: '2026-05-28T22:14:00+08:00',
            reset_at: '2026-06-04T22:14:00+08:00',
          },
        ],
		plan_packages: {
			summary: {
				managed: true,
				active_count: 2,
				daily_limit_usd: 250,
				weekly_limit_usd: 830,
				concurrency: 5,
				latest_expires_at: '2026-10-15T10:00:00+08:00',
			},
			packages: [
				{
					id: 1,
					package_name: 'Double',
					daily_limit_usd: 150,
					weekly_limit_usd: 500,
					concurrency: 2,
					starts_at: '2026-08-15T10:00:00+08:00',
					expires_at: '2026-09-15T10:00:00+08:00',
					is_active: true,
					is_upcoming: false,
				},
				{
					id: 2,
					package_name: 'Triple',
					daily_limit_usd: 100,
					weekly_limit_usd: 330,
					concurrency: 3,
					starts_at: '2026-08-22T10:00:00+08:00',
					expires_at: '2026-10-15T10:00:00+08:00',
					is_active: true,
					is_upcoming: false,
				},
			],
		},
        usage: {
          today: {
            requests: 1,
            input_tokens: 10,
            output_tokens: 20,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            total_tokens: 30,
            actual_cost: 0.01,
          },
          total: {
            requests: 12,
            input_tokens: 100,
            output_tokens: 200,
            cache_creation_tokens: 10,
            cache_read_tokens: 30,
            total_tokens: 340,
            actual_cost: 0.12,
          },
          rpm: 0,
          tpm: 0,
        },
        daily_usage: [
          {
            date: '2026-05-19',
            requests: 12,
            input_tokens: 100,
            output_tokens: 200,
            cache_read_tokens: 30,
            cache_write_tokens: 10,
            total_tokens: 340,
            cost: 0.15,
            actual_cost: 0.12,
          },
        ],
      }),
    }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders daily usage detail rows after a successful query', async () => {
    const wrapper = mount(KeyUsageView, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          LocaleSwitcher: true,
          Icon: true,
        },
      },
    })

    await wrapper.find('input').setValue('sk-test-key')
    await wrapper.find('input').trigger('keydown.enter')
    await flushPromises()
    await nextTick()

    const fetchMock = vi.mocked(fetch)
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/v1/usage?'),
      expect.objectContaining({
        headers: { Authorization: 'Bearer sk-test-key' },
      })
    )
    expect(String(fetchMock.mock.calls[0][0])).toContain('days=30')

    const text = wrapper.text()
    expect(text).toContain('Daily Detail')
    expect(text).toContain('Date')
    expect(text).toContain('Cache Read')
    expect(text).toContain('Cache Write')
    expect(text).toContain('2026-05-19')
    expect(text).toContain('12')
    expect(text).toContain('100')
    expect(text).toContain('200')
    expect(text).toContain('30')
    expect(text).toContain('10')
    expect(text).toContain('$0.12')
    expect(text).toContain('Period')
    expect(text).toContain('05-28')
    expect(text).toContain('06-04')
	expect(text).toContain('Stacked Plans')
	expect(text).toContain('Effective daily limit')
	expect(text).toContain('$250.00')
	expect(text).toContain('$830.00')
	expect(text).toContain('Double')
	expect(text).toContain('Triple')

    wrapper.unmount()
  })

  it('renders dedicated unlimited mode without quota progress', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        mode: 'dedicated_unlimited',
        isValid: true,
        status: 'active',
        planName: 'Dedicated Codex',
        limit_policy: 'unlimited_usage_concurrency_only',
        concurrency: 2,
        usage: {
          today: {
            requests: 3,
            input_tokens: 100,
            output_tokens: 50,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            total_tokens: 150,
            actual_cost: 1.23,
          },
          total: {
            requests: 30,
            input_tokens: 1000,
            output_tokens: 500,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            total_tokens: 1500,
            actual_cost: 12.34,
          },
          rpm: 0,
          tpm: 0,
        },
      }),
    } as Response)

    const wrapper = mount(KeyUsageView, {
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          LocaleSwitcher: true,
          Icon: true,
        },
      },
    })

    await wrapper.find('input').setValue('sk-dedicated-key')
    await wrapper.find('input').trigger('keydown.enter')
    await flushPromises()
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Dedicated Codex')
    expect(text).toContain('Dedicated active')
    expect(text).toContain('Today Usage')
    expect(text).toContain('Total Usage')
    expect(text).toContain('No amount or usage limits; concurrency still applies')
    expect(text).not.toContain('Remaining Quota')

    wrapper.unmount()
  })
})
