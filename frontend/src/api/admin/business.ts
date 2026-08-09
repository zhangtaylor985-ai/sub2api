import { apiClient } from '../client'

export type BusinessDataQuality = 'live' | 'actual' | 'estimated' | 'manual'
export type BusinessCostClass = 'direct' | 'operating'
export type BusinessBillingCycle = 'monthly' | 'yearly' | 'one_time'

export interface BusinessSummary {
  api_key_count: number
  private_subscription_count: number
  customer_count: number
  excluded_api_key_count: number
  api_key_revenue_cents: number
  private_subscription_revenue_cents: number
  total_revenue_cents: number
  direct_cost_cents: number
  operating_cost_cents: number
  gross_profit_cents: number
  net_profit_cents: number
  gross_margin_bps: number
  net_margin_bps: number
  costs_complete: boolean
  anomaly_count: number
}

export interface BusinessLineItem {
  id?: number
  item_type: string
  source_type: string
  source_id?: number
  name: string
  category?: string
  tier?: string
  original_amount_minor: number
  currency: string
  rate_scaled: number
  amount_cny_cents: number
  expires_on?: string
  reason?: string
  included: boolean
  linked_api_key_id?: number
  group_name?: string
  user_email?: string
}

export interface BusinessIssue {
  type: string
  severity: 'error' | 'warning' | 'info'
  source_type: string
  source_id?: number
  source_name: string
  group_id?: number
  group_name?: string
  api_key_expires_at?: string
  subscription_expires_on?: string
  message: string
  suggested_action?: string
}

export interface BusinessReport {
  id?: number
  month: string
  as_of: string
  status: string
  data_quality: BusinessDataQuality
  is_current: boolean
  summary: BusinessSummary
  items: BusinessLineItem[]
  issues: BusinessIssue[]
  notes?: string
  closed_at?: string
  closed_by?: number
}

export interface BusinessHistoryPoint {
  id?: number
  month: string
  status: string
  data_quality: BusinessDataQuality
  is_current: boolean
  summary: BusinessSummary
  customer_delta?: number
  closed_at?: string
}

export interface BusinessCostItem {
  id: number
  name: string
  cost_class: BusinessCostClass
  category: string
  amount_minor: number
  currency: string
  billing_cycle: BusinessBillingCycle
  starts_on: string
  ends_on?: string
  account_id?: number
  account_identifier?: string
  is_free: boolean
  active: boolean
  notes?: string
  created_at: string
  updated_at: string
}

export type BusinessCostPayload = Omit<
  BusinessCostItem,
  'id' | 'created_at' | 'updated_at'
>

export interface BusinessExchangeRate {
  id: number
  month: string
  currency: string
  rate_scaled: number
  source: string
  notes?: string
  created_at: string
  updated_at: string
}

export interface BusinessExchangeRateRefreshResult {
  rate: BusinessExchangeRate
  used_fallback: boolean
}

export interface BusinessPricingRule {
  id: number
  group_id: number
  group_name?: string
  tier: string
  monthly_price_cents: number
  active: boolean
  notes?: string
  created_at: string
  updated_at: string
}

export interface BusinessAPIKeyConfig {
  id: number
  api_key_id: number
  revenue_excluded: boolean
  override_amount_cents?: number
  private_subscription_id?: number
  reason?: string
}

export interface BusinessGroupReference {
  id: number
  name: string
  status: string
  is_exclusive: boolean
}

export interface BusinessAPIKeyReference {
  id: number
  name: string
  status: string
  expires_at?: string
  group_id?: number
  group_name: string
  user_email: string
  config?: BusinessAPIKeyConfig
}

export interface BusinessAccountReference {
  id: number
  name: string
  platform: string
  status: string
}

export interface BusinessPrivateSubscriptionReference {
  id: number
  name: string
  subscription_type: string
  amount_cents: number
  expires_on: string
  created_at: string
}

export interface BusinessReferenceData {
  groups: BusinessGroupReference[]
  api_keys: BusinessAPIKeyReference[]
  accounts: BusinessAccountReference[]
  private_subscriptions: BusinessPrivateSubscriptionReference[]
}

export interface BusinessReconciliationResult {
  as_of: string
  issues: BusinessIssue[]
  error_count: number
  warning_count: number
  info_count: number
}

export interface BusinessInitializationResult {
  pricing_created: number
  pricing_existing: number
  exclusions_created: number
  exclusions_existing: number
  costs_created: number
  costs_existing: number
  exchange_rate_created: boolean
  missing_pricing_tiers: string[]
  missing_excluded_names: string[]
  missing_account_names: string[]
}

export async function getCurrent(): Promise<BusinessReport> {
  const { data } = await apiClient.get<BusinessReport>('/admin/business/dashboard/current')
  return data
}

export async function getHistory(): Promise<BusinessHistoryPoint[]> {
  const { data } = await apiClient.get<BusinessHistoryPoint[]>(
    '/admin/business/dashboard/history'
  )
  return data
}

export async function getMonth(month: string): Promise<BusinessReport> {
  const { data } = await apiClient.get<BusinessReport>(
    `/admin/business/dashboard/months/${month}`
  )
  return data
}

export async function listCosts(): Promise<BusinessCostItem[]> {
  const { data } = await apiClient.get<BusinessCostItem[]>('/admin/business/costs')
  return data
}

export async function createCost(payload: BusinessCostPayload): Promise<BusinessCostItem> {
  const { data } = await apiClient.post<BusinessCostItem>('/admin/business/costs', payload)
  return data
}

export async function updateCost(
  id: number,
  payload: BusinessCostPayload
): Promise<BusinessCostItem> {
  const { data } = await apiClient.put<BusinessCostItem>(`/admin/business/costs/${id}`, payload)
  return data
}

export async function deleteCost(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/business/costs/${id}`)
  return data
}

export async function listPricingRules(): Promise<BusinessPricingRule[]> {
  const { data } = await apiClient.get<BusinessPricingRule[]>('/admin/business/pricing-rules')
  return data
}

export async function upsertPricingRule(payload: {
  group_id: number
  tier: string
  monthly_price_cents: number
  active: boolean
  notes?: string
}): Promise<BusinessPricingRule> {
  const { data } = await apiClient.put<BusinessPricingRule>(
    '/admin/business/pricing-rules',
    payload
  )
  return data
}

export async function listExchangeRates(month: string): Promise<BusinessExchangeRate[]> {
  const { data } = await apiClient.get<BusinessExchangeRate[]>(
    `/admin/business/exchange-rates/${month}`
  )
  return data
}

export async function upsertExchangeRate(
  month: string,
  payload: { currency: string; rate_scaled: number; source?: string; notes?: string }
): Promise<BusinessExchangeRate> {
  const { data } = await apiClient.put<BusinessExchangeRate>(
    `/admin/business/exchange-rates/${month}`,
    payload
  )
  return data
}

export async function refreshExchangeRate(): Promise<BusinessExchangeRateRefreshResult> {
  const { data } = await apiClient.post<BusinessExchangeRateRefreshResult>(
    '/admin/business/exchange-rates/refresh'
  )
  return data
}

export async function getAPIKeyConfig(apiKeyId: number): Promise<BusinessAPIKeyReference> {
  const { data } = await apiClient.get<BusinessAPIKeyReference>(
    `/admin/business/api-key-configs/${apiKeyId}`
  )
  return data
}

export async function upsertAPIKeyConfig(
  apiKeyId: number,
  payload: {
    revenue_excluded: boolean
    override_amount_cents?: number
    private_subscription_id?: number
    reason?: string
  }
): Promise<BusinessAPIKeyConfig> {
  const { data } = await apiClient.put<BusinessAPIKeyConfig>(
    `/admin/business/api-key-configs/${apiKeyId}`,
    payload
  )
  return data
}

export async function getReferences(): Promise<BusinessReferenceData> {
  const { data } = await apiClient.get<BusinessReferenceData>('/admin/business/references')
  return data
}

export async function getReconciliation(): Promise<BusinessReconciliationResult> {
  const { data } = await apiClient.get<BusinessReconciliationResult>(
    '/admin/business/reconciliation'
  )
  return data
}

export async function initializeDefaults(): Promise<BusinessInitializationResult> {
  const { data } = await apiClient.post<BusinessInitializationResult>(
    '/admin/business/initialize'
  )
  return data
}

export async function closeMonth(
  month: string,
  payload: { data_quality: Exclude<BusinessDataQuality, 'live'>; notes?: string }
): Promise<{ created: boolean; snapshot: BusinessReport }> {
  const { data } = await apiClient.post<{ created: boolean; snapshot: BusinessReport }>(
    `/admin/business/snapshots/${month}/close`,
    payload
  )
  return data
}

const businessAPI = {
  getCurrent,
  getHistory,
  getMonth,
  listCosts,
  createCost,
  updateCost,
  deleteCost,
  listPricingRules,
  upsertPricingRule,
  listExchangeRates,
  upsertExchangeRate,
  refreshExchangeRate,
  getAPIKeyConfig,
  upsertAPIKeyConfig,
  getReferences,
  getReconciliation,
  initializeDefaults,
  closeMonth
}

export default businessAPI
