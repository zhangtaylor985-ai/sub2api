<template>
  <AppLayout>
    <div class="session-page mx-auto max-w-[1680px] space-y-6">
      <header class="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
        <div>
          <div class="mb-2 flex flex-wrap items-center gap-2">
            <span class="eyebrow">SESSION DELIVERY / V2</span>
            <span class="status-pill" :class="`status-${overview?.status || 'unknown'}`">
              <span class="status-dot"></span>
              {{ statusLabel(overview?.status) }}
            </span>
            <span v-if="overview?.enabled" class="model-pill">{{ displayModel }}</span>
          </div>
          <h1 class="text-2xl font-semibold tracking-tight text-slate-950 dark:text-white md:text-3xl">
            {{ t('admin.sessionDelivery.title') }}
          </h1>
          <p class="mt-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400">
            {{ t('admin.sessionDelivery.description') }}
          </p>
        </div>
        <div class="flex items-center gap-3">
          <div class="hidden text-right sm:block">
            <p class="text-[11px] font-semibold uppercase tracking-[0.16em] text-slate-400">
              {{ t('admin.sessionDelivery.lastObserved') }}
            </p>
            <p class="mt-1 font-mono text-xs text-slate-600 dark:text-slate-300">{{ observedAtLabel }}</p>
          </div>
          <button class="control-button" type="button" :disabled="loading" @click="refreshAll">
            <svg :class="{ 'animate-spin': loading }" viewBox="0 0 24 24" fill="none" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.8" d="M20 11a8 8 0 10-2.34 5.66M20 4v7h-7" />
            </svg>
            {{ loading ? t('admin.sessionDelivery.refreshing') : t('admin.sessionDelivery.refresh') }}
          </button>
        </div>
      </header>

      <div v-if="loadError" class="alert-panel alert-critical">
        <div>
          <p class="font-semibold">{{ t('admin.sessionDelivery.loadFailed') }}</p>
          <p class="mt-1 text-sm opacity-80">{{ loadError }}</p>
        </div>
        <button type="button" class="text-sm font-semibold underline underline-offset-4" @click="refreshAll">
          {{ t('common.retry') }}
        </button>
      </div>

      <div v-if="overview?.warnings?.length" class="alert-panel" :class="overview.status === 'critical' ? 'alert-critical' : 'alert-warning'">
        <div>
          <p class="font-semibold">{{ t('admin.sessionDelivery.attentionRequired') }}</p>
          <p class="mt-1 text-sm opacity-80">{{ overview.warnings.map(warningLabel).join(' · ') }}</p>
        </div>
      </div>

      <section class="metric-grid">
        <article class="metric-card">
          <div class="metric-topline"><span>{{ t('admin.sessionDelivery.metrics.dbDisk') }}</span><span>DB HOST</span></div>
          <div class="metric-value">{{ formatPercent(remote?.host.disk_used_percent) }}</div>
          <div class="meter"><span :style="{ width: clampPercent(remote?.host.disk_used_percent) }" :class="resourceTone(remote?.host.disk_used_percent, 70, 75)"></span></div>
          <div class="metric-caption">
            {{ formatBytes(remote?.host.disk_used_bytes) }} / {{ formatBytes(remote?.host.disk_total_bytes) }}
          </div>
        </article>
        <article class="metric-card">
          <div class="metric-topline"><span>{{ t('admin.sessionDelivery.metrics.inDatabase') }}</span><span>POSTGRES</span></div>
          <div class="metric-value">{{ formatNumber(remote?.sessions.records_in_database) }}</div>
          <div class="metric-caption split-caption">
            <span>{{ t('admin.sessionDelivery.deliverable') }} {{ formatNumber(remote?.sessions.deliverable_in_database) }}</span>
            <span>{{ formatBytes(remote?.sessions.payload_bytes_in_database) }}</span>
          </div>
        </article>
        <article class="metric-card">
          <div class="metric-topline"><span>{{ t('admin.sessionDelivery.metrics.driveFiles') }}</span><span>SHA VERIFIED</span></div>
          <div class="metric-value">{{ formatNumber(remote?.delivery.archive_files_verified) }}</div>
          <div class="metric-caption split-caption">
            <span>{{ t('admin.sessionDelivery.archivedRecords') }} {{ formatNumber(remote?.delivery.records_archived) }}</span>
            <span>{{ lastVerifiedLabel }}</span>
          </div>
        </article>
        <article class="metric-card accent-card">
          <div class="metric-topline"><span>{{ t('admin.sessionDelivery.metrics.uploaded') }}</span><span>GOOGLE DRIVE</span></div>
          <div class="metric-value">{{ formatBytes(remote?.delivery.archive_bytes_uploaded) }}</div>
          <div class="metric-caption">{{ t('admin.sessionDelivery.fullReadbackVerified') }}</div>
        </article>
      </section>

      <section class="console-panel pipeline-panel">
        <div class="panel-heading">
          <div>
            <p class="section-kicker">LIVE PIPELINE</p>
            <h2>{{ t('admin.sessionDelivery.pipeline.title') }}</h2>
          </div>
          <span class="polling-note"><span></span>{{ t('admin.sessionDelivery.pipeline.polling') }}</span>
        </div>
        <div class="pipeline-grid">
          <template v-for="(step, index) in pipelineSteps" :key="step.code">
            <article class="pipeline-step" :class="`pipeline-${step.tone}`">
              <div class="step-index">0{{ index + 1 }}</div>
              <div>
                <p class="step-code">{{ step.code }}</p>
                <h3>{{ step.title }}</h3>
                <p>{{ step.detail }}</p>
              </div>
              <span class="step-state">{{ step.state }}</span>
            </article>
            <div v-if="index < pipelineSteps.length - 1" class="pipeline-link" aria-hidden="true">
              <span></span><svg viewBox="0 0 20 20" fill="currentColor"><path d="M7 4l6 6-6 6" /></svg>
            </div>
          </template>
        </div>
      </section>

      <div class="grid gap-6 xl:grid-cols-[minmax(0,1.05fr)_minmax(420px,.95fr)]">
        <section class="console-panel">
          <div class="panel-heading">
            <div>
              <p class="section-kicker">ISOLATED DATABASE HOST</p>
              <h2>{{ t('admin.sessionDelivery.host.title') }}</h2>
            </div>
            <span class="host-label">{{ remote?.host.hostname || '—' }}</span>
          </div>
          <div class="resource-grid">
            <div class="resource-row">
              <div class="resource-label"><span>CPU</span><strong>{{ formatPercent(remote?.host.cpu_used_percent) }}</strong></div>
              <div class="meter"><span :style="{ width: clampPercent(remote?.host.cpu_used_percent) }" :class="resourceTone(remote?.host.cpu_used_percent, 70, 90)"></span></div>
              <p>{{ remote?.host.cpu_count || 0 }} {{ t('admin.sessionDelivery.host.cores') }} · Load {{ formatDecimal(remote?.host.load_1) }} / {{ formatDecimal(remote?.host.load_5) }} / {{ formatDecimal(remote?.host.load_15) }}</p>
            </div>
            <div class="resource-row">
              <div class="resource-label"><span>{{ t('admin.sessionDelivery.host.memory') }}</span><strong>{{ memoryPercent }}</strong></div>
              <div class="meter"><span :style="{ width: memoryPercent }" :class="resourceTone(memoryPercentNumber, 75, 90)"></span></div>
              <p>{{ formatBytes(remote?.host.memory_used_bytes) }} / {{ formatBytes(remote?.host.memory_total_bytes) }}</p>
            </div>
            <div class="resource-row">
              <div class="resource-label"><span>{{ t('admin.sessionDelivery.host.disk') }}</span><strong>{{ formatPercent(remote?.host.disk_used_percent) }}</strong></div>
              <div class="meter"><span :style="{ width: clampPercent(remote?.host.disk_used_percent) }" :class="resourceTone(remote?.host.disk_used_percent, 70, 75)"></span></div>
              <p>{{ t('admin.sessionDelivery.host.available') }} {{ formatBytes(remote?.host.disk_available_bytes) }}</p>
            </div>
          </div>
          <div class="detail-strip">
            <div><span>{{ t('admin.sessionDelivery.host.uptime') }}</span><strong>{{ formatUptime(remote?.host.uptime_seconds) }}</strong></div>
            <div><span>{{ t('admin.sessionDelivery.host.dbSize') }}</span><strong>{{ formatBytes(remote?.database.size_bytes) }}</strong></div>
            <div><span>{{ t('admin.sessionDelivery.host.connections') }}</span><strong>{{ remote?.database.connections_total || 0 }} / {{ remote?.database.connections_max || 0 }}</strong></div>
            <div><span>{{ t('admin.sessionDelivery.host.partitions') }}</span><strong>{{ remote?.database.partitions || 0 }}</strong></div>
          </div>
        </section>

        <section class="console-panel">
          <div class="panel-heading">
            <div>
              <p class="section-kicker">SESSION INVENTORY</p>
              <h2>{{ t('admin.sessionDelivery.inventory.title') }}</h2>
            </div>
            <span class="host-label">UTC HOUR</span>
          </div>
          <div class="inventory-list">
            <div class="inventory-row"><span>{{ t('admin.sessionDelivery.inventory.currentHour') }}</span><strong>{{ formatNumber(remote?.sessions.current_hour_records) }}</strong></div>
            <div class="inventory-row"><span>{{ t('admin.sessionDelivery.inventory.lastFiveMinutes') }}</span><strong>{{ formatNumber(remote?.sessions.records_last_5m) }}</strong></div>
            <div class="inventory-row"><span>{{ t('admin.sessionDelivery.inventory.rejected') }}</span><strong>{{ formatNumber(remote?.sessions.rejected_in_database) }}</strong></div>
            <div class="inventory-row"><span>{{ t('admin.sessionDelivery.inventory.archivedDeliveries') }}</span><strong>{{ formatNumber(remote?.delivery.deliveries_archived) }}</strong></div>
          </div>
          <div class="spool-box">
            <div>
              <p>{{ t('admin.sessionDelivery.spool.title') }}</p>
              <strong>{{ formatNumber(overview?.spool?.pending_records) }} {{ t('admin.sessionDelivery.spool.pending') }}</strong>
            </div>
            <div class="text-right">
              <p>{{ formatBytes(overview?.spool?.used_bytes) }} / {{ formatBytes(overview?.spool?.max_bytes) }}</p>
              <strong :class="overview?.spool?.quarantined_records ? 'text-amber-600 dark:text-amber-300' : ''">
                {{ formatNumber(overview?.spool?.quarantined_records) }} {{ t('admin.sessionDelivery.spool.quarantined') }}
              </strong>
            </div>
          </div>
        </section>
      </div>

      <section class="console-panel overflow-hidden">
        <div class="panel-heading px-5 pt-5 md:px-6 md:pt-6">
          <div>
            <p class="section-kicker">VERIFIED EXPORT LEDGER</p>
            <h2>{{ t('admin.sessionDelivery.batches.title') }}</h2>
          </div>
          <span class="host-label">{{ t('admin.sessionDelivery.batches.lastTwelve') }}</span>
        </div>
        <div class="batch-table-wrap">
          <table class="batch-table">
            <thead><tr><th>{{ t('admin.sessionDelivery.batches.hour') }}</th><th>{{ t('admin.sessionDelivery.batches.status') }}</th><th>{{ t('admin.sessionDelivery.batches.records') }}</th><th>{{ t('admin.sessionDelivery.batches.delivery') }}</th><th>{{ t('admin.sessionDelivery.batches.size') }}</th><th>{{ t('admin.sessionDelivery.batches.verifiedAt') }}</th></tr></thead>
            <tbody v-if="remote?.recent_batches?.length">
              <tr v-for="batch in remote.recent_batches" :key="batch.hour">
                <td class="font-mono">{{ formatUTCHour(batch.hour) }}</td>
                <td><span class="batch-status" :class="`batch-${batch.status}`">{{ batchStatusLabel(batch.status) }}</span></td>
                <td>{{ formatNumber(batch.record_count) }}</td>
                <td>{{ formatNumber(batch.delivery_count) }}</td>
                <td>{{ formatBytes(batch.archive_size) }}</td>
                <td>{{ formatDate(batch.verified_at || batch.purged_at) }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="!remote?.recent_batches?.length" class="empty-ledger">
            <div class="empty-clock">:00</div>
            <div><strong>{{ t('admin.sessionDelivery.batches.waitingTitle') }}</strong><p>{{ t('admin.sessionDelivery.batches.waitingDescription') }}</p></div>
          </div>
        </div>
      </section>

      <section class="console-panel policy-panel">
        <div class="panel-heading">
          <div>
            <p class="section-kicker">CAPTURE CONTROL</p>
            <h2>{{ t('admin.sessionDelivery.policy.title') }}</h2>
            <p class="mt-2 max-w-3xl text-sm leading-6 text-slate-500 dark:text-slate-400">{{ t('admin.sessionDelivery.policy.description') }}</p>
          </div>
          <div class="policy-count"><span>{{ t('admin.sessionDelivery.policy.effective') }}</span><strong>{{ policy?.summary.effective_api_keys || 0 }} / {{ policy?.summary.total_api_keys || 0 }}</strong></div>
        </div>

        <div class="mode-grid">
          <button v-for="mode in modeOptions" :key="mode.value" type="button" class="mode-card" :class="{ active: policy?.summary.mode === mode.value, danger: mode.value === 'disabled' }" @click="requestModeChange(mode.value)">
            <span class="mode-radio"><i></i></span>
            <span><strong>{{ mode.title }}</strong><small>{{ mode.description }}</small></span>
          </button>
        </div>

        <div class="policy-note">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.7" d="M12 9v4m0 4h.01M10.3 3.7L2.8 17a2 2 0 001.74 3h14.92a2 2 0 001.74-3L13.7 3.7a2 2 0 00-3.4 0z" /></svg>
          <p>{{ t('admin.sessionDelivery.policy.futureOnly') }}</p>
        </div>

        <div class="key-toolbar">
          <label class="key-search">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><circle cx="11" cy="11" r="7" stroke-width="1.8"/><path d="M20 20l-4-4" stroke-width="1.8" stroke-linecap="round"/></svg>
            <input v-model="search" type="search" :placeholder="t('admin.sessionDelivery.policy.searchPlaceholder')" />
          </label>
          <div class="policy-legend">
            <span><i class="legend-on"></i>{{ t('admin.sessionDelivery.policy.recording') }}</span>
            <span><i class="legend-off"></i>{{ t('admin.sessionDelivery.policy.notRecording') }}</span>
          </div>
        </div>

        <div class="key-table-wrap">
          <table class="key-table">
            <thead><tr><th>API KEY</th><th>{{ t('admin.sessionDelivery.policy.owner') }}</th><th>{{ t('admin.sessionDelivery.policy.group') }}</th><th>{{ t('admin.sessionDelivery.policy.effectiveState') }}</th><th>{{ t('admin.sessionDelivery.policy.rule') }}</th><th></th></tr></thead>
            <tbody>
              <tr v-for="key in policy?.api_keys.items || []" :key="key.id">
                <td><div class="key-identity"><span>#{{ key.id }}</span><strong>{{ key.name }}</strong></div></td>
                <td>{{ key.user_email }}</td>
                <td>{{ key.group_name || '—' }}</td>
                <td><span class="effective-state" :class="key.effective ? 'is-on' : 'is-off'"><i></i>{{ key.effective ? t('admin.sessionDelivery.policy.recording') : t('admin.sessionDelivery.policy.notRecording') }}</span></td>
                <td>
                  <select :value="key.policy" :disabled="savingKeyID === key.id" @change="updateKeyPolicy(key, ($event.target as HTMLSelectElement).value as SessionCaptureKeyPolicy)">
                    <option value="inherit">{{ t('admin.sessionDelivery.policy.inherit') }}</option>
                    <option value="include">{{ t('admin.sessionDelivery.policy.include') }}</option>
                    <option value="exclude">{{ t('admin.sessionDelivery.policy.exclude') }}</option>
                  </select>
                </td>
                <td><button class="only-button" type="button" :disabled="savingKeyID === key.id" @click="requestOnlyKey(key)">{{ t('admin.sessionDelivery.policy.onlyThisKey') }}</button></td>
              </tr>
            </tbody>
          </table>
          <div v-if="policyLoading" class="table-loading">{{ t('common.loading') }}</div>
          <div v-else-if="!policy?.api_keys.items.length" class="table-loading">{{ t('admin.sessionDelivery.policy.noKeys') }}</div>
        </div>
        <div class="key-pagination">
          <span>{{ paginationLabel }}</span>
          <div><button type="button" :disabled="policyPage <= 1" @click="changePage(policyPage - 1)">←</button><button type="button" :disabled="policyPage >= totalPolicyPages" @click="changePage(policyPage + 1)">→</button></div>
        </div>
      </section>
    </div>

    <ConfirmDialog
      :show="confirmation.show"
      :title="confirmation.title"
      :message="confirmation.message"
      :confirm-text="confirmation.confirmText"
      :danger="confirmation.danger"
      @confirm="confirmPolicyAction"
      @cancel="resetConfirmation"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import sessionDeliveryAPI, {
  type SessionCaptureAPIKey,
  type SessionCaptureKeyPolicy,
  type SessionCaptureMode,
  type SessionCapturePolicyResponse,
  type SessionDeliveryOverview,
  type SessionDeliveryStatus
} from '@/api/admin/sessionDelivery'

const { t } = useI18n()
const appStore = useAppStore()
const overview = ref<SessionDeliveryOverview | null>(null)
const policy = ref<SessionCapturePolicyResponse | null>(null)
const loading = ref(false)
const policyLoading = ref(false)
const loadError = ref('')
const search = ref('')
const policyPage = ref(1)
const pageSize = 20
const savingKeyID = ref<number | null>(null)
let refreshController: AbortController | null = null
let policyController: AbortController | null = null
let refreshTimer: number | null = null
let searchTimer: number | null = null

const confirmation = reactive({
  show: false,
  title: '',
  message: '',
  confirmText: '',
  danger: false,
  action: null as null | { type: 'mode'; mode: SessionCaptureMode } | { type: 'only'; key: SessionCaptureAPIKey }
})

const remote = computed(() => overview.value?.remote)
const displayModel = computed(() => (overview.value?.public_model || 'claude-opus-5').replace(/-/g, ' ').replace(/\b\w/g, (char: string) => char.toUpperCase()))
const observedAtLabel = computed(() => formatDate(overview.value?.observed_at, true))
const lastVerifiedLabel = computed(() => remote.value?.delivery.last_verified_at ? formatDate(remote.value.delivery.last_verified_at) : t('admin.sessionDelivery.notYet'))
const memoryPercentNumber = computed(() => {
  const total = remote.value?.host.memory_total_bytes || 0
  return total > 0 ? ((remote.value?.host.memory_used_bytes || 0) / total) * 100 : 0
})
const memoryPercent = computed(() => formatPercent(memoryPercentNumber.value))
const totalPolicyPages = computed(() => Math.max(1, Math.ceil((policy.value?.api_keys.total || 0) / pageSize)))
const paginationLabel = computed(() => t('admin.sessionDelivery.policy.pagination', {
  page: policyPage.value,
  pages: totalPolicyPages.value,
  total: policy.value?.api_keys.total || 0
}))

const modeOptions = computed(() => [
  { value: 'all' as const, title: t('admin.sessionDelivery.policy.modeAll'), description: t('admin.sessionDelivery.policy.modeAllDescription') },
  { value: 'selected' as const, title: t('admin.sessionDelivery.policy.modeSelected'), description: t('admin.sessionDelivery.policy.modeSelectedDescription') },
  { value: 'disabled' as const, title: t('admin.sessionDelivery.policy.modeDisabled'), description: t('admin.sessionDelivery.policy.modeDisabledDescription') }
])

const pipelineSteps = computed(() => {
  const spool = overview.value?.spool
  const delivery = remote.value?.delivery
  const records = remote.value?.sessions.records_in_database || 0
  return [
    {
      code: 'GATEWAY SPOOL', title: t('admin.sessionDelivery.pipeline.spool'),
      detail: t('admin.sessionDelivery.pipeline.spoolDetail', { count: spool?.pending_records || 0, size: formatBytes(spool?.pending_bytes) }),
      state: spool?.quarantined_records ? t('admin.sessionDelivery.pipeline.attention') : t('admin.sessionDelivery.pipeline.flowing'),
      tone: spool?.quarantined_records ? 'warning' : 'healthy'
    },
    {
      code: 'ISOLATED PG 18', title: t('admin.sessionDelivery.pipeline.database'),
      detail: t('admin.sessionDelivery.pipeline.databaseDetail', { count: records, size: formatBytes(remote.value?.database.size_bytes) }),
      state: remote.value ? t('admin.sessionDelivery.pipeline.online') : t('admin.sessionDelivery.pipeline.unavailable'),
      tone: remote.value ? 'healthy' : 'critical'
    },
    {
      code: 'UTC HOURLY', title: t('admin.sessionDelivery.pipeline.package'),
      detail: delivery?.exporting_batches ? t('admin.sessionDelivery.pipeline.exporting') : t('admin.sessionDelivery.pipeline.packageDetail'),
      state: delivery?.failed_batches ? t('admin.sessionDelivery.pipeline.failed') : delivery?.exporting_batches ? t('admin.sessionDelivery.pipeline.working') : t('admin.sessionDelivery.pipeline.scheduled'),
      tone: delivery?.failed_batches ? 'critical' : delivery?.exporting_batches ? 'warning' : 'healthy'
    },
    {
      code: 'GOOGLE DRIVE', title: t('admin.sessionDelivery.pipeline.archive'),
      detail: t('admin.sessionDelivery.pipeline.archiveDetail', { files: delivery?.archive_files_verified || 0, size: formatBytes(delivery?.archive_bytes_uploaded) }),
      state: delivery?.archive_files_verified ? t('admin.sessionDelivery.pipeline.verified') : t('admin.sessionDelivery.pipeline.waiting'),
      tone: delivery?.failed_batches ? 'critical' : 'healthy'
    }
  ]
})

async function refreshAll() {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  refreshController?.abort()
  refreshController = new AbortController()
  try {
    const [overviewResult, policyResult] = await Promise.all([
      sessionDeliveryAPI.overview(refreshController.signal),
      sessionDeliveryAPI.policy({ q: search.value.trim(), page: policyPage.value, page_size: pageSize }, refreshController.signal)
    ])
    overview.value = overviewResult
    policy.value = policyResult
  } catch (error) {
    if ((error as { code?: string })?.code !== 'ERR_CANCELED') loadError.value = extractApiErrorMessage(error, t('admin.sessionDelivery.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function loadPolicy() {
  policyLoading.value = true
  policyController?.abort()
  policyController = new AbortController()
  try {
    policy.value = await sessionDeliveryAPI.policy({ q: search.value.trim(), page: policyPage.value, page_size: pageSize }, policyController.signal)
  } catch (error) {
    if ((error as { code?: string })?.code !== 'ERR_CANCELED') appStore.showError(extractApiErrorMessage(error, t('admin.sessionDelivery.policy.loadFailed')))
  } finally {
    policyLoading.value = false
  }
}

watch(search, () => {
  policyPage.value = 1
  if (searchTimer) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(loadPolicy, 300)
})

function changePage(page: number) {
  policyPage.value = Math.min(Math.max(1, page), totalPolicyPages.value)
  void loadPolicy()
}

function requestModeChange(mode: SessionCaptureMode) {
  if (policy.value?.summary.mode === mode) return
  const option = modeOptions.value.find(item => item.value === mode)!
  confirmation.show = true
  confirmation.title = t('admin.sessionDelivery.policy.confirmModeTitle', { mode: option.title })
  confirmation.message = t(`admin.sessionDelivery.policy.confirmMode_${mode}`)
  confirmation.confirmText = t('admin.sessionDelivery.policy.apply')
  confirmation.danger = mode === 'disabled'
  confirmation.action = { type: 'mode', mode }
}

function requestOnlyKey(key: SessionCaptureAPIKey) {
  confirmation.show = true
  confirmation.title = t('admin.sessionDelivery.policy.onlyConfirmTitle')
  confirmation.message = t('admin.sessionDelivery.policy.onlyConfirmMessage', { name: key.name, id: key.id })
  confirmation.confirmText = t('admin.sessionDelivery.policy.onlyThisKey')
  confirmation.danger = true
  confirmation.action = { type: 'only', key }
}

async function confirmPolicyAction() {
  const action = confirmation.action
  resetConfirmation()
  if (!action) return
  try {
    if (action.type === 'mode') await sessionDeliveryAPI.updateMode(action.mode)
    else await sessionDeliveryAPI.setOnlyAPIKey(action.key.id)
    appStore.showSuccess(t('admin.sessionDelivery.policy.saved'))
    await refreshAll()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.sessionDelivery.policy.saveFailed')))
  }
}

async function updateKeyPolicy(key: SessionCaptureAPIKey, value: SessionCaptureKeyPolicy) {
  if (value === key.policy) return
  savingKeyID.value = key.id
  try {
    await sessionDeliveryAPI.updateAPIKey(key.id, value)
    appStore.showSuccess(t('admin.sessionDelivery.policy.saved'))
    await refreshAll()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.sessionDelivery.policy.saveFailed')))
    await loadPolicy()
  } finally {
    savingKeyID.value = null
  }
}

function resetConfirmation() {
  confirmation.show = false
  confirmation.action = null
}

function statusLabel(status?: SessionDeliveryStatus | 'unknown') {
  return t(`admin.sessionDelivery.status.${status || 'unknown'}`)
}

function warningLabel(code: string) {
  const key = `admin.sessionDelivery.warnings.${code}`
  const translated = t(key)
  return translated === key ? code : translated
}

function batchStatusLabel(status: string) {
  const key = `admin.sessionDelivery.batchStatus.${status}`
  const translated = t(key)
  return translated === key ? status : translated
}

function formatNumber(value?: number) {
  return new Intl.NumberFormat().format(Number.isFinite(value) ? value! : 0)
}

function formatBytes(value?: number) {
  const bytes = Number.isFinite(value) ? Math.max(0, value!) : 0
  if (bytes < 1024) return `${bytes.toFixed(0)} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = bytes / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) { size /= 1024; index++ }
  return `${size >= 100 ? size.toFixed(0) : size.toFixed(1)} ${units[index]}`
}

function formatPercent(value?: number) {
  return `${(Number.isFinite(value) ? value! : 0).toFixed(1)}%`
}

function clampPercent(value?: number) {
  return `${Math.min(100, Math.max(0, Number.isFinite(value) ? value! : 0))}%`
}

function formatDecimal(value?: number) {
  return (Number.isFinite(value) ? value! : 0).toFixed(2)
}

function formatDate(value?: string, withSeconds = false) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat(undefined, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    second: withSeconds ? '2-digit' : undefined, hour12: false
  }).format(date)
}

function formatUTCHour(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return `${date.toISOString().slice(0, 13).replace('T', ' ')}:00Z`
}

function formatUptime(value?: number) {
  const seconds = Number.isFinite(value) ? Math.max(0, value!) : 0
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  return days ? `${days}d ${hours}h` : `${hours}h`
}

function resourceTone(value: number | undefined, warning: number, critical: number) {
  const numeric = Number.isFinite(value) ? value! : 0
  if (numeric >= critical) return 'meter-critical'
  if (numeric >= warning) return 'meter-warning'
  return 'meter-healthy'
}

onMounted(() => {
  void refreshAll()
  refreshTimer = window.setInterval(refreshAll, 15_000)
})

onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (searchTimer) window.clearTimeout(searchTimer)
  refreshController?.abort()
  policyController?.abort()
})
</script>

<style scoped>
.session-page { --line: #dce3ea; --ink: #10202f; --muted: #68798a; }
.eyebrow { border: 1px solid #0f766e; color: #0f766e; padding: .28rem .48rem; border-radius: .25rem; font: 700 .65rem/1 ui-monospace, monospace; letter-spacing: .14em; }
.status-pill,.model-pill { display:inline-flex;align-items:center;gap:.42rem;border:1px solid var(--line);border-radius:999px;padding:.3rem .6rem;font-size:.7rem;font-weight:700;text-transform:uppercase;letter-spacing:.08em;color:#526373;background:rgba(255,255,255,.75) }
.model-pill { font-family:ui-monospace,monospace;color:#334155 }
.status-dot { width:.45rem;height:.45rem;border-radius:50%;background:#94a3b8 }
.status-healthy .status-dot{background:#0d9488;box-shadow:0 0 0 3px rgba(13,148,136,.13)} .status-degraded .status-dot{background:#d97706}.status-critical .status-dot{background:#dc2626}.status-paused .status-dot{background:#64748b}
.control-button{display:inline-flex;align-items:center;gap:.55rem;border:1px solid #cbd5e1;background:#fff;border-radius:.5rem;padding:.62rem .9rem;color:#334155;font-size:.8rem;font-weight:700;box-shadow:0 1px 2px rgba(15,23,42,.04)}.control-button:hover{border-color:#0f766e;color:#0f766e}.control-button:disabled{opacity:.55}.control-button svg{width:1rem;height:1rem}
.alert-panel{display:flex;align-items:center;justify-content:space-between;gap:1rem;border-radius:.55rem;border:1px solid;padding:.85rem 1rem}.alert-warning{border-color:#fcd34d;background:#fffbeb;color:#92400e}.alert-critical{border-color:#fca5a5;background:#fef2f2;color:#991b1b}
.metric-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:1rem}.metric-card{border:1px solid var(--line);border-radius:.65rem;background:rgba(255,255,255,.92);padding:1.05rem 1.1rem;box-shadow:0 8px 24px rgba(15,23,42,.035)}.accent-card{border-top:3px solid #0f766e;padding-top:.93rem}.metric-topline{display:flex;justify-content:space-between;gap:.5rem;color:#64748b;font-size:.65rem;font-weight:800;letter-spacing:.09em;text-transform:uppercase}.metric-topline span:last-child{font-family:ui-monospace,monospace;color:#94a3b8}.metric-value{margin-top:.8rem;color:var(--ink);font:650 1.75rem/1.1 ui-monospace,monospace;letter-spacing:-.05em}.metric-caption{margin-top:.65rem;color:#7a8997;font-size:.72rem}.split-caption{display:flex;justify-content:space-between;gap:.7rem}
.console-panel{border:1px solid var(--line);border-radius:.7rem;background:rgba(255,255,255,.94);padding:1.25rem;box-shadow:0 10px 28px rgba(15,23,42,.04)}.panel-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:1rem}.section-kicker{font:700 .64rem/1 ui-monospace,monospace;letter-spacing:.14em;color:#0f766e}.panel-heading h2{margin-top:.4rem;color:var(--ink);font-size:1.05rem;font-weight:700}.polling-note,.host-label{display:inline-flex;align-items:center;gap:.45rem;border:1px solid var(--line);border-radius:.3rem;padding:.35rem .5rem;color:#64748b;font:700 .65rem/1 ui-monospace,monospace;letter-spacing:.06em}.polling-note span{width:.4rem;height:.4rem;border-radius:50%;background:#0d9488;animation:pulse 2s infinite}
.pipeline-grid{display:grid;grid-template-columns:minmax(0,1fr) 28px minmax(0,1fr) 28px minmax(0,1fr) 28px minmax(0,1fr);align-items:stretch;margin-top:1.2rem}.pipeline-step{position:relative;display:flex;gap:.8rem;min-height:8rem;border:1px solid #dfe6ec;border-top:3px solid #0d9488;border-radius:.45rem;padding:.9rem;background:#f8fafc}.pipeline-warning{border-top-color:#d97706}.pipeline-critical{border-top-color:#dc2626}.step-index{color:#a5b2bf;font:700 .7rem/1 ui-monospace,monospace}.step-code{color:#0f766e;font:700 .62rem/1 ui-monospace,monospace;letter-spacing:.09em}.pipeline-step h3{margin-top:.45rem;color:#1e293b;font-size:.9rem;font-weight:700}.pipeline-step p:not(.step-code){margin-top:.35rem;color:#748392;font-size:.72rem;line-height:1.4}.step-state{position:absolute;bottom:.7rem;left:2.35rem;color:#0f766e;font:700 .62rem/1 ui-monospace,monospace;text-transform:uppercase}.pipeline-warning .step-state{color:#b45309}.pipeline-critical .step-state{color:#b91c1c}.pipeline-link{display:flex;align-items:center;color:#94a3b8}.pipeline-link span{height:1px;flex:1;background:#cbd5e1}.pipeline-link svg{width:.75rem}
.resource-grid{margin-top:1.2rem;display:grid;gap:1.05rem}.resource-label{display:flex;justify-content:space-between;color:#526273;font-size:.75rem;font-weight:700}.resource-label strong{font-family:ui-monospace,monospace;color:#1e293b}.meter{height:.3rem;margin-top:.5rem;overflow:hidden;border-radius:999px;background:#e7edf2}.meter span{display:block;height:100%;border-radius:inherit;transition:width .4s}.meter-healthy{background:#0d9488}.meter-warning{background:#d97706}.meter-critical{background:#dc2626}.resource-row p{margin-top:.38rem;color:#8a98a5;font:500 .68rem/1.2 ui-monospace,monospace}.detail-strip{display:grid;grid-template-columns:repeat(4,1fr);gap:1px;margin-top:1.25rem;border:1px solid var(--line);background:var(--line)}.detail-strip div{background:#f8fafc;padding:.7rem}.detail-strip span{display:block;color:#81909e;font-size:.65rem}.detail-strip strong{display:block;margin-top:.3rem;color:#293949;font:700 .75rem/1 ui-monospace,monospace}
.inventory-list{display:grid;grid-template-columns:1fr 1fr;gap:.7rem;margin-top:1.2rem}.inventory-row{display:flex;align-items:center;justify-content:space-between;border-bottom:1px solid #e8edf1;padding:.55rem 0;color:#6b7c8d;font-size:.76rem}.inventory-row strong{color:#203243;font-family:ui-monospace,monospace}.spool-box{display:flex;justify-content:space-between;gap:1rem;margin-top:1rem;border-left:3px solid #0f766e;background:#f1f5f7;padding:.85rem}.spool-box p{color:#7a8997;font-size:.67rem}.spool-box strong{display:block;margin-top:.25rem;color:#263746;font:700 .74rem/1 ui-monospace,monospace}
.batch-table-wrap{margin:1.1rem -1.25rem -1.25rem;overflow-x:auto}.batch-table,.key-table{width:100%;border-collapse:collapse;text-align:left}.batch-table th,.key-table th{border-top:1px solid var(--line);border-bottom:1px solid var(--line);background:#f8fafc;padding:.65rem 1.25rem;color:#7d8b99;font-size:.62rem;font-weight:800;letter-spacing:.08em}.batch-table td,.key-table td{border-bottom:1px solid #edf1f4;padding:.72rem 1.25rem;color:#526273;font-size:.74rem;white-space:nowrap}.batch-status,.effective-state{display:inline-flex;align-items:center;border-radius:999px;padding:.25rem .5rem;background:#e7f5f2;color:#0f766e;font-size:.65rem;font-weight:700}.batch-failed{background:#fee2e2;color:#b91c1c}.batch-exporting,.batch-archived{background:#fef3c7;color:#92400e}.empty-ledger{display:flex;align-items:center;justify-content:center;gap:1rem;padding:2rem;color:#6b7c8d}.empty-clock{border:1px solid #cbd5e1;border-radius:.35rem;padding:.5rem;color:#0f766e;font:700 1rem/1 ui-monospace,monospace}.empty-ledger strong{color:#334155;font-size:.8rem}.empty-ledger p{margin-top:.2rem;font-size:.72rem}
.policy-panel{padding:1.25rem}.policy-count{text-align:right}.policy-count span{display:block;color:#7a8997;font-size:.65rem;text-transform:uppercase}.policy-count strong{display:block;margin-top:.25rem;color:#0f766e;font:700 1.2rem/1 ui-monospace,monospace}.mode-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:.75rem;margin-top:1.15rem}.mode-card{display:flex;gap:.7rem;text-align:left;border:1px solid #dce3e9;border-radius:.45rem;background:#f8fafc;padding:.85rem;transition:.15s}.mode-card:hover{border-color:#94a3b8}.mode-card.active{border-color:#0f766e;background:#f0fdfa;box-shadow:inset 0 0 0 1px #0f766e}.mode-card.danger.active{border-color:#dc2626;background:#fef2f2;box-shadow:inset 0 0 0 1px #dc2626}.mode-radio{display:flex;align-items:center;justify-content:center;width:1rem;height:1rem;flex:none;border:1px solid #94a3b8;border-radius:50%;margin-top:.1rem}.mode-card.active .mode-radio{border-color:#0f766e}.mode-card.active .mode-radio i{width:.5rem;height:.5rem;border-radius:50%;background:#0f766e}.mode-card.danger.active .mode-radio{border-color:#dc2626}.mode-card.danger.active .mode-radio i{background:#dc2626}.mode-card strong{display:block;color:#263746;font-size:.78rem}.mode-card small{display:block;margin-top:.35rem;color:#7a8997;font-size:.68rem;line-height:1.4}.policy-note{display:flex;align-items:flex-start;gap:.55rem;margin-top:.8rem;color:#8a6414;background:#fffbeb;border:1px solid #fde68a;border-radius:.35rem;padding:.65rem .75rem;font-size:.7rem}.policy-note svg{width:1rem;flex:none}.key-toolbar{display:flex;align-items:center;justify-content:space-between;gap:1rem;margin-top:1.2rem}.key-search{display:flex;align-items:center;gap:.5rem;width:min(28rem,100%);border:1px solid #cfd8e1;border-radius:.4rem;background:#fff;padding:.55rem .7rem}.key-search svg{width:1rem;color:#94a3b8}.key-search input{width:100%;outline:none;color:#334155;font-size:.75rem;background:transparent}.policy-legend{display:flex;gap:1rem;color:#7a8997;font-size:.68rem}.policy-legend span{display:flex;align-items:center;gap:.35rem}.policy-legend i,.effective-state i{width:.4rem;height:.4rem;border-radius:50%}.legend-on,.is-on i{background:#0d9488}.legend-off,.is-off i{background:#94a3b8}.key-table-wrap{position:relative;margin:1rem -1.25rem 0;overflow-x:auto;border-top:1px solid var(--line)}.key-table th{border-top:0}.key-identity{display:flex;align-items:center;gap:.5rem}.key-identity span{color:#94a3b8;font:600 .65rem/1 ui-monospace,monospace}.key-identity strong{color:#27394a;font-size:.74rem}.effective-state.is-off{background:#eef2f5;color:#64748b}.key-table select{min-width:7.5rem;border:1px solid #cbd5e1;border-radius:.3rem;background:#fff;padding:.35rem .5rem;color:#475569;font-size:.7rem}.only-button{border:1px solid #cbd5e1;border-radius:.3rem;padding:.35rem .55rem;color:#475569;font-size:.68rem;font-weight:700}.only-button:hover{border-color:#b91c1c;color:#b91c1c}.only-button:disabled{opacity:.5}.table-loading{text-align:center;padding:1.5rem;color:#94a3b8;font-size:.75rem}.key-pagination{display:flex;align-items:center;justify-content:space-between;margin-top:1rem;color:#7a8997;font-size:.7rem}.key-pagination div{display:flex;gap:.35rem}.key-pagination button{width:2rem;height:1.8rem;border:1px solid #cbd5e1;border-radius:.3rem;color:#475569}.key-pagination button:disabled{opacity:.35}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.35}}
@media(max-width:1200px){.metric-grid{grid-template-columns:repeat(2,1fr)}.pipeline-panel{overflow-x:auto}.pipeline-grid{min-width:1000px}}
@media(max-width:768px){.metric-grid,.mode-grid{grid-template-columns:1fr}.pipeline-panel{overflow-x:visible}.pipeline-grid{display:block;min-width:0}.pipeline-link{height:1.5rem;justify-content:center;transform:rotate(90deg)}.pipeline-link span{display:none}.detail-strip{grid-template-columns:1fr 1fr}.inventory-list{grid-template-columns:1fr}.key-toolbar,.panel-heading{align-items:flex-start;flex-direction:column}.policy-legend{display:none}.metric-value{font-size:1.5rem}}
:global(.dark) .session-page{--line:#273747;--ink:#e5edf5;--muted:#8fa0b0}:global(.dark) .status-pill,:global(.dark) .model-pill{background:rgba(13,24,35,.82);color:#aab8c5}:global(.dark) .control-button,:global(.dark) .metric-card,:global(.dark) .console-panel{background:rgba(12,23,34,.94);border-color:#293a4a}:global(.dark) .metric-value,:global(.dark) .resource-label strong,:global(.dark) .panel-heading h2{color:#e7eef5}:global(.dark) .pipeline-step,:global(.dark) .detail-strip div,:global(.dark) .batch-table th,:global(.dark) .key-table th,:global(.dark) .mode-card{background:#111f2c;border-color:#2a3b4b}:global(.dark) .pipeline-step h3,:global(.dark) .detail-strip strong,:global(.dark) .inventory-row strong,:global(.dark) .spool-box strong,:global(.dark) .mode-card strong,:global(.dark) .key-identity strong{color:#dce7f0}:global(.dark) .spool-box{background:#132832}:global(.dark) .mode-card.active{background:#102a29}:global(.dark) .mode-card.danger.active{background:#311b20}:global(.dark) .key-search,:global(.dark) .key-table select{background:#101d29;border-color:#314252;color:#c7d2dc}:global(.dark) .batch-table td,:global(.dark) .key-table td{border-color:#21313f;color:#9aabb9}:global(.dark) .meter{background:#263746}:global(.dark) .alert-warning{background:#302614;border-color:#6b5218;color:#fcd34d}:global(.dark) .alert-critical{background:#321c21;border-color:#74303a;color:#fca5a5}
</style>
