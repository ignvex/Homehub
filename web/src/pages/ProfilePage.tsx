import { useEffect, useState, type FormEvent } from 'react'
import { Notice, PageState } from '@/components/WorkspaceUI'
import { UserAvatar } from '@/components/UserAvatar'
import { profileApi, type UserProfile } from '@/lib/dashboard'
import { settingsApi, type SafeConfig } from '@/lib/settings'

export function ProfilePage() {
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [gravatarConfig, setGravatarConfig] = useState<SafeConfig['gravatar'] | null>(null)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [changingPassword, setChangingPassword] = useState(false)
  const [refreshingAvatar, setRefreshingAvatar] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    profileApi.get().then(setProfile).catch(cause => setError(cause instanceof Error ? cause.message : '资料载入失败'))
    settingsApi.get().then(cfg => setGravatarConfig(cfg.gravatar)).catch(() => { /* 非管理员可能无权访问 */ })
  }, [])
  async function save(event: FormEvent) { event.preventDefault(); if (!profile) return; setSaving(true); try { const updated = await profileApi.update({ username: profile.username.trim(), email: profile.email.trim() }); setProfile(updated); setRevision(Date.now()); setMessage('个人资料已保存'); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') } finally { setSaving(false) } }
  async function changePassword(event: FormEvent<HTMLFormElement>) { event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const next = String(data.get('new_password')); const confirm = String(data.get('confirm_password')); if (next !== confirm) { setError('两次输入的新密码不一致'); return } setChangingPassword(true); try { await profileApi.changePassword({ old_password: String(data.get('old_password')), new_password: next }); window.location.assign('/login?passwordChanged=1') } catch (cause) { setError(cause instanceof Error ? cause.message : '密码修改失败'); setChangingPassword(false) } }
  function refreshAvatar() {
    setRefreshingAvatar(true)
    setRevision(Date.now())
    setMessage('头像已重新加载。如需从 Gravatar 重新下载，请修改邮箱或联系管理员刷新缓存。')
    setTimeout(() => setRefreshingAvatar(false), 500)
  }
  if (!profile && error) return <PageState kind="error" title="无法读取个人资料" description={error} />
  if (!profile) return <PageState kind="loading" title="正在读取个人资料" />
  return <div className="business-page"><Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} onDismiss={() => setMessage('')} /><div className="profile-layout"><section className="data-panel profile-card"><UserAvatar username={profile.username} size={112} revision={revision} /><div><p>HOMEHUB ACCOUNT</p><h2>{profile.username}</h2><span>{profile.email || '尚未设置邮箱'}</span><b>{profile.role === 'admin' ? '管理员' : '普通用户'}</b></div><div className="profile-avatar-actions"><button className="button button--quiet" disabled={refreshingAvatar} onClick={refreshAvatar}>{refreshingAvatar ? '刷新中...' : '重新加载头像'}</button>{gravatarConfig && <span className="profile-gravatar-status">Gravatar {gravatarConfig.enabled ? '已启用' : '未启用'}{gravatarConfig.enabled ? ` · ${gravatarConfig.default_style} · ${gravatarConfig.rating.toUpperCase()}` : ''}</span>}<a className="button button--quiet" href="/settings">Gravatar 设置</a></div><small>头像使用当前邮箱对应的 Gravatar；修改邮箱后会清除后端缓存并重新下载。</small></section><div className="settings-sections"><section className="data-panel"><header className="data-panel-head"><div><h2>个人资料</h2><p>用户名变更会影响下次登录。</p></div></header><form className="modal-body form-grid" onSubmit={save}><label className="form-field">用户名<input value={profile.username} minLength={3} maxLength={100} onChange={event => setProfile({ ...profile, username: event.target.value })} required /></label><label className="form-field">邮箱<input type="email" value={profile.email} onChange={event => setProfile({ ...profile, email: event.target.value })} /></label><div className="form-actions form-field--wide"><button className="button button--primary" disabled={saving}>{saving ? '保存中...' : '保存资料'}</button></div></form></section><section className="data-panel"><header className="data-panel-head"><div><h2>修改密码</h2><p>成功后全部会话失效，需要重新登录。</p></div></header><form className="modal-body form-grid" onSubmit={changePassword}><label className="form-field form-field--wide">当前密码<input name="old_password" type="password" autoComplete="current-password" required /></label><label className="form-field">新密码<input name="new_password" type="password" autoComplete="new-password" minLength={6} required /></label><label className="form-field">确认新密码<input name="confirm_password" type="password" autoComplete="new-password" minLength={6} required /></label><div className="form-actions form-field--wide"><button className="button button--danger" disabled={changingPassword}>{changingPassword ? '修改中...' : '修改密码并退出'}</button></div></form></section></div></div></div>
}
