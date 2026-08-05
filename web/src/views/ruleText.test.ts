import { describe, expect, it } from 'vitest'
import type { Rule, RulePool } from '../api/types'
import {
  parseRulePoolText, parseRuleText, ruleDownloadName, serializeRulePoolText, serializeRuleText,
} from './ruleText'

const rule: Rule = {
  id: 'manual-1',
  name: '良心云',
  enabled: true,
  origin: 'manual',
  action: 'direct',
  supported: true,
  position: 0,
  conditions: [
    { type: 'domain_suffix', values: ['example.com', 'example.org'] },
    { type: 'ip_cidr', values: ['10.0.0.0/8'] },
    { type: 'ip_is_private', values: [] },
  ],
}

describe('rule text format', () => {
  it('round-trips every field used by a manual rule', () => {
    expect(parseRuleText(serializeRuleText(rule))).toEqual({
      name: '良心云',
      enabled: true,
      action: 'direct',
      conditions: [
        { type: 'domain_suffix', values: ['example.com', 'example.org'] },
        { type: 'ip_cidr', values: ['10.0.0.0/8'] },
        { type: 'ip_is_private', values: [] },
      ],
    })
  })

  it('accepts comments, Chinese values and repeated condition lines', () => {
    expect(parseRuleText(`
# 备注
name=测试规则
enabled=否
action=代理
domain_suffix=example.com
domain_suffix=example.org, example.com
`)).toEqual({
      name: '测试规则', enabled: false, action: 'proxy',
      conditions: [{ type: 'domain_suffix', values: ['example.com', 'example.org'] }],
    })
  })

  it('reports the source line for unsupported keys', () => {
    expect(() => parseRuleText('name=x\naction=proxy\nrule_set=geoip-cn')).toThrow('第 3 行')
  })

  it('uses the requested local timestamp filename', () => {
    expect(ruleDownloadName('良心云', new Date(2026, 7, 5, 17, 42))).toBe('良心云_2026_8_5_17_42.txt')
    expect(ruleDownloadName('a/b:*?', new Date(2026, 7, 5, 17, 42))).toBe('a_b__2026_8_5_17_42.txt')
  })
})

describe('rule pool text format', () => {
  const pool: RulePool = {
    id: 'pool-1', name: '常用规则', enabled: true, position: 0,
    rules: [
      { id: 'pool-rule-1', name: '内网直连', enabled: true, action: 'direct', position: 0, conditions: [{ type: 'ip_is_private', values: [] }] },
      { id: 'pool-rule-2', name: '广告阻断', enabled: false, action: 'block', position: 1, conditions: [{ type: 'domain_suffix', values: ['ads.example'] }] },
    ],
  }

  it('round-trips multiple ordered rules separated by markers', () => {
    expect(parseRulePoolText(serializeRulePoolText(pool))).toEqual([
      { name: '内网直连', enabled: true, action: 'direct', conditions: [{ type: 'ip_is_private', values: [] }] },
      { name: '广告阻断', enabled: false, action: 'block', conditions: [{ type: 'domain_suffix', values: ['ads.example'] }] },
    ])
  })

  it('allows an empty pool and identifies the failing rule block', () => {
    expect(parseRulePoolText('# empty pool\n')).toEqual([])
    expect(() => parseRulePoolText('---\nname=ok\naction=direct\nip_is_private=true\n---\nname=bad\naction=block')).toThrow('规则 2')
  })
})
