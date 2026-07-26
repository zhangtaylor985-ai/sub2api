import { apiClient } from '../client'

export type PrivateSubscriptionStatus = 'active' | 'due_soon' | 'expired'

export interface PrivateSubscription {
  id: number
  name: string
  subscription_type: string
  amount_cents: number
  expires_on: string
  status: PrivateSubscriptionStatus
  days_remaining: number
  reminder_sent: boolean
  reminder_sent_at?: string
  created_at: string
  updated_at: string
}

export interface PrivateSubscriptionSummary {
  total: number
  active: number
  due_soon: number
  expired: number
  total_amount_cents: number
}

export interface PrivateSubscriptionPayload {
  name: string
  subscription_type: string
  amount_cents: number
  expires_on: string
}

export interface PrivateSubscriptionListResponse {
  items: PrivateSubscription[]
  total: number
  page: number
  page_size: number
  pages: number
}

export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    search?: string
    status?: PrivateSubscriptionStatus | ''
    subscription_type?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PrivateSubscriptionListResponse> {
  const { data } = await apiClient.get<PrivateSubscriptionListResponse>(
    '/admin/private-subscriptions',
    {
      params: { page, page_size: pageSize, ...filters },
      signal: options?.signal
    }
  )
  return data
}

export async function summary(): Promise<PrivateSubscriptionSummary> {
  const { data } = await apiClient.get<PrivateSubscriptionSummary>(
    '/admin/private-subscriptions/summary'
  )
  return data
}

export async function getById(id: number): Promise<PrivateSubscription> {
  const { data } = await apiClient.get<PrivateSubscription>(
    `/admin/private-subscriptions/${id}`
  )
  return data
}

export async function create(
  payload: PrivateSubscriptionPayload
): Promise<PrivateSubscription> {
  const { data } = await apiClient.post<PrivateSubscription>(
    '/admin/private-subscriptions',
    payload
  )
  return data
}

export async function update(
  id: number,
  payload: Partial<PrivateSubscriptionPayload>
): Promise<PrivateSubscription> {
  const { data } = await apiClient.put<PrivateSubscription>(
    `/admin/private-subscriptions/${id}`,
    payload
  )
  return data
}

export async function remove(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(
    `/admin/private-subscriptions/${id}`
  )
  return data
}

const privateSubscriptionsAPI = {
  list,
  summary,
  getById,
  create,
  update,
  delete: remove
}

export default privateSubscriptionsAPI
