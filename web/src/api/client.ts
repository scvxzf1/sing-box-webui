import type {
  ApplyRuntime,
  CoreInfo,
  CoreUpdate,
  CreateSubscription,
  ErrorResponse,
  LatencyRequest,
  LatencyResponse,
  NodePool,
  CreateNodePool,
  UpdateNodePool,
  Runtime,
  Session,
  StatusResponse,
  Subscription,
  UpdateSubscription,
  Rule,
  CreateRule,
  UpdateRule,
  RulePool,
  CreateRulePool,
  UpdateRulePool,
  TrafficPolicy,
  UpdateTrafficPolicy,
} from './types'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string

  constructor(status: number, response: ErrorResponse) {
    super(response.error.message)
    this.name = 'ApiError'
    this.status = status
    this.code = response.error.code
    this.requestId = response.requestId
  }
}

let sessionPromise: Promise<Session> | undefined

async function getSession(): Promise<Session> {
  sessionPromise ??= request<Session>('/api/v1/session')
  return sessionPromise
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) {
    headers.set('Content-Type', 'application/json')
  }
  const method = (init.method ?? 'GET').toUpperCase()
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
    const session = await getSession()
    headers.set('X-CSRF-Token', session.csrfToken)
  }

  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    let body: ErrorResponse
    try {
      body = (await response.json()) as ErrorResponse
    } catch {
      body = {
        error: { code: 'response_invalid', message: `请求失败（HTTP ${response.status}）` },
        requestId: response.headers.get('X-Request-ID') ?? '',
      }
    }
    throw new ApiError(response.status, body)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

export function getStatus(signal?: AbortSignal): Promise<StatusResponse> {
  return request('/api/v1/status', { signal })
}

export async function listSubscriptions(signal?: AbortSignal): Promise<Subscription[]> {
  const response = await request<{ items: Subscription[] }>('/api/v1/subscriptions', { signal })
  return response.items
}

export function getSubscription(id: string, signal?: AbortSignal): Promise<Subscription> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}`, { signal })
}

export function createSubscription(input: CreateSubscription): Promise<Subscription> {
  return request('/api/v1/subscriptions', { method: 'POST', body: JSON.stringify(input) })
}

export function updateSubscription(id: string, input: UpdateSubscription): Promise<Subscription> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  })
}

export function deleteSubscription(id: string): Promise<void> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function refreshSubscription(id: string): Promise<Subscription> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/refresh`, { method: 'POST' })
}

export function activateSubscription(id: string): Promise<Subscription> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(id)}/activate`, { method: 'POST' })
}

export function selectNode(subscriptionId: string, nodeId: string): Promise<Subscription> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(subscriptionId)}/selection`, {
    method: 'PUT',
    body: JSON.stringify({ nodeId }),
  })
}

export function testNodeLatency(subscriptionId: string, input: LatencyRequest): Promise<LatencyResponse> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(subscriptionId)}/latency`, {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function listNodePools(signal?: AbortSignal): Promise<NodePool[]> {
  const response = await request<{ items: NodePool[] }>('/api/v1/pools', { signal })
  return response.items
}

export function createNodePool(input: CreateNodePool): Promise<NodePool> {
  return request('/api/v1/pools', { method: 'POST', body: JSON.stringify(input) })
}

export function updateNodePool(id: string, input: UpdateNodePool): Promise<NodePool> {
  return request(`/api/v1/pools/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) })
}

export function deleteNodePool(id: string): Promise<void> {
  return request(`/api/v1/pools/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function listRules(signal?: AbortSignal): Promise<Rule[]> {
  const response = await request<{ items: Rule[] }>('/api/v1/rules', { signal })
  return response.items
}

export function createRule(input: CreateRule): Promise<Rule> {
  return request('/api/v1/rules', { method: 'POST', body: JSON.stringify(input) })
}

export function updateRule(id: string, input: UpdateRule): Promise<Rule> {
  return request(`/api/v1/rules/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) })
}

export function deleteRule(id: string): Promise<void> {
  return request(`/api/v1/rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function reorderRules(ids: string[]): Promise<Rule[]> {
  const response = await request<{ items: Rule[] }>('/api/v1/rules/order', {
    method: 'PUT', body: JSON.stringify({ ids }),
  })
  return response.items
}

export async function listRulePools(signal?: AbortSignal): Promise<RulePool[]> {
  const response = await request<{ items: RulePool[] }>('/api/v1/rule-pools', { signal })
  return response.items
}

export function createRulePool(input: CreateRulePool): Promise<RulePool> {
  return request('/api/v1/rule-pools', { method: 'POST', body: JSON.stringify(input) })
}

export function updateRulePool(id: string, input: UpdateRulePool): Promise<RulePool> {
  return request(`/api/v1/rule-pools/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) })
}

export function deleteRulePool(id: string): Promise<void> {
  return request(`/api/v1/rule-pools/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function reorderRulePools(ids: string[]): Promise<RulePool[]> {
  const response = await request<{ items: RulePool[] }>('/api/v1/rule-pools/order', {
    method: 'PUT', body: JSON.stringify({ ids }),
  })
  return response.items
}

export function getRuntime(signal?: AbortSignal): Promise<Runtime> {
  return request('/api/v1/runtime', { signal })
}

export function applyRuntime(input: ApplyRuntime): Promise<Runtime> {
  return request('/api/v1/runtime/apply', { method: 'POST', body: JSON.stringify(input) })
}

export function stopRuntime(): Promise<Runtime> {
  return request('/api/v1/runtime/stop', { method: 'POST' })
}

export function getTrafficPolicy(signal?: AbortSignal): Promise<TrafficPolicy> {
  return request('/api/v1/traffic-policy', { signal })
}

export function updateTrafficPolicy(input: UpdateTrafficPolicy): Promise<TrafficPolicy> {
  return request('/api/v1/traffic-policy', { method: 'PUT', body: JSON.stringify(input) })
}

export function getCore(signal?: AbortSignal): Promise<CoreInfo> {
  return request('/api/v1/core', { signal })
}

export function updateCore(input: CoreUpdate): Promise<CoreInfo> {
  return request('/api/v1/core/update', { method: 'POST', body: JSON.stringify(input) })
}

export function rollbackCore(): Promise<CoreInfo> {
  return request('/api/v1/core/rollback', { method: 'POST' })
}
