<template>
  <section class="overflow-hidden rounded-xl border border-sky-200 bg-gradient-to-br from-sky-50 via-white to-cyan-50/70 dark:border-sky-900/60 dark:from-sky-950/30 dark:via-dark-800 dark:to-cyan-950/20">
    <div class="flex flex-col gap-4 p-4 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <span class="inline-flex h-8 w-8 items-center justify-center rounded-lg bg-sky-100 text-sky-700 dark:bg-sky-900/50 dark:text-sky-300">
            <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.8" d="M3 13.5h4l2.25-7.5 4.5 12 2.25-7.5h5" />
            </svg>
          </span>
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.externalQuotaGate.title') }}
            </h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.externalQuotaGate.description') }}
            </p>
          </div>
        </div>
      </div>

      <button
        type="button"
        role="switch"
        :aria-checked="enabled"
        :disabled="busy"
        :title="enabled ? t('admin.accounts.externalQuotaGate.disable') : t('admin.accounts.externalQuotaGate.enable')"
        class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2 disabled:cursor-wait disabled:opacity-60 dark:focus:ring-offset-dark-800"
        :class="enabled ? 'bg-sky-500' : 'bg-gray-300 dark:bg-dark-600'"
        @click="toggleGate"
      >
        <span
          class="pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow transition-transform"
          :class="enabled ? 'translate-x-5' : 'translate-x-0'"
        />
      </button>
    </div>

    <div v-if="enabled" class="border-t border-sky-100 bg-white/70 px-4 py-4 dark:border-sky-900/50 dark:bg-dark-900/20">
      <div class="mb-4 flex flex-wrap gap-2 text-[11px] font-medium">
        <span class="rounded-full bg-sky-100 px-2.5 py-1 text-sky-700 dark:bg-sky-900/50 dark:text-sky-200">
          {{ t('admin.accounts.externalQuotaGate.policyInterval') }}
        </span>
        <span class="rounded-full bg-emerald-100 px-2.5 py-1 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200">
          {{ t('admin.accounts.externalQuotaGate.policyLease', { minutes: configuredGrantMinutes }) }}
        </span>
        <span class="rounded-full bg-amber-100 px-2.5 py-1 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200">
          {{ t('admin.accounts.externalQuotaGate.policyDrain') }}
        </span>
      </div>

      <div class="mb-4 flex flex-col gap-3 rounded-lg border border-sky-100 bg-sky-50/70 p-3 dark:border-sky-900/50 dark:bg-sky-950/20 sm:flex-row sm:items-end sm:justify-between">
        <label class="block min-w-0 flex-1">
          <span class="text-xs font-semibold text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.externalQuotaGate.grantMinutes') }}
          </span>
          <span class="mt-0.5 block text-[11px] leading-4 text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.externalQuotaGate.grantMinutesHint') }}
          </span>
          <input
            v-model.number="grantMinutesInput"
            data-testid="external-quota-grant-minutes"
            type="number"
            min="30"
            max="720"
            step="30"
            :disabled="busy"
            class="input mt-2 w-36 tabular-nums"
          >
        </label>
        <button
          type="button"
          data-testid="save-external-quota-grant"
          class="btn btn-primary shrink-0 px-3 py-1.5 text-xs"
          :disabled="busy || !grantMinutesValid || grantMinutesInput === configuredGrantMinutes"
          @click="saveGrantMinutes"
        >
          {{ t('admin.accounts.externalQuotaGate.savePolicy') }}
        </button>
      </div>

      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <span :class="['h-2 w-2 rounded-full', statusDotClass]" />
          <span :class="['text-sm font-semibold', statusTextClass]">{{ decisionLabel }}</span>
          <span :class="['rounded-full px-2 py-0.5 text-[11px]', schedulingBadgeClass]">
            {{ schedulingLabel }}
          </span>
        </div>
        <button type="button" class="btn btn-secondary px-3 py-1.5 text-xs" :disabled="busy" @click="refreshGate">
          <svg v-if="busy" class="mr-1.5 inline h-3.5 w-3.5 animate-spin" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          {{ busy ? t('admin.accounts.externalQuotaGate.refreshing') : t('admin.accounts.externalQuotaGate.refresh') }}
        </button>
      </div>

      <div class="grid grid-cols-2 gap-3 lg:grid-cols-3">
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.primaryUsage') }}</div>
          <div class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ primaryUsage }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.observationBaseline') }}</div>
          <div class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ observationBaseline }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.externalDelta') }}</div>
          <div class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ externalDelta }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.lastCheck') }}</div>
          <div class="mt-1 text-xs font-medium leading-6 text-gray-700 dark:text-gray-200">{{ lastCheck }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.externalDetectedAt') }}</div>
          <div class="mt-1 text-xs font-medium leading-6 text-gray-700 dark:text-gray-200">{{ externalDetectedAt }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.leaseUntil') }}</div>
          <div class="mt-1 text-xs font-medium leading-6 text-gray-700 dark:text-gray-200">{{ leaseUntil }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.activeStickySessions') }}</div>
          <div class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ activeStickySessions }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.inflightAndWaiting') }}</div>
          <div class="mt-1 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ inflightAndWaiting }}</div>
        </div>
        <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
          <div class="text-[11px] uppercase tracking-wide text-gray-400">{{ t('admin.accounts.externalQuotaGate.drainProgress') }}</div>
          <div class="mt-1 text-xs font-medium leading-6 text-gray-700 dark:text-gray-200">{{ drainProgress }}</div>
        </div>
      </div>

      <div v-if="recentEvents.length" class="mt-4 rounded-lg border border-gray-100 bg-white/70 p-3 dark:border-dark-700 dark:bg-dark-800/40">
        <h4 class="text-xs font-semibold text-gray-700 dark:text-gray-200">
          {{ t('admin.accounts.externalQuotaGate.recentEvents') }}
        </h4>
        <ul class="mt-2 space-y-2">
          <li v-for="(event, index) in recentEvents" :key="`${event.occurred_at}-${event.decision}-${index}`" class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-[11px]">
            <div class="flex min-w-0 items-center gap-2">
              <span :class="['h-1.5 w-1.5 shrink-0 rounded-full', event.draining ? 'bg-amber-500' : event.schedulable ? 'bg-emerald-500' : 'bg-gray-400']" />
              <span class="font-medium text-gray-700 dark:text-gray-200">{{ decisionText(event.decision) }}</span>
              <span v-if="event.draining" class="rounded bg-amber-100 px-1.5 py-0.5 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200">
                {{ t('admin.accounts.externalQuotaGate.stickyCountShort', { count: event.active_sticky_sessions ?? 0 }) }}
              </span>
              <span v-if="typeof event.used_percent === 'number'" class="tabular-nums text-gray-500 dark:text-gray-400">
                {{ event.used_percent.toFixed(1) }}%
              </span>
              <span v-if="event.external_delta_percent" class="tabular-nums text-emerald-600 dark:text-emerald-300">
                +{{ event.external_delta_percent.toFixed(1) }}%
              </span>
            </div>
            <span class="shrink-0 text-gray-400">{{ formatTimestamp(event.occurred_at) }}</span>
          </li>
        </ul>
      </div>

      <p class="mt-3 text-xs leading-5 text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.externalQuotaGate.failClosedHint') }}
      </p>
    </div>
    <div v-else class="border-t border-sky-100 px-4 py-3 text-xs text-gray-500 dark:border-sky-900/50 dark:text-gray-400">
      {{ t('admin.accounts.externalQuotaGate.disabledHint') }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { Account, OpenAIExternalQuotaGateEvent, OpenAIExternalQuotaGateState } from '@/types'

const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ updated: [account: Account] }>()

const { t, locale } = useI18n()
const appStore = useAppStore()
const busy = ref(false)
const accountState = ref<Account>(props.account)
const configuredGrantMinutes = computed(() => {
  const value = Number(accountState.value.extra?.openai_external_quota_gate_grant_minutes ?? 120)
  return Number.isInteger(value) && value >= 30 && value <= 720 ? value : 120
})
const grantMinutesInput = ref(configuredGrantMinutes.value)

watch(() => props.account, value => {
  accountState.value = value
  grantMinutesInput.value = Number(value.extra?.openai_external_quota_gate_grant_minutes ?? 120)
})

const enabled = computed(() => accountState.value.extra?.openai_external_quota_gate_enabled === true)
const state = computed<OpenAIExternalQuotaGateState | null>(() =>
  accountState.value.extra?.openai_external_quota_gate_state ?? null
)
const draining = computed(() =>
  accountState.value.extra?.openai_external_quota_gate_draining === true || Boolean(state.value?.drain_started_at)
)
const grantMinutesValid = computed(() =>
  Number.isInteger(grantMinutesInput.value) && grantMinutesInput.value >= 30 && grantMinutesInput.value <= 720
)

const decisionLabels: Record<string, string> = {
  initializing: 'initializing',
  external_decrease_detected: 'externalDecrease',
  lease_active: 'leaseActive',
  lease_active_upstream_error: 'leaseActiveUpstreamError',
  draining_started: 'drainingStarted',
  draining: 'draining',
  draining_without_lease: 'drainingWithoutLease',
  draining_window_changed: 'drainingWindowChanged',
  draining_upstream_error: 'drainingUpstreamError',
  draining_check_error: 'drainingCheckError',
  drain_complete: 'drainComplete',
  upstream_unavailable: 'upstreamUnavailable',
  baseline_created: 'baselineCreated',
  observing_external_usage: 'observingExternalUsage',
  observation_cooldown: 'observationCooldown',
  no_external_decrease: 'noExternalDecrease',
  local_traffic_detected: 'localTraffic',
  lease_expired: 'leaseExpired',
  window_changed: 'windowChanged',
  inactive_lease_discarded: 'inactiveLeaseDiscarded',
  schedulable_without_lease_closed: 'schedulableWithoutLeaseClosed',
  invalid_account: 'invalidAccount',
  upstream_error: 'upstreamError',
  local_usage_error: 'localUsageError'
}

const decisionText = (decision?: string) =>
  t(`admin.accounts.externalQuotaGate.decisions.${decisionLabels[decision || 'initializing'] || 'unknown'}`)

const decisionLabel = computed(() => decisionText(state.value?.decision))

const statusDotClass = computed(() => draining.value ? 'bg-amber-500' : accountState.value.schedulable ? 'bg-emerald-500' : state.value?.decision?.includes('error') ? 'bg-rose-500' : 'bg-gray-400')
const statusTextClass = computed(() => draining.value ? 'text-amber-700 dark:text-amber-300' : accountState.value.schedulable ? 'text-emerald-700 dark:text-emerald-300' : state.value?.decision?.includes('error') ? 'text-rose-700 dark:text-rose-300' : 'text-gray-700 dark:text-gray-300')
const schedulingLabel = computed(() => draining.value
  ? t('admin.accounts.externalQuotaGate.drainingOnly')
  : accountState.value.schedulable
    ? t('admin.accounts.externalQuotaGate.available')
    : t('admin.accounts.externalQuotaGate.unavailable'))
const schedulingBadgeClass = computed(() => draining.value
  ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200'
  : accountState.value.schedulable
    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-200'
    : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300')
const primaryUsage = computed(() => state.value?.primary_window ? `${state.value.primary_window.used_percent.toFixed(1)}%` : '—')
const observationBaseline = computed(() => state.value?.baseline_primary_window ? `${state.value.baseline_primary_window.used_percent.toFixed(1)}%` : '—')
const externalDelta = computed(() => state.value?.external_delta_percent ? `+${state.value.external_delta_percent.toFixed(1)}%` : '—')
const recentEvents = computed<OpenAIExternalQuotaGateEvent[]>(() => [...(state.value?.recent_events ?? [])].reverse())

const formatTimestamp = (value?: string) => {
  if (!value) return '—'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '—' : parsed.toLocaleString(locale.value)
}
const lastCheck = computed(() => formatTimestamp(state.value?.last_attempt_at))
const externalDetectedAt = computed(() => formatTimestamp(state.value?.external_detected_at))
const leaseUntil = computed(() => formatTimestamp(state.value?.lease_until || state.value?.last_lease_until))
const activeStickySessions = computed(() => state.value?.active_sticky_sessions ?? 0)
const inflightAndWaiting = computed(() => `${state.value?.active_requests ?? 0} / ${state.value?.waiting_requests ?? 0}`)
const drainProgress = computed(() => draining.value
  ? t('admin.accounts.externalQuotaGate.drainChecks', { current: state.value?.drain_empty_checks ?? 0, required: 2 })
  : '—')

const applyUpdatedAccount = (account: Account) => {
  accountState.value = account
  emit('updated', account)
}

const toggleGate = async () => {
  if (busy.value) return
  busy.value = true
  try {
    const updated = await adminAPI.accounts.configureExternalQuotaGate(accountState.value.id, !enabled.value, grantMinutesInput.value)
    applyUpdatedAccount(updated)
    appStore.showSuccess(t(enabled.value ? 'admin.accounts.externalQuotaGate.enabledSuccess' : 'admin.accounts.externalQuotaGate.disabledSuccess'))
  } catch (error: any) {
    appStore.showError(error.message || t('admin.accounts.externalQuotaGate.updateFailed'))
  } finally {
    busy.value = false
  }
}

const saveGrantMinutes = async () => {
  if (busy.value || !enabled.value || !grantMinutesValid.value) return
  busy.value = true
  try {
    const updated = await adminAPI.accounts.configureExternalQuotaGate(accountState.value.id, true, grantMinutesInput.value)
    applyUpdatedAccount(updated)
    grantMinutesInput.value = configuredGrantMinutes.value
    appStore.showSuccess(t('admin.accounts.externalQuotaGate.policySaved'))
  } catch (error: any) {
    appStore.showError(error.message || t('admin.accounts.externalQuotaGate.updateFailed'))
  } finally {
    busy.value = false
  }
}

const refreshGate = async () => {
  if (busy.value || !enabled.value) return
  busy.value = true
  try {
    const updated = await adminAPI.accounts.refreshExternalQuotaGate(accountState.value.id)
    applyUpdatedAccount(updated)
    appStore.showSuccess(t('admin.accounts.externalQuotaGate.refreshedSuccess'))
  } catch (error: any) {
    appStore.showError(error.message || t('admin.accounts.externalQuotaGate.refreshFailed'))
  } finally {
    busy.value = false
  }
}
</script>
