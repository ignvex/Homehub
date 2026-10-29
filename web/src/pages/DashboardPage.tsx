import { useEffect, useState } from 'react'
import { dashboardApi, type HealthStatus } from '@/lib/dashboard'
import { balanceApi, type TargetResult } from '@/lib/balance'

function formatUptime(seconds?: number): string {
  if (seconds === undefined || seconds < 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分`
}

const actions: [string, string, string][] = [
  ['/bookmarks', '整理书签', '维护工作空间入口'],
  ['/media', '管理媒体', '上传背景与封面'],
  ['/appearance', '调整外观', '配置首页和登录页'],
  ['/rss', '阅读订阅', '查看 RSS 抓取结果'],
]

export function DashboardPage() {
  const [health, setHealth] = useState<HealthStatus | null>(null)
  const [healthy, setHealthy] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  const [balances, setBalances] = useState<TargetResult[]>([])

  useEffect(() => {
    balanceApi.list().then(setBalances).catch(() => {})
    dashboardApi.health()
      .then(status => {
        setHealth(status)
        setHealthy(status.status === 'ok' && (!status.db || status.db === 'ok'))
      })
      .catch((cause: unknown) => {
        setHealthy(false)
        setError(cause instanceof Error ? cause.message : '健康检查失败')
      })
  }, [])

  return (
    <div className="dashboard">
      <section className="dashboard-status">
        <div>
          <span className={`status-dot ${healthy ? 'is-ok' : healthy === false ? 'is-error' : ''}`} />
          <p>系统状态</p>
          <strong>{healthy === null ? '检查中' : healthy ? '运行正常' : '连接异常'}</strong>
        </div>
        <div>
          <p>数据库</p>
          <strong>{health?.db === 'ok' ? '已连接' : health?.db ? health.db : '—'}</strong>
          <span>PostgreSQL 连接状态</span>
        </div>
        <div>
          <p>运行时长</p>
          <strong>{formatUptime(health?.uptime_seconds)}</strong>
          <span>自上次启动以来</span>
        </div>
      </section>

      <div className="dashboard-grid">
        <section className="work-panel">
          <header>
            <div>
              <p>SERVICE HEALTH</p>
              <h2>服务检查</h2>
            </div>
            <a href="/logs">查看日志 →</a>
          </header>
          {error ? <div className="panel-error">{error}</div> : (
            <div className="metric-table">
              <div>
                <span>HTTP 服务</span>
                <strong>{healthy === null ? '—' : healthy ? '正常' : '异常'}</strong>
                <small>Health endpoint</small>
              </div>
              <div>
                <span>数据库</span>
                <strong>{health?.db ?? '—'}</strong>
                <small>连接与查询</small>
              </div>
              <div>
                <span>整体状态</span>
                <strong>{health?.status ?? '—'}</strong>
                <small>综合判定</small>
              </div>
            </div>
          )}
        </section>

        {balances.length > 0 && <section className="work-panel">
          <header>
            <div>
              <p>BALANCE</p>
              <h2>余额监控</h2>
            </div>
            <a href="/balance">查看详情 →</a>
          </header>
          <div className="metric-table">
            {balances.map(b => (
              <div key={b.name}>
                <span>{b.name}</span>
                <strong>{b.balance?.toFixed(2) ?? '—'} {b.currency || ''}</strong>
                <small style={{ color: b.status === 'ok' ? 'var(--color-success)' : b.status === 'error' ? 'var(--color-error)' : 'var(--color-text-secondary)' }}>{b.status === 'ok' ? '可用' : b.status === 'error' ? '异常' : '查询中'}</small>
              </div>
            ))}
          </div>
        </section>}
      </div>

      <section className="work-panel quick-panel">
        <header>
          <div>
            <p>QUICK ACTIONS</p>
            <h2>常用操作</h2>
          </div>
        </header>
        <div className="quick-actions">
          {actions.map(([href, title, text]) => (
            <a key={href} href={href}>
              <strong>{title}</strong>
              <span>{text}</span>
              <b>↗</b>
            </a>
          ))}
        </div>
      </section>
    </div>
  )
}
