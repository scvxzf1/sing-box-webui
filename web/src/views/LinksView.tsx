import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, ArrowDown, ArrowUp, ArrowUpDown, Download, Eraser, Search, Upload, Waypoints } from 'lucide-react'
import { clearLinks, listLinks } from '../api/client'
import type { Link } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

type SortColumn = 'host' | 'node' | 'download' | 'upload' | 'downloadRate' | 'uploadRate'

interface SortRule {
  column: SortColumn
  desc: boolean
}

const columns: Array<{ key: SortColumn; label: string; numeric?: boolean }> = [
  { key: 'host', label: '主机' },
  { key: 'download', label: '下载量', numeric: true },
  { key: 'upload', label: '上传量', numeric: true },
  { key: 'downloadRate', label: '下载速度', numeric: true },
  { key: 'uploadRate', label: '上传速度', numeric: true },
  { key: 'node', label: '代理节点' },
]

const MAX_CACHE = 1000

export function LinksView() {
  const queryClient = useQueryClient()
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [activeOnly, setActiveOnly] = useState(false)
  const [sortRules, setSortRules] = useState<SortRule[]>([{ column: 'downloadRate', desc: true }])

  // Debounce the search term so polling doesn't fire on every keystroke.
  useEffect(() => {
    const handle = window.setTimeout(() => setSearch(searchInput.trim()), 250)
    return () => window.clearTimeout(handle)
  }, [searchInput])

  const sortParam = useMemo(
    () => sortRules.map((rule) => `${rule.desc ? '-' : ''}${rule.column}`).join(','),
    [sortRules],
  )

  const linksQuery = useQuery({
    queryKey: ['links', search, activeOnly, sortParam],
    queryFn: ({ signal }) =>
      listLinks(
        {
          search: search || undefined,
          active: activeOnly ? true : undefined,
          sort: sortParam || undefined,
          limit: MAX_CACHE,
        },
        signal,
      ),
    refetchInterval: 1_500,
    placeholderData: (previous) => previous,
  })

  const clearMutation = useMutation({
    mutationFn: clearLinks,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['links'] }),
  })

  const toggleSort = (column: SortColumn, additive: boolean) => {
    setSortRules((current) => {
      const existingIndex = current.findIndex((rule) => rule.column === column)
      if (!additive) {
        // Single-column sort: toggle direction if already the only/primary rule.
        if (existingIndex === 0 && current.length === 1) {
          return [{ column, desc: !current[0].desc }]
        }
        return [{ column, desc: true }]
      }
      // Multi-column (shift): toggle in place, or append.
      if (existingIndex >= 0) {
        const next = current.slice()
        next[existingIndex] = { column, desc: !next[existingIndex].desc }
        return next
      }
      return [...current, { column, desc: true }]
    })
  }

  const snapshot = linksQuery.data
  const links = snapshot?.links ?? []

  return (
    <>
      <PageHeading
        eyebrow="CONNECTIONS"
        title="链接状态"
        action={
          <button
            className="button"
            type="button"
            disabled={clearMutation.isPending || links.length === 0}
            onClick={() => clearMutation.mutate()}
          >
            <Eraser size={16} />
            清空记录
          </button>
        }
      />
      {(linksQuery.error || clearMutation.error) && <InlineError error={linksQuery.error ?? clearMutation.error} />}

      <section className="traffic-status-band" aria-label="链接统计">
        <div className={`traffic-state traffic-state--${snapshot?.running ? 'active' : 'idle'}`}>
          <span className="status-dot" />
          <div>
            <span>监控状态</span>
            <strong>{snapshot?.running ? '运行中' : '未运行'}</strong>
          </div>
        </div>
        <Metric icon={Activity} label="活跃连接" value={`${snapshot?.stats.active ?? 0}`} />
        <Metric icon={Download} label="总下载速率" value={formatRate(snapshot?.stats.downloadRate ?? 0)} />
        <Metric icon={Upload} label="总上传速率" value={formatRate(snapshot?.stats.uploadRate ?? 0)} />
        <Metric icon={Waypoints} label="缓存链接" value={`${snapshot?.stats.total ?? 0} / ${snapshot?.stats.trackedCapacity ?? MAX_CACHE}`} />
      </section>

      <div className="links-toolbar">
        <label className="links-search">
          <Search size={16} aria-hidden="true" />
          <input
            type="search"
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            placeholder="搜索主机 / 节点 / 网络…"
            aria-label="搜索链接"
          />
        </label>
        <label className="links-active-toggle">
          <input type="checkbox" checked={activeOnly} onChange={(event) => setActiveOnly(event.target.checked)} />
          <span>仅看活跃</span>
        </label>
        {sortRules.length > 0 && (
          <div className="links-sort-summary" aria-live="polite">
            <ArrowUpDown size={14} aria-hidden="true" />
            {sortRules.map((rule) => (
              <span className="links-sort-chip" key={rule.column}>
                {columnLabel(rule.column)}
                {rule.desc ? <ArrowDown size={12} aria-hidden="true" /> : <ArrowUp size={12} aria-hidden="true" />}
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="links-table-wrap panel">
        {links.length === 0 ? (
          <div className="empty-state">
            {snapshot?.running
              ? search || activeOnly
                ? '没有匹配的链接'
                : '暂无链接——通过代理产生流量后会显示在这里'
              : '代理未运行。请先在「连接」页启动代理。'}
          </div>
        ) : (
          <table className="links-table">
            <thead>
              <tr>
                {columns.map((column) => (
                  <SortHeader
                    key={column.key}
                    column={column.key}
                    label={column.label}
                    numeric={column.numeric}
                    rules={sortRules}
                    onToggle={toggleSort}
                  />
                ))}
              </tr>
            </thead>
            <tbody>
              {links.map((link) => (
                <LinkRow key={link.id} link={link} />
              ))}
            </tbody>
          </table>
        )}
      </div>
      <p className="links-hint">提示：点击列头排序，按住 Shift 点击可叠加多列排序。缓存上限 {MAX_CACHE} 条，超出后自动移除最旧的记录。</p>
    </>
  )
}

function Metric({ icon: Icon, label, value }: { icon: typeof Activity; label: string; value: string }) {
  return (
    <div className="traffic-metric">
      <Icon size={18} />
      <div>
        <span>{label}</span>
        <strong>{value}</strong>
      </div>
    </div>
  )
}

interface SortHeaderProps {
  column: SortColumn
  label: string
  numeric?: boolean
  rules: SortRule[]
  onToggle: (column: SortColumn, additive: boolean) => void
}

function SortHeader({ column, label, numeric, rules, onToggle }: SortHeaderProps) {
  const index = rules.findIndex((rule) => rule.column === column)
  const rule = index >= 0 ? rules[index] : undefined
  return (
    <th className={numeric ? 'links-th--numeric' : undefined} aria-sort={rule ? (rule.desc ? 'descending' : 'ascending') : 'none'}>
      <button
        type="button"
        className={`links-sort-button ${rule ? 'links-sort-button--active' : ''}`}
        onClick={(event) => onToggle(column, event.shiftKey)}
        title="点击排序，Shift+点击叠加多列"
      >
        <span>{label}</span>
        {rule ? (
          <span className="links-sort-indicator">
            {rule.desc ? <ArrowDown size={13} aria-hidden="true" /> : <ArrowUp size={13} aria-hidden="true" />}
            {rules.length > 1 && <em>{index + 1}</em>}
          </span>
        ) : (
          <ArrowUpDown size={13} aria-hidden="true" className="links-sort-idle" />
        )}
      </button>
    </th>
  )
}

function LinkRow({ link }: { link: Link }) {
  return (
    <tr className={link.active ? undefined : 'links-row--closed'}>
      <td className="links-cell-host" title={link.host}>
        <span className={`links-live-dot ${link.active ? 'links-live-dot--on' : ''}`} aria-hidden="true" />
        <span className="links-host-text">{link.host}</span>
      </td>
      <td className="links-cell-num">{formatBytes(link.download)}</td>
      <td className="links-cell-num">{formatBytes(link.upload)}</td>
      <td className="links-cell-num links-cell-rate">{link.active ? formatRate(link.downloadRate) : '—'}</td>
      <td className="links-cell-num links-cell-rate">{link.active ? formatRate(link.uploadRate) : '—'}</td>
      <td className="links-cell-node">
        <span className={`links-node-badge links-node-badge--${nodeKind(link.node)}`}>{nodeLabel(link.node)}</span>
      </td>
    </tr>
  )
}

function columnLabel(column: SortColumn): string {
  return columns.find((entry) => entry.key === column)?.label ?? column
}

function nodeKind(node: string): 'direct' | 'block' | 'proxy' {
  if (node === 'direct') return 'direct'
  if (node === 'block') return 'block'
  return 'proxy'
}

function nodeLabel(node: string): string {
  if (node === 'direct') return '直连'
  if (node === 'block') return '拦截'
  return node
}

function formatBytes(value: number): string {
  if (value >= 1 << 30) return `${(value / (1 << 30)).toFixed(2)} GiB`
  if (value >= 1 << 20) return `${(value / (1 << 20)).toFixed(1)} MiB`
  if (value >= 1 << 10) return `${(value / (1 << 10)).toFixed(1)} KiB`
  return `${value} B`
}

function formatRate(value: number): string {
  if (value < 1) return '0 B/s'
  return `${formatBytes(Math.round(value))}/s`
}
