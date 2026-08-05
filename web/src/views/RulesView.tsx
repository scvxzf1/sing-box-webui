import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle, ArrowLeft, ArrowRight, ChevronDown, ChevronUp, Database, Download, FileCode2, FilePenLine,
  Globe2, Layers3, LockKeyhole, Pencil, Plus, Route, Save, Trash2, X,
} from 'lucide-react'
import {
  createRule, createRulePool, deleteRule, deleteRulePool, listRulePools, listRules,
  reorderRulePools, reorderRules, updateRule, updateRulePool,
} from '../api/client'
import type { CreateRule, PoolRule, Rule, RuleCondition, RulePool } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'
import {
  parseRulePoolText, parseRuleText, ruleDownloadName, serializeRulePoolText, serializeRuleText,
} from './ruleText'

type RuleTab = 'pool' | 'subscription'
type ConditionType = RuleCondition['type']
type RuleAction = Rule['action']

type RuleDraft = {
  id?: string
  name: string
  enabled: boolean
  action: RuleAction
  conditions: Array<{ type: ConditionType; values: string }>
}

type TextRuleDraft = {
  rule: Rule
  text: string
}

type RulePoolDraft = {
  id?: string
  name: string
  enabled: boolean
}

type TextRulePoolDraft = {
  pool: RulePool
  text: string
}

type PoolRuleDraft = {
  pool: RulePool
  draft: RuleDraft
}

type TextPoolRuleDraft = {
  pool: RulePool
  rule: PoolRule
  text: string
}

const manualRuleGroup = '__manual__'

const conditionOptions: Array<{ value: ConditionType; label: string; placeholder: string }> = [
  { value: 'domain', label: '完整域名', placeholder: '示例：example.com 或 api.example.com\n只匹配完全相同的域名，不含 http://、端口和路径\n多个值请换行填写，也可以使用逗号分隔' },
  { value: 'domain_suffix', label: '域名后缀', placeholder: '示例：example.com\n匹配 example.com 以及 www.example.com 等所有子域名\n不要填写 *. 或 http://；多个值请换行或用逗号分隔' },
  { value: 'domain_keyword', label: '域名关键字', placeholder: '示例：google 或 youtube\n域名中只要包含该文字就会匹配，不需要星号或通配符\n多个关键字请换行或用逗号分隔' },
  { value: 'ip_cidr', label: 'IP / CIDR', placeholder: '单个 IP 示例：8.8.8.8\n网段示例：10.0.0.0/8 或 2001:db8::/32\n多个 IP 或网段请换行填写，也可以使用逗号分隔' },
  { value: 'ip_is_private', label: '私有 IP', placeholder: '' },
  { value: 'port', label: '端口', placeholder: '示例：80、443 或 8080\n只填写 1-65535 的端口数字，不要填写冒号\n多个端口请换行或用逗号分隔' },
  { value: 'port_range', label: '端口范围', placeholder: '示例：8000:9000\n格式必须是“起始端口:结束端口”，起始值不能大于结束值\n多个范围请换行或用逗号分隔' },
  { value: 'process_name', label: '进程名', placeholder: '示例：firefox 或 curl\n填写程序的进程名称，不是窗口标题或完整文件路径\n多个进程名请换行或用逗号分隔' },
  { value: 'network', label: '网络协议', placeholder: '可填写：tcp 或 udp\n同时匹配两种协议可填写：tcp, udp\n不支持 http、https 等应用层协议' },
  { value: 'protocol', label: '应用协议', placeholder: '常用示例：dns、http、tls、quic、bittorrent\n填写 sing-box 能识别的应用协议名称\n多个协议请换行或用逗号分隔' },
]

const emptyDraft = (): RuleDraft => ({
  name: '', enabled: true, action: 'proxy', conditions: [{ type: 'domain_suffix', values: '' }],
})

export function RulesView() {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<RuleTab>('pool')
  const [activeRuleGroup, setActiveRuleGroup] = useState<string | null>(null)
  const [draft, setDraft] = useState<RuleDraft | null>(null)
  const [textDraft, setTextDraft] = useState<TextRuleDraft | null>(null)
  const [poolDraft, setPoolDraft] = useState<RulePoolDraft | null>(null)
  const [poolTextDraft, setPoolTextDraft] = useState<TextRulePoolDraft | null>(null)
  const [poolRuleDraft, setPoolRuleDraft] = useState<PoolRuleDraft | null>(null)
  const [poolRuleTextDraft, setPoolRuleTextDraft] = useState<TextPoolRuleDraft | null>(null)
  const [subscriptionFilter, setSubscriptionFilter] = useState('all')
  const rulesQuery = useQuery({ queryKey: ['rules'], queryFn: ({ signal }) => listRules(signal) })
  const poolsQuery = useQuery({ queryKey: ['rule-pools'], queryFn: ({ signal }) => listRulePools(signal) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['rules'] })
  const invalidatePools = () => queryClient.invalidateQueries({ queryKey: ['rule-pools'] })

  const saveMutation = useMutation({
    mutationFn: async (value: RuleDraft) => {
      const input: CreateRule = {
        name: value.name.trim(), enabled: value.enabled, action: value.action,
        conditions: value.conditions.map((condition) => ({
          type: condition.type,
          values: condition.type === 'ip_is_private' ? [] : splitValues(condition.values),
        })),
      }
      return value.id ? updateRule(value.id, input) : createRule(input)
    },
    onSuccess: () => setDraft(null),
    onSettled: invalidate,
  })
  const textSaveMutation = useMutation({
    mutationFn: ({ rule, input }: { rule: Rule; input: CreateRule }) => (
      rule.origin === 'manual' ? updateRule(rule.id, input) : createRule(input)
    ),
    onSuccess: () => setTextDraft(null),
    onSettled: invalidate,
  })
  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => updateRule(id, { enabled }),
    onSettled: invalidate,
  })
  const deleteMutation = useMutation({
    mutationFn: deleteRule,
    onSettled: invalidate,
  })
  const orderMutation = useMutation({
    mutationFn: reorderRules,
    onSettled: invalidate,
  })
  const poolSaveMutation = useMutation({
    mutationFn: (value: RulePoolDraft) => value.id
      ? updateRulePool(value.id, { name: value.name.trim(), enabled: value.enabled })
      : createRulePool({ name: value.name.trim(), enabled: value.enabled, rules: [] }),
    onSuccess: () => {
      setPoolDraft(null)
    },
    onSettled: invalidatePools,
  })
  const poolTextSaveMutation = useMutation({
    mutationFn: ({ id, rules }: { id: string; rules: CreateRule[] }) => updateRulePool(id, { rules }),
    onSuccess: () => setPoolTextDraft(null),
    onSettled: invalidatePools,
  })
  const poolToggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => updateRulePool(id, { enabled }),
    onSettled: invalidatePools,
  })
  const poolDeleteMutation = useMutation({
    mutationFn: deleteRulePool,
    onSettled: invalidatePools,
  })
  const poolOrderMutation = useMutation({
    mutationFn: reorderRulePools,
    onSettled: invalidatePools,
  })
  const poolRulesMutation = useMutation({
    mutationFn: ({ pool, rules }: { pool: RulePool; rules: CreateRule[] }) => updateRulePool(pool.id, { rules }),
    onSettled: invalidatePools,
  })
  const poolRuleSaveMutation = useMutation({
    mutationFn: ({ pool, draft: value }: PoolRuleDraft) => {
      const input = draftToCreateRule(value)
      const rules = (pool.rules ?? []).map(toCreateRule)
      const index = (pool.rules ?? []).findIndex((rule) => rule.id === value.id)
      if (index >= 0) rules[index] = input
      else rules.push(input)
      return updateRulePool(pool.id, { rules })
    },
    onSuccess: () => setPoolRuleDraft(null),
    onSettled: invalidatePools,
  })
  const poolRuleTextSaveMutation = useMutation({
    mutationFn: ({ pool, rule, input }: { pool: RulePool; rule: PoolRule; input: CreateRule }) => updateRulePool(pool.id, {
      rules: (pool.rules ?? []).map((item) => item.id === rule.id ? input : toCreateRule(item)),
    }),
    onSuccess: () => setPoolRuleTextDraft(null),
    onSettled: invalidatePools,
  })

  const rules = rulesQuery.data ?? []
  const rulePools = poolsQuery.data ?? []
  const activePool = activeRuleGroup && activeRuleGroup !== manualRuleGroup
    ? rulePools.find((pool) => pool.id === activeRuleGroup)
    : undefined
  const manualRules = rules.filter((rule) => rule.origin === 'manual')
  const builtinRule = rules.find((rule) => rule.origin === 'builtin')
  const subscriptionRules = rules.filter((rule) => rule.origin === 'subscription')
  const subscriptions = useMemo(() => {
    const names = new Map<string, string>()
    for (const rule of subscriptionRules) names.set(rule.subscriptionId ?? '', rule.subscriptionName || '未命名订阅')
    return [...names].filter(([id]) => id).sort((a, b) => a[1].localeCompare(b[1], 'zh-CN'))
  }, [subscriptionRules])
  const filteredSubscriptionRules = subscriptionFilter === 'all'
    ? subscriptionRules
    : subscriptionRules.filter((rule) => rule.subscriptionId === subscriptionFilter)
  const mutationError = saveMutation.error ?? toggleMutation.error ?? deleteMutation.error ?? orderMutation.error
    ?? poolSaveMutation.error ?? poolToggleMutation.error ?? poolDeleteMutation.error ?? poolOrderMutation.error
    ?? poolRulesMutation.error ?? poolRuleSaveMutation.error ?? poolRuleTextSaveMutation.error

  const openEditor = (rule?: Rule) => {
    setDraft(rule ? {
      id: rule.id, name: rule.name, enabled: rule.enabled, action: rule.action,
      conditions: (rule.conditions ?? []).map((condition) => ({
        type: condition.type, values: (condition.values ?? []).join(', '),
      })),
    } : emptyDraft())
  }
  const openTextEditor = (rule: Rule) => {
    textSaveMutation.reset()
    setTextDraft({ rule, text: serializeRuleText(rule) })
  }
  const moveRule = (id: string, direction: -1 | 1) => {
    const index = manualRules.findIndex((rule) => rule.id === id)
    const target = index + direction
    if (index < 0 || target < 0 || target >= manualRules.length) return
    const next = manualRules.map((rule) => rule.id)
    ;[next[index], next[target]] = [next[target], next[index]]
    orderMutation.mutate(next)
  }
  const openPoolEditor = (pool?: RulePool) => {
    poolSaveMutation.reset()
    setPoolDraft(pool ? { id: pool.id, name: pool.name, enabled: pool.enabled } : { name: '', enabled: true })
  }
  const openPoolTextEditor = (pool: RulePool) => {
    poolTextSaveMutation.reset()
    setPoolTextDraft({ pool, text: serializeRulePoolText(pool) })
  }
  const movePool = (id: string, direction: -1 | 1) => {
    const index = rulePools.findIndex((pool) => pool.id === id)
    const target = index + direction
    if (index < 0 || target < 0 || target >= rulePools.length) return
    const next = rulePools.map((pool) => pool.id)
    ;[next[index], next[target]] = [next[target], next[index]]
    poolOrderMutation.mutate(next)
  }
  const openPoolRuleEditor = (pool: RulePool, rule?: PoolRule) => setPoolRuleDraft({
    pool,
    draft: rule ? {
      id: rule.id, name: rule.name, enabled: rule.enabled, action: rule.action,
      conditions: rule.conditions.map((condition) => ({ type: condition.type, values: (condition.values ?? []).join(', ') })),
    } : emptyDraft(),
  })
  const openPoolRuleTextEditor = (pool: RulePool, rule: PoolRule) => {
    poolRuleTextSaveMutation.reset()
    setPoolRuleTextDraft({ pool, rule, text: serializeRuleText(rule) })
  }
  const replacePoolRules = (pool: RulePool, rules: PoolRule[]) => poolRulesMutation.mutate({ pool, rules: rules.map(toCreateRule) })
  const movePoolRule = (pool: RulePool, id: string, direction: -1 | 1) => {
    const rules = [...(pool.rules ?? [])]
    const index = rules.findIndex((rule) => rule.id === id)
    const target = index + direction
    if (index < 0 || target < 0 || target >= rules.length) return
    ;[rules[index], rules[target]] = [rules[target], rules[index]]
    replacePoolRules(pool, rules)
  }

  return (
    <>
      <PageHeading
        eyebrow="ROUTING RULES"
        title="规则管理"
        action={tab === 'pool' ? activeRuleGroup ? (
          <button className="button button--primary" type="button" onClick={() => activeRuleGroup === manualRuleGroup ? openEditor() : activePool && openPoolRuleEditor(activePool)}>
            <Plus size={16} />新增规则
          </button>
        ) : (
          <button className="button button--primary" type="button" onClick={() => openPoolEditor()}><Plus size={16} />创建规则池</button>
        ) : undefined}
      />
      {(rulesQuery.error || poolsQuery.error || mutationError) && <InlineError error={rulesQuery.error ?? poolsQuery.error ?? mutationError} />}

      <div className="rules-tabs" role="tablist" aria-label="规则来源">
        <button className={tab === 'pool' ? 'rules-tab rules-tab--active' : 'rules-tab'} type="button" role="tab" aria-selected={tab === 'pool'} onClick={() => setTab('pool')}>
          <Layers3 size={16} />规则池 <span>{rulePools.length}</span>
        </button>
        <button className={tab === 'subscription' ? 'rules-tab rules-tab--active' : 'rules-tab'} type="button" role="tab" aria-selected={tab === 'subscription'} onClick={() => { setTab('subscription'); setActiveRuleGroup(null) }}>
          <Database size={16} />订阅规则 <span>{subscriptionRules.length}</span>
        </button>
      </div>

      {tab === 'pool' ? activeRuleGroup ? (
        <section className="rules-panel rule-group-detail" aria-label={activeRuleGroup === manualRuleGroup ? '未归类本地规则' : activePool?.name}>
          <div className="rule-group-toolbar">
            <button className="button" type="button" onClick={() => setActiveRuleGroup(null)}><ArrowLeft size={16} />返回规则池</button>
            <div><strong>{activeRuleGroup === manualRuleGroup ? '未归类本地规则' : activePool?.name}</strong><span>{activeRuleGroup === manualRuleGroup ? manualRules.length : (activePool?.rules ?? []).length} 条规则</span></div>
            {activePool && <div className="rule-group-toolbar-actions"><button className="button" type="button" onClick={() => openPoolTextEditor(activePool)}><FilePenLine size={15} />整池文本编辑</button><button className="icon-button" type="button" title="下载规则池" aria-label={`下载规则池 ${activePool.name}`} onClick={() => downloadRulePoolText(activePool)}><Download size={16} /></button></div>}
          </div>
          <div className="panel">
            <div className="rules-table-heading"><span>状态</span><span>规则</span><span>匹配条件</span><span>动作</span><span>排序与操作</span></div>
            {activeRuleGroup === manualRuleGroup ? manualRules.map((rule, index) => (
              <article className="rule-row" key={rule.id}>
                <RuleToggle rule={rule} pending={toggleMutation.isPending} onChange={(enabled) => toggleMutation.mutate({ id: rule.id, enabled })} />
                <div className="rule-title"><strong>{rule.name}</strong><small>未归类 · #{index + 1}</small></div><ConditionSummary conditions={rule.conditions ?? []} /><ActionBadge action={rule.action} />
                <div className="rule-actions"><button className="icon-button" type="button" title="上移" aria-label={`上移 ${rule.name}`} disabled={index === 0 || orderMutation.isPending} onClick={() => moveRule(rule.id, -1)}><ChevronUp size={16} /></button><button className="icon-button" type="button" title="下移" aria-label={`下移 ${rule.name}`} disabled={index === manualRules.length - 1 || orderMutation.isPending} onClick={() => moveRule(rule.id, 1)}><ChevronDown size={16} /></button><button className="icon-button" type="button" title="编辑" aria-label={`编辑 ${rule.name}`} onClick={() => openEditor(rule)}><Pencil size={15} /></button><button className="icon-button" type="button" title="纯文本编辑" aria-label={`用纯文本编辑 ${rule.name}`} onClick={() => openTextEditor(rule)}><FilePenLine size={15} /></button><button className="icon-button" type="button" title="下载文本" aria-label={`下载 ${rule.name} 规则文本`} onClick={() => downloadRuleText(rule)}><Download size={15} /></button><button className="icon-button icon-button--danger" type="button" title="删除" aria-label={`删除 ${rule.name}`} disabled={deleteMutation.isPending} onClick={() => window.confirm(`删除规则“${rule.name}”？`) && deleteMutation.mutate(rule.id)}><Trash2 size={15} /></button></div>
              </article>
            )) : (activePool?.rules ?? []).map((rule, index, poolRules) => (
              <article className="rule-row" key={rule.id}>
                <label className="rule-toggle" title={rule.enabled ? '已启用' : '已关闭'}><input type="checkbox" checked={rule.enabled} disabled={poolRulesMutation.isPending} onChange={(event) => replacePoolRules(activePool, poolRules.map((item) => item.id === rule.id ? { ...item, enabled: event.target.checked } : item))} /><span aria-hidden="true" /><em>{rule.enabled ? '开' : '关'}</em></label>
                <div className="rule-title"><strong>{rule.name}</strong><small>{activePool?.name} · #{index + 1}</small></div><ConditionSummary conditions={rule.conditions} /><ActionBadge action={rule.action} />
                <div className="rule-actions"><button className="icon-button" type="button" title="上移" aria-label={`上移 ${rule.name}`} disabled={index === 0 || poolRulesMutation.isPending} onClick={() => activePool && movePoolRule(activePool, rule.id, -1)}><ChevronUp size={16} /></button><button className="icon-button" type="button" title="下移" aria-label={`下移 ${rule.name}`} disabled={index === poolRules.length - 1 || poolRulesMutation.isPending} onClick={() => activePool && movePoolRule(activePool, rule.id, 1)}><ChevronDown size={16} /></button><button className="icon-button" type="button" title="编辑" aria-label={`编辑 ${rule.name}`} onClick={() => activePool && openPoolRuleEditor(activePool, rule)}><Pencil size={15} /></button><button className="icon-button" type="button" title="纯文本编辑" aria-label={`用纯文本编辑 ${rule.name}`} onClick={() => activePool && openPoolRuleTextEditor(activePool, rule)}><FilePenLine size={15} /></button><button className="icon-button" type="button" title="下载文本" aria-label={`下载 ${rule.name} 规则文本`} onClick={() => downloadText(rule.name, serializeRuleText(rule))}><Download size={15} /></button><button className="icon-button icon-button--danger" type="button" title="删除" aria-label={`删除 ${rule.name}`} disabled={poolRulesMutation.isPending} onClick={() => activePool && window.confirm(`从规则池中删除“${rule.name}”？`) && replacePoolRules(activePool, poolRules.filter((item) => item.id !== rule.id))}><Trash2 size={15} /></button></div>
              </article>
            ))}
            {activeRuleGroup === manualRuleGroup && !manualRules.length && !rulesQuery.isPending && <div className="rules-empty">暂无未归类规则</div>}
            {activePool && !(activePool.rules ?? []).length && <div className="rules-empty">规则池为空</div>}
            {activeRuleGroup === manualRuleGroup && builtinRule && <article className="rule-row rule-row--builtin"><span className="rule-locked" title="固定启用"><LockKeyhole size={15} /></span><div className="rule-title"><strong>{builtinRule.name}</strong><small>内置兜底</small></div><div className="rule-condition"><Globe2 size={15} /><span>其余所有流量</span></div><ActionBadge action={builtinRule.action} /><div className="rule-actions rule-actions--locked"><LockKeyhole size={14} /><span>固定末位</span><button className="icon-button" type="button" title="下载文本" aria-label={`下载 ${builtinRule.name} 规则文本`} onClick={() => downloadRuleText(builtinRule)}><Download size={15} /></button></div></article>}
          </div>
        </section>
      ) : (
        <section className="rules-panel panel rule-pools-panel" aria-label="规则池">
          <div className="rule-pools-heading"><span>状态</span><span>规则池</span><span>规则数量</span><span>顺序与操作</span></div>
          <article className="rule-pool"><div className="rule-pool-header"><span className="rule-locked"><Route size={15} /></span><div className="rule-pool-title"><strong>未归类本地规则</strong><small>兼容现有独立规则</small></div><div className="rule-pool-count"><strong>{manualRules.filter((rule) => rule.enabled).length}/{manualRules.length}</strong><span>已启用</span></div><div className="rule-actions rule-pool-actions"><button className="button" type="button" onClick={() => setActiveRuleGroup(manualRuleGroup)}>进入<ArrowRight size={15} /></button></div></div></article>
          {rulePools.map((pool, index) => { const poolRules = pool.rules ?? []; const enabledRules = poolRules.filter((rule) => rule.enabled).length; return <article className="rule-pool" key={pool.id}><div className="rule-pool-header"><label className="rule-toggle" title={pool.enabled ? '已启用' : '已关闭'}><input type="checkbox" checked={pool.enabled} disabled={poolToggleMutation.isPending} onChange={(event) => poolToggleMutation.mutate({ id: pool.id, enabled: event.target.checked })} /><span aria-hidden="true" /><em>{pool.enabled ? '开' : '关'}</em></label><div className="rule-pool-title"><strong>{pool.name}</strong><small>规则池 · #{index + 1}</small></div><div className="rule-pool-count"><strong>{enabledRules}/{poolRules.length}</strong><span>已启用</span></div><div className="rule-actions rule-pool-actions"><button className="icon-button" type="button" title="上移" aria-label={`上移规则池 ${pool.name}`} disabled={index === 0 || poolOrderMutation.isPending} onClick={() => movePool(pool.id, -1)}><ChevronUp size={16} /></button><button className="icon-button" type="button" title="下移" aria-label={`下移规则池 ${pool.name}`} disabled={index === rulePools.length - 1 || poolOrderMutation.isPending} onClick={() => movePool(pool.id, 1)}><ChevronDown size={16} /></button><button className="icon-button" type="button" title="编辑规则池" aria-label={`编辑规则池 ${pool.name}`} onClick={() => openPoolEditor(pool)}><Pencil size={15} /></button><button className="icon-button" type="button" title="纯文本编辑全部规则" aria-label={`用纯文本编辑规则池 ${pool.name}`} onClick={() => openPoolTextEditor(pool)}><FilePenLine size={15} /></button><button className="icon-button" type="button" title="下载规则池文本" aria-label={`下载规则池 ${pool.name}`} onClick={() => downloadRulePoolText(pool)}><Download size={15} /></button><button className="button" type="button" onClick={() => setActiveRuleGroup(pool.id)}>进入<ArrowRight size={15} /></button><button className="icon-button icon-button--danger" type="button" title="删除规则池" aria-label={`删除规则池 ${pool.name}`} disabled={poolDeleteMutation.isPending} onClick={() => window.confirm(`删除规则池“${pool.name}”及其中 ${poolRules.length} 条规则？`) && poolDeleteMutation.mutate(pool.id)}><Trash2 size={15} /></button></div></div></article> })}
          {poolsQuery.isPending && <div className="rules-empty">加载规则池...</div>}
        </section>
      ) : (
        <section className="rules-panel panel" aria-label="订阅规则">
          <div className="subscription-rule-toolbar">
            <label><span>订阅来源</span><select value={subscriptionFilter} onChange={(event) => setSubscriptionFilter(event.target.value)}><option value="all">全部订阅</option>{subscriptions.map(([id, name]) => <option value={id} key={id}>{name}</option>)}</select></label>
            <div><strong>{filteredSubscriptionRules.filter((rule) => rule.supported).length}</strong><span>兼容</span><strong>{filteredSubscriptionRules.filter((rule) => !rule.supported).length}</strong><span>未兼容</span></div>
          </div>
          <div className="rules-table-heading rules-table-heading--subscription">
            <span>状态</span><span>来源与规则</span><span>匹配条件</span><span>动作</span><span>兼容性</span>
          </div>
          {filteredSubscriptionRules.map((rule) => (
            <article className={`rule-row rule-row--subscription ${rule.supported ? '' : 'rule-row--unsupported'}`} key={rule.id}>
              <RuleToggle rule={rule} pending={toggleMutation.isPending} onChange={(enabled) => toggleMutation.mutate({ id: rule.id, enabled })} />
              <div className="rule-title"><strong>{rule.name}</strong><small>{rule.subscriptionName || rule.subscriptionId}</small></div>
              <ConditionSummary conditions={rule.conditions ?? []} />
              <ActionBadge action={rule.action} />
              <div className="rule-support">
                <div className={`rule-support-status ${rule.supported ? 'rule-support--ok' : 'rule-support--error'}`}>
                  {rule.supported ? <><span className="status-dot status-dot--ok" />可启用</> : <><AlertTriangle size={14} /><span title={rule.unsupportedReason}>{rule.unsupportedReason}</span></>}
                </div>
                {rule.source && <details><summary><FileCode2 size={13} />原始规则</summary><code>{rule.source}</code></details>}
                <div className="rule-support-actions">
                  <button className="icon-button" type="button" title="纯文本编辑" aria-label={`用纯文本编辑 ${rule.name}`} onClick={() => openTextEditor(rule)}><FilePenLine size={15} /></button>
                  <button className="icon-button" type="button" title="下载文本" aria-label={`下载 ${rule.name} 规则文本`} onClick={() => downloadRuleText(rule)}><Download size={15} /></button>
                </div>
              </div>
            </article>
          ))}
          {rulesQuery.isPending && <div className="rules-empty">加载规则...</div>}
          {!filteredSubscriptionRules.length && !rulesQuery.isPending && <div className="rules-empty">当前没有订阅规则</div>}
        </section>
      )}

      {draft && <RuleEditor draft={draft} pending={saveMutation.isPending} onChange={setDraft} onClose={() => setDraft(null)} onSave={() => saveMutation.mutate(draft)} />}
      {textDraft && (
        <RuleTextEditor
          draft={textDraft}
          pending={textSaveMutation.isPending}
          saveError={textSaveMutation.error}
          onChange={(text) => setTextDraft({ ...textDraft, text })}
          onClose={() => setTextDraft(null)}
          onSave={(input) => textSaveMutation.mutate({ rule: textDraft.rule, input })}
        />
      )}
      {poolDraft && <RulePoolEditor draft={poolDraft} pending={poolSaveMutation.isPending} onChange={setPoolDraft} onClose={() => setPoolDraft(null)} onSave={() => poolSaveMutation.mutate(poolDraft)} />}
      {poolTextDraft && (
        <RulePoolTextEditor
          draft={poolTextDraft}
          pending={poolTextSaveMutation.isPending}
          saveError={poolTextSaveMutation.error}
          onChange={(text) => setPoolTextDraft({ ...poolTextDraft, text })}
          onClose={() => setPoolTextDraft(null)}
          onSave={(poolRules) => poolTextSaveMutation.mutate({ id: poolTextDraft.pool.id, rules: poolRules })}
        />
      )}
      {poolRuleDraft && (
        <RuleEditor
          draft={poolRuleDraft.draft}
          pending={poolRuleSaveMutation.isPending}
          onChange={(nextDraft) => setPoolRuleDraft({ ...poolRuleDraft, draft: nextDraft })}
          onClose={() => setPoolRuleDraft(null)}
          onSave={() => poolRuleSaveMutation.mutate(poolRuleDraft)}
        />
      )}
      {poolRuleTextDraft && (
        <RuleTextEditor
          draft={poolRuleTextDraft}
          pending={poolRuleTextSaveMutation.isPending}
          saveError={poolRuleTextSaveMutation.error}
          onChange={(text) => setPoolRuleTextDraft({ ...poolRuleTextDraft, text })}
          onClose={() => setPoolRuleTextDraft(null)}
          onSave={(input) => poolRuleTextSaveMutation.mutate({
            pool: poolRuleTextDraft.pool,
            rule: poolRuleTextDraft.rule,
            input,
          })}
        />
      )}
    </>
  )
}

function RuleToggle({ rule, pending, onChange }: { rule: Rule; pending: boolean; onChange: (enabled: boolean) => void }) {
  return (
    <label className="rule-toggle" title={!rule.supported ? rule.unsupportedReason : rule.enabled ? '已启用' : '已关闭'}>
      <input type="checkbox" checked={rule.enabled} disabled={rule.locked || !rule.supported || pending} onChange={(event) => onChange(event.target.checked)} />
      <span aria-hidden="true" />
      <em>{rule.enabled ? '开' : '关'}</em>
    </label>
  )
}

function ConditionSummary({ conditions }: { conditions: RuleCondition[] }) {
  if (!conditions.length) return <div className="rule-condition"><span>全部流量</span></div>
  return (
    <div className="rule-condition">
      {conditions.map((condition) => (
        <span key={condition.type} title={(condition.values ?? []).join(', ')}>
          {conditionLabel(condition.type)}: {condition.type === 'ip_is_private' ? '是' : compactValues(condition.values ?? [])}
        </span>
      ))}
    </div>
  )
}

function ActionBadge({ action }: { action: RuleAction }) {
  return <span className={`rule-action rule-action--${action}`}>{action === 'proxy' ? '代理' : action === 'direct' ? '直连' : '阻断'}</span>
}

function RuleEditor({ draft, pending, onChange, onClose, onSave }: { draft: RuleDraft; pending: boolean; onChange: (draft: RuleDraft) => void; onClose: () => void; onSave: () => void }) {
  const dialogRef = useModalBehavior<HTMLFormElement>(onClose)
  const valid = draft.name.trim().length > 0 && draft.conditions.length > 0 && draft.conditions.every((condition) => condition.type === 'ip_is_private' || splitValues(condition.values).length > 0)
  const usedTypes = new Set(draft.conditions.map((condition) => condition.type))
  const addCondition = () => {
    const nextType = conditionOptions.find((option) => !usedTypes.has(option.value))?.value
    if (nextType) onChange({ ...draft, conditions: [...draft.conditions, { type: nextType, values: '' }] })
  }
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <form ref={dialogRef} className="rule-editor" role="dialog" aria-modal="true" aria-labelledby="rule-editor-title" onSubmit={(event) => { event.preventDefault(); if (valid) onSave() }}>
        <div className="rule-editor-heading"><div><strong id="rule-editor-title">{draft.id ? '编辑规则' : '新增规则'}</strong><span>本地路由规则</span></div><button className="icon-button" type="button" title="关闭" aria-label="关闭规则编辑器" onClick={onClose}><X size={16} /></button></div>
        <div className="rule-editor-body">
          <div className="rule-editor-grid">
            <label className="rule-editor-field rule-editor-field--wide"><span>名称</span><input autoFocus maxLength={80} required value={draft.name} placeholder="例如：内网直连" onChange={(event) => onChange({ ...draft, name: event.target.value })} /></label>
            <label className="rule-editor-field"><span>动作</span><select value={draft.action} onChange={(event) => onChange({ ...draft, action: event.target.value as RuleAction })}><option value="proxy">代理</option><option value="direct">直连</option><option value="block">阻断</option></select></label>
            <label className="rule-editor-toggle"><input type="checkbox" checked={draft.enabled} onChange={(event) => onChange({ ...draft, enabled: event.target.checked })} /><span><strong>启用规则</strong><small>保存后立即更新当前运行配置</small></span></label>
          </div>
          <section className="rule-editor-conditions">
            <div className="rule-editor-section-heading"><strong>匹配条件</strong><button className="button" type="button" disabled={draft.conditions.length >= conditionOptions.length} onClick={addCondition}><Plus size={15} />添加条件</button></div>
            {draft.conditions.map((condition, index) => {
              const option = conditionOptions.find((item) => item.value === condition.type)!
              return (
                <div className="rule-condition-editor" key={`${condition.type}-${index}`}>
                  <select aria-label={`条件 ${index + 1} 类型`} value={condition.type} onChange={(event) => {
                    const conditions = [...draft.conditions]
                    conditions[index] = { type: event.target.value as ConditionType, values: '' }
                    onChange({ ...draft, conditions })
                  }}>{conditionOptions.map((item) => <option key={item.value} value={item.value} disabled={usedTypes.has(item.value) && item.value !== condition.type}>{item.label}</option>)}</select>
                  {condition.type === 'ip_is_private' ? <div className="rule-private-value">自动匹配局域网、回环及其他私有 IP 地址，无需填写内容</div> : <textarea rows={4} required value={condition.values} placeholder={option.placeholder} onChange={(event) => {
                    const conditions = [...draft.conditions]
                    conditions[index] = { ...condition, values: event.target.value }
                    onChange({ ...draft, conditions })
                  }} />}
                  <button className="icon-button icon-button--danger" type="button" title="移除条件" aria-label={`移除条件 ${index + 1}`} disabled={draft.conditions.length === 1} onClick={() => onChange({ ...draft, conditions: draft.conditions.filter((_, itemIndex) => itemIndex !== index) })}><Trash2 size={15} /></button>
                </div>
              )
            })}
          </section>
        </div>
        <div className="rule-editor-actions"><button className="button" type="button" onClick={onClose}>取消</button><button className="button button--primary" type="submit" disabled={!valid || pending}><Save size={16} />{pending ? '保存中' : '保存规则'}</button></div>
      </form>
    </div>
  )
}

function RulePoolEditor({ draft, pending, onChange, onClose, onSave }: {
  draft: RulePoolDraft
  pending: boolean
  onChange: (draft: RulePoolDraft) => void
  onClose: () => void
  onSave: () => void
}) {
  const dialogRef = useModalBehavior<HTMLFormElement>(onClose)
  const valid = draft.name.trim().length > 0
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <form ref={dialogRef} className="rule-editor rule-pool-editor" role="dialog" aria-modal="true" aria-labelledby="rule-pool-editor-title" onSubmit={(event) => { event.preventDefault(); if (valid) onSave() }}>
        <div className="rule-editor-heading">
          <div><strong id="rule-pool-editor-title">{draft.id ? '编辑规则池' : '创建规则池'}</strong><span>批量路由规则</span></div>
          <button className="icon-button" type="button" title="关闭" aria-label="关闭规则池编辑器" onClick={onClose}><X size={16} /></button>
        </div>
        <div className="rule-editor-body">
          <div className="rule-editor-grid rule-pool-editor-grid">
            <label className="rule-editor-field rule-editor-field--wide"><span>规则池名称</span><input autoFocus maxLength={80} required value={draft.name} placeholder="例如：常用直连规则" onChange={(event) => onChange({ ...draft, name: event.target.value })} /></label>
            <label className="rule-editor-toggle rule-editor-field--wide"><input type="checkbox" checked={draft.enabled} onChange={(event) => onChange({ ...draft, enabled: event.target.checked })} /><span><strong>启用规则池</strong><small>仅编译池中已启用的规则</small></span></label>
          </div>
        </div>
        <div className="rule-editor-actions"><button className="button" type="button" onClick={onClose}>取消</button><button className="button button--primary" type="submit" disabled={!valid || pending}><Save size={16} />{pending ? '保存中' : draft.id ? '保存规则池' : '创建规则池'}</button></div>
      </form>
    </div>
  )
}

function RuleTextEditor({ draft, pending, saveError, onChange, onClose, onSave }: {
  draft: TextRuleDraft | TextPoolRuleDraft
  pending: boolean
  saveError: unknown
  onChange: (text: string) => void
  onClose: () => void
  onSave: (input: CreateRule) => void
}) {
  const dialogRef = useModalBehavior<HTMLFormElement>(onClose)
  const [parseError, setParseError] = useState<string | null>(null)
  const createsCopy = 'origin' in draft.rule && draft.rule.origin === 'subscription'
  const submit = () => {
    try {
      setParseError(null)
      onSave(parseRuleText(draft.text))
    } catch (error) {
      setParseError(error instanceof Error ? error.message : '规则文本格式错误')
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <form ref={dialogRef} className="rule-editor rule-text-editor" role="dialog" aria-modal="true" aria-labelledby="rule-text-editor-title" onSubmit={(event) => { event.preventDefault(); submit() }}>
        <div className="rule-editor-heading">
          <div><strong id="rule-text-editor-title">纯文本编辑</strong><span>{draft.rule.name}</span></div>
          <button className="icon-button" type="button" title="关闭" aria-label="关闭纯文本编辑器" onClick={onClose}><X size={16} /></button>
        </div>
        <div className="rule-editor-body rule-text-editor-body">
          {createsCopy && <div className="rule-text-copy-note"><FilePenLine size={15} /><span>订阅规则由上游更新维护；保存后将创建一条可独立编辑的本地规则。</span></div>}
          <label className="rule-text-field">
            <span>规则文本</span>
            <textarea autoFocus spellCheck={false} value={draft.text} onChange={(event) => { setParseError(null); onChange(event.target.value) }} />
          </label>
          {(parseError || saveError) && <InlineError error={parseError ? new Error(parseError) : saveError} />}
          <div className="rule-text-format">
            <strong>可用条件键</strong>
            <code>domain</code><code>domain_suffix</code><code>domain_keyword</code><code>ip_cidr</code><code>ip_is_private</code><code>port</code><code>port_range</code><code>process_name</code><code>network</code><code>protocol</code>
          </div>
        </div>
        <div className="rule-editor-actions">
          <button className="button" type="button" onClick={onClose}>取消</button>
          <button className="button button--primary" type="submit" disabled={pending}><Save size={16} />{pending ? '保存中' : createsCopy ? '保存为本地副本' : '保存规则'}</button>
        </div>
      </form>
    </div>
  )
}

function RulePoolTextEditor({ draft, pending, saveError, onChange, onClose, onSave }: {
  draft: TextRulePoolDraft
  pending: boolean
  saveError: unknown
  onChange: (text: string) => void
  onClose: () => void
  onSave: (rules: CreateRule[]) => void
}) {
  const dialogRef = useModalBehavior<HTMLFormElement>(onClose)
  const [parseError, setParseError] = useState<string | null>(null)
  const [confirmEmpty, setConfirmEmpty] = useState(false)
  const submit = () => {
    try {
      const rules = parseRulePoolText(draft.text)
      if (!rules.length && (draft.pool.rules ?? []).length && !confirmEmpty) {
        setConfirmEmpty(true)
        setParseError('文本为空将删除池中全部规则；请再次点击“确认清空规则池”')
        return
      }
      setParseError(null)
      onSave(rules)
    } catch (error) {
      setConfirmEmpty(false)
      setParseError(error instanceof Error ? error.message : '规则池文本格式错误')
    }
  }
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}>
      <form ref={dialogRef} className="rule-editor rule-text-editor" role="dialog" aria-modal="true" aria-labelledby="rule-pool-text-editor-title" onSubmit={(event) => { event.preventDefault(); submit() }}>
        <div className="rule-editor-heading">
          <div><strong id="rule-pool-text-editor-title">纯文本编辑规则池</strong><span>{draft.pool.name} · {(draft.pool.rules ?? []).length} 条规则</span></div>
          <button className="icon-button" type="button" title="关闭" aria-label="关闭规则池文本编辑器" onClick={onClose}><X size={16} /></button>
        </div>
        <div className="rule-editor-body rule-text-editor-body">
          <div className="rule-text-copy-note rule-text-pool-note"><Layers3 size={15} /><span>单独一行 <code>---</code> 表示下一条规则；保存时会整池校验并一次性替换，顺序即为执行顺序。</span></div>
          <label className="rule-text-field"><span>规则池文本</span><textarea autoFocus spellCheck={false} value={draft.text} onChange={(event) => { setParseError(null); setConfirmEmpty(false); onChange(event.target.value) }} /></label>
          {(parseError || saveError) && <InlineError error={parseError ? new Error(parseError) : saveError} />}
          <div className="rule-text-format"><strong>可用条件键</strong><code>domain</code><code>domain_suffix</code><code>domain_keyword</code><code>ip_cidr</code><code>ip_is_private</code><code>port</code><code>port_range</code><code>process_name</code><code>network</code><code>protocol</code></div>
        </div>
        <div className="rule-editor-actions"><button className="button" type="button" onClick={onClose}>取消</button><button className={`button ${confirmEmpty ? 'button--danger' : 'button--primary'}`} type="submit" disabled={pending}><Save size={16} />{pending ? '保存中' : confirmEmpty ? '确认清空规则池' : '保存全部规则'}</button></div>
      </form>
    </div>
  )
}

function useModalBehavior<T extends HTMLElement>(onClose: () => void) {
  const dialogRef = useRef<T>(null)
  const onCloseRef = useRef(onClose)
  onCloseRef.current = onClose
  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onCloseRef.current()
        return
      }
      if (event.key !== 'Tab' || !dialogRef.current) return
      const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled)'))
      if (!focusable.length) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = previousOverflow
      previousFocus?.focus()
    }
  }, [])
  return dialogRef
}

function splitValues(value: string) {
  return [...new Set(value.split(/[\n,，]/).map((item) => item.trim()).filter(Boolean))]
}

function draftToCreateRule(value: RuleDraft): CreateRule {
  return {
    name: value.name.trim(),
    enabled: value.enabled,
    action: value.action,
    conditions: value.conditions.map((condition) => ({
      type: condition.type,
      values: condition.type === 'ip_is_private' ? [] : splitValues(condition.values),
    })),
  }
}

function toCreateRule(rule: PoolRule): CreateRule {
  return {
    name: rule.name,
    enabled: rule.enabled,
    action: rule.action,
    conditions: rule.conditions,
  }
}

function downloadRuleText(rule: Rule) {
  downloadText(rule.name, serializeRuleText(rule))
}

function downloadRulePoolText(pool: RulePool) {
  downloadText(pool.name, serializeRulePoolText(pool))
}

function downloadText(name: string, content: string) {
  const url = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = ruleDownloadName(name)
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

function conditionLabel(type: ConditionType) {
  return conditionOptions.find((option) => option.value === type)?.label ?? type
}

function compactValues(values: string[]) {
  if (!values.length) return '无'
  const first = values[0].length > 30 ? `${values[0].slice(0, 30)}...` : values[0]
  return values.length > 1 ? `${first} +${values.length - 1}` : first
}
