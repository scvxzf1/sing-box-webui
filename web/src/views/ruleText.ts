import type { CreateRule, Rule, RuleCondition, RulePool } from '../api/types'

type ConditionType = RuleCondition['type']
type RuleAction = Rule['action']

const conditionTypes: ConditionType[] = [
  'domain', 'domain_suffix', 'domain_keyword', 'ip_cidr', 'ip_is_private',
  'port', 'port_range', 'process_name', 'network', 'protocol',
]

type SerializableRule = Pick<Rule, 'name' | 'enabled' | 'action' | 'conditions'> & {
  source?: string
}

export function serializeRuleText(rule: SerializableRule) {
  const lines = [
    '# sing-box WebUI rule',
    '# 每行使用 key=value；空行和以 # 开头的行会被忽略',
    '# 同一条件的多个值用逗号分隔；不同条件之间需要同时满足（AND）',
    ...serializeRuleBody(rule),
  ]
  if (!(rule.conditions ?? []).length) {
    lines.push('# 当前规则没有可转换的条件，保存前请添加至少一个可用条件键')
  }
  if (rule.source) {
    lines.push('', '# 上游原始规则（仅供参考，不参与保存）')
    lines.push(...rule.source.split(/\r?\n/).map((line) => `# ${line}`))
  }
  return `${lines.join('\n')}\n`
}

export function serializeRulePoolText(pool: RulePool) {
  const lines = [
    '# sing-box WebUI rule pool',
    '# 使用单独一行 --- 分隔每条规则；从上到下即为执行顺序',
    '# 文本为空表示清空规则池',
  ]
  for (const rule of pool.rules ?? []) {
    lines.push('', '---', ...serializeRuleBody(rule))
  }
  return `${lines.join('\n')}\n`
}

export function parseRulePoolText(text: string): CreateRule[] {
  const segments: string[][] = [[]]
  for (const line of text.split(/\r?\n/)) {
    if (line.trim() === '---') {
      segments.push([])
    } else {
      segments[segments.length - 1].push(line)
    }
  }
  const ruleSegments = segments.filter((segment) => segment.some((line) => {
    const value = line.trim()
    return value.length > 0 && !value.startsWith('#')
  }))
  return ruleSegments.map((segment, index) => {
    try {
      return parseRuleText(segment.join('\n'))
    } catch (error) {
      const message = error instanceof Error ? error.message : '格式错误'
      throw new Error(`规则 ${index + 1}：${message}`)
    }
  })
}

function serializeRuleBody(rule: Pick<Rule, 'name' | 'enabled' | 'action' | 'conditions'>) {
  const lines = [`name=${rule.name}`, `enabled=${rule.enabled}`, `action=${rule.action}`]
  for (const condition of rule.conditions ?? []) {
    lines.push(condition.type === 'ip_is_private'
      ? 'ip_is_private=true'
      : `${condition.type}=${(condition.values ?? []).join(', ')}`)
  }
  return lines
}

export function parseRuleText(text: string): CreateRule {
  let name = ''
  let enabled = true
  let action: RuleAction | undefined
  let hasName = false
  let hasEnabled = false
  let hasAction = false
  const conditions = new Map<ConditionType, string[]>()

  for (const [index, rawLine] of text.split(/\r?\n/).entries()) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue
    const separator = line.indexOf('=')
    if (separator < 1) throw new Error(`第 ${index + 1} 行应为 key=value 格式`)
    const key = line.slice(0, separator).trim()
    const value = line.slice(separator + 1).trim()
    if (key === 'name') {
      if (hasName) throw new Error(`第 ${index + 1} 行重复设置 name`)
      if (!value) throw new Error(`第 ${index + 1} 行的规则名称不能为空`)
      name = value
      hasName = true
      continue
    }
    if (key === 'enabled') {
      if (hasEnabled) throw new Error(`第 ${index + 1} 行重复设置 enabled`)
      enabled = parseRuleEnabled(value, index + 1)
      hasEnabled = true
      continue
    }
    if (key === 'action') {
      if (hasAction) throw new Error(`第 ${index + 1} 行重复设置 action`)
      action = parseRuleAction(value, index + 1)
      hasAction = true
      continue
    }
    const conditionType = conditionTypes.find((type) => type === key)
    if (!conditionType) throw new Error(`第 ${index + 1} 行的键“${key}”不受支持`)
    if (conditionType === 'ip_is_private') {
      if (!parseRuleEnabled(value, index + 1)) throw new Error(`第 ${index + 1} 行 ip_is_private 只能设为 true`)
      conditions.set(conditionType, [])
      continue
    }
    const values = splitValues(value)
    if (!values.length) throw new Error(`第 ${index + 1} 行的 ${key} 至少需要一个值`)
    conditions.set(conditionType, [...new Set([...(conditions.get(conditionType) ?? []), ...values])])
  }

  if (!hasName) throw new Error('缺少必填项 name')
  if (!hasAction || !action) throw new Error('缺少必填项 action，可填 proxy、direct 或 block')
  if (!conditions.size) throw new Error('至少需要一个匹配条件')
  return {
    name,
    enabled,
    action,
    conditions: [...conditions].map(([type, values]) => ({ type, values })),
  }
}

function splitValues(value: string) {
  return [...new Set(value.split(/[\n,，]/).map((item) => item.trim()).filter(Boolean))]
}

function parseRuleEnabled(value: string, line: number) {
  const normalized = value.trim().toLowerCase()
  if (['true', '1', 'on', '是', '开', '启用'].includes(normalized)) return true
  if (['false', '0', 'off', '否', '关', '禁用'].includes(normalized)) return false
  throw new Error(`第 ${line} 行应填 true 或 false`)
}

function parseRuleAction(value: string, line: number): RuleAction {
  const normalized = value.trim().toLowerCase()
  if (normalized === 'proxy' || normalized === '代理') return 'proxy'
  if (normalized === 'direct' || normalized === '直连') return 'direct'
  if (normalized === 'block' || normalized === '阻断') return 'block'
  throw new Error(`第 ${line} 行 action 应填 proxy、direct 或 block`)
}

export function ruleDownloadName(name: string, now = new Date()) {
  const safeName = name.trim()
    .replace(/[<>:"/\\|?*]+/g, '_')
    .replace(/\p{Cc}+/gu, '_')
    .replace(/[. ]+$/g, '') || '规则'
  return `${safeName}_${now.getFullYear()}_${now.getMonth() + 1}_${now.getDate()}_${now.getHours()}_${now.getMinutes()}.txt`
}
