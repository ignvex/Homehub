import { useEffect, useState, type FormEvent } from 'react'
import { pasteApi, type PasteResponse, type PasteSummary } from '@/lib/paste'
import { formatDateTime } from '@/lib/types'

export function PastePage() {
  const pathname = window.location.pathname
  const pasteID = pathname.startsWith('/paste/') ? pathname.slice(7) : null
  const isView = !!pasteID

  const [showCreate, setShowCreate] = useState(false) // always at top — React hooks rule
  const [content, setContent] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<PasteResponse | null>(null)
  const [copied, setCopied] = useState('')
  const [history, setHistory] = useState<PasteSummary[]>([])
  const [paste, setPaste] = useState<PasteResponse | null>(null)
  const [viewLoading, setViewLoading] = useState(true)
  const [viewError, setViewError] = useState('')
  const [deleting, setDeleting] = useState(false)
  const [deleted, setDeleted] = useState(false)

  useEffect(() => { if (!isView) pasteApi.list().then(setHistory).catch(() => {}) }, [isView])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (!content.trim()) return
    setBusy(true); setError('')
    try { const r = await pasteApi.create(content.trim()); setResult(r); setContent(''); pasteApi.list().then(setHistory).catch(() => {}) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '创建失败') }
    finally { setBusy(false) }
  }

  function copy(text: string, label: string) {
    navigator.clipboard.writeText(text).then(() => { setCopied(label); setTimeout(() => setCopied(''), 2000) }).catch(() => setError('复制失败'))
  }

  useEffect(() => {
    if (!pasteID) return
    pasteApi.get(pasteID)
      .then(data => { setPaste(data) })
      .catch(cause => setViewError(cause instanceof Error ? cause.message : '无法读取粘贴'))
      .finally(() => setViewLoading(false))
  }, [pasteID])

  async function remove() {
    setDeleting(true)
    try { await pasteApi.delete(pasteID!); setDeleted(true) }
    catch (cause) { setViewError(cause instanceof Error ? cause.message : '删除失败') }
    finally { setDeleting(false) }
  }

  // ============ 查看模式 ============
  if (isView) {
    if (deleted) return <main style={styles.page}><div style={styles.card}><h1 style={styles.h1}>已删除</h1><p style={styles.p}><a href="/paste" style={styles.link}>返回列表</a></p></div></main>
    if (viewLoading) return <main style={styles.page}><p style={{ color: 'var(--color-text-secondary)' }}>正在加载粘贴内容…</p></main>
    if (!paste || viewError) return <main style={styles.page}><div style={styles.card}><h1 style={styles.h1}>{viewError || '粘贴不存在或已过期'}</h1><p style={styles.p}><a href="/paste" style={styles.link}>返回列表</a></p></div></main>
    return <main style={styles.page}>
      <div style={styles.card}>
        <div style={styles.head}><h1 style={styles.h1}>Pastebin</h1><span style={{ color: 'var(--color-text-secondary)', fontSize: 13 }}>{formatDateTime(paste.created_at)}{paste.expires_at ? ` · 过期于 ${formatDateTime(paste.expires_at)}` : ''}</span></div>
        <pre style={styles.pre}>{paste.content}</pre>
        <div style={styles.actions}>
          <button style={styles.btn} onClick={() => copy(paste.content, '内容')}>{copied === '内容' ? '已复制 ✓' : '复制内容'}</button>
          <button style={styles.btn} onClick={() => copy(`${window.location.origin}/paste/${paste.id}`, '链接')}>{copied === '链接' ? '已复制 ✓' : '复制分享链接'}</button>
          {pasteApi.hasToken(paste.id) && <button style={{ ...styles.btn, ...styles.danger }} disabled={deleting} onClick={() => void remove()}>{deleting ? '删除中...' : '删除'}</button>}
        </div>
        {viewError && <p style={{ color: 'var(--color-error)', fontSize: 13 }}>{viewError}</p>}
      </div>
      <p style={styles.p}><a href="/paste" style={styles.link}>返回列表</a> · <a href="/" style={styles.link}>返回首页</a></p>
    </main>
  }

  // ============ 创建模式（列表优先） ============
  return <main style={styles.page}>
    {history.length > 0 && <div style={styles.card}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ fontSize: 18, margin: 0 }}>最近粘贴</h2>
        <button style={styles.btn} onClick={() => { setShowCreate(v => !v); setResult(null) }}>{showCreate ? '收起创建区' : '创建新粘贴'}</button>
      </div>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
        <thead><tr style={{ borderBottom: '1px solid var(--color-border)' }}><th style={{ textAlign: 'left', padding: '8px 12px', color: 'var(--color-text-secondary)', fontWeight: 600, fontSize: 11 }}>内容预览</th><th style={{ textAlign: 'left', padding: '8px 12px', color: 'var(--color-text-secondary)', fontWeight: 600, fontSize: 11, width: 140 }}>创建时间</th><th style={{ textAlign: 'left', padding: '8px 12px', color: 'var(--color-text-secondary)', fontWeight: 600, fontSize: 11, width: 140 }}>过期时间</th><th style={{ width: 60 }} /></tr></thead>
        <tbody>{history.map(item => <tr key={item.id} style={{ borderBottom: '1px solid color-mix(in srgb, var(--color-border) 50%, transparent)' }}><td style={{ padding: '8px 12px' }}><a href={`/paste/${item.id}`} style={{ color: 'var(--color-link)', textDecoration: 'none', overflow: 'hidden', display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', wordBreak: 'break-all' }}>{item.content_preview || '(空)'}</a></td><td style={{ padding: '8px 12px', color: 'var(--color-text-secondary)', fontSize: 12 }}>{formatDateTime(item.created_at)}</td><td style={{ padding: '8px 12px', color: 'var(--color-text-secondary)', fontSize: 12 }}>{formatDateTime(item.expires_at)}</td><td style={{ padding: '8px 12px' }}><a href={`/paste/${item.id}`} style={{ color: 'var(--color-link)', textDecoration: 'none', fontSize: 12 }}>查看</a></td></tr>)}</tbody>
      </table>
    </div>}
    {showCreate && <div style={{ ...styles.card, marginTop: history.length > 0 ? 24 : 0 }}>
      {error && <p style={{ color: 'var(--color-error)', fontSize: 13 }}>{error}</p>}
      {result ? <div>
        <h2 style={{ fontSize: 18, margin: '0 0 8px', color: 'var(--color-primary)' }}>粘贴已创建</h2>
        <div style={{ display: 'grid', gap: 8, marginBottom: 16 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}><input style={styles.input} value={`${window.location.origin}/paste/${result.id}`} readOnly /><button style={styles.btn} onClick={() => copy(`${window.location.origin}/paste/${result.id}`, '分享链接')}>{copied === '分享链接' ? '已复制 ✓' : '复制'}</button></div>
        </div>
        <p style={styles.p}><a href={`/paste/${result.id}`} style={styles.link}>查看粘贴</a> · <button onClick={() => setResult(null)} style={{ ...styles.btn, marginLeft: 12 }}>创建新的</button></p>
      </div> : <form onSubmit={create}>
        <textarea value={content} onChange={event => setContent(event.target.value)} placeholder="在此粘贴文本内容..." style={{ ...styles.pre, minHeight: 200, resize: 'vertical', marginBottom: 16, width: '100%', boxSizing: 'border-box', border: '1px solid var(--color-border)' }} required />
        <button style={styles.btn} disabled={busy || !content.trim()}>{busy ? '创建中...' : '创建粘贴'}</button>
      </form>}
    </div>}
    {!history.length && !showCreate && <div style={styles.card}><p style={styles.p}>还没有粘贴记录，点击创建新粘贴开始。</p><button style={styles.btn} onClick={() => setShowCreate(true)}>创建新粘贴</button></div>}
    <p style={styles.p}><a href="/" style={styles.link}>返回首页</a></p>
  </main>
}

const styles: Record<string, React.CSSProperties> = {
  page: { minHeight: '100vh', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 40, background: 'var(--color-bg)', color: 'var(--color-text)' },
  card: { width: 'min(720px, 100%)', background: 'var(--color-surface)', border: '1px solid var(--color-border)', borderRadius: 16, padding: 32, boxShadow: 'var(--shadow-md)' },
  h1: { margin: '0 0 8px', fontSize: 28 },
  p: { color: 'var(--color-text-secondary)', margin: '8px 0 20px', fontSize: 14 },
  pre: { background: 'var(--color-surface-muted)', color: 'var(--color-text)', padding: 16, borderRadius: 10, fontSize: 14, lineHeight: 1.6, overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all' },
  head: { display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: 8, marginBottom: 16 },
  actions: { display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 16 },
  btn: { padding: '10px 20px', border: '1px solid var(--color-border)', borderRadius: 8, background: 'var(--color-surface)', color: 'var(--color-text)', cursor: 'pointer', fontWeight: 650, fontSize: 14, transition: 'background .15s' },
  danger: { borderColor: 'var(--color-error)', color: 'var(--color-error)' },
  input: { flex: 1, minWidth: 200, padding: '10px 14px', border: '1px solid var(--color-border)', borderRadius: 8, background: 'var(--color-input-bg)', color: 'var(--color-text)', fontSize: 13 },
  link: { color: 'var(--color-link)', textDecoration: 'none' },
}
