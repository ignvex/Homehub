import { useEffect, useState } from 'react'
import { dashboardApi, type CrawlerStats } from '@/lib/dashboard'

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

  useEffect(() => {
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
            <a href="/bilibili">打开控制台 →</a>
          </header>
          {error ? (
            <div className="panel-error">{error}</div>
          ) : (
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

        <section className="work-panel">
          <header>
            <div>
              <p>CRAWLER PROGRESS</p>
              <h2>采集进度</h2>
            </div>
          </header>
          <div className="progress-list">
            {stats?.progress?.length ? stats.progress.map(item => (
              <div key={item.type}>
                <div>
                  <strong>{labels[item.type]}</strong>
                  <span className={item.enabled ? 'is-enabled' : ''}>{item.enabled ? '已启用' : '已停用'}</span>
                </div>
                <div className="progress-track"><i style={{ width: item.progress_pct }} /></div>
                <small>{item.progress_pct} · 窗口 {item.window_base.toLocaleString()}</small>
              </div>
            )) : <div className="panel-loading">暂无进度数据</div>}
          </div>
        </section>
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
