import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  BarElement: {},
  CategoryScale: {},
  Filler: {},
  Legend: {},
  LineElement: {},
  LinearScale: {},
  PointElement: {},
  Tooltip: {}
}))

vi.mock('vue-chartjs', async () => {
  const { defineComponent } = await import('vue')
  return {
    Chart: defineComponent({
      name: 'BusinessChartStub',
      props: {
        type: { type: String, required: true },
        data: { type: Object, required: true },
        options: { type: Object, required: true }
      },
      template: '<div class="business-chart-stub" />'
    })
  }
})

import BusinessTrendChart from '../BusinessTrendChart.vue'
import type { BusinessHistoryPoint, BusinessSummary } from '@/api/admin/business'

function summary(overrides: Partial<BusinessSummary> = {}): BusinessSummary {
  return {
    api_key_count: 2,
    private_subscription_count: 1,
    customer_count: 3,
    excluded_api_key_count: 0,
    api_key_revenue_cents: 219_000,
    private_subscription_revenue_cents: 36_500,
    total_revenue_cents: 255_500,
    direct_cost_cents: 101_250,
    operating_cost_cents: 10_000,
    gross_profit_cents: 154_250,
    net_profit_cents: 144_250,
    gross_margin_bps: 6_037,
    net_margin_bps: 5_645,
    costs_complete: true,
    anomaly_count: 0,
    ...overrides
  }
}

describe('BusinessTrendChart', () => {
  it('renders historical/current points and exposes complete month tooltip detail', async () => {
    const points: BusinessHistoryPoint[] = [
      { month: '2026-07-01T00:00:00+08:00', status: 'locked', data_quality: 'actual', is_current: false, summary: summary() },
      { month: '2026-08-01T00:00:00+08:00', status: 'live', data_quality: 'live', is_current: true, customer_delta: 2, summary: summary({ customer_count: 5, total_revenue_cents: 300_000 }) }
    ]
    const wrapper = mount(BusinessTrendChart, { props: { points } })
    const chart = wrapper.findComponent({ name: 'BusinessChartStub' })

    expect(chart.exists()).toBe(true)
    expect(chart.props('type')).toBe('bar')
    const data = chart.props('data') as { labels: string[]; datasets: Array<{ label: string; data: number[] }> }
    expect(data.labels).toEqual(['2026-07', '2026-08'])
    expect(data.datasets.map((dataset) => dataset.label)).toEqual(['收入', '直接成本', '运营费用', '毛利', '净利润'])
    expect(data.datasets[0].data).toEqual([255_500, 300_000])

    const options = chart.props('options') as {
      plugins: { tooltip: { callbacks: { afterBody: (items: Array<{ dataIndex: number }>) => string[] } } }
    }
    expect(options.plugins.tooltip.callbacks.afterBody([{ dataIndex: 1 }])).toEqual([
      '客户数: 5',
      '较上月客户: +2',
      '数据状态: 实时'
    ])

    const monthButtons = wrapper.findAll('button')
    expect(monthButtons).toHaveLength(2)
    expect(monthButtons[1].attributes('aria-label')).toContain('收入 ¥3,000')
    expect(monthButtons[1].attributes('aria-label')).toContain('净利润 ¥1,442.5')
    expect(monthButtons[1].attributes('aria-label')).toContain('较上月客户增加 2 位')
    await monthButtons[1].trigger('click')
    expect(wrapper.emitted('select')).toEqual([['2026-08']])
  })

  it('shows an explicit empty state without inventing future points', () => {
    const wrapper = mount(BusinessTrendChart, { props: { points: [] } })
    expect(wrapper.findComponent({ name: 'BusinessChartStub' }).exists()).toBe(false)
    expect(wrapper.text()).toContain('不包含任何未来预测')
    expect(wrapper.text()).toContain('暂无月度数据')
  })
})
