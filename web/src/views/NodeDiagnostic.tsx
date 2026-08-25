import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Gauge, Loader2, MapPin, Play } from 'lucide-react'
import { runConnectivityDiagnostic } from '../api/client'
import type { ConnectivityDiagnosticInput, ConnectivityDiagnosticResult } from '../api/types'
import { InlineError } from '../components/InlineError'

type DiagnosticKind = ConnectivityDiagnosticInput['kind']
type ActiveProxyMode = 'system-proxy' | 'tun'

const providers = {
  quality: [
    { id: '123169', label: '123169', url: 'https://my.123169.xyz/v1/info' },
    { id: 'ippure', label: 'IPPure', url: 'https://my.ippure.com/v1/info' },
  ],
  exit: [
    { id: 'ipify', label: 'ipify.org', url: 'https://api.ipify.org' },
    { id: 'ipsb', label: 'ip.sb', url: 'https://ip.sb' },
    { id: 'ifconfigme', label: 'ifconfig.me', url: 'https://ifconfig.me' },
    { id: 'icanhazip', label: 'icanhazip.com', url: 'https://icanhazip.com' },
    { id: 'ipinfo', label: 'ipinfo.io', url: 'https://ipinfo.io' },
  ],
} as const

export function NodeDiagnostic({ kind, step, mode }: { kind: DiagnosticKind; step: number; mode?: ActiveProxyMode }) {
  const options = providers[kind]
  const [provider, setProvider] = useState<string>(options[0].id)
  const [result, setResult] = useState<ConnectivityDiagnosticResult | null>(null)
  const mutation = useMutation({
    mutationFn: () => runConnectivityDiagnostic({ kind, provider: provider as ConnectivityDiagnosticInput['provider'] }),
    onSuccess: setResult,
  })
  const quality = kind === 'quality'
  const selected = options.find((item) => item.id === provider) ?? options[0]
  return (
    <div className="connection-step">
      <span className="step-index">{step}</span>
      <div className="node-diagnostic">
        <div className="node-diagnostic__head">
          <h2>{quality ? <Gauge size={16} aria-hidden="true" /> : <MapPin size={16} aria-hidden="true" />} {quality ? '节点质量检测' : '节点落地检测'}</h2>
          <button
            className="button button--primary button--sm"
            type="button"
            disabled={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {mutation.isPending ? <Loader2 size={15} className="spin" aria-hidden="true" /> : <Play size={15} aria-hidden="true" />}
            {mutation.isPending ? '检测中…' : '开始检测'}
          </button>
        </div>
        <div className="node-diagnostic__controls">
          <select
            aria-label={quality ? '节点质量检测服务' : '节点落地检测服务'}
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value)
              setResult(null)
              mutation.reset()
            }}
          >
            {options.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
          </select>
          <span title={selected.url}>{selected.url}</span>
        </div>
        {mutation.error && <InlineError error={mutation.error} />}
        {result ? <DiagnosticResult kind={kind} result={result} mode={mode} /> : !mutation.error && <div className="muted-line">尚未检测</div>}
      </div>
    </div>
  )
}

function DiagnosticResult({ kind, result, mode }: { kind: DiagnosticKind; result: ConnectivityDiagnosticResult; mode?: ActiveProxyMode }) {
  const location = [result.countryCode || result.country, result.region, result.city].filter(Boolean).join(' · ')
  const routeLabel = mode === 'tun' ? 'TUN链路' : mode === 'system-proxy' ? '系统代理' : '当前网络'
  return (
    <div className="node-diagnostic__result" aria-live="polite">
      <div className="node-diagnostic__primary">
        <strong>{result.ip}</strong>
        <span>{location || '位置未知'} · {result.latencyMs} ms · {routeLabel}</span>
      </div>
      <div className="node-diagnostic__facts">
        {result.asn && <span><small>ASN</small>{result.asn.startsWith('AS') ? result.asn : `AS${result.asn}`}</span>}
        {result.organization && <span><small>网络</small>{result.organization}</span>}
        {kind === 'quality' && result.fraudScore != null && <span><small>欺诈分</small>{result.fraudScore}</span>}
        {kind === 'quality' && result.residential != null && <span><small>住宅属性</small>{result.residential ? '住宅 IP' : '机房 IP'}</span>}
      </div>
    </div>
  )
}
