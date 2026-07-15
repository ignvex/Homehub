import { useEffect, useState } from 'react'
import { dashboardApi, type CrawlerStats } from '@/lib/dashboard'
import { balanceApi, type TargetResult } from '@/lib/balance'

const labels = { video: '视频', video_page: '分P', article: '专栏', user: 'UP 主' }
const actions: [string, string, string][] = [
  ['/bookmarks', '整理书签', '维护工作空间入口'],
  ['/media', '管理媒体', '上传背景与封面'],
  ['/appearance', '调整外观', '配置首页和登录页'],
  ['/bilibili', '查看采集', '检查爬虫状态'],
]

export function DashboardPage() {
  const [stats, setStats] = useState<CrawlerStats | null>(null)
  const [healthy, setHealthy] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  const [balances, setBalances] = useState<TargetResult[]>([])

  useEffect(() => {
    balanceApi.list().then(setBalances).catch(() => {})
    Promise.allSettled([dashboardApi.health(), dashboardApi.stats()]).then(([health, crawler]) => {
      setHealthy(health.status === 'fulfilled' && health.value.status === 'ok' && (!health.value.db || health.value.db === 'ok'))
      if (crawler.status === 'fulfilled') setStats(crawler.value)
      else setError(crawler.reason instanceof Error ? crawler.reason.message : '统计数据暂不可用')
    })
  }, [])

  const tables = stats ? [stats.video, stats.video_page, stats.article, stats.user] : []
  return (
    <div className="dashboard">
      <section className="dashboard-status">
        <div>
          <span className={`status-dot ${healthy ? 'is-ok' : healthy === false ? 'is-error' : ''}`} />
          <p>系统状态</p>
          <strong>{healthy === null ? '检查中' : healthy ? '运行正常' : '连接异常'}</strong>
        </div>
        <div>
          <p>归档占用</p>
          <strong>{stats?.total_mb ?? '—'}</strong>
          <span>预估数据库空间</span>
        </div>
        <div>
          <p>采集记录</p>
          <strong>{stats ? tables.reduce((sum, item) => sum + item.row_count, 0).toLocaleString() : '—'}</strong>
          <span>跨四个数据表</span>
        </div>
      </section>

      <div className="dashboard-grid">
        <section className="work-panel">
          <header>
            <div>
              <p>DATA INVENTORY</p>
              <h2>数据资产</h2>
            </div>
            <a href="/bilibili">查看采集 →</a>
          </header>
          {error ? <div className="panel-error">{error}</div> : (
            <div className="metric-table">
              {tables.length ? tables.map(item => (
                <div key={item.table}>
                  <span>{labels[item.table as keyof typeof labels] || item.table}</span>
                  <strong>{item.row_count.toLocaleString()}</strong>
                  <small>{item.est_mb}</small>
                </div>
              )) : <div className="panel-loading">正在读取统计…</div>}
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