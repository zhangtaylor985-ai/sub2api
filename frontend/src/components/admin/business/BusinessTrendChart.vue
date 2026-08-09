<template>
  <section class="ledger-panel" aria-labelledby="business-trend-title">
    <div class="flex flex-col gap-2 border-b border-slate-200/80 px-5 py-4 dark:border-slate-700/80 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <p class="ledger-kicker">MONTHLY CLOSE</p>
        <h2 id="business-trend-title" class="text-lg font-semibold text-slate-950 dark:text-white">
          月度经营轨迹
        </h2>
        <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
          仅展示已锁账月份和本月实时数据，不包含任何未来预测。
        </p>
      </div>
      <div class="flex items-center gap-3 text-[11px] text-slate-500 dark:text-slate-400">
        <span><i class="mr-1 inline-block h-2 w-2 rounded-sm bg-slate-800 dark:bg-slate-200"></i>收入</span>
        <span><i class="mr-1 inline-block h-2 w-2 rounded-sm bg-amber-400"></i>成本</span>
        <span><i class="mr-1 inline-block h-0.5 w-3 bg-emerald-500 align-middle"></i>净利</span>
      </div>
    </div>

    <div v-if="points.length" class="px-3 pb-2 pt-5 sm:px-5">
      <div class="h-72" role="img" aria-label="月度收入、成本、毛利和净利润图表">
        <Chart type="bar" :data="chartData" :options="chartOptions" />
      </div>
      <div class="mt-3 flex gap-2 overflow-x-auto pb-2" aria-label="可键盘选择的月份列表">
        <button
          v-for="point in points"
          :key="monthKey(point.month)"
          type="button"
          class="shrink-0 rounded-lg border border-slate-200 bg-white px-3 py-2 text-left text-xs transition hover:border-emerald-400 hover:bg-emerald-50 focus:outline-none focus:ring-2 focus:ring-emerald-500/40 dark:border-slate-700 dark:bg-slate-900 dark:hover:border-emerald-600 dark:hover:bg-emerald-950/30"
          :aria-label="monthButtonLabel(point)"
          @click="emit('select', monthKey(point.month))"
        >
          <span class="block font-semibold text-slate-800 dark:text-slate-100">{{ monthKey(point.month) }}</span>
          <span class="mt-0.5 block text-slate-500 dark:text-slate-400">
            {{ qualityLabel(point.data_quality) }} · {{ point.summary.customer_count }} 位
          </span>
        </button>
      </div>
    </div>
    <div v-else class="flex h-72 items-center justify-center px-6 text-center text-sm text-slate-500 dark:text-slate-400">
      暂无月度数据。初始化经营配置后，本月实时数据会显示在这里。
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import {
  BarElement,
  CategoryScale,
  Chart as ChartJS,
  Filler,
  Legend,
  LineElement,
  LinearScale,
  PointElement,
  Tooltip
} from 'chart.js'
import { Chart } from 'vue-chartjs'
import type { BusinessHistoryPoint } from '@/api/admin/business'
import {
  businessMonthKey,
  businessQualityLabel,
  formatBusinessCNY
} from '@/utils/business'

ChartJS.register(
  BarElement,
  CategoryScale,
  Filler,
  Legend,
  LineElement,
  LinearScale,
  PointElement,
  Tooltip
)

const props = defineProps<{ points: BusinessHistoryPoint[] }>()
const emit = defineEmits<{ (event: 'select', month: string): void }>()

const points = computed(() => props.points)
const monthKey = businessMonthKey
const qualityLabel = businessQualityLabel

function monthButtonLabel(point: BusinessHistoryPoint): string {
  const delta = point.customer_delta
  const deltaText = delta === undefined ? '无上月可比数据' : `较上月客户${delta >= 0 ? '增加' : '减少'} ${Math.abs(delta)} 位`
  return [
    monthKey(point.month),
    qualityLabel(point.data_quality),
    `收入 ${formatBusinessCNY(point.summary.total_revenue_cents)}`,
    `直接成本 ${formatBusinessCNY(point.summary.direct_cost_cents)}`,
    `运营费用 ${formatBusinessCNY(point.summary.operating_cost_cents)}`,
    `毛利 ${formatBusinessCNY(point.summary.gross_profit_cents)}`,
    `净利润 ${formatBusinessCNY(point.summary.net_profit_cents)}`,
    `客户 ${point.summary.customer_count} 位`,
    deltaText
  ].join('，')
}

const chartData = computed(() => ({
  labels: points.value.map((point) => monthKey(point.month)),
  datasets: [
    {
      type: 'bar' as const,
      label: '收入',
      data: points.value.map((point) => point.summary.total_revenue_cents),
      backgroundColor: '#0f2742',
      borderRadius: 5,
      maxBarThickness: 34,
      order: 4
    },
    {
      type: 'bar' as const,
      label: '直接成本',
      data: points.value.map((point) => point.summary.direct_cost_cents),
      backgroundColor: '#f2b84b',
      borderRadius: 5,
      maxBarThickness: 34,
      order: 3
    },
    {
      type: 'bar' as const,
      label: '运营费用',
      data: points.value.map((point) => point.summary.operating_cost_cents),
      backgroundColor: '#d97757',
      borderRadius: 5,
      maxBarThickness: 34,
      order: 2
    },
    {
      type: 'line' as const,
      label: '毛利',
      data: points.value.map((point) => point.summary.gross_profit_cents),
      borderColor: '#38a169',
      backgroundColor: '#38a16922',
      pointBackgroundColor: '#38a169',
      pointRadius: 3,
      borderWidth: 2,
      tension: 0.28,
      fill: false,
      order: 1
    },
    {
      type: 'line' as const,
      label: '净利润',
      data: points.value.map((point) => point.summary.net_profit_cents),
      borderColor: '#0f9f73',
      backgroundColor: '#0f9f7320',
      pointBackgroundColor: '#0f9f73',
      pointRadius: 4,
      borderWidth: 3,
      tension: 0.28,
      fill: false,
      order: 0
    }
  ]
}))

const chartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' as const },
  onClick: (_event: unknown, elements: Array<{ index: number }>) => {
    const index = elements[0]?.index
    const point = index === undefined ? undefined : points.value[index]
    if (point) emit('select', monthKey(point.month))
  },
  plugins: {
    legend: { display: false },
    tooltip: {
      padding: 12,
      callbacks: {
        label: (context: { dataset: { label?: string }; raw: unknown }) =>
          `${context.dataset.label}: ${formatBusinessCNY(Number(context.raw))}`,
        afterBody: (items: Array<{ dataIndex: number }>) => {
          const point = points.value[items[0]?.dataIndex ?? -1]
          if (!point) return []
          const delta = point.customer_delta
          return [
            `客户数: ${point.summary.customer_count}`,
            delta === undefined ? '较上月: 无可比数据' : `较上月客户: ${delta >= 0 ? '+' : ''}${delta}`,
            `数据状态: ${qualityLabel(point.data_quality)}`
          ]
        }
      }
    }
  },
  scales: {
    x: {
      stacked: false,
      grid: { display: false },
      ticks: { color: '#64748b', font: { size: 11 } }
    },
    y: {
      beginAtZero: true,
      grid: { color: 'rgba(148, 163, 184, 0.16)' },
      ticks: {
        color: '#64748b',
        callback: (value: string | number) => {
          const yuan = Number(value) / 100
          return yuan >= 10_000 ? `¥${(yuan / 10_000).toFixed(1)}万` : `¥${Math.round(yuan)}`
        }
      }
    }
  }
}))
</script>

<style scoped>
.ledger-panel {
  @apply overflow-hidden rounded-2xl border border-slate-200/80 bg-[#fffdf8] shadow-sm dark:border-slate-700/80 dark:bg-slate-900/90;
}

.ledger-kicker {
  @apply mb-1 font-mono text-[10px] font-semibold tracking-[0.2em] text-emerald-700 dark:text-emerald-400;
}
</style>
