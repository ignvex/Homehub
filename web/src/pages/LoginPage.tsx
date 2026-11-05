import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { api, ApiRequestError } from '@/lib/api'
import { getWebUIConfig, type LoginWebUIConfig } from '@/lib/webui'
import { mediaURL } from '@/lib/media'

const fallback: LoginWebUIConfig = { layout: 'center', assets: [], background: { mode: 'single', interval_seconds: 30, transition_effect: 'fade', fade_duration_ms: 1000, selected_asset_ids: [] } }

export function LoginPage() {
  const [config, setConfig] = useState(fallback)
  const [index, setIndex] = useState(0)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [passwordChanged] = useState(() => new URLSearchParams(window.location.search).get('passwordChanged') === '1')

  useEffect(() => { getWebUIConfig().then(value => setConfig(value.login)).catch(() => undefined) }, [])
  useEffect(() => {
    if (config.assets.length < 2 || config.background.mode === 'single') return
    const timer = window.setInterval(() => setIndex(value => config.background.mode === 'random'
      ? (value + 1 + Math.floor(Math.random() * (config.assets.length - 1))) % config.assets.length
      : (value + 1) % config.assets.length), Math.max(1, config.background.interval_seconds) * 1000)
    return () => window.clearInterval(timer)
  }, [config])

  async function submit(event: FormEvent) {
    event.preventDefault(); setSubmitting(true); setError('')
    try {
      await api.post('/auth/login', { username, password })
      const candidate = new URLSearchParams(window.location.search).get('returnTo') || '/'
      const returnTo = candidate.startsWith('/') && !candidate.startsWith('//') ? candidate : '/'
      window.location.href = returnTo
    }
    catch (cause) { setError(cause instanceof ApiRequestError ? cause.message : '登录失败，请稍后重试') }
    finally { setSubmitting(false) }
  }

  const asset = config.assets[index % Math.max(config.assets.length, 1)]
  return <main className={`login-page login-${config.layout}`}>
    <div className="login-background" aria-hidden="true">{asset?.media_type === 'video' ? <video src={mediaURL(asset.file_path)} autoPlay muted loop playsInline /> : asset ? <div style={{ backgroundImage: `url("${mediaURL(asset.file_path)}")` }} /> : <div className="login-fallback" />}<span /></div>
    <a className="login-brand" href="/"><b>H</b>HomeHub</a><ThemeToggle />
    <section className="login-card">
      <p className="section-kicker">WELCOME BACK</p><h1>登录 HomeHub</h1><p>回到属于你的个人空间。</p>
      <form onSubmit={submit}><label>用户名<input autoComplete="username" value={username} onChange={e => setUsername(e.target.value)} required /></label><label>密码<input type="password" autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} required /></label>{passwordChanged && !error && <div className="login-info" role="status">密码已修改，请重新登录</div>}{error && <div className="login-error" role="alert">{error}</div>}<button disabled={submitting}>{submitting ? '正在登录…' : '登录'}</button></form>
      <a className="back-home" href="/">← 返回首页</a>
    </section>
  </main>
}
