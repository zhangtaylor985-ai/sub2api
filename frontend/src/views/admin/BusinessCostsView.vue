<template>
  <AppLayout>
    <div class="business-shell space-y-5">
      <header class="flex flex-col gap-4 rounded-2xl border border-slate-200 bg-[#fffdf8] p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p class="ledger-kicker">COST LEDGER</p>
          <h1 class="text-2xl font-semibold text-slate-950 dark:text-white">成本管理</h1>
          <p class="mt-2 max-w-2xl text-sm text-slate-500 dark:text-slate-400">
            只记录实际需要支付的账号订阅、域名和其他费用；服务器、代理及免费账号不进入成本账本。
          </p>
        </div>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
          <button type="button" class="btn btn-primary" @click="openCreate">
            <Icon name="plus" size="sm" class="mr-1" />新增成本
          </button>
        </div>
      </header>

      <section class="grid gap-4 lg:grid-cols-[1fr_1fr]">
        <article class="ledger-panel p-5">
          <div class="flex items-start justify-between gap-4">
            <div>
              <p class="ledger-kicker">MONTHLY FX</p>
              <h2 class="text-lg font-semibold text-slate-950 dark:text-white">月度汇率</h2>
              <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">当前月由 ECB 工作日日参考汇率自动更新；失败时保留最近成功值，再兜底 6.75。历史锁账不会被重写。</p>
            </div>
            <div class="flex items-center gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="syncingRate || !isCurrentRateMonth" @click="refreshRate">
                {{ syncingRate ? '同步中…' : '同步 ECB' }}
              </button>
              <input v-model="rateMonth" type="month" class="input w-36" @change="loadRates" />
            </div>
          </div>
          <form class="mt-5 grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end" @submit.prevent="saveRate">
            <div>
              <label class="input-label" for="business-rate-currency">币种</label>
              <select id="business-rate-currency" v-model="rateForm.currency" class="input" @change="syncSelectedRate">
                <option v-for="currency in currencies.filter((value) => value !== 'CNY')" :key="currency" :value="currency">{{ currency }}/CNY</option>
              </select>
            </div>
            <div>
              <label class="input-label" for="business-rate-value">1 {{ rateForm.currency }} = 人民币</label>
              <input id="business-rate-value" v-model="rateForm.rate" type="number" min="0.000001" step="0.000001" class="input font-mono" placeholder="6.750000" />
            </div>
            <button type="submit" class="btn btn-primary" :disabled="savingRate">{{ savingRate ? '保存中…' : '保存汇率' }}</button>
          </form>
          <div class="mt-4 flex flex-wrap gap-2">
            <span v-for="rate in rates" :key="rate.currency" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-700 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200">
              <span class="font-mono">{{ rate.currency }} 1 = CNY {{ (rate.rate_scaled / 1_000_000).toFixed(6) }}</span>
              <span class="ml-2 text-slate-400">{{ rate.source }} · {{ updatedAt(rate.updated_at) }}</span>
            </span>
            <span v-if="!rates.length" class="text-xs text-amber-700 dark:text-amber-300">本月尚未设置外币汇率。</span>
          </div>
        </article>

        <article class="ledger-panel p-5">
          <p class="ledger-kicker">CURRENT STRUCTURE</p>
          <h2 class="text-lg font-semibold text-slate-950 dark:text-white">成本结构摘要</h2>
          <div class="mt-5 grid grid-cols-2 gap-3">
            <div class="summary-tile">
              <p>直接成本定义</p>
              <strong>{{ directCostCount }} 项</strong>
            </div>
            <div class="summary-tile">
              <p>运营费用定义</p>
              <strong>{{ operatingCostCount }} 项</strong>
            </div>
            <div class="summary-tile">
              <p>年费项目</p>
              <strong>{{ yearlyCostCount }} 项</strong>
            </div>
            <div class="summary-tile">
              <p>当前启用</p>
              <strong>{{ activeCostCount }} 项</strong>
            </div>
          </div>
        </article>
      </section>

      <section class="ledger-panel overflow-hidden">
        <div class="flex flex-col gap-3 border-b border-slate-200 px-5 py-4 dark:border-slate-700 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p class="ledger-kicker">COST DEFINITIONS</p>
            <h2 class="text-lg font-semibold text-slate-950 dark:text-white">成本项目</h2>
          </div>
          <div class="flex gap-2">
            <select v-model="classFilter" class="input w-36">
              <option value="">全部层级</option><option value="direct">直接成本</option><option value="operating">运营费用</option>
            </select>
            <input v-model="search" type="search" class="input w-48" placeholder="搜索名称或账号" />
          </div>
        </div>
        <div v-if="loading" class="flex min-h-64 items-center justify-center"><LoadingSpinner /></div>
        <div v-else-if="!filteredCosts.length" class="flex min-h-64 flex-col items-center justify-center px-6 text-center">
          <Icon name="calculator" size="xl" class="text-slate-300" />
          <p class="mt-3 font-semibold text-slate-800 dark:text-slate-100">暂无成本项目</p>
          <p class="mt-1 text-sm text-slate-500">可以运行经营初始化，或手工新增第一项成本。</p>
        </div>
        <div v-else class="overflow-x-auto">
          <table class="min-w-full divide-y divide-slate-200 text-left text-sm dark:divide-slate-700">
            <thead class="bg-slate-50 text-[11px] uppercase tracking-wider text-slate-500 dark:bg-slate-800 dark:text-slate-400">
              <tr><th class="px-5 py-3">名称 / 账号</th><th class="px-4 py-3">层级</th><th class="px-4 py-3">原币金额</th><th class="px-4 py-3">周期</th><th class="px-4 py-3">生效区间</th><th class="px-4 py-3">状态</th><th class="px-4 py-3 text-right">操作</th></tr>
            </thead>
            <tbody class="divide-y divide-slate-100 bg-white dark:divide-slate-800 dark:bg-slate-900">
              <tr v-for="cost in filteredCosts" :key="cost.id" class="hover:bg-slate-50/80 dark:hover:bg-slate-800/50">
                <td class="px-5 py-4"><p class="font-semibold text-slate-900 dark:text-white">{{ cost.name }}</p><p class="mt-1 text-xs text-slate-400">{{ cost.account_identifier || cost.category }} · #{{ cost.id }}</p></td>
                <td class="px-4 py-4"><span :class="cost.cost_class === 'direct' ? 'badge-direct' : 'badge-operating'">{{ cost.cost_class === 'direct' ? '直接成本' : '运营费用' }}</span></td>
                <td class="whitespace-nowrap px-4 py-4 font-mono font-semibold"><span v-if="cost.is_free" class="text-emerald-700 dark:text-emerald-300">免费</span><span v-else>{{ formatOriginal(cost.amount_minor, cost.currency) }}</span></td>
                <td class="px-4 py-4 text-xs text-slate-600 dark:text-slate-300">{{ cycleLabel(cost.billing_cycle) }}</td>
                <td class="whitespace-nowrap px-4 py-4 text-xs text-slate-500 dark:text-slate-400">{{ dateOnly(cost.starts_on) }}<span v-if="cost.ends_on"> → {{ dateOnly(cost.ends_on) }}</span></td>
                <td class="px-4 py-4"><span :class="cost.active ? 'status-active' : 'status-inactive'">{{ cost.active ? '启用' : '停用' }}</span></td>
                <td class="px-4 py-4"><div class="flex justify-end gap-1"><button type="button" class="icon-button" aria-label="编辑成本" @click="openEdit(cost)"><Icon name="edit" size="sm" /></button><button type="button" class="icon-button hover:!text-rose-600" aria-label="删除成本" @click="askDelete(cost)"><Icon name="trash" size="sm" /></button></div></td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>

    <BaseDialog :show="showForm" :title="editingId ? '编辑成本' : '新增成本'" width="wide" @close="closeForm">
      <form id="business-cost-form" class="grid gap-4 sm:grid-cols-2" @submit.prevent="saveCost">
        <div class="sm:col-span-2"><label class="input-label">名称</label><input v-model="form.name" class="input" maxlength="160" placeholder="例如：Oracle 服务器" /></div>
        <div><label class="input-label">成本层级</label><select v-model="form.cost_class" class="input"><option value="direct">直接成本</option><option value="operating">运营费用</option></select></div>
        <div><label class="input-label">类别</label><input v-model="form.category" class="input" maxlength="50" placeholder="subscription_account / server / proxy" /></div>
        <div><label class="input-label">币种</label><select v-model="form.currency" class="input"><option v-for="currency in currencies" :key="currency" :value="currency">{{ currency }}</option></select></div>
        <div><label class="input-label">原币金额</label><input v-model="form.amount" type="number" min="0" step="0.01" class="input font-mono" :disabled="form.is_free" /></div>
        <div><label class="input-label">周期</label><select v-model="form.billing_cycle" class="input"><option value="monthly">每月</option><option value="yearly">每年</option><option value="one_time">一次性</option></select></div>
        <div><label class="input-label">关联上游账号（可选）</label><select v-model="form.account_id" class="input"><option value="">不关联</option><option v-for="account in references.accounts" :key="account.id" :value="String(account.id)">{{ account.name }} · {{ account.platform }}</option></select></div>
        <div><label class="input-label">开始日期</label><input v-model="form.starts_on" type="date" class="input" /></div>
        <div><label class="input-label">结束日期（可选）</label><input v-model="form.ends_on" type="date" class="input" /></div>
        <div class="sm:col-span-2"><label class="input-label">账号标识 / 邮箱（可选）</label><input v-model="form.account_identifier" class="input" maxlength="160" /></div>
        <div class="sm:col-span-2"><label class="input-label">备注</label><textarea v-model="form.notes" class="input min-h-20" /></div>
        <label class="check-row"><input v-model="form.is_free" type="checkbox" @change="form.is_free && (form.amount = '0')" /><span><strong>免费项目</strong><small>保留结构说明，但折算成本固定为 0。</small></span></label>
        <label class="check-row"><input v-model="form.active" type="checkbox" /><span><strong>启用</strong><small>停用后不参与当前及后续月份计算。</small></span></label>
      </form>
      <template #footer><button type="button" class="btn btn-secondary" @click="closeForm">取消</button><button type="submit" form="business-cost-form" class="btn btn-primary" :disabled="savingCost">{{ savingCost ? '保存中…' : '保存成本' }}</button></template>
    </BaseDialog>

    <BaseDialog :show="Boolean(deletingCost)" title="删除成本项目" width="narrow" @close="deletingCost = null">
      <p class="text-sm leading-6 text-slate-600 dark:text-slate-300">将软删除“{{ deletingCost?.name }}”。已锁账的历史月份不会改变，当前和未来月份将不再计算该项目。</p>
      <template #footer><button type="button" class="btn btn-secondary" @click="deletingCost = null">取消</button><button type="button" class="btn btn-danger" :disabled="deleting" @click="confirmDelete">{{ deleting ? '删除中…' : '确认删除' }}</button></template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import type { BusinessCostItem, BusinessCostPayload, BusinessExchangeRate, BusinessReferenceData } from '@/api/admin/business'
import { useAppStore } from '@/stores'
import { businessBeijingDateKey, businessMonthKey, formatBusinessDate, parseBusinessMoneyToMinor, parseBusinessRateToScaled } from '@/utils/business'

const appStore = useAppStore()
const currencies = ['CNY', 'USD', 'PHP', 'HKD', 'EUR', 'SGD']
const costs = ref<BusinessCostItem[]>([])
const rates = ref<BusinessExchangeRate[]>([])
const references = reactive<BusinessReferenceData>({ groups: [], api_keys: [], accounts: [], private_subscriptions: [] })
const loading = ref(false)
const search = ref('')
const classFilter = ref('')
const showForm = ref(false)
const editingId = ref<number | null>(null)
const savingCost = ref(false)
const deletingCost = ref<BusinessCostItem | null>(null)
const deleting = ref(false)
const rateMonth = ref(businessMonthKey(new Date()))
const savingRate = ref(false)
const syncingRate = ref(false)
const rateForm = reactive({ currency: 'USD', rate: '6.750000' })

const form = reactive({
  name: '', cost_class: 'direct' as 'direct' | 'operating', category: 'subscription_account',
  amount: '0', currency: 'CNY', billing_cycle: 'monthly' as 'monthly' | 'yearly' | 'one_time',
  starts_on: businessBeijingDateKey(), ends_on: '', account_id: '',
  account_identifier: '', is_free: false, active: true, notes: ''
})

const filteredCosts = computed(() => {
  const query = search.value.trim().toLowerCase()
  return costs.value.filter((cost) => (!classFilter.value || cost.cost_class === classFilter.value) && (!query || `${cost.name} ${cost.account_identifier || ''} ${cost.category}`.toLowerCase().includes(query)))
})
const directCostCount = computed(() => costs.value.filter((cost) => cost.cost_class === 'direct').length)
const operatingCostCount = computed(() => costs.value.filter((cost) => cost.cost_class === 'operating').length)
const yearlyCostCount = computed(() => costs.value.filter((cost) => cost.billing_cycle === 'yearly').length)
const activeCostCount = computed(() => costs.value.filter((cost) => cost.active).length)
const isCurrentRateMonth = computed(() => rateMonth.value === businessMonthKey(new Date()))

function errorMessage(error: unknown) { return (error as { message?: string })?.message || '操作失败，请稍后重试。' }

async function loadAll() {
  loading.value = true
  try {
    const [costResult, referenceResult] = await Promise.all([adminAPI.business.listCosts(), adminAPI.business.getReferences()])
    costs.value = costResult
    Object.assign(references, referenceResult)
    await loadRates()
  } catch (error) {
    console.error('Failed to load business costs:', error)
    appStore.showError(errorMessage(error))
  } finally { loading.value = false }
}

async function loadRates() {
  if (!rateMonth.value) return
  try {
    rates.value = await adminAPI.business.listExchangeRates(rateMonth.value)
    syncSelectedRate()
  } catch (error) { appStore.showError(errorMessage(error)) }
}

function syncSelectedRate() {
  const selected = rates.value.find((rate) => rate.currency === rateForm.currency)
  rateForm.rate = selected ? (selected.rate_scaled / 1_000_000).toFixed(6) : ''
}

async function saveRate() {
  const rateScaled = parseBusinessRateToScaled(rateForm.rate)
  if (rateScaled === null) { appStore.showWarning('请输入最多 6 位小数的有效汇率。'); return }
  savingRate.value = true
  try {
    await adminAPI.business.upsertExchangeRate(rateMonth.value, { currency: rateForm.currency, rate_scaled: rateScaled, source: 'manual' })
    appStore.showSuccess('月度汇率已保存。')
    await loadRates()
  } catch (error) { appStore.showError(errorMessage(error)) } finally { savingRate.value = false }
}

async function refreshRate() {
  syncingRate.value = true
  try {
    const result = await adminAPI.business.refreshExchangeRate()
    appStore.showSuccess(result.used_fallback ? 'ECB 暂不可用，已保留现有汇率或使用 6.75 兜底。' : 'ECB 汇率已同步。')
    rateMonth.value = businessMonthKey(new Date())
    await loadRates()
  } catch (error) { appStore.showError(errorMessage(error)) } finally { syncingRate.value = false }
}

function resetForm() {
  Object.assign(form, { name: '', cost_class: 'direct', category: 'subscription_account', amount: '0', currency: 'CNY', billing_cycle: 'monthly', starts_on: businessBeijingDateKey(), ends_on: '', account_id: '', account_identifier: '', is_free: false, active: true, notes: '' })
  editingId.value = null
}
function openCreate() { resetForm(); showForm.value = true }
function openEdit(cost: BusinessCostItem) {
  editingId.value = cost.id
  Object.assign(form, { name: cost.name, cost_class: cost.cost_class, category: cost.category, amount: String(cost.amount_minor / 100), currency: cost.currency, billing_cycle: cost.billing_cycle, starts_on: dateOnly(cost.starts_on), ends_on: dateOnly(cost.ends_on), account_id: cost.account_id ? String(cost.account_id) : '', account_identifier: cost.account_identifier || '', is_free: cost.is_free, active: cost.active, notes: cost.notes || '' })
  showForm.value = true
}
function closeForm() { showForm.value = false; resetForm() }

function buildPayload(): BusinessCostPayload | null {
  const amountMinor = form.is_free ? 0 : parseBusinessMoneyToMinor(form.amount)
  if (!form.name.trim() || !form.category.trim() || !form.starts_on || amountMinor === null) { appStore.showWarning('请完整填写名称、类别、最多 2 位小数的金额和开始日期。'); return null }
  return { name: form.name.trim(), cost_class: form.cost_class, category: form.category.trim(), amount_minor: amountMinor, currency: form.currency, billing_cycle: form.billing_cycle, starts_on: form.starts_on, ends_on: form.ends_on || undefined, account_id: form.account_id ? Number(form.account_id) : undefined, account_identifier: form.account_identifier.trim() || undefined, is_free: form.is_free, active: form.active, notes: form.notes.trim() || undefined }
}

async function saveCost() {
  const payload = buildPayload(); if (!payload) return
  savingCost.value = true
  try {
    if (editingId.value) await adminAPI.business.updateCost(editingId.value, payload)
    else await adminAPI.business.createCost(payload)
    appStore.showSuccess(editingId.value ? '成本已更新。' : '成本已创建。')
    closeForm(); costs.value = await adminAPI.business.listCosts()
  } catch (error) { appStore.showError(errorMessage(error)) } finally { savingCost.value = false }
}

function askDelete(cost: BusinessCostItem) { deletingCost.value = cost }
async function confirmDelete() {
  if (!deletingCost.value) return
  deleting.value = true
  try { await adminAPI.business.deleteCost(deletingCost.value.id); appStore.showSuccess('成本已删除。'); deletingCost.value = null; costs.value = await adminAPI.business.listCosts() }
  catch (error) { appStore.showError(errorMessage(error)) } finally { deleting.value = false }
}

function dateOnly(value?: string) { return value ? formatBusinessDate(value) : '' }
function cycleLabel(value: string) { return ({ monthly: '每月', yearly: '每年', one_time: '一次性' } as Record<string, string>)[value] || value }
function formatOriginal(minor: number, currency: string) { try { return new Intl.NumberFormat('zh-CN', { style: 'currency', currency }).format(minor / 100) } catch { return `${currency} ${(minor / 100).toFixed(2)}` } }
function updatedAt(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false }) }

onMounted(loadAll)
</script>

<style scoped>
.business-shell { font-variant-numeric: tabular-nums; }
.ledger-panel { @apply rounded-2xl border border-slate-200/80 bg-[#fffdf8] shadow-sm dark:border-slate-700/80 dark:bg-slate-900/90; }
.ledger-kicker { @apply mb-1 font-mono text-[10px] font-semibold tracking-[0.2em] text-emerald-700 dark:text-emerald-400; }
.summary-tile { @apply rounded-xl border border-slate-200 bg-white p-3 dark:border-slate-700 dark:bg-slate-800; }
.summary-tile p { @apply text-[10px] font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400; }
.summary-tile strong { @apply mt-2 block font-mono text-lg text-slate-900 dark:text-white; }
.badge-direct { @apply rounded-full bg-sky-100 px-2 py-1 text-xs font-semibold text-sky-700 dark:bg-sky-950 dark:text-sky-300; }
.badge-operating { @apply rounded-full bg-orange-100 px-2 py-1 text-xs font-semibold text-orange-700 dark:bg-orange-950 dark:text-orange-300; }
.status-active { @apply rounded-full bg-emerald-100 px-2 py-1 text-xs font-semibold text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300; }
.status-inactive { @apply rounded-full bg-slate-100 px-2 py-1 text-xs font-semibold text-slate-500 dark:bg-slate-800 dark:text-slate-400; }
.icon-button { @apply rounded-lg p-2 text-slate-500 transition hover:bg-slate-100 hover:text-slate-900 dark:hover:bg-slate-800 dark:hover:text-white; }
.check-row { @apply flex cursor-pointer items-start gap-3 rounded-xl border border-slate-200 p-3 dark:border-slate-700; }
.check-row input { @apply mt-1 h-4 w-4 rounded border-slate-300 text-emerald-600 focus:ring-emerald-500; }
.check-row span { @apply flex flex-col; }
.check-row strong { @apply text-sm text-slate-800 dark:text-slate-100; }
.check-row small { @apply mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400; }
</style>
