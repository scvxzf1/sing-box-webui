import type {
  ApplyRuntime,
  CoreInfo,
  CoreUpdate,
  CreateSubscription,
  ErrorResponse,
  LatencyRequest,
  LatencyResponse,
  LinkSnapshot,
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
  DnsProfile,
  UpdateDnsProfile,
  ConnectivityTarget,
  CreateConnectivityTarget,
  UpdateConnectivityTarget,
  ConnectivityTestResponse,
  ConnectivityDiagnosticInput,
  ConnectivityDiagnosticResult,
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

export const AUTH_REQUIRED_EVENT = 'sing-box-webui:auth-required'

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
    if (response.status === 401 && path !== '/api/v1/auth/login') {
      sessionPromise = undefined
      window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT))
    }
    throw new ApiError(response.status, body)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return (await response.json()) as T
}

export function getAuthSession(): Promise<Session> {
  return getSession()
}

export async function login(token: string): Promise<void> {
  const response = await fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
  if (!response.ok) {
    let body: ErrorResponse
    try {
      body = (await response.json()) as ErrorResponse
    } catch {
      body = { error: { code: 'response_invalid', message: `请求失败（HTTP ${response.status}）` }, requestId: '' }
    }
    throw new ApiError(response.status, body)
  }
  sessionPromise = undefined
  await getSession()
}

export async function logout(): Promise<void> {
  await request('/api/v1/auth/logout', { method: 'POST' })
  sessionPromise = undefined
}

export function getStatus(signal?: AbortSignal): Promise<StatusResponse> {
  return request('/api/v1/status', { signal })
}

export async function listSubscriptions(signal?: AbortSignal): Promise<Subscription[]> {
  const response = await request<{ items: Subscription[] }>('/api/v1/subscriptions', { signal })
  return response.items
}

export async function reorderSubscriptions(ids: string[]): Promise<Subscription[]> {
  const response = await request<{ items: Subscription[] }>('/api/v1/subscriptions/order', {
    method: 'PUT',
    body: JSON.stringify({ ids }),
  })
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

export async function reorderNodePools(ids: string[]): Promise<NodePool[]> {
  const response = await request<{ items: NodePool[] }>('/api/v1/pools/order', {
    method: 'PUT',
    body: JSON.stringify({ ids }),
  })
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

export interface ListLinksParams {
  search?: string
  active?: boolean
  /** Comma-separated sort keys; prefix with `-` for descending. */
  sort?: string
  offset?: number
  limit?: number
}

export function listLinks(params: ListLinksParams = {}, signal?: AbortSignal): Promise<LinkSnapshot> {
  const query = new URLSearchParams()
  if (params.search) query.set('search', params.search)
  if (params.active !== undefined) query.set('active', String(params.active))
  if (params.sort) query.set('sort', params.sort)
  if (params.offset) query.set('offset', String(params.offset))
  if (params.limit) query.set('limit', String(params.limit))
  const suffix = query.size > 0 ? `?${query.toString()}` : ''
  return request(`/api/v1/links${suffix}`, { signal })
}

export function clearLinks(): Promise<void> {
  return request('/api/v1/links/clear', { method: 'POST' })
}

export function getTrafficPolicy(signal?: AbortSignal): Promise<TrafficPolicy> {
  return request('/api/v1/traffic-policy', { signal })
}

export function updateTrafficPolicy(input: UpdateTrafficPolicy): Promise<TrafficPolicy> {
  return request('/api/v1/traffic-policy', { method: 'PUT', body: JSON.stringify(input) })
}

export function getDnsProfile(signal?: AbortSignal): Promise<DnsProfile> {
  return request('/api/v1/dns/profile', { signal })
}

export function updateDnsProfile(input: UpdateDnsProfile): Promise<DnsProfile> {
  return request('/api/v1/dns/profile', { method: 'PUT', body: JSON.stringify(input) })
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

export async function listConnectivityTargets(signal?: AbortSignal): Promise<ConnectivityTarget[]> {
  const response = await request<{ items: ConnectivityTarget[] }>('/api/v1/connectivity', { signal })
  return response.items
}

export function createConnectivityTarget(input: CreateConnectivityTarget): Promise<ConnectivityTarget> {
  return request('/api/v1/connectivity', { method: 'POST', body: JSON.stringify(input) })
}

export function updateConnectivityTarget(id: string, input: UpdateConnectivityTarget): Promise<ConnectivityTarget> {
  return request(`/api/v1/connectivity/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(input) })
}

export function deleteConnectivityTarget(id: string): Promise<void> {
  return request(`/api/v1/connectivity/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function testConnectivity(id: string): Promise<ConnectivityTestResponse> {
  return request(`/api/v1/connectivity/${encodeURIComponent(id)}/test`, { method: 'POST' })
}

export function testAllConnectivity(): Promise<ConnectivityTestResponse> {
  return request('/api/v1/connectivity/test', { method: 'POST' })
}

export function runConnectivityDiagnostic(input: ConnectivityDiagnosticInput): Promise<ConnectivityDiagnosticResult> {
  return request('/api/v1/connectivity/diagnostic', { method: 'POST', body: JSON.stringify(input) })
}
