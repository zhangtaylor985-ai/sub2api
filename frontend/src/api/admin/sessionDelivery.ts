import { apiClient } from '../client'

export type SessionDeliveryStatus = 'healthy' | 'degraded' | 'critical' | 'paused'
export type SessionCaptureMode = 'all' | 'selected' | 'disabled'
export type SessionCaptureKeyPolicy = 'inherit' | 'include' | 'exclude'

export interface SessionCapturePolicySummary {
  mode: SessionCaptureMode
  total_api_keys: number
  effective_api_keys: number
  included_api_keys: number
  excluded_api_keys: number
  updated_at: string
  updated_by?: number
}

export interface SessionCaptureAPIKey {
  id: number
  name: string
  status: string
  user_email: string
  group_name: string
  policy: SessionCaptureKeyPolicy
  effective: boolean
  policy_updated_at?: string
}

export interface SessionCaptureAPIKeyPage {
  items: SessionCaptureAPIKey[]
  total: number
  page: number
  page_size: number
}

export interface SessionCapturePolicyResponse {
  summary: SessionCapturePolicySummary
  api_keys: SessionCaptureAPIKeyPage
}

export interface SessionSpoolStatus {
  used_bytes: number
  max_bytes: number
  used_percent: number
  pending_records: number
  pending_bytes: number
  quarantined_records: number
  quarantined_bytes: number
  temporary_files: number
  temporary_bytes: number
  oldest_pending_at?: string
}

export interface SessionHostStatus {
  hostname: string
  cpu_count: number
  cpu_used_percent: number
  load_1: number
  load_5: number
  load_15: number
  memory_total_bytes: number
  memory_used_bytes: number
  memory_available_bytes: number
  swap_total_bytes: number
  swap_used_bytes: number
  disk_total_bytes: number
  disk_used_bytes: number
  disk_available_bytes: number
  disk_used_percent: number
  uptime_seconds: number
}

export interface SessionDatabaseStatus {
  status: string
  size_bytes: number
  connections_active: number
  connections_total: number
  connections_max: number
  partitions: number
}

export interface SessionDataStatus {
  records_in_database: number
  deliverable_in_database: number
  rejected_in_database: number
  payload_bytes_in_database: number
  current_hour_records: number
  records_last_5m: number
  first_ingested_at?: string
  last_ingested_at?: string
}

export interface SessionArchiveStatus {
  archive_files_verified: number
  archive_bytes_uploaded: number
  records_archived: number
  deliveries_archived: number
  rejected_archived: number
  failed_batches: number
  exporting_batches: number
  last_verified_at?: string
}

export interface SessionRecentBatch {
  hour: string
  status: string
  record_count: number
  delivery_count: number
  rejected_count: number
  archive_backend?: string
  archive_size: number
  error_message?: string
  started_at: string
  archived_at?: string
  verified_at?: string
  purged_at?: string
}

export interface SessionRemoteStatus {
  status: SessionDeliveryStatus
  observed_at: string
  warnings: string[]
  host: SessionHostStatus
  database: SessionDatabaseStatus
  sessions: SessionDataStatus
  delivery: SessionArchiveStatus
  recent_batches: SessionRecentBatch[]
}

export interface SessionDeliveryOverview {
  status: SessionDeliveryStatus
  observed_at: string
  enabled: boolean
  public_model: string
  warnings: string[]
  policy?: SessionCapturePolicySummary
  spool?: SessionSpoolStatus
  remote?: SessionRemoteStatus
}

export async function getSessionDeliveryOverview(signal?: AbortSignal): Promise<SessionDeliveryOverview> {
  const { data } = await apiClient.get<SessionDeliveryOverview>('/admin/session-delivery/overview', { signal })
  return data
}

export async function getSessionCapturePolicy(
  params: { q?: string; page?: number; page_size?: number },
  signal?: AbortSignal
): Promise<SessionCapturePolicyResponse> {
  const { data } = await apiClient.get<SessionCapturePolicyResponse>('/admin/session-delivery/policy', { params, signal })
  return data
}

export async function updateSessionCaptureMode(mode: SessionCaptureMode): Promise<void> {
  await apiClient.put('/admin/session-delivery/policy/mode', { mode })
}

export async function updateSessionCaptureAPIKey(id: number, policy: SessionCaptureKeyPolicy): Promise<void> {
  await apiClient.put(`/admin/session-delivery/policy/api-keys/${id}`, { policy })
}

export async function setOnlySessionCaptureAPIKey(id: number): Promise<void> {
  await apiClient.post(`/admin/session-delivery/policy/api-keys/${id}/only`)
}

export const sessionDeliveryAPI = {
  overview: getSessionDeliveryOverview,
  policy: getSessionCapturePolicy,
  updateMode: updateSessionCaptureMode,
  updateAPIKey: updateSessionCaptureAPIKey,
  setOnlyAPIKey: setOnlySessionCaptureAPIKey
}

export default sessionDeliveryAPI
