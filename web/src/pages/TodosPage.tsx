import { useEffect, useState, type FormEvent } from 'react'
import { DeleteButton } from '@/components/Icon'
import { ConfirmDialog, Modal, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useQueryState } from '@/hooks/useQueryState'
import { useAuth } from '@/lib/auth'
import { CONTENT_WRITE_LEVEL, hasLevel } from '@/lib/permissions'
import { todosApi, todoStatuses, type Todo, type TodoSort, type TodoStatus } from '@/lib/todos'
import { formatDateTime } from '@/lib/types'

/** priority 以字符串保存草稿，避免清空输入框时 Number('') 变成 0 被静默提交。 */
const emptyDraft = { title: '', description: '', priority: '0', due_date: '' }
const statusTone: Record<TodoStatus, 'neutral' | 'success' | 'warning' | 'info'> = { planned: 'neutral', pending: 'warning', in_progress: 'info', completed: 'success', paused: 'neutral' }
const sortValues: TodoSort[] = ['priority', 'due_date', 'created_at']

export function TodosPage() {
  const { auth } = useAuth()
  const canWrite = hasLevel(auth, CONTENT_WRITE_LEVEL)
  const notice = useNotice()
  const [items, setItems] = useState<Todo[]>([])
  const [statusParam, setStatus] = useQueryState('status', '')
  const [sortParam, setSort] = useQueryState('sort', 'priority')
  const status = (todoStatuses.some(item => item.value === statusParam) ? statusParam : '') as TodoStatus | ''
  const sort = (sortValues.includes(sortParam as TodoSort) ? sortParam : 'priority') as TodoSort
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState<(typeof emptyDraft & { id?: number; status?: TodoStatus }) | null>(null)
  const [pendingDelete, setPendingDelete] = useState<Todo | null>(null)

  async function load(silent = false) {
    if (!silent) setLoading(true)
    try { setItems(await todosApi.list(status, sort)); setLoadError('') }
    catch (cause) { const message = errorMessage(cause, '无法读取待办事项'); if (silent) notice.error(message); else setLoadError(message) }
    finally { setLoading(false) }
  }
  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void load(items.length > 0) }, [status, sort])

  async function run(action: () => Promise<unknown>, success: string, inModal = false) {
    setBusy(true); setFormError('')
    try { await action(); notice.success(success); await load(true); return true }
    catch (cause) { const message = errorMessage(cause, '操作失败'); if (inModal) setFormError(message); else notice.error(message); return false }
    finally { setBusy(false) }
  }
  function closeModals() { setDraft(null); setPendingDelete(null); setFormError('') }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!draft) return
    const priorityText = draft.priority.trim()
    const priority = Number(priorityText)
    if (!priorityText || !Number.isInteger(priority)) { setFormError('优先级需要填写整数'); return }
    const body = { title: draft.title.trim(), description: draft.description.trim(), priority, ...(draft.due_date ? { due_date: new Date(`${draft.due_date}T00:00:00`).toISOString() } : {}) }
    const saved = await run(() => draft.id ? todosApi.update(draft.id, body) : todosApi.create(body), draft.id ? '待办已更新' : '待办已创建', true)
    if (saved) closeModals()
  }
  async function remove() { if (!pendingDelete) return; const saved = await run(() => todosApi.delete(pendingDelete.id), '待办已删除', true); if (saved) closeModals() }
  async function move(index: number, offset: number) { const ordered = [...items]; const target = index + offset; if (target < 0 || target >= ordered.length) return; [ordered[index], ordered[target]] = [ordered[target], ordered[index]]; await run(() => todosApi.reorder(ordered.map(item => item.id)), '待办顺序已更新') }

  if (loading && !items.length) return <PageState kind="loading" title="正在读取待办事项" />
  if (loadError && !items.length) return <PageState kind="error" title="无法读取待办事项" description={loadError} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />
  return <div className="business-page">
    {canWrite && <div className="page-toolbar page-toolbar--end"><button className="button button--primary" onClick={() => setDraft({ ...emptyDraft })}>新建待办</button></div>}
    {notice.element}
    <div className="filter-bar"><select aria-label="状态筛选" value={status} onChange={event => setStatus(event.target.value)}><option value="">全部状态</option>{todoStatuses.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select><select aria-label="排序方式" value={sort} onChange={event => setSort(event.target.value)}><option value="priority">按优先级</option><option value="due_date">按截止日期</option><option value="created_at">按创建时间</option></select><button className="button button--quiet" onClick={() => void load(true)} disabled={loading}>刷新</button></div>
    {!items.length ? <PageState kind="empty" title="没有符合条件的待办" description={canWrite ? '新建一项任务，或调整筛选条件。' : '调整筛选条件试试。'} action={canWrite ? <button className="button button--primary" onClick={() => setDraft({ ...emptyDraft })}>新建待办</button> : undefined} /> : <div className="table-wrap"><table className="data-table"><thead><tr><th>任务</th><th>状态</th><th>优先级</th><th>截止日期</th>{canWrite && <th>操作</th>}</tr></thead><tbody>{items.map((item, index) => <tr key={item.id}>
      <td data-label="任务"><strong>{item.title}</strong>{item.description && <p className="table-subtext">{item.description}</p>}</td>
      <td data-label="状态">{canWrite && <select value={item.status} disabled={busy} onChange={event => void run(() => todosApi.updateStatus(item.id, event.target.value as TodoStatus), '任务状态已更新')} aria-label={`${item.title}的状态`}>{todoStatuses.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>}<StatusBadge tone={statusTone[item.status]}>{todoStatuses.find(option => option.value === item.status)?.label}</StatusBadge></td>
      <td data-label="优先级">{item.priority}</td>
      <td data-label="截止日期">{item.due_date ? formatDateTime(item.due_date) : '未设置'}</td>
      {canWrite && <td data-label="操作"><div className="table-actions"><button className="icon-button" disabled={busy || index === 0} onClick={() => void move(index, -1)} aria-label={`上移待办“${item.title}”`}>↑</button><button className="icon-button" disabled={busy || index === items.length - 1} onClick={() => void move(index, 1)} aria-label={`下移待办“${item.title}”`}>↓</button><button className="button button--quiet" onClick={() => setDraft({ id: item.id, title: item.title, description: item.description ?? '', priority: String(item.priority), due_date: item.due_date?.slice(0, 10) ?? '', status: item.status })}>编辑</button><DeleteButton label={`删除待办“${item.title}”`} onClick={() => setPendingDelete(item)} /></div></td>}
    </tr>)}</tbody></table></div>}
    <Modal open={draft !== null} title={draft?.id ? '编辑待办' : '新建待办'} description={draft?.id && !draft.due_date ? '当前接口无法可靠清空已存在的截止日期；留空时保持原值。' : undefined} error={formError} busy={busy} onClose={closeModals} footer={<><button className="button button--quiet" type="button" onClick={closeModals} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="todo-form" disabled={busy}>{busy ? '保存中…' : '保存'}</button></>}><form id="todo-form" className="form-grid" onSubmit={save}><label className="form-field form-field--wide">标题<input autoFocus value={draft?.title ?? ''} onChange={event => setDraft(value => value ? { ...value, title: event.target.value } : value)} required maxLength={500} /></label><label className="form-field form-field--wide">说明<textarea value={draft?.description ?? ''} onChange={event => setDraft(value => value ? { ...value, description: event.target.value } : value)} /></label><label className="form-field">优先级<input type="number" step={1} required value={draft?.priority ?? ''} onChange={event => setDraft(value => value ? { ...value, priority: event.target.value } : value)} /></label><label className="form-field">截止日期<input type="date" value={draft?.due_date ?? ''} onChange={event => setDraft(value => value ? { ...value, due_date: event.target.value } : value)} /></label></form></Modal>
    <ConfirmDialog open={pendingDelete !== null} title="删除待办" description={`将删除“${pendingDelete?.title ?? ''}”，删除后无法恢复。`} confirmLabel="删除" danger busy={busy} error={formError} onClose={closeModals} onConfirm={() => void remove()} />
  </div>
}
