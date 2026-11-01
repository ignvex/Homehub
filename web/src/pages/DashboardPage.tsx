import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { MetricRow, Panel, StatusDot } from '@/components/Surface'
import { toneOf } from '@/lib/tone'
import { dashboardApi, type HealthStatus } from '@/lib/dashboard'
import { balanceApi, type BalanceTarget } from '@/lib/balance'
import { extensionHubApi, type ExtensionHubStatus } from '@/lib/extensionHub'
import { relativeTime } from '@/lib/format'

// ============================================================
// 仪表盘 — 面板式概览
// 面板、度量行、状态灯来自共享组件，本页只负责数据编排与两个专有构件。
// ============================================================

const REFRESH_INTERVAL_MS = 30_000

/** 秒数转「3 天 4 小时」；未就绪时给出占位符。 */
function formatUptime(seconds?: number): string {
  if (seconds === undefined || seconds < 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分`
}

/** 顶部指标格。 */
function StatTile({ label, value, dot, note }: {
  label: string
  value: ReactNode
  dot?: 'ok' | 'error' | 'warn' | 'idle'
  note?: string
}) {
  return <div className="dash-stat">
    <p className="dash-stat-label">{label}</p>
    <div className="dash-stat-value">
      {dot && <StatusDot tone={dot} />}
      <strong>{value}</strong>
    </div>
    {note && <small>{note}</small>}
  </div>
}

// ------------------------------------------------------------
// 图标
// ------------------------------------------------------------

function PulseIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12h4l2.5-6 4 12 2.5-6h5" /></svg>
}

function WalletIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="6" width="18" height="13" rx="1" /><path d="M3 10h18M16 14.5h2" /></svg>
}

function BoltIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M13 3 5 14h6l-1 7 8-11h-6z" /></svg>
}

function LinkIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true" {...strokeProps}><path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7L11.7 6.6" /><path d="M14 10a4 4 0 0 0-5.7 0l-3 3A4 4 0 0 0 11 18.7l1.3-1.3" /></svg>
}

function RefreshIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 5v6h-6" /></svg>
}

const strokeProps = { fill: 'none', stroke: 'currentColor', strokeWidth: 1.7, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const }

const actions: { href: string; title: string; text: string; icon: ReactNode }[] = [
  { href: '/bookmarks', title: '整理书签', text: '工作空间入口', icon: <svg viewBox="0 0 24 24" {...strokeProps}><path d="M6 4.5h12v15l-6-3.7-6 3.7z" /></svg> },
  { href: '/media', title: '管理媒体', text: '背景与封面', icon: <svg viewBox="0 0 24 24" {...strokeProps}><rect x="4" y="5" width="16" height="14" rx="1" /><path d="m5 16 4-4 3 3 2-2 5 5" /><circle cx="15.5" cy="9.5" r="1" /></svg> },
  { href: '/appearance', title: '调整外观', text: '首页和登录页', icon: <svg viewBox="0 0 24 24" {...strokeProps}><circle cx="12" cy="12" r="3" /><path d="M12 3v2m0 14v2M3 12h2m14 0h2M5.6 5.6 7 7m10 10 1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4" /></svg> },
  { href: '/rss', title: '阅读订阅', text: 'RSS 抓取结果', icon: <svg viewBox="0 0 24 24" {...strokeProps}><path d="M5 5a14 14 0 0 1 14 14M5 11a8 8 0 0 1 8 8" /><circle cx="6" cy="18" r="1" /></svg> },
]

export function DashboardPage() {
  const [health, setHealth] = useState<HealthStatus | null>(null)
  const [healthy, setHealthy] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  const [balances, setBalances] = useState<BalanceTarget[]>([])
  const [extension, setExtension] = useState<ExtensionHubStatus | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [, setTick] = useState(0)
  const mountedRef = useRef(true)

  const load = useCallback(async (manual: boolean) => {
    if (manual) setRefreshing(true)
    // Extension 状态是可选的：未启用时接口返回 200 但 enabled=false，
    // 所以这里不会因为“没配”而报错。
    const [healthResult, balanceResult, extensionResult] = await Promise.allSettled([
      dashboardApi.health(),
      balanceApi.list(),
      extensionHubApi.status(),
    ])
    if (!mountedRef.current) return

    if (healthResult.status === 'fulfilled') {
      const status = healthResult.value
      setHealth(status)
      setHealthy(status.status === 'ok' && (!status.db || status.db === 'ok'))
      setError('')
    } else {
      setHealthy(false)
      setError(healthResult.reason instanceof Error ? healthResult.reason.message : '健康检查失败')
    }

    setBalances(balanceResult.status === 'fulfilled' ? balanceResult.value ?? [] : [])
    setExtension(extensionResult.status === 'fulfilled' ? extensionResult.value : null)
    setRefreshing(false)
  }, [])

  useEffect(() => {
    mountedRef.current = true
    void load(false)
    const timer = window.setInterval(() => { void load(false) }, REFRESH_INTERVAL_MS)
    return () => {
      mountedRef.current = false
      window.clearInterval(timer)
    }
  }, [load])

  // 余额卡片的「最后检查」是相对时间，需要自己往前走。
  // 25 秒刷新一次，让「3 分钟前」这类文案不会停在原地。
  useEffect(() => {
    const timer = window.setInterval(() => setTick(value => value + 1), 25_000)
    return () => window.clearInterval(timer)
  }, [])

  const dbTone = health?.db === 'ok' ? 'ok' : health?.db ? 'error' : 'idle'
  const serviceTone = healthy === null ? 'idle' : healthy ? 'ok' : 'error'
  const dbLabel = health?.db === 'ok' ? '已连接' : health?.db ?? '—'
  const serviceLabel = healthy === null ? '检查中' : healthy ? '运行正常' : '连接异常'
  const okTargets = balances.filter(item => item.status === 'ok').length
  // Extension 的四层状态压成一句话：仪表盘上不该给五盏灯。
  const extensionLabel = !extension || !extension.enabled
    ? '未启用'
    : !extension.authenticated
      ? (extension.error_code === 'config' ? '配置不完整' : '签名未通过')
      : extension.ready === false
        ? '依赖未就绪'
        : extension.rss_available
          ? `已连接 · ${extension.subscription_count} 条订阅`
          : '已连接'
  const extensionTone: 'ok' | 'error' | 'warn' | 'idle' = !extension || !extension.enabled
    ? 'idle'
    : !extension.authenticated
      ? 'error'
      : extension.ready === false
        ? 'warn'
        : 'ok'
  const readyLabel = extension?.ready === true ? '就绪' : extension?.ready === false ? '未就绪' : '未探测到'
  const failedTargets = balances.filter(item => item.status === 'error').length

  return <div className="dashboard">
    <section className="dash-stats" aria-label="系统概况">
      <StatTile label="系统状态" value={serviceLabel} dot={serviceTone} note={health?.status ? `health: ${health.status}` : undefined} />
      <StatTile label="数据库" value={dbLabel} dot={dbTone} note="PostgreSQL" />
      <StatTile label="Extension" value={extensionLabel} dot={extensionTone} note={extension?.enabled ? extension.base_url : '未配置'} />
      <StatTile label="运行时长" value={formatUptime(health?.uptime_seconds)} note="自上次启动以来" />
    </section>

    <div className="dash-grid">
      <Panel
        title="服务检查"
        icon={<PulseIcon />}
        action={<a className="table-link" href="/logs">查看日志 →</a>}
        footer={<>
          <span>每 {REFRESH_INTERVAL_MS / 1000} 秒自动刷新</span>
          <button type="button" className="dash-refresh" onClick={() => { void load(true) }} disabled={refreshing}>
            <RefreshIcon />
            {refreshing ? '刷新中' : '立即刷新'}
          </button>
        </>}
      >
        {error
          ? <p className="dash-error" role="alert">{error}</p>
          : <>
            <MetricRow label="HTTP 服务" hint="health endpoint" value={serviceLabel} tone={toneOf(healthy ? 'ok' : healthy === false ? 'error' : 'idle')} />
            <MetricRow label="数据库" hint="连接与查询" value={dbLabel} tone={toneOf(health?.db)} />
            <MetricRow label="整体状态" hint="综合判定" value={health?.status ?? '—'} tone={toneOf(health?.status)} />
            <MetricRow label="启动时长" hint="进程运行时间" value={formatUptime(health?.uptime_seconds)} />
            {extension?.enabled && <MetricRow
              label="Extension 依赖"
              hint="/readyz"
              value={readyLabel}
              tone={toneOf(extension.ready === false ? 'error' : extension.ready === true ? 'ok' : 'idle')}
              note={extension.ready_detail && extension.ready === false ? extension.ready_detail : undefined}
            />}
          </>}
      </Panel>

      <Panel
        title="余额监控"
        icon={<WalletIcon />}
        action={<a className="table-link" href="/balance">查看详情 →</a>}
        footer={balances.length > 0
          ? <span>{balances.length} 个监控点 · {okTargets} 可用{failedTargets > 0 ? ` · ${failedTargets} 异常` : ''}</span>
          : <span>尚未配置监控目标</span>}
      >
        {balances.length === 0
          ? <div className="dash-empty">
            <p>没有可展示的余额监控点。</p>
            <a className="table-link" href="/balance">去配置 →</a>
          </div>
          : balances.map(item => {
            const checked = item.last_checked_at ? relativeTime(item.last_checked_at) : ''
            const amount = typeof item.balance === 'number'
              ? `${item.balance.toFixed(2)} ${item.currency || ''}`.trim()
              : '—'
            return <MetricRow
              key={item.name}
              label={item.name}
              hint={item.type || undefined}
              value={amount}
              tone={toneOf(item.status)}
              note={item.status === 'error' && item.last_error ? item.last_error : checked || undefined}
            />
          })}
      </Panel>
    </div>

    {extension?.enabled && <Panel
      title="Extension 对接"
      icon={<LinkIcon />}
      action={<a className="table-link" href="/extension">管理 →</a>}
      footer={<span>{extension.public_key ? `宿主公钥 ${extension.public_key.slice(0, 12)}…` : '未加载私钥'}</span>}
    >
      <MetricRow label="签名通道" hint="宿主私钥被认可" value={extension.authenticated ? '已通过' : '未通过'}
        tone={toneOf(extension.authenticated ? 'ok' : 'error')}
        note={!extension.authenticated ? extension.error_message : undefined} />
      <MetricRow label="依赖就绪" hint="对端数据库与存储" value={readyLabel}
        tone={toneOf(extension.ready === false ? 'error' : extension.ready === true ? 'ok' : 'idle')}
        note={extension.ready === false ? extension.ready_detail : undefined} />
      <MetricRow label="RSS 模块" hint="对端阅读器开关" value={extension.rss_available ? `已启用 · ${extension.subscription_count} 条订阅` : '未启用'}
        tone={toneOf(extension.rss_available ? 'ok' : 'warn')} />
      <MetricRow label="地址" value={extension.base_url ?? '—'} />
    </Panel>}

    <Panel title="快捷入口" icon={<BoltIcon />}>
      <div className="dash-tiles">
        {actions.map(item => <a className="dash-tile" key={item.href} href={item.href}>
          <span className="dash-tile-icon" aria-hidden="true">{item.icon}</span>
          <span className="dash-tile-copy">
            <strong>{item.title}</strong>
            <small>{item.text}</small>
          </span>
          <span className="dash-tile-arrow" aria-hidden="true">→</span>
        </a>)}
      </div>
    </Panel>
  </div>
}
