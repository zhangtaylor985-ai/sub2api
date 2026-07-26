<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="space-y-4">
          <div class="grid grid-cols-2 gap-3 xl:grid-cols-4">
            <div class="summary-card">
              <div class="summary-card-glow bg-blue-500/10"></div>
              <div class="summary-icon bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-300">
                <Icon name="users" size="md" />
              </div>
              <div class="min-w-0">
                <p class="summary-label">{{ t('admin.privateSubscriptions.summary.total') }}</p>
                <p class="summary-value">{{ summaryValue(summary.total) }}</p>
                <p class="summary-detail">
                  {{
                    t('admin.privateSubscriptions.summary.activeDetail', {
                      count: summaryValue(summary.active)
                    })
                  }}
                </p>
              </div>
            </div>

            <div class="summary-card">
              <div class="summary-card-glow bg-amber-500/10"></div>
              <div
                class="summary-icon bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-300"
              >
                <Icon name="clock" size="md" />
              </div>
              <div class="min-w-0">
                <p class="summary-label">{{ t('admin.privateSubscriptions.summary.dueSoon') }}</p>
                <p class="summary-value text-amber-600 dark:text-amber-300">
                  {{ summaryValue(summary.due_soon) }}
                </p>
                <p class="summary-detail">{{ t('admin.privateSubscriptions.summary.dueSoonDetail') }}</p>
              </div>
            </div>

            <div class="summary-card">
              <div class="summary-card-glow bg-red-500/10"></div>
              <div class="summary-icon bg-red-50 text-red-600 dark:bg-red-500/10 dark:text-red-300">
                <Icon name="exclamationTriangle" size="md" />
              </div>
              <div class="min-w-0">
                <p class="summary-label">{{ t('admin.privateSubscriptions.summary.expired') }}</p>
                <p class="summary-value text-red-600 dark:text-red-300">
                  {{ summaryValue(summary.expired) }}
                </p>
                <p class="summary-detail">{{ t('admin.privateSubscriptions.summary.expiredDetail') }}</p>
              </div>
            </div>

            <div class="summary-card">
              <div class="summary-card-glow bg-emerald-500/10"></div>
              <div
                class="summary-icon bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-300"
              >
                <Icon name="dollar" size="md" />
              </div>
              <div class="min-w-0">
                <p class="summary-label">{{ t('admin.privateSubscriptions.summary.amount') }}</p>
                <p class="truncate text-xl font-semibold tracking-tight text-gray-950 dark:text-white">
                  {{
                    loadingSummary
                      ? '—'
                      : formatCNYFromCents(summary.total_amount_cents)
                  }}
                </p>
                <p class="summary-detail">{{ t('admin.privateSubscriptions.summary.amountDetail') }}</p>
              </div>
            </div>
          </div>

          <div
            class="flex items-start gap-3 rounded-xl border border-sky-200/80 bg-sky-50/70 px-4 py-3 text-sm text-sky-900 dark:border-sky-800/60 dark:bg-sky-950/30 dark:text-sky-100"
          >
            <Icon name="bell" size="md" class="mt-0.5 flex-shrink-0 text-sky-600 dark:text-sky-300" />
            <div>
              <p class="font-medium">{{ t('admin.privateSubscriptions.reminderBanner.title') }}</p>
              <p class="mt-0.5 text-xs leading-5 text-sky-700 dark:text-sky-300">
                {{ t('admin.privateSubscriptions.reminderBanner.description') }}
              </p>
            </div>
          </div>
        </div>
      </template>

      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-52 flex-1 sm:max-w-72">
            <input
              v-model="searchQuery"
              type="search"
              class="input"
              :placeholder="t('admin.privateSubscriptions.filters.search')"
              @input="handleTextFilter"
            />
          </div>
          <Select
            v-model="filters.status"
            :options="statusFilterOptions"
            class="w-40"
            @change="handleStatusChange"
          />
          <div class="w-36">
            <input
              v-model="filters.subscriptionType"
              type="search"
              class="input"
              :placeholder="t('admin.privateSubscriptions.filters.type')"
              @input="handleTextFilter"
            />
          </div>

          <div class="flex flex-1 items-center justify-end gap-2">
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="loading"
              :title="t('common.refresh')"
              @click="refreshAll"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button type="button" class="btn btn-primary" @click="openCreateDialog">
              <Icon name="plus" size="md" class="mr-1" />
              {{ t('admin.privateSubscriptions.create') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="subscriptions"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="expires_on"
          default-sort-order="asc"
          @sort="handleSort"
        >
          <template #cell-name="{ value, row }">
            <div class="min-w-36">
              <p class="font-medium text-gray-950 dark:text-white">{{ value }}</p>
              <p class="mt-1 text-xs text-gray-400 dark:text-dark-400">#{{ row.id }}</p>
            </div>
          </template>

          <template #cell-subscription_type="{ value }">
            <span
              class="inline-flex rounded-lg border border-violet-200 bg-violet-50 px-2.5 py-1 text-xs font-semibold tracking-wide text-violet-700 dark:border-violet-800/70 dark:bg-violet-950/40 dark:text-violet-300"
            >
              {{ value }}
            </span>
          </template>

          <template #cell-amount_cents="{ value }">
            <span class="whitespace-nowrap font-medium text-gray-900 dark:text-gray-100">
              {{ formatCNYFromCents(value) }}
            </span>
          </template>

          <template #cell-expires_on="{ row }">
            <div class="min-w-36">
              <p class="font-medium text-gray-900 dark:text-gray-100">
                {{ formatDateOnly(row.expires_on) }}
              </p>
              <p :class="['mt-1 text-xs', remainingClass(row.days_remaining)]">
                {{ remainingLabel(row.days_remaining) }}
              </p>
            </div>
          </template>

          <template #cell-status="{ value }">
            <span :class="['badge', statusClass(value)]">
              {{ statusLabel(value) }}
            </span>
          </template>

          <template #cell-reminder="{ row }">
            <div class="flex min-w-28 items-center gap-2 text-xs">
              <span :class="['h-2 w-2 rounded-full', reminderDotClass(row)]"></span>
              <span :class="reminderTextClass(row)">
                {{ reminderLabel(row) }}
              </span>
            </div>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                type="button"
                class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-800 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                :title="t('common.edit')"
                @click="openEditDialog(row)"
              >
                <Icon name="edit" size="sm" />
              </button>
              <button
                type="button"
                class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                :title="t('common.delete')"
                @click="openDeleteDialog(row)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.privateSubscriptions.empty.title')"
              :description="t('admin.privateSubscriptions.empty.description')"
              :action-text="t('admin.privateSubscriptions.create')"
              @action="openCreateDialog"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showEditDialog"
      :title="
        isEditing
          ? t('admin.privateSubscriptions.edit')
          : t('admin.privateSubscriptions.create')
      "
      width="normal"
      @close="closeEditDialog"
    >
      <form id="private-subscription-form" class="space-y-5" @submit.prevent="handleSave">
        <div>
          <label class="input-label" for="private-subscription-name">
            {{ t('admin.privateSubscriptions.form.name') }}
          </label>
          <input
            id="private-subscription-name"
            v-model="form.name"
            type="text"
            maxlength="120"
            class="input"
            :placeholder="t('admin.privateSubscriptions.form.namePlaceholder')"
            autocomplete="off"
          />
          <p v-if="formErrors.name" class="mt-1.5 text-xs text-red-600 dark:text-red-400">
            {{ formErrors.name }}
          </p>
        </div>

        <div>
          <label class="input-label" for="private-subscription-type">
            {{ t('admin.privateSubscriptions.form.type') }}
          </label>
          <div class="mb-2 flex gap-2">
            <button
              v-for="preset in typePresets"
              :key="preset"
              type="button"
              :class="[
                'rounded-lg border px-3 py-1.5 text-xs font-semibold transition-colors',
                form.subscriptionType === preset
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-500/10 dark:text-primary-300'
                  : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-300'
              ]"
              @click="form.subscriptionType = preset"
            >
              {{ preset }}
            </button>
          </div>
          <input
            id="private-subscription-type"
            v-model="form.subscriptionType"
            type="text"
            maxlength="50"
            class="input"
            :placeholder="t('admin.privateSubscriptions.form.typePlaceholder')"
            autocomplete="off"
          />
          <p class="input-hint">{{ t('admin.privateSubscriptions.form.typeHint') }}</p>
          <p v-if="formErrors.subscriptionType" class="mt-1.5 text-xs text-red-600 dark:text-red-400">
            {{ formErrors.subscriptionType }}
          </p>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label" for="private-subscription-amount">
              {{ t('admin.privateSubscriptions.form.amount') }}
            </label>
            <div class="relative">
              <span
                class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-sm text-gray-400"
              >
                ¥
              </span>
              <input
                id="private-subscription-amount"
                v-model="form.amountYuan"
                type="text"
                inputmode="decimal"
                class="input pl-8"
                placeholder="0.00"
                autocomplete="off"
              />
            </div>
            <p class="input-hint">{{ t('admin.privateSubscriptions.form.amountHint') }}</p>
            <p v-if="formErrors.amount" class="mt-1.5 text-xs text-red-600 dark:text-red-400">
              {{ formErrors.amount }}
            </p>
          </div>

          <div>
            <label class="input-label" for="private-subscription-expiry">
              {{ t('admin.privateSubscriptions.form.expiresOn') }}
            </label>
            <input
              id="private-subscription-expiry"
              v-model="form.expiresOn"
              type="date"
              class="input"
            />
            <p class="input-hint">{{ t('admin.privateSubscriptions.form.expiresOnHint') }}</p>
            <p v-if="formErrors.expiresOn" class="mt-1.5 text-xs text-red-600 dark:text-red-400">
              {{ formErrors.expiresOn }}
            </p>
          </div>
        </div>

        <div
          class="flex gap-3 rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-xs leading-5 text-gray-600 dark:border-dark-700 dark:bg-dark-900/60 dark:text-dark-300"
        >
          <Icon name="bell" size="sm" class="mt-0.5 flex-shrink-0 text-sky-500" />
          <p>{{ t('admin.privateSubscriptions.form.reminderHint') }}</p>
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeEditDialog">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="private-subscription-form"
            class="btn btn-primary"
            :disabled="saving"
          >
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.privateSubscriptions.deleteTitle')"
      :message="
        t('admin.privateSubscriptions.deleteConfirm', {
          name: deletingSubscription?.name ?? ''
        })
      "
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="closeDeleteDialog"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  PrivateSubscription,
  PrivateSubscriptionPayload,
  PrivateSubscriptionStatus,
  PrivateSubscriptionSummary
} from '@/api/admin'
import type { Column } from '@/components/common/types'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useAppStore } from '@/stores/app'
import {
  formatCNYFromCents,
  parseCNYToCents
} from '@/utils/privateSubscriptions'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const { t, locale } = useI18n()
const appStore = useAppStore()

const subscriptions = ref<PrivateSubscription[]>([])
const loading = ref(false)
const loadingSummary = ref(false)
const summary = reactive<PrivateSubscriptionSummary>({
  total: 0,
  active: 0,
  due_soon: 0,
  expired: 0,
  total_amount_cents: 0
})

const searchQuery = ref('')
const filters = reactive({
  status: '' as PrivateSubscriptionStatus | '',
  subscriptionType: ''
})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = reactive({
  sort_by: 'expires_on',
  sort_order: 'asc' as 'asc' | 'desc'
})

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.privateSubscriptions.filters.allStatuses') },
  { value: 'active', label: t('admin.privateSubscriptions.status.active') },
  { value: 'due_soon', label: t('admin.privateSubscriptions.status.dueSoon') },
  { value: 'expired', label: t('admin.privateSubscriptions.status.expired') }
])

const columns = computed<Column[]>(() => [
  {
    key: 'name',
    label: t('admin.privateSubscriptions.columns.name'),
    sortable: true
  },
  {
    key: 'subscription_type',
    label: t('admin.privateSubscriptions.columns.type'),
    sortable: true
  },
  {
    key: 'amount_cents',
    label: t('admin.privateSubscriptions.columns.amount'),
    sortable: true
  },
  {
    key: 'expires_on',
    label: t('admin.privateSubscriptions.columns.expiresOn'),
    sortable: true
  },
  {
    key: 'status',
    label: t('admin.privateSubscriptions.columns.status')
  },
  {
    key: 'reminder',
    label: t('admin.privateSubscriptions.columns.reminder')
  },
  {
    key: 'actions',
    label: t('admin.privateSubscriptions.columns.actions')
  }
])

function summaryValue(value: number): string {
  return loadingSummary.value ? '—' : String(value)
}

function statusLabel(status: PrivateSubscriptionStatus): string {
  return t(`admin.privateSubscriptions.status.${status === 'due_soon' ? 'dueSoon' : status}`)
}

function statusClass(status: PrivateSubscriptionStatus): string {
  if (status === 'expired') return 'badge-danger'
  if (status === 'due_soon') return 'badge-warning'
  return 'badge-success'
}

function formatDateOnly(value: string): string {
  const parts = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!parts) return value
  const date = new Date(Number(parts[1]), Number(parts[2]) - 1, Number(parts[3]))
  return new Intl.DateTimeFormat(locale.value, {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  }).format(date)
}

function remainingLabel(days: number): string {
  if (days < 0) {
    return t('admin.privateSubscriptions.remaining.expired', { days: Math.abs(days) })
  }
  if (days === 0) return t('admin.privateSubscriptions.remaining.today')
  if (days === 1) return t('admin.privateSubscriptions.remaining.tomorrow')
  return t('admin.privateSubscriptions.remaining.days', { days })
}

function remainingClass(days: number): string {
  if (days < 0) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-amber-600 dark:text-amber-400'
  return 'text-gray-500 dark:text-dark-400'
}

function reminderLabel(row: PrivateSubscription): string {
  if (row.reminder_sent) return t('admin.privateSubscriptions.reminder.sent')
  if (row.days_remaining === 1) return t('admin.privateSubscriptions.reminder.pending')
  if (row.days_remaining < 1) return t('admin.privateSubscriptions.reminder.notSent')
  return t('admin.privateSubscriptions.reminder.scheduled')
}

function reminderDotClass(row: PrivateSubscription): string {
  if (row.reminder_sent) return 'bg-emerald-500'
  if (row.days_remaining === 1) return 'bg-amber-500'
  if (row.days_remaining < 1) return 'bg-gray-300 dark:bg-dark-600'
  return 'bg-sky-400'
}

function reminderTextClass(row: PrivateSubscription): string {
  if (row.reminder_sent) return 'text-emerald-700 dark:text-emerald-300'
  if (row.days_remaining === 1) return 'text-amber-700 dark:text-amber-300'
  return 'text-gray-500 dark:text-dark-400'
}

function errorDetail(error: unknown): string | undefined {
  return (error as { response?: { data?: { detail?: string } } }).response?.data?.detail
}

let currentController: AbortController | null = null

async function loadSubscriptions() {
  currentController?.abort()
  const controller = new AbortController()
  currentController = controller

  try {
    loading.value = true
    const result = await adminAPI.privateSubscriptions.list(
      pagination.page,
      pagination.page_size,
      {
        search: searchQuery.value.trim() || undefined,
        status: filters.status,
        subscription_type: filters.subscriptionType.trim() || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      },
      { signal: controller.signal }
    )

    if (controller.signal.aborted || currentController !== controller) return
    subscriptions.value = result.items
    pagination.total = result.total
    pagination.pages = result.pages
    pagination.page = result.page
    pagination.page_size = result.page_size
  } catch (error: unknown) {
    const requestError = error as { name?: string; code?: string }
    if (
      controller.signal.aborted ||
      currentController !== controller ||
      requestError.name === 'AbortError' ||
      requestError.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Failed to load private subscriptions:', error)
    appStore.showError(
      errorDetail(error) ?? t('admin.privateSubscriptions.errors.failedToLoad')
    )
  } finally {
    if (currentController === controller) {
      currentController = null
      loading.value = false
    }
  }
}

async function loadSummary() {
  loadingSummary.value = true
  try {
    Object.assign(summary, await adminAPI.privateSubscriptions.summary())
  } catch (error: unknown) {
    console.error('Failed to load private subscription summary:', error)
  } finally {
    loadingSummary.value = false
  }
}

async function refreshAll() {
  await Promise.all([loadSubscriptions(), loadSummary()])
}

function handlePageChange(page: number) {
  pagination.page = page
  loadSubscriptions()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadSubscriptions()
}

function handleStatusChange() {
  pagination.page = 1
  loadSubscriptions()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadSubscriptions()
}

let searchDebounceTimer: number | null = null
function handleTextFilter() {
  if (searchDebounceTimer !== null) window.clearTimeout(searchDebounceTimer)
  searchDebounceTimer = window.setTimeout(() => {
    pagination.page = 1
    loadSubscriptions()
  }, 300)
}

const typePresets = ['5X', '20X']
const showEditDialog = ref(false)
const saving = ref(false)
const editingSubscription = ref<PrivateSubscription | null>(null)
const isEditing = computed(() => editingSubscription.value !== null)
const form = reactive({
  name: '',
  subscriptionType: '5X',
  amountYuan: '',
  expiresOn: ''
})
const formErrors = reactive({
  name: '',
  subscriptionType: '',
  amount: '',
  expiresOn: ''
})

function resetFormErrors() {
  formErrors.name = ''
  formErrors.subscriptionType = ''
  formErrors.amount = ''
  formErrors.expiresOn = ''
}

function resetForm() {
  form.name = ''
  form.subscriptionType = '5X'
  form.amountYuan = ''
  form.expiresOn = ''
  resetFormErrors()
}

function openCreateDialog() {
  editingSubscription.value = null
  resetForm()
  showEditDialog.value = true
}

function openEditDialog(row: PrivateSubscription) {
  editingSubscription.value = row
  form.name = row.name
  form.subscriptionType = row.subscription_type
  form.amountYuan = (row.amount_cents / 100).toFixed(2)
  form.expiresOn = row.expires_on
  resetFormErrors()
  showEditDialog.value = true
}

function closeEditDialog() {
  showEditDialog.value = false
  editingSubscription.value = null
  resetFormErrors()
}

function buildPayload(): PrivateSubscriptionPayload | null {
  resetFormErrors()
  const name = form.name.trim()
  const subscriptionType = form.subscriptionType.trim()
  const amountCents = parseCNYToCents(form.amountYuan)

  if (!name) {
    formErrors.name = t('admin.privateSubscriptions.validation.nameRequired')
  }
  if (!subscriptionType) {
    formErrors.subscriptionType = t('admin.privateSubscriptions.validation.typeRequired')
  }
  if (amountCents === null) {
    formErrors.amount = t('admin.privateSubscriptions.validation.amountInvalid')
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(form.expiresOn)) {
    formErrors.expiresOn = t('admin.privateSubscriptions.validation.expiryRequired')
  }

  if (
    formErrors.name ||
    formErrors.subscriptionType ||
    formErrors.amount ||
    formErrors.expiresOn ||
    amountCents === null
  ) {
    return null
  }

  return {
    name,
    subscription_type: subscriptionType,
    amount_cents: amountCents,
    expires_on: form.expiresOn
  }
}

async function handleSave() {
  const payload = buildPayload()
  if (!payload) return

  saving.value = true
  try {
    if (editingSubscription.value) {
      await adminAPI.privateSubscriptions.update(editingSubscription.value.id, payload)
      appStore.showSuccess(t('admin.privateSubscriptions.success.updated'))
    } else {
      await adminAPI.privateSubscriptions.create(payload)
      appStore.showSuccess(t('admin.privateSubscriptions.success.created'))
    }
    closeEditDialog()
    await refreshAll()
  } catch (error: unknown) {
    console.error('Failed to save private subscription:', error)
    appStore.showError(
      errorDetail(error) ??
        t(
          editingSubscription.value
            ? 'admin.privateSubscriptions.errors.failedToUpdate'
            : 'admin.privateSubscriptions.errors.failedToCreate'
        )
    )
  } finally {
    saving.value = false
  }
}

const showDeleteDialog = ref(false)
const deletingSubscription = ref<PrivateSubscription | null>(null)

function openDeleteDialog(row: PrivateSubscription) {
  deletingSubscription.value = row
  showDeleteDialog.value = true
}

function closeDeleteDialog() {
  showDeleteDialog.value = false
  deletingSubscription.value = null
}

async function confirmDelete() {
  if (!deletingSubscription.value) return

  try {
    await adminAPI.privateSubscriptions.delete(deletingSubscription.value.id)
    appStore.showSuccess(t('admin.privateSubscriptions.success.deleted'))
    if (subscriptions.value.length === 1 && pagination.page > 1) {
      pagination.page -= 1
    }
    closeDeleteDialog()
    await refreshAll()
  } catch (error: unknown) {
    console.error('Failed to delete private subscription:', error)
    appStore.showError(
      errorDetail(error) ?? t('admin.privateSubscriptions.errors.failedToDelete')
    )
  }
}

onMounted(refreshAll)

onUnmounted(() => {
  if (searchDebounceTimer !== null) window.clearTimeout(searchDebounceTimer)
  currentController?.abort()
})
</script>

<style scoped>
.summary-card {
  @apply relative flex min-h-28 items-center gap-3 overflow-hidden rounded-2xl border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800;
}

.summary-card-glow {
  @apply pointer-events-none absolute -right-8 -top-8 h-24 w-24 rounded-full blur-2xl;
}

.summary-icon {
  @apply relative flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl;
}

.summary-label {
  @apply truncate text-xs font-medium text-gray-500 dark:text-dark-300;
}

.summary-value {
  @apply mt-0.5 text-2xl font-semibold tracking-tight text-gray-950 dark:text-white;
}

.summary-detail {
  @apply mt-0.5 truncate text-xs text-gray-400 dark:text-dark-400;
}
</style>
