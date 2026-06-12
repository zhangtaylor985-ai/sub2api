/**
 * Admin API Keys API endpoints
 * Handles API key management for administrators
 */

import { apiClient } from '../client'
import type { ApiKey, OpenAIMessagesDispatchModelConfig, PaginatedResponse } from '@/types'

export interface UpdateApiKeyGroupResult {
  api_key: ApiKey
  auto_granted_group_access: boolean
  granted_group_id?: number
  granted_group_name?: string
}

export interface AdminUpdateApiKeyPolicyPayload {
  group_id?: number | null
  status?: 'active' | 'inactive'
  quota?: number
  rate_multiplier?: number
  concurrency?: number
  expires_at?: string
  reset_quota?: boolean
  rate_limit_5h?: number
  rate_limit_1d?: number
  rate_limit_7d?: number
  window_7d_start?: string
  reset_rate_limit_usage?: boolean
  allow_claude_family?: boolean
  allow_gpt_family?: boolean
  messages_dispatch_model_config?: OpenAIMessagesDispatchModelConfig
}

export interface AdminAPIKeyFilters {
  search?: string
  status?: string
  group_id?: number | null
  user_id?: number | null
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

export interface AdminCreateAPIKeyPayload {
  user_id?: number | null
  name: string
  custom_key?: string
  group_id?: number | null
  status?: 'active' | 'inactive'
  quota?: number
  rate_multiplier?: number
  expires_at?: string
  rate_limit_5h?: number
  rate_limit_1d?: number
  rate_limit_7d?: number
  concurrency?: number
  allow_claude_family?: boolean
  allow_gpt_family?: boolean
  messages_dispatch_model_config?: OpenAIMessagesDispatchModelConfig
}

export interface ApiKeyTokenPackage {
  id: number
  api_key_id: number
  amount_usd: number
  used_usd: number
  remaining_usd: number
  note?: string
  created_by?: string
  started_at: string
  created_at: string
  updated_at: string
}

export interface ApiKeyTokenPackageUsage {
  id: number
  package_id: number
  api_key_id: number
  request_id?: string
  request_fingerprint?: string
  model?: string
  cost_usd: number
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  total_tokens: number
  requested_at: string
  created_at: string
}

export interface ApiKeyTokenPackageSummary {
  packages: ApiKeyTokenPackage[]
  usages: ApiKeyTokenPackageUsage[]
  remaining_usd: number
}

export async function listApiKeys(
  page: number = 1,
  pageSize: number = 20,
  filters?: AdminAPIKeyFilters,
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<ApiKey>> {
  const params: Record<string, unknown> = {
    page,
    page_size: pageSize,
    ...filters
  }
  if (params.group_id === null || params.group_id === undefined || params.group_id === '') {
    delete params.group_id
  }
  if (params.user_id === null || params.user_id === undefined || params.user_id === '') {
    delete params.user_id
  }
  const { data } = await apiClient.get<PaginatedResponse<ApiKey>>('/admin/api-keys', {
    params,
    signal: options?.signal
  })
  return data
}

export async function createApiKey(payload: AdminCreateAPIKeyPayload): Promise<UpdateApiKeyGroupResult> {
  const { data } = await apiClient.post<UpdateApiKeyGroupResult>('/admin/api-keys', payload)
  return data
}

/**
 * Update an API key's group binding
 * @param id - API Key ID
 * @param groupId - Group ID (0 to unbind, positive to bind, null/undefined to skip)
 * @returns Updated API key with auto-grant info
 */
export async function updateApiKeyGroup(id: number, groupId: number | null): Promise<UpdateApiKeyGroupResult> {
  const { data } = await apiClient.put<UpdateApiKeyGroupResult>(`/admin/api-keys/${id}`, {
    group_id: groupId === null ? 0 : groupId
  })
  return data
}

export async function updateApiKeyPolicy(id: number, payload: AdminUpdateApiKeyPolicyPayload): Promise<UpdateApiKeyGroupResult> {
  const { data } = await apiClient.put<UpdateApiKeyGroupResult>(`/admin/api-keys/${id}`, payload)
  return data
}

export async function addTokenPackage(id: number, payload: { amount_usd: number; note?: string }): Promise<ApiKeyTokenPackage> {
  const { data } = await apiClient.post<ApiKeyTokenPackage>(`/admin/api-keys/${id}/token-packages`, payload)
  return data
}

export async function listTokenPackages(id: number): Promise<ApiKeyTokenPackageSummary> {
  const { data } = await apiClient.get<ApiKeyTokenPackageSummary>(`/admin/api-keys/${id}/token-packages`)
  return data
}

export const apiKeysAPI = {
  list: listApiKeys,
  create: createApiKey,
  updateApiKeyGroup,
  updateApiKeyPolicy,
  addTokenPackage,
  listTokenPackages
}

export default apiKeysAPI
