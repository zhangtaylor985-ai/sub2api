<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col justify-between gap-4 lg:flex-row lg:items-start">
          <div class="flex flex-1 flex-wrap items-center gap-3">
            <div class="relative w-full sm:w-72">
              <Icon
                name="search"
                size="md"
                class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.apiKeys.searchPlaceholder')"
                class="input pl-10"
                @input="handleSearch"
              />
            </div>
            <Select
              v-model="filters.status"
              :options="statusFilterOptions"
              class="w-40"
              @change="loadApiKeys"
            />
            <Select
              v-model="filters.group_id"
              :options="groupFilterOptions"
              class="w-56"
              searchable
              @change="loadApiKeys"
            />
          </div>

          <div class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-3 lg:w-auto">
            <button
              @click="loadApiKeys"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button @click="openCreateDialog" class="btn btn-primary">
              <Icon name="plus" size="md" class="mr-2" />
              {{ t('admin.apiKeys.create') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="apiKeys"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-key="{ row }">
            <div class="min-w-60">
              <div class="font-medium text-gray-900 dark:text-white">{{ row.name }}</div>
              <div class="mt-1 flex items-center gap-2">
                <code class="truncate font-mono text-xs text-gray-500 dark:text-dark-300">
                  {{ maskKey(row.key) }}
                </code>
                <button
                  class="text-gray-400 transition-colors hover:text-primary-600 dark:hover:text-primary-400"
                  :title="t('keys.copyToClipboard')"
                  @click="copyToClipboard(row.key)"
                >
                  <Icon name="copy" size="sm" />
                </button>
              </div>
            </div>
          </template>

          <template #cell-owner="{ row }">
            <div class="text-sm">
              <div class="font-medium text-gray-800 dark:text-gray-100">
                {{ ownerLabel(row) }}
              </div>
              <div class="text-xs text-gray-500 dark:text-dark-400">ID {{ row.user_id }}</div>
            </div>
          </template>

          <template #cell-group="{ row }">
            <GroupBadge
              v-if="row.group_id && row.group"
              :name="row.group.name"
              :platform="row.group.platform"
              :subscription-type="row.group.subscription_type"
              :rate-multiplier="row.group.rate_multiplier"
            />
            <span v-else class="text-sm italic text-gray-400">
              {{ t('admin.apiKeys.ungrouped') }}
            </span>
          </template>

          <template #cell-limits="{ row }">
            <div class="space-y-0.5 text-xs text-gray-600 dark:text-dark-300">
              <div>{{ t('admin.apiKeys.form.totalQuota') }}: {{ formatLimit(row.quota) }}</div>
              <div>{{ t('admin.apiKeys.form.dailyLimit') }}: {{ formatLimit(effectiveDailyLimit(row)) }}</div>
              <div>{{ t('admin.apiKeys.form.weeklyLimit') }}: {{ formatLimit(effectiveWeeklyLimit(row)) }}</div>
            </div>
          </template>

          <template #cell-usage="{ row }">
            <div class="space-y-0.5 text-xs text-gray-600 dark:text-dark-300">
              <div>Total: ${{ formatMoney(row.quota_used) }}</div>
              <div>1d: ${{ formatMoney(row.usage_1d) }}</div>
              <div>7d: ${{ formatMoney(row.usage_7d) }}</div>
              <div v-if="weeklyWindowPeriod(row)" class="text-gray-400 dark:text-dark-500">
                {{ t('admin.apiKeys.weeklyPeriod') }}: {{ weeklyWindowPeriod(row) }}
              </div>
            </div>
          </template>

          <template #cell-concurrency="{ row }">
            <span class="text-sm text-gray-700 dark:text-gray-300">
              {{ formatConcurrency(row) }}
            </span>
          </template>

          <template #cell-model_access="{ row }">
            <span class="text-xs text-gray-600 dark:text-dark-300">
              {{ formatModelAccess(row) }}
            </span>
          </template>

          <template #cell-status="{ value }">
            <span :class="['badge', statusClass(value)]">{{ value }}</span>
          </template>

          <template #cell-expires_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ value ? formatDateTime(value) : t('admin.apiKeys.neverExpires') }}
            </span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ formatDateTime(value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <button
              class="flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              :title="t('admin.apiKeys.editPolicy')"
              @click="openEditDialog(row)"
            >
              <Icon name="edit" size="sm" />
            </button>
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
      :show="showCreateDialog"
      :title="t('admin.apiKeys.create')"
      width="wide"
      @close="closeCreateDialog"
    >
      <form id="create-api-key-form" class="space-y-4" @submit.prevent="handleCreate">
        <div class="grid gap-4 md:grid-cols-2">
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.name') }}</span>
            <input
              v-model.trim="createForm.name"
              type="text"
              required
              class="input"
              :placeholder="t('admin.apiKeys.form.namePlaceholder')"
            />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.customKey') }}</span>
            <input
              v-model.trim="createForm.custom_key"
              type="text"
              class="input font-mono"
              :placeholder="t('admin.apiKeys.form.customKeyPlaceholder')"
            />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.ownerUserId') }}</span>
            <input
              v-model.number="createForm.user_id"
              type="number"
              min="1"
              class="input"
              :placeholder="t('admin.apiKeys.form.ownerUserIdPlaceholder')"
            />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.group') }}</span>
            <Select v-model="createForm.group_id" :options="groupFormOptions" searchable />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.status') }}</span>
            <Select v-model="createForm.status" :options="statusFormOptions" />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.totalQuota') }}</span>
            <input v-model.number="createForm.quota" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroUnlimited') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.dailyLimit') }}</span>
            <input v-model.number="createForm.rate_limit_1d" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroInheritGroupLimit') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.weeklyLimit') }}</span>
            <input v-model.number="createForm.rate_limit_7d" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroInheritGroupLimit') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.fiveHourLimit') }}</span>
            <input v-model.number="createForm.rate_limit_5h" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroUnlimited') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.concurrency') }}</span>
            <input v-model.number="createForm.concurrency" type="number" min="0" step="1" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroInheritConcurrency') }}</span>
          </label>
          <label class="space-y-1 md:col-span-2">
            <span class="input-label">{{ t('admin.apiKeys.form.expiresAt') }}</span>
            <input v-model="createForm.expires_at_local" type="datetime-local" class="input" />
          </label>
          <div class="space-y-2 md:col-span-2">
            <span class="input-label">{{ t('admin.apiKeys.form.modelAccess') }}</span>
            <div class="flex flex-wrap gap-4 text-sm text-gray-700 dark:text-dark-200">
              <label class="inline-flex items-center gap-2">
                <input v-model="createForm.allow_claude_family" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>{{ t('admin.apiKeys.form.allowClaudeFamily') }}</span>
              </label>
              <label class="inline-flex items-center gap-2">
                <input v-model="createForm.allow_gpt_family" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>{{ t('admin.apiKeys.form.allowGPTFamily') }}</span>
              </label>
            </div>
          </div>
          <div class="space-y-3 md:col-span-2">
            <div>
              <span class="input-label">{{ t('admin.apiKeys.form.dispatchOverride') }}</span>
              <div class="input-hint">{{ t('admin.apiKeys.form.dispatchOverrideHint') }}</div>
            </div>
            <div class="grid gap-3 md:grid-cols-3">
              <label class="space-y-1">
                <span class="input-label">{{ t('admin.apiKeys.form.opusMappedModel') }}</span>
                <input v-model="createForm.dispatch_opus_mapped_model" type="text" class="input" placeholder="gpt-5.4" />
              </label>
              <label class="space-y-1">
                <span class="input-label">{{ t('admin.apiKeys.form.sonnetMappedModel') }}</span>
                <input v-model="createForm.dispatch_sonnet_mapped_model" type="text" class="input" placeholder="gpt-5.4" />
              </label>
              <label class="space-y-1">
                <span class="input-label">{{ t('admin.apiKeys.form.haikuMappedModel') }}</span>
                <input v-model="createForm.dispatch_haiku_mapped_model" type="text" class="input" placeholder="gpt-5.4" />
              </label>
            </div>
          </div>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeCreateDialog">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" form="create-api-key-form" class="btn btn-primary" :disabled="submitting">
            {{ submitting ? t('common.saving') : t('common.create') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showEditDialog"
      :title="t('admin.apiKeys.editPolicy')"
      width="wide"
      @close="closeEditDialog"
    >
      <form v-if="editingKey" id="edit-api-key-form" class="space-y-4" @submit.prevent="handleUpdate">
        <div class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900/40">
          <div class="font-medium text-gray-900 dark:text-white">{{ editingKey.name }}</div>
          <code class="mt-1 block truncate font-mono text-xs text-gray-500 dark:text-dark-300">
            {{ maskKey(editingKey.key) }}
          </code>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.group') }}</span>
            <Select v-model="editForm.group_id" :options="groupFormOptions" searchable />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.status') }}</span>
            <Select v-model="editForm.status" :options="statusFormOptions" />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.totalQuota') }}</span>
            <input v-model.number="editForm.quota" type="number" min="0" step="0.0001" class="input" />
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.dailyLimit') }}</span>
            <input v-model.number="editForm.rate_limit_1d" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroInheritGroupLimit') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.weeklyLimit') }}</span>
            <input v-model.number="editForm.rate_limit_7d" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroInheritGroupLimit') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.fiveHourLimit') }}</span>
            <input v-model.number="editForm.rate_limit_5h" type="number" min="0" step="0.0001" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroUnlimited') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.concurrency') }}</span>
            <input v-model.number="editForm.concurrency" type="number" min="0" step="1" class="input" />
            <span class="input-hint">{{ t('admin.apiKeys.form.zeroInheritConcurrency') }}</span>
          </label>
          <label class="space-y-1">
            <span class="input-label">{{ t('admin.apiKeys.form.expiresAt') }}</span>
            <input
              v-model="editForm.expires_at_local"
              type="datetime-local"
              class="input"
              :disabled="editForm.clear_expires_at"
            />
          </label>
          <label class="space-y-1 md:col-span-2">
            <span class="input-label">{{ t('admin.apiKeys.form.weeklyWindowStart') }}</span>
            <input v-model="editForm.window_7d_start_local" type="datetime-local" class="input" />
            <span class="input-hint">
              {{ t('admin.apiKeys.form.weeklyWindowHint') }}
              <template v-if="editWeeklyWindowPeriod">
                · {{ t('admin.apiKeys.weeklyPeriod') }}: {{ editWeeklyWindowPeriod }}
              </template>
            </span>
          </label>
          <div class="space-y-2 md:col-span-2">
            <span class="input-label">{{ t('admin.apiKeys.form.modelAccess') }}</span>
            <div class="flex flex-wrap gap-4 text-sm text-gray-700 dark:text-dark-200">
              <label class="inline-flex items-center gap-2">
                <input v-model="editForm.allow_claude_family" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>{{ t('admin.apiKeys.form.allowClaudeFamily') }}</span>
              </label>
              <label class="inline-flex items-center gap-2">
                <input v-model="editForm.allow_gpt_family" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>{{ t('admin.apiKeys.form.allowGPTFamily') }}</span>
              </label>
            </div>
          </div>
          <div class="space-y-3 md:col-span-2">
            <div>
              <span class="input-label">{{ t('admin.apiKeys.form.dispatchOverride') }}</span>
              <div class="input-hint">{{ t('admin.apiKeys.form.dispatchOverrideHint') }}</div>
            </div>
            <div class="grid gap-3 md:grid-cols-3">
              <label class="space-y-1">
                <span class="input-label">{{ t('admin.apiKeys.form.opusMappedModel') }}</span>
                <input v-model="editForm.dispatch_opus_mapped_model" type="text" class="input" placeholder="gpt-5.4" />
              </label>
              <label class="space-y-1">
                <span class="input-label">{{ t('admin.apiKeys.form.sonnetMappedModel') }}</span>
                <input v-model="editForm.dispatch_sonnet_mapped_model" type="text" class="input" placeholder="gpt-5.4" />
              </label>
              <label class="space-y-1">
                <span class="input-label">{{ t('admin.apiKeys.form.haikuMappedModel') }}</span>
                <input v-model="editForm.dispatch_haiku_mapped_model" type="text" class="input" placeholder="gpt-5.4" />
              </label>
            </div>
          </div>
        </div>
        <div class="flex flex-wrap gap-4 text-xs text-gray-600 dark:text-dark-300">
          <label class="inline-flex items-center gap-2">
            <input v-model="editForm.clear_expires_at" type="checkbox" class="rounded border-gray-300 text-primary-600" />
            <span>{{ t('admin.apiKeys.form.clearExpires') }}</span>
          </label>
          <label class="inline-flex items-center gap-2">
            <input v-model="editForm.reset_quota" type="checkbox" class="rounded border-gray-300 text-primary-600" />
            <span>{{ t('admin.apiKeys.form.resetQuota') }}</span>
          </label>
          <label class="inline-flex items-center gap-2">
            <input v-model="editForm.reset_rate_limit_usage" type="checkbox" class="rounded border-gray-300 text-primary-600" />
            <span>{{ t('admin.apiKeys.form.resetRateUsage') }}</span>
          </label>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeEditDialog">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" form="edit-api-key-form" class="btn btn-primary" :disabled="submitting">
            {{ submitting ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatDateTime } from '@/utils/format'
import type { AdminGroup, ApiKey, OpenAIMessagesDispatchModelConfig } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const apiKeys = ref<ApiKey[]>([])
const groups = ref<AdminGroup[]>([])
const loading = ref(false)
const submitting = ref(false)
const searchQuery = ref('')
const showCreateDialog = ref(false)
const showEditDialog = ref(false)
const editingKey = ref<ApiKey | null>(null)

const filters = reactive({
  status: '',
  group_id: '' as string | number
})

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})

const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

const defaultCreateForm = () => ({
  name: '',
  custom_key: '',
  user_id: null as number | null,
  group_id: '' as string | number,
  status: 'active' as 'active' | 'inactive',
  quota: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  concurrency: 0,
  allow_claude_family: true,
  allow_gpt_family: true,
  dispatch_opus_mapped_model: '',
  dispatch_sonnet_mapped_model: '',
  dispatch_haiku_mapped_model: '',
  dispatch_exact_model_mappings: {} as Record<string, string>,
  expires_at_local: ''
})

const createForm = reactive(defaultCreateForm())

const editForm = reactive({
  group_id: '' as string | number,
  status: 'active' as 'active' | 'inactive',
  quota: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  concurrency: 0,
  allow_claude_family: true,
  allow_gpt_family: true,
  dispatch_opus_mapped_model: '',
  dispatch_sonnet_mapped_model: '',
  dispatch_haiku_mapped_model: '',
  dispatch_exact_model_mappings: {} as Record<string, string>,
  expires_at_local: '',
  window_7d_start_local: '',
  clear_expires_at: false,
  reset_quota: false,
  reset_rate_limit_usage: false
})

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.apiKeys.allStatus') },
  { value: 'active', label: 'active' },
  { value: 'inactive', label: 'inactive' },
  { value: 'quota_exhausted', label: 'quota_exhausted' },
  { value: 'expired', label: 'expired' }
])

const statusFormOptions = computed(() => [
  { value: 'active', label: 'active' },
  { value: 'inactive', label: 'inactive' }
])

const groupFilterOptions = computed(() => [
  { value: '', label: t('admin.apiKeys.allGroups') },
  { value: 0, label: t('admin.apiKeys.ungrouped') },
  ...groups.value.map((group) => ({ value: group.id, label: `${group.name} · ${group.platform}` }))
])

const groupFormOptions = computed(() => [
  { value: '', label: t('admin.apiKeys.ungrouped') },
  ...groups.value.map((group) => ({ value: group.id, label: `${group.name} · ${group.platform}` }))
])

const columns = computed<Column[]>(() => [
  { key: 'key', label: t('admin.apiKeys.columns.key') },
  { key: 'owner', label: t('admin.apiKeys.columns.owner') },
  { key: 'group', label: t('admin.apiKeys.columns.group') },
  { key: 'limits', label: t('admin.apiKeys.columns.limits') },
  { key: 'usage', label: t('admin.apiKeys.columns.usage') },
  { key: 'concurrency', label: t('admin.apiKeys.columns.concurrency'), sortable: true },
  { key: 'model_access', label: t('admin.apiKeys.columns.modelAccess') },
  { key: 'status', label: t('admin.apiKeys.columns.status'), sortable: true },
  { key: 'expires_at', label: t('admin.apiKeys.columns.expiresAt'), sortable: true },
  { key: 'created_at', label: t('admin.apiKeys.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('admin.apiKeys.columns.actions') }
])

let abortController: AbortController | null = null
let searchTimeout: ReturnType<typeof setTimeout> | null = null

const numericValue = (value: unknown): number => {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : 0
}

const selectedGroupID = (value: string | number): number | null => {
  if (value === '' || value === null || value === undefined) return null
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : null
}

const selectedPolicyGroupID = (value: string | number): number => {
  if (value === '' || value === null || value === undefined) return 0
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : 0
}

const toDateTimeLocal = (value?: string | null) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offsetMs = date.getTimezoneOffset() * 60 * 1000
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16)
}

const toISOStringOrEmpty = (value: string) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    throw new Error(t('admin.apiKeys.errors.invalidExpiresAt'))
  }
  return date.toISOString()
}

const toWeeklyWindowISOStringOrEmpty = (value: string) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    throw new Error(t('admin.apiKeys.errors.invalidWeeklyWindowStart'))
  }
  const now = Date.now()
  if (date.getTime() > now + 60_000 || date.getTime() < now - 7 * 24 * 60 * 60 * 1000) {
    throw new Error(t('admin.apiKeys.errors.invalidWeeklyWindowRange'))
  }
  return date.toISOString()
}

const formatWindowBoundary = (value?: string | null) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

const formatWindowPeriod = (start?: string | null, end?: string | null) => {
  const s = formatWindowBoundary(start)
  const e = formatWindowBoundary(end)
  return s && e ? `${s} - ${e}` : ''
}

const weeklyWindowPeriod = (key: ApiKey) => formatWindowPeriod(key.window_7d_start, key.reset_7d_at)

const editWeeklyWindowPeriod = computed(() => {
  if (!editForm.window_7d_start_local) return ''
  const date = new Date(editForm.window_7d_start_local)
  if (Number.isNaN(date.getTime())) return ''
  const end = new Date(date.getTime() + 7 * 24 * 60 * 60 * 1000)
  return formatWindowPeriod(date.toISOString(), end.toISOString())
})

const maskKey = (key: string) => {
  if (!key) return ''
  if (key.length <= 18) return key
  return `${key.slice(0, 10)}...${key.slice(-8)}`
}

const formatMoney = (value: number) => Number(value || 0).toFixed(4).replace(/0+$/, '').replace(/\.$/, '')

const formatLimit = (value: number) => {
  const n = Number(value || 0)
  return n > 0 ? `$${formatMoney(n)}` : t('admin.apiKeys.unlimited')
}

const effectiveDailyLimit = (key: ApiKey) => {
  if (key.rate_limit_1d > 0) return key.rate_limit_1d
  return key.group?.daily_limit_usd || 0
}

const effectiveWeeklyLimit = (key: ApiKey) => {
  if (key.rate_limit_7d > 0) return key.rate_limit_7d
  return key.group?.weekly_limit_usd || 0
}

const ownerLabel = (key: ApiKey) => {
  if (key.user?.email) {
    return key.user.email === 'admin-api-keys@sub2api.local'
      ? t('admin.apiKeys.systemOwner')
      : key.user.email
  }
  return key.user?.username || `User ${key.user_id}`
}

const formatConcurrency = (key: ApiKey) => {
  if (key.concurrency > 0) return `Key ${key.concurrency}`
  if (key.group?.concurrency && key.group.concurrency > 0) return `Group ${key.group.concurrency}`
  return t('admin.apiKeys.inherited')
}

const formatModelAccess = (key: ApiKey) => {
  const parts: string[] = []
  if (key.allow_claude_family) parts.push(t('admin.apiKeys.modelAccessClaude'))
  if (key.allow_gpt_family) parts.push(t('admin.apiKeys.modelAccessGPT'))
  const base = parts.length > 0 ? parts.join(' / ') : t('admin.apiKeys.modelAccessNone')
  const override = formatDispatchOverride(key.messages_dispatch_model_config)
  return override ? `${base} · ${override}` : base
}

const dispatchConfigFromForm = (form: {
  dispatch_opus_mapped_model: string
  dispatch_sonnet_mapped_model: string
  dispatch_haiku_mapped_model: string
  dispatch_exact_model_mappings?: Record<string, string>
}): OpenAIMessagesDispatchModelConfig => ({
  opus_mapped_model: form.dispatch_opus_mapped_model.trim(),
  sonnet_mapped_model: form.dispatch_sonnet_mapped_model.trim(),
  haiku_mapped_model: form.dispatch_haiku_mapped_model.trim(),
  exact_model_mappings: form.dispatch_exact_model_mappings || {}
})

const applyDispatchConfigToForm = (
  form: {
    dispatch_opus_mapped_model: string
    dispatch_sonnet_mapped_model: string
    dispatch_haiku_mapped_model: string
    dispatch_exact_model_mappings?: Record<string, string>
  },
  config?: OpenAIMessagesDispatchModelConfig | null
) => {
  form.dispatch_opus_mapped_model = config?.opus_mapped_model?.trim() || ''
  form.dispatch_sonnet_mapped_model = config?.sonnet_mapped_model?.trim() || ''
  form.dispatch_haiku_mapped_model = config?.haiku_mapped_model?.trim() || ''
  form.dispatch_exact_model_mappings = { ...(config?.exact_model_mappings || {}) }
}

const formatDispatchOverride = (config?: OpenAIMessagesDispatchModelConfig | null) => {
  if (!config) return ''
  const values = [
    config.opus_mapped_model?.trim(),
    config.sonnet_mapped_model?.trim(),
    config.haiku_mapped_model?.trim()
  ].filter(Boolean)
  if (values.length === 0 && Object.keys(config.exact_model_mappings || {}).length === 0) return ''
  const unique = Array.from(new Set(values))
  return unique.length === 1
    ? t('admin.apiKeys.dispatchOverrideShort', { model: unique[0] })
    : t('admin.apiKeys.dispatchOverrideMixed')
}

const statusClass = (status: string) => {
  if (status === 'active') return 'badge-success'
  if (status === 'inactive') return 'badge-gray'
  return 'badge-danger'
}

const loadGroups = async () => {
  try {
    groups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

const loadApiKeys = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  loading.value = true
  try {
    const groupID =
      filters.group_id === ''
        ? null
        : Number.isFinite(Number(filters.group_id))
          ? Number(filters.group_id)
          : null
    const response = await adminAPI.apiKeys.list(
      pagination.page,
      pagination.page_size,
      {
        search: searchQuery.value.trim() || undefined,
        status: filters.status || undefined,
        group_id: groupID,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      },
      { signal: currentController.signal }
    )
    apiKeys.value = response.items
    pagination.total = response.total
    pagination.page = response.page
    pagination.page_size = response.page_size
  } catch (error: any) {
    if (error?.code === 'ERR_CANCELED') return
    appStore.showError(error?.message || t('admin.apiKeys.errors.loadFailed'))
  } finally {
    if (abortController === currentController) {
      abortController = null
      loading.value = false
    }
  }
}

const handleSearch = () => {
  if (searchTimeout) clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadApiKeys()
  }, 300)
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadApiKeys()
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadApiKeys()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadApiKeys()
}

const resetCreateForm = () => {
  Object.assign(createForm, defaultCreateForm())
}

const openCreateDialog = () => {
  resetCreateForm()
  showCreateDialog.value = true
}

const closeCreateDialog = () => {
  showCreateDialog.value = false
  resetCreateForm()
}

const openEditDialog = (key: ApiKey) => {
  editingKey.value = key
  editForm.group_id = key.group_id ?? ''
  editForm.status = key.status === 'inactive' ? 'inactive' : 'active'
  editForm.quota = key.quota || 0
  editForm.rate_limit_5h = key.rate_limit_5h || 0
  editForm.rate_limit_1d = key.rate_limit_1d || 0
  editForm.rate_limit_7d = key.rate_limit_7d || 0
  editForm.concurrency = key.concurrency || 0
  editForm.allow_claude_family = key.allow_claude_family !== false
  editForm.allow_gpt_family = key.allow_gpt_family !== false
  applyDispatchConfigToForm(editForm, key.messages_dispatch_model_config)
  editForm.expires_at_local = toDateTimeLocal(key.expires_at)
  editForm.window_7d_start_local = toDateTimeLocal(key.window_7d_start)
  editForm.clear_expires_at = false
  editForm.reset_quota = false
  editForm.reset_rate_limit_usage = false
  showEditDialog.value = true
}

const closeEditDialog = () => {
  showEditDialog.value = false
  editingKey.value = null
}

const handleCreate = async () => {
  if (!createForm.name.trim()) {
    appStore.showError(t('admin.apiKeys.errors.nameRequired'))
    return
  }
  submitting.value = true
  try {
    const expiresAt = toISOStringOrEmpty(createForm.expires_at_local)
    const result = await adminAPI.apiKeys.create({
      name: createForm.name.trim(),
      custom_key: createForm.custom_key.trim() || undefined,
      user_id: createForm.user_id || undefined,
      group_id: selectedGroupID(createForm.group_id),
      status: createForm.status,
      quota: numericValue(createForm.quota),
      rate_limit_5h: numericValue(createForm.rate_limit_5h),
      rate_limit_1d: numericValue(createForm.rate_limit_1d),
      rate_limit_7d: numericValue(createForm.rate_limit_7d),
      concurrency: numericValue(createForm.concurrency),
      allow_claude_family: createForm.allow_claude_family,
      allow_gpt_family: createForm.allow_gpt_family,
      messages_dispatch_model_config: dispatchConfigFromForm(createForm),
      expires_at: expiresAt || undefined
    })
    apiKeys.value.unshift(result.api_key)
    pagination.total += 1
    appStore.showSuccess(t('admin.apiKeys.created'))
    closeCreateDialog()
    loadApiKeys()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.apiKeys.errors.createFailed'))
  } finally {
    submitting.value = false
  }
}

const handleUpdate = async () => {
  if (!editingKey.value) return
  submitting.value = true
  try {
    const expiresAt = editForm.clear_expires_at ? '' : toISOStringOrEmpty(editForm.expires_at_local)
    const originalWeeklyWindowStartLocal = toDateTimeLocal(editingKey.value.window_7d_start)
    const weeklyWindowStart =
      editForm.window_7d_start_local !== originalWeeklyWindowStartLocal
        ? toWeeklyWindowISOStringOrEmpty(editForm.window_7d_start_local)
        : undefined
    const result = await adminAPI.apiKeys.updateApiKeyPolicy(editingKey.value.id, {
      group_id: selectedPolicyGroupID(editForm.group_id),
      status: editForm.status,
      quota: numericValue(editForm.quota),
      rate_limit_5h: numericValue(editForm.rate_limit_5h),
      rate_limit_1d: numericValue(editForm.rate_limit_1d),
      rate_limit_7d: numericValue(editForm.rate_limit_7d),
      concurrency: numericValue(editForm.concurrency),
      allow_claude_family: editForm.allow_claude_family,
      allow_gpt_family: editForm.allow_gpt_family,
      messages_dispatch_model_config: dispatchConfigFromForm(editForm),
      expires_at: editForm.clear_expires_at || editForm.expires_at_local ? expiresAt : undefined,
      window_7d_start: weeklyWindowStart,
      reset_quota: editForm.reset_quota,
      reset_rate_limit_usage: editForm.reset_rate_limit_usage
    })
    const idx = apiKeys.value.findIndex((item) => item.id === editingKey.value?.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    appStore.showSuccess(t('admin.apiKeys.updated'))
    closeEditDialog()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.apiKeys.errors.updateFailed'))
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  loadGroups()
  loadApiKeys()
})

onUnmounted(() => {
  if (abortController) abortController.abort()
  if (searchTimeout) clearTimeout(searchTimeout)
})
</script>
