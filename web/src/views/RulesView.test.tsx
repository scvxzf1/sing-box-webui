import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Rule, RulePool } from '../api/types'
import { RulesView } from './RulesView'

const api = vi.hoisted(() => ({
  createRule: vi.fn(), deleteRule: vi.fn(), listRules: vi.fn(), reorderRules: vi.fn(), updateRule: vi.fn(),
  createRulePool: vi.fn(), deleteRulePool: vi.fn(), listRulePools: vi.fn(), reorderRulePools: vi.fn(), updateRulePool: vi.fn(),
}))

vi.mock('../api/client', () => api)

const rules: Rule[] = [
  {
    id: 'manual-rule', name: '本地直连', enabled: true, origin: 'manual', action: 'direct', supported: true,
    conditions: [{ type: 'domain_suffix', values: ['example.com'] }], position: 0,
  },
  {
    id: 'subscription-rule', name: 'rule_set: geoip-cn', enabled: false, origin: 'subscription',
    subscriptionId: 'subscription-1', subscriptionName: 'Main', action: 'direct', supported: false,
    unsupportedReason: 'unsupported condition "rule_set"', source: '{"rule_set":["geoip-cn"]}', position: 0,
  },
  { id: 'builtin-global-proxy', name: '全局代理', enabled: true, origin: 'builtin', action: 'proxy', supported: true, position: 1073741824, locked: true },
]

const rulePools: RulePool[] = [{
  id: 'pool-1', name: '常用规则', enabled: true, position: 0,
  rules: [{ id: 'pool-rule-1', name: '内网直连', enabled: true, action: 'direct', position: 0, conditions: [{ type: 'ip_is_private', values: [] }] }],
}]

describe('RulesView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    for (const mock of Object.values(api)) mock.mockReset()
    api.listRules.mockResolvedValue(rules)
    api.listRulePools.mockResolvedValue(rulePools)
    api.createRule.mockResolvedValue({
      id: 'manual-1', name: '内网直连', enabled: true, origin: 'manual', action: 'direct', supported: true,
      conditions: [{ type: 'ip_is_private', values: [] }], position: 0,
    })
    api.updateRule.mockResolvedValue(rules[0])
    api.createRulePool.mockResolvedValue({ id: 'pool-2', name: '新规则池', enabled: true, position: 1, rules: [] })
    api.updateRulePool.mockResolvedValue(rulePools[0])
  })

  it('creates a multi-field manual rule and disables unsupported subscription toggles', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><RulesView /></QueryClientProvider>)

    const manualPool = (await screen.findByText('未归类本地规则')).closest('article')!
    await user.click(within(manualPool).getByRole('button', { name: '进入' }))
    expect(await screen.findByText('全局代理')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '新增规则' }))
    expect(screen.getByPlaceholderText(/匹配 example\.com 以及 www\.example\.com/)).toBeInTheDocument()
    await user.type(screen.getByLabelText('名称'), '内网直连')
    await user.selectOptions(screen.getByRole('combobox', { name: '条件 1 类型' }), 'ip_is_private')
    await user.selectOptions(screen.getByRole('combobox', { name: '动作' }), 'direct')
    await user.click(screen.getByRole('button', { name: '保存规则' }))

    await waitFor(() => expect(api.createRule).toHaveBeenCalledWith({
      name: '内网直连', enabled: true, action: 'direct', conditions: [{ type: 'ip_is_private', values: [] }],
    }))
    await user.click(screen.getByRole('tab', { name: '订阅规则 1' }))
    expect(await screen.findByText('unsupported condition "rule_set"')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: '关' })).toBeDisabled()
  })

  it('updates a local rule through the text editor', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><RulesView /></QueryClientProvider>)

    const manualPool = (await screen.findByText('未归类本地规则')).closest('article')!
    await user.click(within(manualPool).getByRole('button', { name: '进入' }))
    expect(await screen.findByText('本地直连')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '用纯文本编辑 本地直连' }))
    fireEvent.change(screen.getByRole('textbox', { name: '规则文本' }), {
      target: { value: 'name=本地代理\nenabled=true\naction=proxy\ndomain_suffix=example.org' },
    })
    await user.click(screen.getByRole('button', { name: '保存规则' }))

    await waitFor(() => expect(api.updateRule).toHaveBeenCalledWith('manual-rule', {
      name: '本地代理', enabled: true, action: 'proxy',
      conditions: [{ type: 'domain_suffix', values: ['example.org'] }],
    }))
  })

  it('creates a local copy when editing a subscription rule as text', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><RulesView /></QueryClientProvider>)

    await screen.findByText('未归类本地规则')
    await user.click(await screen.findByRole('tab', { name: '订阅规则 1' }))
    await user.click(screen.getByRole('button', { name: '用纯文本编辑 rule_set: geoip-cn' }))
    expect(screen.getByText(/保存后将创建一条可独立编辑的本地规则/)).toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: '规则文本' }), {
      target: { value: 'name=CN 直连\nenabled=false\naction=direct\nip_cidr=10.0.0.0/8' },
    })
    await user.click(screen.getByRole('button', { name: '保存为本地副本' }))

    await waitFor(() => expect(api.createRule).toHaveBeenCalledWith({
      name: 'CN 直连', enabled: false, action: 'direct',
      conditions: [{ type: 'ip_cidr', values: ['10.0.0.0/8'] }],
    }))
  })

  it('creates a rule pool and atomically replaces all rules from text', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><RulesView /></QueryClientProvider>)

    await screen.findByText('未归类本地规则')
    await user.click(screen.getByRole('button', { name: '创建规则池' }))
    await user.type(screen.getByLabelText('规则池名称'), '新规则池')
    await user.click(within(screen.getByRole('dialog', { name: '创建规则池' })).getByRole('button', { name: '创建规则池' }))
    await waitFor(() => expect(api.createRulePool).toHaveBeenCalledWith({ name: '新规则池', enabled: true, rules: [] }))

    await user.click(screen.getByRole('button', { name: '用纯文本编辑规则池 常用规则' }))
    fireEvent.change(screen.getByRole('textbox', { name: '规则池文本' }), {
      target: { value: '---\nname=内网直连\nenabled=true\naction=direct\nip_is_private=true\n---\nname=广告阻断\nenabled=true\naction=block\ndomain_suffix=ads.example' },
    })
    await user.click(screen.getByRole('button', { name: '保存全部规则' }))
    await waitFor(() => expect(api.updateRulePool).toHaveBeenCalledWith('pool-1', { rules: [
      { name: '内网直连', enabled: true, action: 'direct', conditions: [{ type: 'ip_is_private', values: [] }] },
      { name: '广告阻断', enabled: true, action: 'block', conditions: [{ type: 'domain_suffix', values: ['ads.example'] }] },
    ] }))
  })

  it('enters a rule pool subview and saves a member through the pool API', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><RulesView /></QueryClientProvider>)

    const poolRow = (await screen.findByText('常用规则')).closest('article')!
    await user.click(within(poolRow).getByRole('button', { name: '进入' }))
    expect(screen.getByRole('button', { name: '返回规则池' })).toBeInTheDocument()
    expect(screen.getByText('内网直连')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '编辑 内网直连' }))
    await user.clear(screen.getByLabelText('名称'))
    await user.type(screen.getByLabelText('名称'), '局域网直连')
    await user.click(screen.getByRole('button', { name: '保存规则' }))

    await waitFor(() => expect(api.updateRulePool).toHaveBeenCalledWith('pool-1', { rules: [{
      name: '局域网直连', enabled: true, action: 'direct',
      conditions: [{ type: 'ip_is_private', values: [] }],
    }] }))
    await user.click(screen.getByRole('button', { name: '返回规则池' }))
    expect(screen.getByRole('button', { name: '创建规则池' })).toBeInTheDocument()
  })
})
