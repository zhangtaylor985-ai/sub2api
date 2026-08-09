<template>
  <AppLayout>
    <div class="business-shell space-y-5">
      <header class="ledger-hero">
        <div class="relative z-10 max-w-3xl">
          <p class="font-mono text-[11px] font-semibold tracking-[0.24em] text-emerald-300">
            OPERATING LEDGER · {{ currentMonthLabel }}
          </p>
          <h1 class="mt-2 text-2xl font-semibold tracking-tight text-white sm:text-3xl">
            经营盈利看板
          </h1>
          <p class="mt-2 max-w-2xl text-sm leading-6 text-slate-300">
            当前数据实时读取有效 API Key 与客户订阅；历史月份使用锁账快照。这里不生成续费概率或未来盈利预测。
          </p>
        </div>
        <div class="relative z-10 mt-5 flex flex-wrap items-center gap-2 sm:mt-0 sm:justify-end">
          <span v-if="current" class="hero-badge">
            <span class="h-2 w-2 animate-pulse rounded-full bg-emerald-400"></span>
            截至 {{ formatAsOf(current.as_of) }}
          </span>
          <button type="button" class="hero-button" :disabled="loading" @click="loadAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            刷新
          </button>
        </div>
      </header>

      <div v-if="loading && !current" class="ledger-panel flex min-h-80 items-center justify-center">
        <LoadingSpinner />
      </div>

      <div v-else-if="loadError && !current" class="ledger-panel flex min-h-80 flex-col items-center justify-center px-6 text-center">
        <Icon name="exclamationTriangle" size="xl" class="text-rose-500" />
        <h2 class="mt-4 text-lg font-semibold text-slate-900 dark:text-white">经营数据暂时无法读取</h2>
        <p class="mt-2 max-w-md text-sm text-slate-500 dark:text-slate-400">{{ loadError }}</p>
        <button type="button" class="btn btn-primary mt-5" @click="loadAll">重新加载</button>
      </div>

      <template v-else-if="current">
        <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5" aria-label="核心经营指标">
          <article class="metric-card metric-card-revenue">
            <p class="metric-label">本月收入</p>
            <p class="metric-value">{{ money(current.summary.total_revenue_cents) }}</p>
            <p class="metric-note">
              Key {{ money(current.summary.api_key_revenue_cents) }} · 客户订阅
              {{ money(current.summary.private_subscription_revenue_cents) }}
            </p>
          </article>
          <article class="metric-card">
            <p class="metric-label">直接成本</p>
            <p class="metric-value text-amber-700 dark:text-amber-300">
              {{ money(current.summary.direct_cost_cents) }}
            </p>
            <p class="metric-note">账号订阅及交付直接成本</p>
          </article>
          <article class="metric-card metric-card-profit">
            <p class="metric-label">毛利</p>
            <p class="metric-value text-emerald-700 dark:text-emerald-300">
              {{ money(current.summary.gross_profit_cents) }}
            </p>
            <p class="metric-note">毛利率 {{ percent(current.summary.gross_margin_bps) }}</p>
          </article>
          <article class="metric-card">
            <p class="metric-label">运营费用</p>
            <p class="metric-value text-orange-700 dark:text-orange-300">
              {{ money(current.summary.operating_cost_cents) }}
            </p>
            <p class="metric-note">服务器、代理、域名及其他费用</p>
          </article>
          <article class="metric-card metric-card-net">
            <div class="flex items-center justify-between gap-2">
              <p class="metric-label">净利润</p>
              <span :class="current.summary.costs_complete ? 'quality-good' : 'quality-warn'">
                {{ current.summary.costs_complete ? '成本完整' : '待补成本' }}
              </span>
            </div>
            <p class="metric-value text-emerald-700 dark:text-emerald-300">
              {{ money(current.summary.net_profit_cents) }}
            </p>
            <p class="metric-note">净利率 {{ percent(current.summary.net_margin_bps) }}</p>
          </article>
        </section>

        <div
          v-if="!current.summary.costs_complete"
          class="flex items-start gap-3 rounded-2xl border border-amber-300/80 bg-amber-50 px-4 py-3 text-sm text-amber-950 dark:border-amber-800/80 dark:bg-amber-950/30 dark:text-amber-100"
          role="status"
        >
          <Icon name="exclamationTriangle" class="mt-0.5 shrink-0 text-amber-600" />
          <div class="flex-1">
            <p class="font-semibold">当前净利润仍是阶段值</p>
            <p class="mt-1 text-xs leading-5 text-amber-800 dark:text-amber-300">
              外币汇率或运营费用尚未完整录入。收入与已录成本仍准确展示，但请补齐服务器、代理等成本后再把净利润当作最终口径。
            </p>
          </div>
          <RouterLink to="/admin/business/costs" class="shrink-0 font-semibold underline underline-offset-4">
            补录成本
          </RouterLink>
        </div>

        <BusinessTrendChart :points="history" @select="openMonth" />

        <section class="grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
          <article class="ledger-panel p-5">
            <div class="flex items-start justify-between gap-4">
              <div>
                <p class="ledger-kicker">REVENUE MIX</p>
                <h2 class="text-lg font-semibold text-slate-950 dark:text-white">当月收入结构</h2>
              </div>
              <span class="font-mono text-xs text-slate-500 dark:text-slate-400">
                {{ current.summary.customer_count }} 个计费客户
              </span>
            </div>
            <div class="mt-6 space-y-5">
              <div>
                <div class="mb-2 flex items-end justify-between gap-4">
                  <div>
                    <p class="text-sm font-semibold text-slate-800 dark:text-slate-100">API Key</p>
                    <p class="text-xs text-slate-500 dark:text-slate-400">
                      {{ current.summary.api_key_count }} 个有效 Key，另有
                      {{ current.summary.excluded_api_key_count }} 个按政策排除
                    </p>
                  </div>
                  <p class="font-mono text-sm font-semibold text-slate-900 dark:text-white">
                    {{ money(current.summary.api_key_revenue_cents) }}
                  </p>
                </div>
                <div class="h-2 overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
                  <div class="h-full rounded-full bg-slate-800 dark:bg-slate-200" :style="{ width: revenueShare('api') }"></div>
                </div>
              </div>
              <div>
                <div class="mb-2 flex items-end justify-between gap-4">
                  <div>
                    <p class="text-sm font-semibold text-slate-800 dark:text-slate-100">客户订阅</p>
                    <p class="text-xs text-slate-500 dark:text-slate-400">
                      {{ current.summary.private_subscription_count }} 个有效订阅，显式关联时只计一次
                    </p>
                  </div>
                  <p class="font-mono text-sm font-semibold text-emerald-700 dark:text-emerald-300">
                    {{ money(current.summary.private_subscription_revenue_cents) }}
                  </p>
                </div>
                <div class="h-2 overflow-hidden rounded-full bg-slate-100 dark:bg-slate-800">
                  <div class="h-full rounded-full bg-emerald-500" :style="{ width: revenueShare('private') }"></div>
                </div>
              </div>
            </div>
            <button type="button" class="mt-6 inline-flex items-center gap-1 text-xs font-semibold text-emerald-700 hover:text-emerald-800 dark:text-emerald-400" @click="openMonth(currentMonthKey)">
              查看本月全部收入与成本明细
              <Icon name="arrowRight" size="xs" />
            </button>
          </article>

          <article class="ledger-panel p-5">
            <div class="flex items-start justify-between gap-4">
              <div>
                <p class="ledger-kicker">CONTROL TOTAL</p>
                <h2 class="text-lg font-semibold text-slate-950 dark:text-white">利润核算桥</h2>
              </div>
              <Icon name="calculator" class="text-slate-400" />
            </div>
            <dl class="mt-5 space-y-3 font-mono text-sm">
              <div class="bridge-row">
                <dt>总收入</dt>
                <dd>{{ money(current.summary.total_revenue_cents) }}</dd>
              </div>
              <div class="bridge-row bridge-deduct">
                <dt>− 直接成本</dt>
                <dd>{{ money(current.summary.direct_cost_cents) }}</dd>
              </div>
              <div class="bridge-row border-t-2 border-slate-800 pt-3 dark:border-slate-200">
                <dt class="font-semibold">= 毛利</dt>
                <dd class="font-semibold text-emerald-700 dark:text-emerald-300">
                  {{ money(current.summary.gross_profit_cents) }}
                </dd>
              </div>
              <div class="bridge-row bridge-deduct">
                <dt>− 运营费用</dt>
                <dd>{{ money(current.summary.operating_cost_cents) }}</dd>
              </div>
              <div class="bridge-row border-t-2 border-double border-slate-800 pt-3 dark:border-slate-200">
                <dt class="font-bold">= 净利润</dt>
                <dd class="text-base font-bold text-emerald-700 dark:text-emerald-300">
                  {{ money(current.summary.net_profit_cents) }}
                </dd>
              </div>
            </dl>
          </article>
        </section>

        <section v-if="current.issues.length" class="ledger-panel overflow-hidden">
          <div class="flex items-center justify-between border-b border-slate-200 px-5 py-4 dark:border-slate-700">
            <div>
              <p class="ledger-kicker">RECONCILIATION</p>
              <h2 class="text-lg font-semibold text-slate-950 dark:text-white">需要关注的经营数据</h2>
            </div>
            <RouterLink to="/admin/business/reconciliation" class="text-xs font-semibold text-emerald-700 hover:underline dark:text-emerald-400">
              查看全部 {{ current.issues.length }} 项
            </RouterLink>
          </div>
          <div class="divide-y divide-slate-100 dark:divide-slate-800">
            <div v-for="issue in current.issues.slice(0, 5)" :key="`${issue.type}-${issue.source_id}-${issue.source_name}`" class="flex items-start gap-3 px-5 py-3">
              <span :class="issueDotClass(issue.severity)"></span>
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-semibold text-slate-800 dark:text-slate-100">{{ issue.source_name }}</p>
                <p class="mt-0.5 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ issue.message }}</p>
              </div>
              <span class="font-mono text-[10px] uppercase text-slate-400">{{ issue.type }}</span>
            </div>
          </div>
        </section>
      </template>
    </div>

    <BaseDialog :show="showMonthDialog" :title="monthDialogTitle" width="extra-wide" @close="closeMonthDialog">
      <div v-if="loadingMonth" class="flex min-h-64 items-center justify-center"><LoadingSpinner /></div>
      <div v-else-if="selectedMonth" class="space-y-5">
        <div v-if="selectedMonth.notes" class="rounded-xl border border-sky-200 bg-sky-50 px-4 py-3 text-sm text-sky-900 dark:border-sky-900 dark:bg-sky-950/30 dark:text-sky-200">
          <p class="text-[10px] font-semibold uppercase tracking-wider text-sky-600 dark:text-sky-400">锁账说明</p>
          <p class="mt-1 leading-6">{{ selectedMonth.notes }}</p>
        </div>
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          <div v-for="metric in selectedMonthMetrics" :key="metric.label" class="rounded-xl border border-slate-200 bg-slate-50 px-3 py-3 dark:border-slate-700 dark:bg-slate-800/60">
            <p class="text-[10px] font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400">{{ metric.label }}</p>
            <p class="mt-1 font-mono text-sm font-semibold text-slate-900 dark:text-white">{{ metric.value }}</p>
          </div>
        </div>
        <div class="overflow-x-auto rounded-xl border border-slate-200 dark:border-slate-700">
          <table class="min-w-full divide-y divide-slate-200 text-left text-sm dark:divide-slate-700">
            <thead class="bg-slate-50 text-[11px] uppercase tracking-wider text-slate-500 dark:bg-slate-800 dark:text-slate-400">
              <tr><th class="px-4 py-3">项目</th><th class="px-4 py-3">类型</th><th class="px-4 py-3">原币</th><th class="px-4 py-3">人民币</th><th class="px-4 py-3">到期日</th><th class="px-4 py-3">计入口径</th></tr>
            </thead>
            <tbody class="divide-y divide-slate-100 bg-white dark:divide-slate-800 dark:bg-slate-900">
              <tr v-for="item in selectedMonth.items" :key="`${item.item_type}-${item.source_id}-${item.name}`" :class="!item.included ? 'opacity-55' : ''">
                <td class="px-4 py-3"><p class="font-medium text-slate-900 dark:text-white">{{ item.name }}</p><p v-if="item.group_name || item.user_email" class="mt-0.5 text-xs text-slate-400">{{ item.group_name || item.user_email }}</p></td>
                <td class="whitespace-nowrap px-4 py-3 text-xs text-slate-600 dark:text-slate-300">{{ itemType(item.item_type) }}</td>
                <td class="whitespace-nowrap px-4 py-3 font-mono text-xs">{{ lineAmount(item) }}</td>
                <td class="whitespace-nowrap px-4 py-3 font-mono font-semibold">{{ money(item.amount_cny_cents) }}</td>
                <td class="whitespace-nowrap px-4 py-3 text-xs">{{ formatDate(item.expires_on) }}</td>
                <td class="min-w-64 px-4 py-3 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ item.reason || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import BusinessTrendChart from '@/components/admin/business/BusinessTrendChart.vue'
import { adminAPI } from '@/api/admin'
import type { BusinessHistoryPoint, BusinessIssue, BusinessReport } from '@/api/admin/business'
import { useAppStore } from '@/stores'
import {
  businessItemTypeLabel,
  businessLineAmount,
  businessMonthKey,
  businessQualityLabel,
  formatBusinessCNY,
  formatBusinessDate,
  formatBusinessMonth,
  formatBusinessPercent
} from '@/utils/business'

const appStore = useAppStore()
const current = ref<BusinessReport | null>(null)
const history = ref<BusinessHistoryPoint[]>([])
const loading = ref(false)
const loadError = ref('')
const showMonthDialog = ref(false)
const loadingMonth = ref(false)
const selectedMonth = ref<BusinessReport | null>(null)
const selectedMonthKey = ref('')

const money = formatBusinessCNY
const percent = formatBusinessPercent
const formatDate = formatBusinessDate
const itemType = businessItemTypeLabel
const lineAmount = businessLineAmount
const currentMonthKey = computed(() => (current.value ? businessMonthKey(current.value.month) : ''))
const currentMonthLabel = computed(() => (current.value ? formatBusinessMonth(current.value.month) : formatBusinessMonth(new Date())))
const monthDialogTitle = computed(() => {
  if (!selectedMonthKey.value) return '月份经营明细'
  const quality = selectedMonth.value ? ` · ${businessQualityLabel(selectedMonth.value.data_quality)}` : ''
  return `${formatBusinessMonth(selectedMonthKey.value)}经营明细${quality}`
})

const selectedMonthMetrics = computed(() => {
  const summary = selectedMonth.value?.summary
  if (!summary) return []
  return [
    { label: '收入', value: money(summary.total_revenue_cents) },
    { label: '直接成本', value: money(summary.direct_cost_cents) },
    { label: '毛利', value: money(summary.gross_profit_cents) },
    { label: '运营费用', value: money(summary.operating_cost_cents) },
    { label: '净利润', value: money(summary.net_profit_cents) },
    { label: '客户', value: `${summary.customer_count} 位` }
  ]
})

function errorMessage(error: unknown): string {
  const value = error as { message?: string }
  return value?.message || '网络或服务异常，请稍后再试。'
}

async function loadAll() {
  loading.value = true
  loadError.value = ''
  try {
    const [currentResult, historyResult] = await Promise.all([
      adminAPI.business.getCurrent(),
      adminAPI.business.getHistory()
    ])
    current.value = currentResult
    const currentKey = businessMonthKey(currentResult.month)
    history.value = historyResult.filter((point) => businessMonthKey(point.month) <= currentKey)
  } catch (error) {
    console.error('Failed to load business dashboard:', error)
    loadError.value = errorMessage(error)
    appStore.showError(loadError.value)
  } finally {
    loading.value = false
  }
}

async function openMonth(month: string) {
  if (!month) return
  selectedMonthKey.value = month
  showMonthDialog.value = true
  loadingMonth.value = true
  selectedMonth.value = null
  try {
    selectedMonth.value = await adminAPI.business.getMonth(month)
  } catch (error) {
    console.error('Failed to load business month:', error)
    appStore.showError(errorMessage(error))
    showMonthDialog.value = false
  } finally {
    loadingMonth.value = false
  }
}

function closeMonthDialog() {
  showMonthDialog.value = false
  selectedMonth.value = null
}

function revenueShare(source: 'api' | 'private'): string {
  if (!current.value || current.value.summary.total_revenue_cents <= 0) return '0%'
  const amount = source === 'api'
    ? current.value.summary.api_key_revenue_cents
    : current.value.summary.private_subscription_revenue_cents
  return `${Math.min(100, Math.max(0, (amount / current.value.summary.total_revenue_cents) * 100))}%`
}

function formatAsOf(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false
  }).format(date)
}

function issueDotClass(severity: BusinessIssue['severity']): string {
  const color = severity === 'error' ? 'bg-rose-500' : severity === 'warning' ? 'bg-amber-500' : 'bg-sky-500'
  return `mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full ${color}`
}

onMounted(loadAll)
</script>

<style scoped>
.business-shell {
  font-variant-numeric: tabular-nums;
}

.ledger-hero {
  @apply relative overflow-hidden rounded-2xl bg-[#0b1f35] px-5 py-6 shadow-lg sm:flex sm:items-center sm:justify-between sm:px-7;
  background-image:
    radial-gradient(circle at 85% 10%, rgba(16, 185, 129, 0.2), transparent 30%),
    linear-gradient(120deg, rgba(255, 255, 255, 0.04) 1px, transparent 1px);
  background-size: auto, 22px 22px;
}

.hero-badge {
  @apply inline-flex items-center gap-2 rounded-full border border-white/15 bg-white/10 px-3 py-2 text-xs text-slate-200 backdrop-blur;
}

.hero-button {
  @apply inline-flex items-center gap-2 rounded-full border border-white/20 bg-white/10 px-3 py-2 text-xs font-semibold text-white transition hover:bg-white/20 disabled:opacity-50;
}

.ledger-panel {
  @apply rounded-2xl border border-slate-200/80 bg-[#fffdf8] shadow-sm dark:border-slate-700/80 dark:bg-slate-900/90;
}

.metric-card {
  @apply relative min-h-36 overflow-hidden rounded-2xl border border-slate-200/80 bg-[#fffdf8] p-4 shadow-sm dark:border-slate-700/80 dark:bg-slate-900/90;
}

.metric-card::after {
  content: '';
  @apply absolute bottom-0 left-0 h-1 w-full bg-slate-300 dark:bg-slate-700;
}

.metric-card-revenue::after { @apply bg-slate-800 dark:bg-slate-200; }
.metric-card-profit::after { @apply bg-emerald-400; }
.metric-card-net::after { @apply bg-emerald-600; }
.metric-label { @apply text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-500 dark:text-slate-400; }
.metric-value { @apply mt-4 font-mono text-2xl font-semibold tracking-tight text-slate-950 dark:text-white; }
.metric-note { @apply mt-2 text-[11px] leading-5 text-slate-500 dark:text-slate-400; }
.quality-good { @apply rounded-full bg-emerald-100 px-2 py-1 text-[10px] font-semibold text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300; }
.quality-warn { @apply rounded-full bg-amber-100 px-2 py-1 text-[10px] font-semibold text-amber-700 dark:bg-amber-950 dark:text-amber-300; }
.ledger-kicker { @apply mb-1 font-mono text-[10px] font-semibold tracking-[0.2em] text-emerald-700 dark:text-emerald-400; }
.bridge-row { @apply flex items-center justify-between gap-4 text-slate-700 dark:text-slate-200; }
.bridge-deduct { @apply text-amber-700 dark:text-amber-300; }
</style>
