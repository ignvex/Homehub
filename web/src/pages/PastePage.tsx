import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ConfirmDialog, PageState } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { copyText } from '@/lib/clipboard'
import { useLocation } from '@/lib/navigation'
import { pasteApi, type PasteResponse, type PasteSummary } from '@/lib/paste'
import { formatDateTime } from '@/lib/types'

const shareURL = (id: string) => `${window.location.origin}/paste/${id}`

export function PastePage() {
  const { pathname } = useLocation()
  const pasteID = pathname.startsWith('/paste/') ? pathname.slice(7) : null
  return <main className="paste-page">
    <div className="paste-column">
      {pasteID ? <PasteView key={pasteID} id={pasteID} /> : <PasteHome />}
      <p className="paste-links">{pasteID && <><a href="/paste">返回列表</a> · </>}<a href="/">返回首页</a></p>
    </div>
  </main>
}

function PasteView({ id }: { id: string }) {
  const notice = useNotice()
  const [paste, setPaste] = useState<PasteResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const [deleted, setDeleted] = useState(false)

  const load = useCallback(() => {
    setLoading(true); setLoadError('')
    pasteApi.get(id)
      .then(setPaste)
      .catch(cause => setLoadError(errorMessage(cause, '无法读取粘贴')))
      .finally(() => setLoading(false))
  }, [id])
  useEffect(load, [load])

  async function copy(text: string, label: string) {
    if (await copyText(text)) notice.success(`${label}已复制`)
    else notice.error('复制失败')
  }

  async function remove() {
    setDeleting(true); setDeleteError('')
    try { await pasteApi.delete(id); setConfirming(false); setDeleted(true) }
    catch (cause) { setDeleteError(errorMessage(cause, '删除失败')) }
    finally { setDeleting(false) }
  }

  if (deleted) return <section className="data-panel data-panel-body paste-card"><h1>已删除</h1><p className="paste-muted">这条粘贴已被删除。</p></section>
  if (loading) return <PageState kind="loading" title="正在加载粘贴内容" />
  if (loadError || !paste) return <PageState kind="error" title="无法读取粘贴" description={loadError || '粘贴不存在或已过期'} action={<button type="button" className="button button--primary" onClick={load}>重试</button>} />

  return <section className="data-panel data-panel-body paste-card">
    <header className="paste-head"><h1>Pastebin</h1><span className="paste-muted">{formatDateTime(paste.created_at)}{paste.expires_at ? ` · 过期于 ${formatDateTime(paste.expires_at)}` : ''}</span></header>
    {notice.element}
    <pre className="paste-content">{paste.content}</pre>
    <div className="form-actions">
      <button type="button" className="button button--quiet" onClick={() => void copy(paste.content, '内容')}>复制内容</button>
      <button type="button" className="button button--quiet" onClick={() => void copy(shareURL(paste.id), '分享链接')}>复制分享链接</button>
      {pasteApi.hasToken(paste.id) && <button type="button" className="button button--danger" onClick={() => setConfirming(true)}>删除</button>}
    </div>
    <ConfirmDialog open={confirming} title="删除粘贴" description="删除后分享链接将立即失效，内容无法恢复。" confirmLabel="删除" danger busy={deleting} error={deleteError} onClose={() => { setConfirming(false); setDeleteError('') }} onConfirm={() => void remove()} />
  </section>
}

function PasteHome() {
  const notice = useNotice()
  const [history, setHistory] = useState<PasteSummary[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)
  const [historyError, setHistoryError] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [content, setContent] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<PasteResponse | null>(null)

  const loadHistory = useCallback(async () => {
    try { setHistory(await pasteApi.list()); setHistoryError('') }
    catch (cause) { setHistoryError(errorMessage(cause, '无法读取粘贴记录')) }
    finally { setHistoryLoading(false) }
  }, [])
  useEffect(() => { void loadHistory() }, [loadHistory])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (!content.trim()) return
    setBusy(true)
    try { setResult(await pasteApi.create(content.trim())); setContent(''); notice.success('粘贴已创建'); void loadHistory() }
    catch (cause) { notice.error(errorMessage(cause, '创建失败')) }
    finally { setBusy(false) }
  }

  async function copy(text: string) {
    if (await copyText(text)) notice.success('分享链接已复制')
    else notice.error('复制失败')
  }

  const createPanel = <section className="data-panel data-panel-body paste-card">
    {result ? <>
      <h2>粘贴已创建</h2>
      <div className="paste-share"><input aria-label="分享链接" value={shareURL(result.id)} readOnly /><button type="button" className="button button--quiet" onClick={() => void copy(shareURL(result.id))}>复制</button></div>
      <div className="form-actions"><a className="button button--quiet" href={`/paste/${result.id}`}>查看粘贴</a><button type="button" className="button button--primary" onClick={() => setResult(null)}>创建新的</button></div>
    </> : <form className="paste-form" onSubmit={create}>
      <label className="form-field">粘贴内容<textarea className="code-textarea code-textarea--wrap" value={content} onChange={event => setContent(event.target.value)} placeholder="在此粘贴文本内容…" required /></label>
      <div className="form-actions"><button type="submit" className="button button--primary" disabled={busy || !content.trim()}>{busy ? '创建中…' : '创建粘贴'}</button></div>
    </form>}
  </section>

  return <>
    {notice.element}
    {historyLoading ? <PageState kind="loading" title="正在读取粘贴记录" />
      : historyError ? <PageState kind="error" title="无法读取粘贴记录" description={historyError} action={<button type="button" className="button button--primary" onClick={() => { setHistoryLoading(true); void loadHistory() }}>重试</button>} />
      : history.length > 0 ? <section className="data-panel">
        <header className="data-panel-head"><div><h2>最近粘贴</h2></div><button type="button" className="button button--quiet" onClick={() => { setShowCreate(value => !value); setResult(null) }} aria-expanded={showCreate}>{showCreate ? '收起创建区' : '创建新粘贴'}</button></header>
        <div className="table-wrap"><table className="data-table"><thead><tr><th>内容预览</th><th>创建时间</th><th>过期时间</th><th>操作</th></tr></thead><tbody>{history.map(item => <tr key={item.id}>
          <td data-label="内容预览"><a className="table-link paste-preview" href={`/paste/${item.id}`}>{item.content_preview || '(空)'}</a></td>
          <td data-label="创建时间">{formatDateTime(item.created_at)}</td>
          <td data-label="过期时间">{formatDateTime(item.expires_at)}</td>
          <td data-label="操作"><a className="table-link" href={`/paste/${item.id}`}>查看</a></td>
        </tr>)}</tbody></table></div>
      </section>
      : !showCreate && <PageState kind="empty" title="还没有粘贴记录" description="创建一条粘贴，获得可分享的链接。" action={<button type="button" className="button button--primary" onClick={() => setShowCreate(true)}>创建新粘贴</button>} />}
    {showCreate && createPanel}
  </>
}
