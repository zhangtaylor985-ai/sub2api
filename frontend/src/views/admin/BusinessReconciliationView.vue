<template>
  <AppLayout>
    <div class="business-shell space-y-5">
      <header class="ledger-panel p-5 sm:flex sm:items-end sm:justify-between">
        <div>
          <p class="ledger-kicker">DATA CONTROL</p>
          <h1 class="text-2xl font-semibold text-slate-950 dark:text-white">数据对账</h1>
          <p class="mt-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400">
            这里专门处理无到期时间、缺价格、缺汇率和历史快照缺口。正常到期不会被列为经营异常。
          </p>
        </div>
        <div class="mt-4 flex flex-wrap gap-2 sm:mt-0">
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadAll"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />刷新</button>
          <button type="button" class="btn btn-primary" :disabled="initializing" @click="runInitialization"><Icon name="sparkles" size="sm" class="mr-1" />{{ initializing ? '初始化中…' : '初始化经营口径' }}</button>
        </div>
      </header>

      <section class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div class="control-card"><p>阻断项</p><strong class="text-rose-600 dark:text-rose-300">{{ reconciliation?.error_count ?? 0 }}</strong><small>无到期、缺价格或缺汇率</small></div>
        <div class="control-card"><p>警告项</p><strong class="text-amber-600 dark:text-amber-300">{{ reconciliation?.warning_count ?? 0 }}</strong><small>用户、分组或成本口径需核对</small></div>
        <div class="control-card"><p>信息项</p><strong class="text-sky-600 dark:text-sky-300">{{ reconciliation?.info_count ?? 0 }}</strong><small>包括历史快照缺口</small></div>
        <div class="control-card"><p>已配置 Key</p><strong>{{ configuredKeyCount }}</strong><small>经营收入排除或金额覆盖</small></div>
      </section>

      <section class="ledger-panel p-5">
        <div class="grid gap-4 lg:grid-cols-[1fr_1fr_auto] lg:items-end">
          <div>
            <p class="ledger-kicker">MONTH CLOSE</p>
            <h2 class="text-lg font-semibold text-slate-950 dark:text-white">历史月份锁账 / 补跑</h2>
            <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">上线前无法精确重建的月份请选择“估算”或“人工修正”，不要标成实际。</p>
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div><label class="input-label">月份</label><input v-model="closeForm.month" type="month" class="input" :max="previousMonthKey()" /></div>
            <div><label class="input-label">数据质量</label><select v-model="closeForm.quality" class="input"><option value="actual">实际</option><option value="estimated">估算</option><option value="manual">人工修正</option></select></div>
            <div class="col-span-2"><label class="input-label">锁账说明 <span v-if="closeForm.quality !== 'actual'">（必填）</span></label><input v-model="closeForm.notes" type="text" class="input" placeholder="说明数据来源、估算依据或人工修正原因" /></div>
          </div>
          <button type="button" class="btn btn-primary" :disabled="closing" @click="closeSelectedMonth">{{ closing ? '锁账中…' : '生成锁账快照' }}</button>
        </div>
      </section>

      <section class="ledger-panel overflow-hidden">
        <div class="flex flex-col gap-3 border-b border-slate-200 px-5 py-4 dark:border-slate-700 sm:flex-row sm:items-end sm:justify-between">
          <div><p class="ledger-kicker">EXCEPTIONS</p><h2 class="text-lg font-semibold text-slate-950 dark:text-white">经营异常</h2></div>
          <div class="flex gap-2"><select v-model="severityFilter" class="input w-32"><option value="">全部级别</option><option value="error">阻断</option><option value="warning">警告</option><option value="info">信息</option></select><input v-model="issueSearch" class="input w-48" type="search" placeholder="搜索名称或异常" /></div>
        </div>
        <div v-if="loading" class="flex min-h-56 items-center justify-center"><LoadingSpinner /></div>
        <div v-else-if="!filteredIssues.length" class="flex min-h-56 flex-col items-center justify-center text-center"><Icon name="checkCircle" size="xl" class="text-emerald-500" /><p class="mt-3 font-semibold text-slate-800 dark:text-white">当前筛选下没有异常</p></div>
        <div v-else class="divide-y divide-slate-100 dark:divide-slate-800">
          <article v-for="issue in filteredIssues" :key="`${issue.type}-${issue.source_id}-${issue.source_name}`" class="grid gap-3 px-5 py-4 lg:grid-cols-[auto_1fr_auto] lg:items-start">
            <span :class="severityBadge(issue.severity)">{{ severityLabel(issue.severity) }}</span>
            <div><div class="flex flex-wrap items-center gap-2"><h3 class="font-semibold text-slate-900 dark:text-white">{{ issue.source_name }}</h3><span class="font-mono text-[10px] text-slate-400">{{ issue.type }}</span><span v-if="issue.group_name" class="text-xs text-slate-400">{{ issue.group_name }}</span></div><p class="mt-1 text-sm leading-6 text-slate-600 dark:text-slate-300">{{ issue.message }}</p><p v-if="issue.suggested_action" class="mt-1 text-xs text-emerald-700 dark:text-emerald-400">建议：{{ issue.suggested_action }}</p></div>
            <button v-if="issue.source_type === 'api_key' && issue.source_id" type="button" class="btn btn-secondary btn-sm" @click="openKeyById(issue.source_id)">配置 Key</button>
          </article>
        </div>
      </section>

      <section class="ledger-panel overflow-hidden">
        <div class="border-b border-slate-200 px-5 py-4 dark:border-slate-700"><p class="ledger-kicker">PRICE BOOK</p><h2 class="text-lg font-semibold text-slate-950 dark:text-white">车型价格规则</h2><p class="mt-1 text-xs text-slate-500">价格按分组 ID 绑定，分组改名不会丢失规则。</p></div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-slate-200 text-left text-sm dark:divide-slate-700">
            <thead class="bg-slate-50 text-[11px] uppercase tracking-wider text-slate-500 dark:bg-slate-800"><tr><th class="px-5 py-3">分组</th><th class="px-4 py-3">车型</th><th class="px-4 py-3">月价（元）</th><th class="px-4 py-3">启用</th><th class="px-4 py-3 text-right">保存</th></tr></thead>
            <tbody class="divide-y divide-slate-100 bg-white dark:divide-slate-800 dark:bg-slate-900">
              <tr v-for="row in pricingRows" :key="row.group_id"><td class="px-5 py-3"><p class="font-semibold text-slate-900 dark:text-white">{{ row.group_name }}</p><p class="text-xs text-slate-400">#{{ row.group_id }}</p></td><td class="px-4 py-3"><select v-model="row.tier" class="input min-w-32"><option value="dedicated">独享车</option><option value="double">2人车</option><option value="triple">3人车</option><option value="quad">4人车</option></select></td><td class="px-4 py-3"><input v-model="row.price_yuan" type="number" min="0" step="0.01" class="input w-32 font-mono" /></td><td class="px-4 py-3"><input v-model="row.active" type="checkbox" class="h-4 w-4 rounded text-emerald-600" /></td><td class="px-4 py-3 text-right"><button type="button" class="btn btn-secondary btn-sm" @click="savePricing(row)">保存</button></td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="ledger-panel overflow-hidden">
        <div class="flex flex-col gap-3 border-b border-slate-200 px-5 py-4 dark:border-slate-700 sm:flex-row sm:items-end sm:justify-between"><div><p class="ledger-kicker">KEY POLICY</p><h2 class="text-lg font-semibold text-slate-950 dark:text-white">API Key 经营配置</h2></div><input v-model="keySearch" type="search" class="input w-full sm:w-64" placeholder="搜索 Key 名称、用户或分组" /></div>
        <div class="max-h-[34rem] overflow-auto">
          <table class="min-w-full divide-y divide-slate-200 text-left text-sm dark:divide-slate-700">
            <thead class="sticky top-0 z-10 bg-slate-50 text-[11px] uppercase tracking-wider text-slate-500 dark:bg-slate-800"><tr><th class="px-5 py-3">Key 名称</th><th class="px-4 py-3">分组 / 用户</th><th class="px-4 py-3">到期</th><th class="px-4 py-3">经营策略</th><th class="px-4 py-3 text-right">操作</th></tr></thead>
            <tbody class="divide-y divide-slate-100 bg-white dark:divide-slate-800 dark:bg-slate-900">
              <tr v-for="key in filteredKeys" :key="key.id"><td class="px-5 py-3"><p class="font-semibold text-slate-900 dark:text-white">{{ key.name }}</p><p class="text-xs text-slate-400">#{{ key.id }}</p></td><td class="px-4 py-3"><p class="text-xs text-slate-700 dark:text-slate-200">{{ key.group_name || '未分组' }}</p><p class="mt-1 text-xs text-slate-400">{{ key.user_email }}</p></td><td class="whitespace-nowrap px-4 py-3 text-xs" :class="key.expires_at ? 'text-slate-600 dark:text-slate-300' : 'font-semibold text-rose-600'">{{ key.expires_at ? dateOnly(key.expires_at) : '未设置' }}</td><td class="px-4 py-3"><span v-if="key.config?.revenue_excluded" class="policy-excluded">排除</span><span v-else-if="key.config?.override_amount_cents !== undefined" class="policy-override">金额覆盖</span><span v-else class="text-xs text-slate-400">跟随分组价格或流量包</span></td><td class="px-4 py-3 text-right"><button type="button" class="btn btn-secondary btn-sm" @click="openKey(key)">配置</button></td></tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>

    <BaseDialog :show="Boolean(selectedKey)" title="API Key 经营配置" width="wide" @close="selectedKey = null">
      <div v-if="selectedKey" class="space-y-5">
        <div class="rounded-xl bg-slate-50 p-4 dark:bg-slate-800"><p class="font-semibold text-slate-900 dark:text-white">{{ selectedKey.name }}</p><p class="mt-1 text-xs text-slate-500">#{{ selectedKey.id }} · {{ selectedKey.group_name || '未分组' }} · {{ selectedKey.user_email }}</p></div>
        <label class="check-row"><input v-model="keyForm.revenue_excluded" type="checkbox" @change="handleExclusionToggle" /><span><strong>从经营收入中排除</strong><small>适用于内部、测试或无需计费的 Key；配置按 ID 生效。</small></span></label>
        <div><label class="input-label">月收入覆盖（元，可选）</label><input v-model="keyForm.override_yuan" type="number" min="0" step="0.01" class="input font-mono" placeholder="留空则跟随分组价格或流量包" :disabled="keyForm.revenue_excluded" /></div>
        <div><label class="input-label">原因 / 备注</label><textarea v-model="keyForm.reason" class="input min-h-20" /></div>
      </div>
      <template #footer><button type="button" class="btn btn-secondary" @click="selectedKey = null">取消</button><button type="button" class="btn btn-primary" :disabled="savingKey" @click="saveKeyConfig">{{ savingKey ? '保存中…' : '保存配置' }}</button></template>
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
import type { BusinessAPIKeyReference, BusinessIssue, BusinessPricingRule, BusinessReconciliationResult, BusinessReferenceData } from '@/api/admin/business'
import { useAppStore } from '@/stores'
import { businessMonthKey, formatBusinessDate, parseBusinessMoneyToMinor } from '@/utils/business'

interface PricingRow { group_id: number; group_name: string; tier: string; price_yuan: string; active: boolean }
const appStore = useAppStore()
const loading = ref(false)
const initializing = ref(false)
const closing = ref(false)
const reconciliation = ref<BusinessReconciliationResult | null>(null)
const references = reactive<BusinessReferenceData>({ groups: [], api_keys: [], accounts: [], private_subscriptions: [] })
const pricingRules = ref<BusinessPricingRule[]>([])
const severityFilter = ref('')
const issueSearch = ref('')
const keySearch = ref('')
const selectedKey = ref<BusinessAPIKeyReference | null>(null)
const savingKey = ref(false)
const closeForm = reactive({ month: previousMonthKey(), quality: 'actual' as 'actual' | 'estimated' | 'manual', notes: '' })
const keyForm = reactive({ revenue_excluded: false, override_yuan: '', reason: '' })

const configuredKeyCount = computed(() => references.api_keys.filter((key) => key.config).length)
const filteredIssues = computed(() => {
  const query = issueSearch.value.trim().toLowerCase()
  return (reconciliation.value?.issues || []).filter((issue) => (!severityFilter.value || issue.severity === severityFilter.value) && (!query || `${issue.source_name} ${issue.type} ${issue.message}`.toLowerCase().includes(query)))
})
const filteredKeys = computed(() => {
  const query = keySearch.value.trim().toLowerCase()
  return references.api_keys.filter((key) => !query || `${key.name} ${key.group_name} ${key.user_email}`.toLowerCase().includes(query))
})
const pricingRows = computed<PricingRow[]>(() => references.groups.map((group) => {
  const rule = pricingRules.value.find((item) => item.group_id === group.id)
  return { group_id: group.id, group_name: group.name, tier: rule?.tier || inferTier(group.name, group.is_exclusive), price_yuan: String((rule?.monthly_price_cents ?? defaultPrice(rule?.tier || inferTier(group.name, group.is_exclusive))) / 100), active: rule?.active ?? true }
}).filter((row) => row.tier))

function errorMessage(error: unknown) { return (error as { message?: string })?.message || '操作失败，请稍后重试。' }
async function loadAll() {
  loading.value = true
  try {
    const [reconciliationResult, referenceResult, ruleResult] = await Promise.all([adminAPI.business.getReconciliation(), adminAPI.business.getReferences(), adminAPI.business.listPricingRules()])
    reconciliation.value = reconciliationResult; Object.assign(references, referenceResult); pricingRules.value = ruleResult
  } catch (error) { console.error('Failed to load business reconciliation:', error); appStore.showError(errorMessage(error)) }
  finally { loading.value = false }
}

async function runInitialization() {
  initializing.value = true
  try {
    const result = await adminAPI.business.initializeDefaults()
    const missing = result.missing_pricing_tiers.length + result.missing_excluded_names.length + result.missing_account_names.length
    appStore.showSuccess(`初始化完成：新增价格 ${result.pricing_created}、排除 ${result.exclusions_created}、成本 ${result.costs_created}${missing ? `；仍有 ${missing} 项需人工核对` : ''}。`, 6000)
    await loadAll()
  } catch (error) { appStore.showError(errorMessage(error)) } finally { initializing.value = false }
}

async function closeSelectedMonth() {
  if (!closeForm.month) return
  if (closeForm.quality !== 'actual' && !closeForm.notes.trim()) { appStore.showWarning('估算或人工修正快照必须填写锁账说明。'); return }
  closing.value = true
  try { const result = await adminAPI.business.closeMonth(closeForm.month, { data_quality: closeForm.quality, notes: closeForm.notes.trim() || undefined }); appStore.showSuccess(result.created ? `${closeForm.month} 已锁账。` : `${closeForm.month} 已存在锁账快照，本次未重复创建。`); await loadAll() }
  catch (error) { appStore.showError(errorMessage(error)) } finally { closing.value = false }
}

async function savePricing(row: PricingRow) {
  const monthlyPriceCents = parseBusinessMoneyToMinor(row.price_yuan)
  if (monthlyPriceCents === null) { appStore.showWarning('请输入最多 2 位小数的有效月价。'); return }
  try { await adminAPI.business.upsertPricingRule({ group_id: row.group_id, tier: row.tier, monthly_price_cents: monthlyPriceCents, active: row.active }); appStore.showSuccess(`${row.group_name} 价格已保存。`); pricingRules.value = await adminAPI.business.listPricingRules() }
  catch (error) { appStore.showError(errorMessage(error)) }
}

function openKey(key: BusinessAPIKeyReference) {
  selectedKey.value = key
  Object.assign(keyForm, { revenue_excluded: key.config?.revenue_excluded ?? false, override_yuan: key.config?.override_amount_cents === undefined ? '' : String(key.config.override_amount_cents / 100), reason: key.config?.reason || '' })
}
function openKeyById(id: number) { const key = references.api_keys.find((item) => item.id === id); if (key) openKey(key) }
function handleExclusionToggle() { if (keyForm.revenue_excluded) keyForm.override_yuan = '' }
async function saveKeyConfig() {
  if (!selectedKey.value) return
  const override = keyForm.override_yuan.trim() === '' ? undefined : parseBusinessMoneyToMinor(keyForm.override_yuan)
  if (override === null) { appStore.showWarning('请输入最多 2 位小数的有效覆盖金额。'); return }
  savingKey.value = true
  try { await adminAPI.business.upsertAPIKeyConfig(selectedKey.value.id, { revenue_excluded: keyForm.revenue_excluded, override_amount_cents: override, reason: keyForm.reason.trim() || undefined }); appStore.showSuccess('Key 经营配置已保存。'); selectedKey.value = null; await loadAll() }
  catch (error) { appStore.showError(errorMessage(error)) } finally { savingKey.value = false }
}

function severityBadge(value: BusinessIssue['severity']) { return value === 'error' ? 'severity-error' : value === 'warning' ? 'severity-warning' : 'severity-info' }
function severityLabel(value: BusinessIssue['severity']) { return value === 'error' ? '阻断' : value === 'warning' ? '警告' : '信息' }
function dateOnly(value?: string) { return formatBusinessDate(value) }
function inferTier(name: string, exclusive: boolean) { const value = name.toLowerCase(); if (value.includes('独享') || value.includes('dedicated') || exclusive) return 'dedicated'; if (value.includes('2人') || value.includes('双人')) return 'double'; if (value.includes('3人') || value.includes('三人')) return 'triple'; if (value.includes('4人') || value.includes('四人')) return 'quad'; return '' }
function defaultPrice(tier: string) { return ({ dedicated: 146000, double: 73000, triple: 48500, quad: 36500 } as Record<string, number>)[tier] || 0 }
function previousMonthKey() { const date = new Date(); date.setDate(1); date.setMonth(date.getMonth() - 1); return businessMonthKey(date) }

onMounted(loadAll)
</script>

<style scoped>
.business-shell { font-variant-numeric: tabular-nums; }
.ledger-panel { @apply rounded-2xl border border-slate-200/80 bg-[#fffdf8] shadow-sm dark:border-slate-700/80 dark:bg-slate-900/90; }
.ledger-kicker { @apply mb-1 font-mono text-[10px] font-semibold tracking-[0.2em] text-emerald-700 dark:text-emerald-400; }
.control-card { @apply rounded-2xl border border-slate-200 bg-[#fffdf8] p-4 shadow-sm dark:border-slate-700 dark:bg-slate-900; }
.control-card p { @apply text-[10px] font-semibold uppercase tracking-wider text-slate-500; }
.control-card strong { @apply mt-2 block font-mono text-2xl text-slate-900 dark:text-white; }
.control-card small { @apply mt-1 block text-[11px] leading-5 text-slate-400; }
.severity-error { @apply inline-flex w-12 justify-center rounded-full bg-rose-100 px-2 py-1 text-xs font-semibold text-rose-700 dark:bg-rose-950 dark:text-rose-300; }
.severity-warning { @apply inline-flex w-12 justify-center rounded-full bg-amber-100 px-2 py-1 text-xs font-semibold text-amber-700 dark:bg-amber-950 dark:text-amber-300; }
.severity-info { @apply inline-flex w-12 justify-center rounded-full bg-sky-100 px-2 py-1 text-xs font-semibold text-sky-700 dark:bg-sky-950 dark:text-sky-300; }
.policy-excluded { @apply rounded-full bg-slate-200 px-2 py-1 text-xs font-semibold text-slate-700 dark:bg-slate-700 dark:text-slate-200; }
.policy-override { @apply rounded-full bg-violet-100 px-2 py-1 text-xs font-semibold text-violet-700 dark:bg-violet-950 dark:text-violet-300; }
.check-row { @apply flex cursor-pointer items-start gap-3 rounded-xl border border-slate-200 p-3 dark:border-slate-700; }
.check-row input { @apply mt-1 h-4 w-4 rounded border-slate-300 text-emerald-600; }
.check-row span { @apply flex flex-col; }
.check-row strong { @apply text-sm text-slate-800 dark:text-slate-100; }
.check-row small { @apply mt-1 text-xs leading-5 text-slate-500 dark:text-slate-400; }
</style>
