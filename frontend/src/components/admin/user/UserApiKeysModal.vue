<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-700">
        <div class="flex h-10 w-10 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
          <span class="text-lg font-medium text-primary-700 dark:text-primary-300">{{ user.email.charAt(0).toUpperCase() }}</span>
        </div>
        <div><p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p><p class="text-sm text-gray-500 dark:text-dark-400">{{ user.username }}</p></div>
      </div>
      <div v-if="loading" class="flex justify-center py-8"><svg class="h-8 w-8 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg></div>
      <div v-else-if="apiKeys.length === 0" class="py-8 text-center"><p class="text-sm text-gray-500">{{ t('admin.users.noApiKeys') }}</p></div>
      <div v-else ref="scrollContainerRef" class="max-h-96 space-y-3 overflow-y-auto" @scroll="closeGroupSelector">
        <div v-for="key in apiKeys" :key="key.id" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-start justify-between">
            <div class="min-w-0 flex-1">
              <div class="mb-1 flex items-center gap-2"><span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span><span :class="['badge text-xs', key.status === 'active' ? 'badge-success' : 'badge-danger']">{{ key.status }}</span></div>
              <p class="truncate font-mono text-sm text-gray-500">{{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}</p>
            </div>
            <button
              type="button"
              class="flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              @click="togglePolicyEditor(key)"
            >
              <Icon :name="editingKeyId === key.id ? 'x' : 'edit'" size="sm" />
              <span>{{ editingKeyId === key.id ? '关闭' : '编辑策略' }}</span>
            </button>
          </div>
          <div class="mt-3 flex flex-wrap gap-4 text-xs text-gray-500">
            <div class="flex items-center gap-1">
              <span>{{ t('admin.users.group') }}:</span>
              <button
                :ref="(el) => setGroupButtonRef(key.id, el)"
                @click="openGroupSelector(key)"
                class="-mx-1 -my-0.5 flex cursor-pointer items-center gap-1 rounded-md px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                :disabled="updatingKeyIds.has(key.id)"
              >
                <GroupBadge
                  v-if="key.group_id && key.group"
                  :name="key.group.name"
                  :platform="key.group.platform"
                  :subscription-type="key.group.subscription_type"
                  :rate-multiplier="key.group.rate_multiplier"
                />
                <span v-else class="text-gray-400 italic">{{ t('admin.users.none') }}</span>
                <svg v-if="updatingKeyIds.has(key.id)" class="h-3 w-3 animate-spin text-primary-500" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg>
                <svg v-else class="h-3 w-3 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9" /></svg>
              </button>
            </div>
            <div class="flex items-center gap-1"><span>{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span></div>
            <div class="flex items-center gap-1"><span>过期: {{ key.expires_at ? formatDateTime(key.expires_at) : '永久有效' }}</span></div>
            <div class="flex items-center gap-1"><span>总额度: {{ key.quota > 0 ? `$${key.quota.toFixed(2)}` : '不限' }}</span></div>
            <div class="flex items-center gap-1"><span>日限额: {{ key.rate_limit_1d > 0 ? `$${key.rate_limit_1d.toFixed(2)}` : '不限' }}</span></div>
            <div class="flex items-center gap-1"><span>并发: {{ key.concurrency > 0 ? key.concurrency : '继承' }}</span></div>
          </div>
          <form
            v-if="editingKeyId === key.id"
            class="mt-4 space-y-4 rounded-lg border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900/40"
            @submit.prevent="savePolicy(key)"
          >
            <div class="grid gap-3 md:grid-cols-2">
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">状态</span>
                <select v-model="policyForm.status" class="input">
                  <option value="active">active</option>
                  <option value="inactive">inactive</option>
                </select>
              </label>
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">总额度 USD，0 = 不限</span>
                <input v-model.number="policyForm.quota" type="number" min="0" step="0.0001" class="input" />
              </label>
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">5 小时限额 USD，0 = 不限</span>
                <input v-model.number="policyForm.rate_limit_5h" type="number" min="0" step="0.0001" class="input" />
              </label>
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">日限额 USD，0 = 不限</span>
                <input v-model.number="policyForm.rate_limit_1d" type="number" min="0" step="0.0001" class="input" />
              </label>
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">周限额 USD，0 = 不限</span>
                <input v-model.number="policyForm.rate_limit_7d" type="number" min="0" step="0.0001" class="input" />
              </label>
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">并发上限，0 = 继承分组 / 用户</span>
                <input v-model.number="policyForm.concurrency" type="number" min="0" step="1" class="input" />
              </label>
              <label class="space-y-1">
                <span class="text-xs font-medium text-gray-600 dark:text-dark-300">过期时间</span>
                <input v-model="policyForm.expires_at_local" type="datetime-local" class="input" :disabled="policyForm.clear_expires_at" />
              </label>
            </div>
            <div class="flex flex-wrap gap-4 text-xs text-gray-600 dark:text-dark-300">
              <label class="inline-flex items-center gap-2">
                <input v-model="policyForm.clear_expires_at" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>清空过期时间</span>
              </label>
              <label class="inline-flex items-center gap-2">
                <input v-model="policyForm.reset_quota" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>重置总额度用量</span>
              </label>
              <label class="inline-flex items-center gap-2">
                <input v-model="policyForm.reset_rate_limit_usage" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                <span>重置限速窗口用量</span>
              </label>
            </div>
            <div class="flex justify-end gap-2">
              <button type="button" class="btn btn-secondary btn-sm" @click="cancelPolicyEditor">取消</button>
              <button type="submit" class="btn btn-primary btn-sm" :disabled="savingPolicy">
                <Icon v-if="!savingPolicy" name="check" size="sm" />
                <span>{{ savingPolicy ? '保存中...' : '保存策略' }}</span>
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  </BaseDialog>

  <!-- Group Selector Dropdown -->
  <Teleport to="body">
    <div
      v-if="groupSelectorKeyId !== null && dropdownPosition"
      ref="dropdownRef"
      class="animate-in fade-in slide-in-from-top-2 fixed z-[100000020] w-64 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 duration-200 dark:bg-dark-800 dark:ring-white/10"
      :style="{ top: dropdownPosition.top + 'px', left: dropdownPosition.left + 'px' }"
    >
      <div class="max-h-64 overflow-y-auto p-1.5">
        <!-- Unbind option -->
        <button
          @click="changeGroup(selectedKeyForGroup!, null)"
          :class="[
            'flex w-full items-center rounded-lg px-3 py-2 text-sm transition-colors',
            !selectedKeyForGroup?.group_id
              ? 'bg-primary-50 dark:bg-primary-900/20'
              : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <span class="text-gray-500 italic">{{ t('admin.users.none') }}</span>
          <svg
            v-if="!selectedKeyForGroup?.group_id"
            class="ml-auto h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400"
            fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2"
          ><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg>
        </button>
        <!-- Group options -->
        <button
          v-for="group in allGroups"
          :key="group.id"
          @click="changeGroup(selectedKeyForGroup!, group.id)"
          :class="[
            'flex w-full items-center justify-between rounded-lg px-3 py-2 text-sm transition-colors',
            selectedKeyForGroup?.group_id === group.id
              ? 'bg-primary-50 dark:bg-primary-900/20'
              : 'hover:bg-gray-100 dark:hover:bg-dark-700'
          ]"
        >
          <GroupOptionItem
            :name="group.name"
            :platform="group.platform"
            :subscription-type="group.subscription_type"
            :rate-multiplier="group.rate_multiplier"
            :description="group.description"
            :selected="selectedKeyForGroup?.group_id === group.id"
          />
        </button>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser, AdminGroup, ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null }>()
const emit = defineEmits(['close'])
const { t } = useI18n()
const appStore = useAppStore()

const apiKeys = ref<ApiKey[]>([])
const allGroups = ref<AdminGroup[]>([])
const loading = ref(false)
const updatingKeyIds = ref(new Set<number>())
const groupSelectorKeyId = ref<number | null>(null)
const dropdownPosition = ref<{ top: number; left: number } | null>(null)
const dropdownRef = ref<HTMLElement | null>(null)
const scrollContainerRef = ref<HTMLElement | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())
const editingKeyId = ref<number | null>(null)
const savingPolicy = ref(false)
const policyForm = ref({
  status: 'active' as 'active' | 'inactive',
  quota: 0 as number | null,
  rate_limit_5h: 0 as number | null,
  rate_limit_1d: 0 as number | null,
  rate_limit_7d: 0 as number | null,
  concurrency: 0 as number | null,
  expires_at_local: '',
  clear_expires_at: false,
  reset_quota: false,
  reset_rate_limit_usage: false
})

const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

watch(() => props.show, (v) => {
  if (v && props.user) {
    load()
    loadGroups()
  } else {
    closeGroupSelector()
    cancelPolicyEditor()
  }
})

const load = async () => {
  if (!props.user) return
  loading.value = true
  groupButtonRefs.value.clear()
  try {
    const res = await adminAPI.users.getUserApiKeys(props.user.id)
    apiKeys.value = res.items || []
  } catch (error) {
    console.error('Failed to load API keys:', error)
  } finally {
    loading.value = false
  }
}

const loadGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    allGroups.value = groups
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

const DROPDOWN_HEIGHT = 272 // max-h-64 = 16rem = 256px + padding
const DROPDOWN_GAP = 4

const openGroupSelector = (key: ApiKey) => {
  if (groupSelectorKeyId.value === key.id) {
    closeGroupSelector()
  } else {
    const buttonEl = groupButtonRefs.value.get(key.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()
      const spaceBelow = window.innerHeight - rect.bottom
      const openUpward = spaceBelow < DROPDOWN_HEIGHT && rect.top > spaceBelow
      dropdownPosition.value = {
        top: openUpward ? rect.top - DROPDOWN_HEIGHT - DROPDOWN_GAP : rect.bottom + DROPDOWN_GAP,
        left: rect.left
      }
    }
    groupSelectorKeyId.value = key.id
  }
}

const closeGroupSelector = () => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
}

const toDateTimeLocal = (value?: string | null) => {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offsetMs = date.getTimezoneOffset() * 60 * 1000
  return new Date(date.getTime() - offsetMs).toISOString().slice(0, 16)
}

const numberOrZero = (value: unknown) => {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : 0
}

const togglePolicyEditor = (key: ApiKey) => {
  closeGroupSelector()
  if (editingKeyId.value === key.id) {
    cancelPolicyEditor()
    return
  }
  editingKeyId.value = key.id
  policyForm.value = {
    status: key.status === 'inactive' ? 'inactive' : 'active',
    quota: key.quota || 0,
    rate_limit_5h: key.rate_limit_5h || 0,
    rate_limit_1d: key.rate_limit_1d || 0,
    rate_limit_7d: key.rate_limit_7d || 0,
    concurrency: key.concurrency || 0,
    expires_at_local: toDateTimeLocal(key.expires_at),
    clear_expires_at: false,
    reset_quota: false,
    reset_rate_limit_usage: false
  }
}

const cancelPolicyEditor = () => {
  editingKeyId.value = null
  savingPolicy.value = false
}

const savePolicy = async (key: ApiKey) => {
  if (savingPolicy.value) return

  let expiresAt = ''
  if (!policyForm.value.clear_expires_at && policyForm.value.expires_at_local) {
    const date = new Date(policyForm.value.expires_at_local)
    if (Number.isNaN(date.getTime())) {
      appStore.showError('过期时间格式无效')
      return
    }
    expiresAt = date.toISOString()
  }

  savingPolicy.value = true
  try {
    const result = await adminAPI.apiKeys.updateApiKeyPolicy(key.id, {
      status: policyForm.value.status,
      quota: numberOrZero(policyForm.value.quota),
      rate_limit_5h: numberOrZero(policyForm.value.rate_limit_5h),
      rate_limit_1d: numberOrZero(policyForm.value.rate_limit_1d),
      rate_limit_7d: numberOrZero(policyForm.value.rate_limit_7d),
      concurrency: numberOrZero(policyForm.value.concurrency),
      expires_at: policyForm.value.clear_expires_at || !policyForm.value.expires_at_local ? '' : expiresAt,
      reset_quota: policyForm.value.reset_quota,
      reset_rate_limit_usage: policyForm.value.reset_rate_limit_usage
    })
    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    appStore.showSuccess('API Key 策略已更新')
    cancelPolicyEditor()
  } catch (error: any) {
    appStore.showError(error?.message || '更新 API Key 策略失败')
  } finally {
    savingPolicy.value = false
  }
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  closeGroupSelector()
  if (key.group_id === newGroupId || (!key.group_id && newGroupId === null)) return

  updatingKeyIds.value.add(key.id)
  try {
    const result = await adminAPI.apiKeys.updateApiKeyGroup(key.id, newGroupId)
    // Update local data
    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(t('admin.users.groupChangedWithGrant', { group: result.granted_group_name }))
    } else {
      appStore.showSuccess(t('admin.users.groupChangedSuccess'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

const handleKeyDown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && groupSelectorKeyId.value !== null) {
    event.stopPropagation()
    closeGroupSelector()
  }
}

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (dropdownRef.value && !dropdownRef.value.contains(target)) {
    // Check if the click is on one of the group trigger buttons
    for (const el of groupButtonRefs.value.values()) {
      if (el.contains(target)) return
    }
    closeGroupSelector()
  }
}

const handleClose = () => {
  closeGroupSelector()
  emit('close')
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeyDown, true)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeyDown, true)
})
</script>
