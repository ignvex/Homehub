import { useEffect, useState, type FormEvent } from 'react'
import { profileApi, type UserProfile } from '@/lib/dashboard'
import { UserAvatar } from '@/components/UserAvatar'

export function ProfilePage({ settings = false }: { settings?: boolean }) {
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [message, setMessage] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => { profileApi.get().then(setProfile).catch(error => setMessage(error instanceof Error ? error.message : '资料载入失败')) }, [])
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!profile) return
    try { const updated = await profileApi.update({ username: profile.username, email: profile.email }); setProfile(updated); setRevision(Date.now()); setMessage('资料已保存') } catch (error) { setMessage(error instanceof Error ? error.message : '保存失败') }
  }
  async function changePassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const data = new FormData(event.currentTarget)
    try { await profileApi.changePassword({ old_password: String(data.get('old_password')), new_password: String(data.get('new_password')) }); window.location.assign('/login') } catch (error) { setMessage(error instanceof Error ? error.message : '密码修改失败') }
  }
  if (!profile) return <div className="panel-loading">{message || '正在载入个人资料…'}</div>
  return <div className="profile-layout"><section className="work-panel profile-card"><UserAvatar username={profile.username} size={112} revision={revision} /><div><p>HOMEHUB ACCOUNT</p><h2>{profile.username}</h2><span>{profile.email || '尚未设置邮箱'}</span><b>{profile.role === 'admin' ? '管理员' : profile.role}</b></div><small>头像由 Gravatar 提供；无可用头像时自动使用安全的本地回退。</small></section><section className="work-panel settings-panel"><header><div><p>{settings ? 'ACCOUNT SETTINGS' : 'PROFILE'}</p><h2>{settings ? '个人设置' : '个人资料'}</h2></div></header><form onSubmit={save}><label>用户名<input value={profile.username} minLength={3} maxLength={32} onChange={e => setProfile({ ...profile, username: e.target.value })} /></label><label>邮箱<input type="email" value={profile.email} onChange={e => setProfile({ ...profile, email: e.target.value })} /></label><button type="submit">保存资料</button></form>{settings && <form className="password-form" onSubmit={changePassword}><h3>修改密码</h3><label>当前密码<input name="old_password" type="password" required /></label><label>新密码<input name="new_password" type="password" minLength={8} required /></label><button type="submit">修改并重新登录</button></form>}{message && <div className="form-message">{message}</div>}</section></div>
}
