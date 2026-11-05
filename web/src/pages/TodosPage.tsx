import { useEffect, useState, type FormEvent } from 'react'
import { DeleteButton } from '@/components/Icon'
import { UserAvatar } from '@/components/UserAvatar'
import { ConfirmDialog, Modal, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useQueryState } from '@/hooks/useQueryState'
import { useAuth } from '@/lib/auth'
import { CONTENT_WRITE_LEVEL, LEVEL_MEMBER, hasLevel } from '@/lib/permissions'
import { todosApi, todoStatuses, type Todo, type TodoOwner, type TodoSort, type TodoStatus, type UserBrief } from '@/lib/todos'
import { formatDateTime } from '@/lib/types'

/** priority 以字符串保存草稿，避免清空输入框时 Number('') 变成 0 被静默提交。 */
const emptyDraft = { title: '', description: '', priority: '0', due_date: '', assignee_id: '' }
const statusTone: Record<TodoStatus, 'neutral' | 'success' | 'warning' | 'info'> = { planned: 'neutral', pending: 'warning', in_progress: 'info', completed: 'success', paused: 'neutral' }
const sortValues: TodoSort[] = ['priority', 'due_date', 'created_at']
const owners: { value: TodoOwner; label: string }[] = [
  { value: '', label: '全部' },
  { value: 'mine', label: '我创建的' },
  { value: 'assigned', label: '指派给我' },
]
const REORDER_BLOCKED_HINT = '列表里有别人创建的待办，成员只能排序自己创建的；切到「我创建的」再调整顺序'

/** TodoPermissions 是当前用户对一条待办能做的事。真正的校验在后端，这里只决定显示什么。 */
interface TodoPermissions { edit: boolean; status: boolean }

/** UserTag 是创建者 / 负责人的头像 + 用户名。 */
function UserTag({ user, empty }: { user?: UserBrief | null; empty: string }) {
  if (!user) return <span className="table-subtext">{empty}</span>
  return <span className="todo-user"><UserAvatar username={user.username} userId={user.id} size={20} />{user.username}</span>
}

export function TodosPage() {
  const { auth } = useAuth()
  // ≤3 级可改任意待办；4 级只能改自己创建的、改指派给自己的状态；5 级只读。
  const canAll = hasLevel(auth, CONTENT_WRITE_LEVEL)
  const canCreate = hasLevel(auth, LEVEL_MEMBER)
  const me = auth?.id
  const notice = useNotice()
  const [items, setItems] = useState<Todo[]>([])
  const [statusParam, setStatus] = useQueryState('status', '')
  const [sortParam, setSort] = useQueryState('sort', 'priority')
  const [ownerParam, setOwner] = useQueryState('owner', '')
  const status = (todoStatuses.some(item => item.value === statusParam) ? statusParam : '') as TodoStatus | ''
  const sort = (sortValues.includes(sortParam as TodoSort) ? sortParam : 'priority') as TodoSort
  const owner = (owners.some(item => item.value === ownerParam) ? ownerParam : '') as TodoOwner
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState<(typeof emptyDraft & { id?: number; status?: TodoStatus }) | null>(null)
  const [pendingDelete, setPendingDelete] = useState<Todo | null>(null)
  const [directory, setDirectory] = useState<UserBrief[] | null>(null)

  function permissions(item: Todo): TodoPermissions {
    if (canAll) return { edit: true, status: true }
    if (!canCreate || !me) return { edit: false, status: false }
    const own = item.created_by === me
    return { edit: own, status: own || item.assignee_id === me }
  }
  const anyActionable = items.some(item => { const p = permissions(item); return p.edit || p.status })
  // 重排提交的是整张列表：4 级只要列表里混着别人的待办，后端就会整体拒绝。
  const reorderBlocked = !canAll && items.some(item => item.created_by !== me)

  async function load(silent = false) {
    if (!silent) setLoading(true)
    try { setItems(await todosApi.list(status, sort, owner)); setLoadError('') }
    catch (cause) { const message = errorMessage(cause, '无法读取待办事项'); if (silent) notice.error(message); else setLoadError(message) }
    finally { setLoading(false) }
  }
  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void load(items.length > 0) }, [status, sort, owner])

  async function run(action: () => Promise<unknown>, success: string, inModal = false) {
    setBusy(true); setFormError('')
    // 403 的说明（例如「被指派的待办只能改状态」）来自服务端，原样展示。
    try { await action(); notice.success(success); await load(true); return true }
    catch (cause) { const message = errorMessage(cause, '操作失败'); if (inModal) setFormError(message); else notice.error(message); return false }
    finally { setBusy(false) }
  }
  function closeModals() { setDraft(null); setPendingDelete(null); setFormError('') }

  /** openDraft 打开新建 / 编辑弹窗，并在第一次打开时取用户目录。 */
  function openDraft(next: typeof draft) {
    setDraft(next)
    if (directory === null) {
      todosApi.directory().then(list => setDirectory(list ?? [])).catch(() => setDirectory([]))
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!draft) return
    const priorityText = draft.priority.trim()
    const priority = Number(priorityText)
    if (!priorityText || !Number.isInteger(priority)) { setFormError('优先级需要填写整数'); return }
    const title = draft.title.trim()
    const description = draft.description.trim()
    const dueDate = draft.due_date ? new Date(`${draft.due_date}T00:00:00`).toISOString() : ''
    const assignee = draft.assignee_id ? Number(draft.assignee_id) : 0
    const saved = await run(() => draft.id
      // 编辑：空值显式发 null 表示清空；省略会被后端理解成「不改」。
      ? todosApi.update(draft.id, { title, priority, description: description || null, due_date: dueDate || null, assignee_id: assignee || null })
      : todosApi.create({ title, priority, ...(description ? { description } : {}), ...(dueDate ? { due_date: dueDate } : {}), ...(assignee ? { assignee_id: assignee } : {}) }),
    draft.id ? '待办已更新' : '待办已创建', true)
    if (saved) closeModals()
  }
  async function remove() { if (!pendingDelete) return; const saved = await run(() => todosApi.delete(pendingDelete.id), '待办已删除', true); if (saved) closeModals() }
  async function move(index: number, offset: number) { const ordered = [...items]; const target = index + offset; if (target < 0 || target >= ordered.length) return; [ordered[index], ordered[target]] = [ordered[target], ordered[index]]; await run(() => todosApi.reorder(ordered.map(item => item.id)), '待办顺序已更新') }

  // 负责人可能已被停用而不在目录里：仍把当前值放进选项，避免下拉静默变成「未指派」。
  const assigneeOptions = (() => {
    const list = directory ?? []
    const currentID = Number(draft?.assignee_id)
    if (!currentID || list.some(user => user.id === currentID)) return list
    const current = items.find(item => item.id === draft?.id)?.assignee
    return current ? [...list, current] : list
  })()

  if (loading && !items.length) return <PageState kind="loading" title="正在读取待办事项" />
  if (loadError && !items.length) return <PageState kind="error" title="无法读取待办事项" description={loadError} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />
  return <div className="business-page">
    {canCreate && <div className="page-toolbar page-toolbar--end"><button className="button button--primary" onClick={() => openDraft({ ...emptyDraft })}>新建待办</button></div>}
    {notice.element}
    <div className="filter-bar">
      <select aria-label="归属筛选" value={owner} onChange={event => setOwner(event.target.value)}>{owners.map(item => <option key={item.value || 'all'} value={item.value}>{item.label}</option>)}</select>
      <select aria-label="状态筛选" value={status} onChange={event => setStatus(event.target.value)}><option value="">全部状态</option>{todoStatuses.map(item => <option key={item.value} value={item.value}>{item.label}</option>)}</select>
      <select aria-label="排序方式" value={sort} onChange={event => setSort(event.target.value)}><option value="priority">按优先级</option><option value="due_date">按截止日期</option><option value="created_at">按创建时间</option></select>
      <button className="button button--quiet" onClick={() => void load(true)} disabled={loading}>刷新</button>
    </div>
    {!items.length
      ? <PageState kind="empty" title="没有符合条件的待办" description={canCreate ? '新建一项任务，或调整筛选条件。' : '调整筛选条件试试。'} action={canCreate ? <button className="button button--primary" onClick={() => openDraft({ ...emptyDraft })}>新建待办</button> : undefined} />
      : <div className="table-wrap"><table className="data-table"><thead><tr><th>任务</th><th>状态</th><th>优先级</th><th>截止日期</th><th>创建者</th><th>负责人</th>{anyActionable && <th>操作</th>}</tr></thead><tbody>{items.map((item, index) => {
        const can = permissions(item)
        const label = todoStatuses.find(option => option.value === item.status)?.label
        return <tr key={item.id}>
          <td data-label="任务"><strong>{item.title}</strong>{item.description && <p className="table-subtext">{item.description}</p>}</td>
          <td data-label="状态">{can.status && <select value={item.status} disabled={busy} onChange={event => void run(() => todosApi.updateStatus(item.id, event.target.value as TodoStatus), '任务状态已更新')} aria-label={`${item.title}的状态`}>{todoStatuses.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>}<StatusBadge tone={statusTone[item.status]}>{label}</StatusBadge></td>
          <td data-label="优先级">{item.priority}</td>
          <td data-label="截止日期">{item.due_date ? formatDateTime(item.due_date) : '未设置'}</td>
          <td data-label="创建者"><UserTag user={item.creator} empty="未知" /></td>
          <td data-label="负责人"><UserTag user={item.assignee} empty="未指派" /></td>
          {anyActionable && <td data-label="操作">{can.edit && <div className="table-actions">
            <button className="icon-button" disabled={busy || index === 0 || reorderBlocked} title={reorderBlocked ? REORDER_BLOCKED_HINT : undefined} onClick={() => void move(index, -1)} aria-label={`上移待办“${item.title}”`}>↑</button>
            <button className="icon-button" disabled={busy || index === items.length - 1 || reorderBlocked} title={reorderBlocked ? REORDER_BLOCKED_HINT : undefined} onClick={() => void move(index, 1)} aria-label={`下移待办“${item.title}”`}>↓</button>
            <button className="button button--quiet" onClick={() => openDraft({ id: item.id, title: item.title, description: item.description ?? '', priority: String(item.priority), due_date: item.due_date?.slice(0, 10) ?? '', assignee_id: item.assignee_id ? String(item.assignee_id) : '', status: item.status })}>编辑</button>
            <DeleteButton label={`删除待办“${item.title}”`} onClick={() => setPendingDelete(item)} />
          </div>}</td>}
        </tr>
      })}</tbody></table></div>}
    <Modal open={draft !== null} title={draft?.id ? '编辑待办' : '新建待办'} description={draft?.id ? '说明、截止日期留空即清空。' : undefined} error={formError} busy={busy} onClose={closeModals} footer={<><button className="button button--quiet" type="button" onClick={closeModals} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="todo-form" disabled={busy}>{busy ? '保存中…' : '保存'}</button></>}>
      <form id="todo-form" className="form-grid" onSubmit={save}>
        <label className="form-field form-field--wide">标题<input autoFocus value={draft?.title ?? ''} onChange={event => setDraft(value => value ? { ...value, title: event.target.value } : value)} required maxLength={500} /></label>
        <label className="form-field form-field--wide">说明<textarea value={draft?.description ?? ''} onChange={event => setDraft(value => value ? { ...value, description: event.target.value } : value)} /></label>
        <label className="form-field">优先级<input type="number" step={1} required value={draft?.priority ?? ''} onChange={event => setDraft(value => value ? { ...value, priority: event.target.value } : value)} /></label>
        <label className="form-field">截止日期<input type="date" value={draft?.due_date ?? ''} onChange={event => setDraft(value => value ? { ...value, due_date: event.target.value } : value)} /></label>
        <label className="form-field form-field--wide">负责人
          <select value={draft?.assignee_id ?? ''} disabled={directory === null} onChange={event => setDraft(value => value ? { ...value, assignee_id: event.target.value } : value)}>
            <option value="">{directory === null ? '正在读取用户…' : '未指派'}</option>
            {assigneeOptions.map(user => <option key={user.id} value={user.id}>{user.username}</option>)}
          </select>
        </label>
      </form>
    </Modal>
    <ConfirmDialog open={pendingDelete !== null} title="删除待办" description={`将删除“${pendingDelete?.title ?? ''}”，删除后无法恢复。`} confirmLabel="删除" danger busy={busy} error={formError} onClose={closeModals} onConfirm={() => void remove()} />
  </div>
}
