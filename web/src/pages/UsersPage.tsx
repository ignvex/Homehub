import { useEffect, useState, type FormEvent } from 'react'
import { ConfirmDialog, Modal, Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { UserAvatar } from '@/components/UserAvatar'
import { adminApi, type ManagedUser } from '@/lib/admin'
import { useAuth } from '@/lib/auth'
import { LEVEL_ADMIN, LEVEL_OPERATOR, assignableLevels, canManage, levelInfo, levelLabel } from '@/lib/permissions'
import { formatDateTime } from '@/lib/types'

interface UserDraft { id?: number; username: string; email: string; password: string; level: number; is_active?: boolean }

const levelTone = (level: number) => level === LEVEL_ADMIN ? 'info' : level === LEVEL_OPERATOR ? 'warning' : 'neutral'

/** 不能管理某账号时给出原因，用于按钮 title。 */
function manageBlockReason(actor: { id?: number; level?: number } | null, user: ManagedUser): string {
  if (actor?.id === user.id) return '不能在这里修改自己的账号，请到个人中心'
  return `只能管理低于「${levelLabel(actor?.level)}」等级的账号`
}

export function UsersPage() {
  const { auth } = useAuth()
  const options = assignableLevels(auth?.level)
  const defaultLevel = options[options.length - 1]?.level ?? 0
  const [items, setItems] = useState<ManagedUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [draft, setDraft] = useState<UserDraft | null>(null)
  const [resetTarget, setResetTarget] = useState<ManagedUser | null>(null)
  const [newPassword, setNewPassword] = useState('')
  const [pendingDelete, setPendingDelete] = useState<ManagedUser | null>(null)

  async function load() { setLoading(true); try { setItems(await adminApi.listUsers()); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取用户') } finally { setLoading(false) } }
  useEffect(() => { void load() }, [])
  async function run(action: () => Promise<unknown>, success: string) { setBusy(true); try { await action(); setMessage(success); await load(); return true } catch (cause) { setError(cause instanceof Error ? cause.message : '操作失败'); return false } finally { setBusy(false) } }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!draft) return
    // 后端以 level 为准；不再发送 role（旧的 role 字段无法表达 1–5 级）。
    const saved = await run(() => draft.id
      ? adminApi.updateUser(draft.id, { username: draft.username.trim(), email: draft.email.trim(), level: draft.level, is_active: draft.is_active })
      : adminApi.createUser({ username: draft.username.trim(), email: draft.email.trim(), password: draft.password, level: draft.level }), draft.id ? '用户已更新' : '用户已创建')
    if (saved) setDraft(null)
  }
  async function resetPassword(event: FormEvent) { event.preventDefault(); if (!resetTarget) return; const saved = await run(() => adminApi.resetPassword(resetTarget.id, newPassword), '密码已重置，该用户的现有会话已失效'); if (saved) { setResetTarget(null); setNewPassword('') } }
  async function remove() { if (!pendingDelete) return; const saved = await run(() => adminApi.deleteUser(pendingDelete.id), '用户已删除'); if (saved) setPendingDelete(null) }

  const openCreate = () => setDraft({ username: '', email: '', password: '', level: defaultLevel })
  const canCreate = options.length > 0

  if (loading) return <PageState kind="loading" title="正在读取用户" />
  if (error && !items.length) return <PageState kind="error" title="无法读取用户" description={error} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />
  return <div className="business-page">
    <div className="page-toolbar"><div><p>访问控制</p><h2>用户管理</h2></div><button className="button button--primary" disabled={!canCreate} onClick={openCreate}>创建用户</button></div>
    <Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} onDismiss={() => setMessage('')} />
    {!items.length ? <PageState kind="empty" title="没有用户记录" action={canCreate ? <button className="button button--primary" onClick={openCreate}>创建用户</button> : undefined} /> : <div className="table-wrap"><table className="data-table"><thead><tr><th>用户</th><th>等级</th><th>状态</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{items.map(user => {
      const manageable = canManage(auth, user)
      const reason = manageable ? undefined : manageBlockReason(auth, user)
      return <tr key={user.id}>
        <td data-label="用户"><div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}><UserAvatar username={user.username} size={32} userId={user.id} /><div><strong>{user.username}</strong><br /><span className="table-subtext">{user.email || '未设置邮箱'}</span></div></div></td>
        <td data-label="等级"><StatusBadge tone={levelTone(user.level)}><span title={levelInfo(user.level)?.description}>{levelLabel(user.level)}</span></StatusBadge></td>
        <td data-label="状态"><StatusBadge tone={user.is_active ? 'success' : 'danger'}>{user.is_active ? '已启用' : '已停用'}</StatusBadge></td>
        <td data-label="创建时间">{formatDateTime(user.created_at)}</td>
        <td data-label="操作"><div className="table-actions">
          <button className="button button--quiet" disabled={!manageable} title={reason} onClick={() => setDraft({ id: user.id, username: user.username, email: user.email ?? '', password: '', level: user.level, is_active: user.is_active })}>编辑</button>
          <button className="button button--quiet" disabled={!manageable} title={reason} onClick={() => setResetTarget(user)}>重置密码</button>
          <button className="icon-button" title="刷新头像" aria-label="刷新头像" disabled={busy} onClick={() => void run(() => adminApi.refreshAvatar(user.id), '头像缓存已刷新')}>↻</button>
          <button className="icon-button" aria-label="删除用户" disabled={!manageable} title={reason} onClick={() => setPendingDelete(user)}>×</button>
        </div></td>
      </tr>
    })}</tbody></table></div>}
    <Modal open={draft !== null} title={draft?.id ? '编辑用户' : '创建用户'} description="只能分配低于自己的等级。" onClose={() => !busy && setDraft(null)} footer={<><button className="button button--quiet" onClick={() => setDraft(null)} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="user-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button></>}><form id="user-form" className="form-grid" onSubmit={save}>
      <label className="form-field">用户名<input autoFocus minLength={3} maxLength={100} value={draft?.username ?? ''} onChange={event => setDraft(value => value ? { ...value, username: event.target.value } : value)} required /></label>
      <label className="form-field">邮箱<input type="email" value={draft?.email ?? ''} onChange={event => setDraft(value => value ? { ...value, email: event.target.value } : value)} /></label>
      {!draft?.id && <label className="form-field">初始密码<input type="password" minLength={6} value={draft?.password ?? ''} onChange={event => setDraft(value => value ? { ...value, password: event.target.value } : value)} required /></label>}
      <label className="form-field">等级<select value={draft?.level ?? defaultLevel} onChange={event => setDraft(value => value ? { ...value, level: Number(event.target.value) } : value)} required>{options.map(option => <option key={option.level} value={option.level}>{option.label}（{option.description}）</option>)}</select></label>
      {draft?.id && <label className="choice-tile form-field--wide"><input type="checkbox" checked={draft.is_active ?? true} onChange={event => setDraft(value => value ? { ...value, is_active: event.target.checked } : value)} /><span>允许该用户登录</span></label>}
    </form></Modal>
    <Modal open={resetTarget !== null} title="重置用户密码" description={`为“${resetTarget?.username ?? ''}”设置新密码，成功后其全部会话会失效。`} onClose={() => !busy && setResetTarget(null)} footer={<><button className="button button--quiet" onClick={() => setResetTarget(null)} disabled={busy}>取消</button><button className="button button--danger" type="submit" form="reset-password-form" disabled={busy}>{busy ? '重置中...' : '重置密码'}</button></>}><form id="reset-password-form" onSubmit={resetPassword}><label className="form-field">新密码<input autoFocus type="password" minLength={6} value={newPassword} onChange={event => setNewPassword(event.target.value)} required /></label></form></Modal>
    <ConfirmDialog open={pendingDelete !== null} title="删除用户" description={`将删除“${pendingDelete?.username ?? ''}”及其全部会话。不能删除当前登录账户。`} confirmLabel="删除用户" danger busy={busy} onClose={() => setPendingDelete(null)} onConfirm={() => void remove()} />
  </div>
}
