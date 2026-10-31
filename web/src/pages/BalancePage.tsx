import { useEffect, useMemo, useState } from 'react'
import { Pie } from 'react-chartjs-2'
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js'
import { Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { balanceApi, type BalanceStatus, type TargetResult } from '@/lib/balance'
import { formatDateTime } from '@/lib/types'

ChartJS.register(ArcElement, Tooltip, Legend)

const typeColors: Record<string, string> = {
  deepseek: '#4F8B89',
}
const fallbackColors = ['#4F8B89', '#E8804F', '#6B8AC4', '#A07BB8', '#C4A35A', '#D4737A']

export function BalancePage() {
  const [items, setItems] = useState<TargetResult[]>([])
  const [status, setStatus] = useState<BalanceStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  async function load() {
    setLoading(true)
    try {
      const [results, stat] = await Promise.all([balanceApi.list(), balanceApi.status()])
      setItems(results); setStatus(stat); setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取余额') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  async function refresh() {
    setBusy(true)
    try { await balanceApi.refresh(); setMessage('已刷新'); await load() }
    catch (cause) { setError(cause instanceof Error ? cause.message : '刷新失败') }
    finally { setBusy(false) }
  }

  const total = useMemo(() => items.reduce((sum, item) => sum + (item.balance || 0), 0), [items])
  const chartData = useMemo(() => ({
    labels: items.length ? items.map(item => item.name) : ['无数据'],
    datasets: [{
      data: items.length ? items.map(item => item.balance || 0) : [1],
      backgroundColor: items.map((item, i) => typeColors[item.type] || fallbackColors[i % fallbackColors.length]),
      borderColor: 'var(--color-surface)',
      borderWidth: 3,
    }],
  }), [items])

  if (loading) return <PageState kind="loading" title="正在读取余额信息" />
  if (error && !items.length) return <PageState kind="error" title="无法读取余额" description={error} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />

  return <div className="business-page">
    <div className="page-toolbar"><div><p>余额监控</p><h2>LLM 余额</h2></div><div className="toolbar-actions">{status && <span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>{status.ok_count}/{status.target_count} 可用 · {status.last_refresh_at ? formatDateTime(status.last_refresh_at) : '未刷新'}</span>}<button className="button button--primary" disabled={busy} onClick={() => void refresh()}>{busy ? '刷新中...' : '刷新余额'}</button></div></div>
    <Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} onDismiss={() => setMessage('')} />
    {!items.length ? <PageState kind="empty" title="没有监控点" description="在 config.json 中配置 balance_monitor.targets 后刷新。" /> :
    <div className="balance-grid">
      <section className="data-panel" style={{ gridColumn: '1 / -1' }}>
        <header className="data-panel-head"><div><h2>数据资产</h2><p>总余额 · 各平台占比</p></div><strong style={{ fontSize: 26, color: 'var(--color-primary)' }}>{total.toFixed(2)}</strong></header>
        <div style={{ display: 'flex', alignItems: 'center', gap: 28, padding: 20, flexWrap: 'wrap' }}>
          <div style={{ width: 200, height: 200, flexShrink: 0 }}>{chartData.labels.length > 0 && <Pie data={chartData} options={{ plugins: { legend: { display: false } }, maintainAspectRatio: false }} />}</div>
          <div style={{ display: 'grid', gap: 8, flex: 1, minWidth: 200 }}>
            {items.map((item, i) => <div key={item.name} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}><span style={{ width: 10, height: 10, borderRadius: 4, background: typeColors[item.type] || fallbackColors[i % fallbackColors.length], flexShrink: 0 }} /><span>{item.name}</span></div>
              <strong>{item.balance?.toFixed(2) ?? '—'} {item.currency || ''}</strong>
            </div>)}
          </div>
        </div>
      </section>
      <section className="data-panel" style={{ gridColumn: '1 / -1' }}>
        <header className="data-panel-head"><div><h2>监控点详情</h2><p>各平台查询状态与剩余额度</p></div></header>
        <div className="table-wrap"><table className="data-table"><thead><tr><th>平台</th><th>类型</th><th>余额</th><th>状态</th><th>最后检查</th></tr></thead><tbody>{items.map(item => <tr key={item.name}><td data-label="平台"><strong>{item.name}</strong><br /><small className="table-subtext">{item.base_url}</small></td><td data-label="类型"><StatusBadge tone="neutral">{item.type}</StatusBadge></td><td data-label="余额"><strong>{item.balance?.toFixed(2) ?? '—'}</strong> {item.currency || ''}</td><td data-label="状态"><StatusBadge tone={item.status === 'ok' ? 'success' : item.status === 'error' ? 'danger' : 'warning'}>{item.status === 'ok' ? '正常' : item.status === 'error' ? item.last_error ? '异常' : '错误' : '查询中'}</StatusBadge></td><td data-label="最后检查">{item.last_checked_at ? formatDateTime(item.last_checked_at) : '—'}</td></tr>)}</tbody></table></div>
      </section>
      <section className="data-panel" style={{ gridColumn: '1 / -1' }}>
        <header className="data-panel-head"><div><h2>配置管理</h2><p>监控点的增删改在 config.json 中手动编辑，支持自定义认证 header</p></div></header>
        <div className="modal-body" style={{ display: 'grid', gap: 16 }}>
          <div style={{ background: 'var(--color-surface-muted)', borderRadius: 6, padding: 16 }}>
            <p style={{ margin: '0 0 12px', fontWeight: 600, fontSize: 13 }}>config.json — balance_monitor 配置示例：</p>
            <pre style={{ margin: 0, padding: 14, background: 'var(--color-input-bg)', borderRadius: 6, fontSize: 12, lineHeight: 1.6, overflow: 'auto', border: '1px solid var(--color-border)' }}>{`{
  "balance_monitor": {
    "enabled": true,
    "refresh_interval_seconds": 3600,
    "targets": [
      {
        "name": "DeepSeek 主号",
        "type": "deepseek",
        "base_url": "https://api.deepseek.com",
        "api_key": "<your-api-key>",
        "enabled": true
      }
    ]
  }
}`}</pre>
          </div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
            <a href="/settings" className="button button--primary" style={{ textDecoration: 'none' }}>打开配置编辑器</a>
            <span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>目前仅支持 DeepSeek。未来按需逐一适配新的供应商。</span>
          </div>
        </div>
      </section>
    </div>}
  </div>
}
