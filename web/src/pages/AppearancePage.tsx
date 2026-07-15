import { useEffect, useState, type FormEvent } from 'react'
import { Notice, PageState } from '@/components/WorkspaceUI'
import { useTheme } from '@/hooks/useTheme'
import type { BackgroundSettings } from '@/lib/home'
import { mediaURL } from '@/lib/media'
import { mediaAssetsApi, type MediaAsset } from '@/lib/mediaAssets'
import { settingsApi } from '@/lib/settings'

const fallback: BackgroundSettings = { mode: 'single', interval_seconds: 30, transition_effect: 'fade', fade_duration_ms: 1000, selected_asset_ids: [] }

export function AppearancePage() {
  const { theme, setTheme } = useTheme()
  const [settings, setSettings] = useState<BackgroundSettings>(fallback)
  const [assets, setAssets] = useState<MediaAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  useEffect(() => { Promise.all([settingsApi.getBackground(), mediaAssetsApi.list()]).then(([background, media]) => { setSettings(background); setAssets(media) }).catch(cause => setError(cause instanceof Error ? cause.message : '无法读取外观配置')).finally(() => setLoading(false)) }, [])
  function toggleAsset(id: number) { setSettings(current => ({ ...current, selected_asset_ids: current.selected_asset_ids.includes(id) ? current.selected_asset_ids.filter(value => value !== id) : [...current.selected_asset_ids, id] })) }
  async function save(event: FormEvent) { event.preventDefault(); setBusy(true); try { await settingsApi.updateBackground(settings); setMessage('背景配置已保存。注意：该接口当前更新旧版全局 background，首页和登录页实际使用 webui 配置时可能不会同步。'); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') } finally { setBusy(false) } }
  if (loading) return <PageState kind="loading" title="正在读取外观设置" />
  return <div className="business-page"><div className="page-toolbar"><div><p>界面偏好</p><h2>外观与背景</h2></div></div><Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} tone="info" onDismiss={() => setMessage('')} /><section className="data-panel"><header className="data-panel-head"><div><h2>当前浏览器主题</h2><p>仅保存在当前浏览器，不修改服务端配置。</p></div></header><div className="modal-body"><div className="toolbar-actions"><button className={`button ${theme === 'light' ? 'button--primary' : 'button--quiet'}`} onClick={() => setTheme('light')}>浅色</button><button className={`button ${theme === 'dark' ? 'button--primary' : 'button--quiet'}`} onClick={() => setTheme('dark')}>深色</button><button className={`button ${theme === 'system' ? 'button--primary' : 'button--quiet'}`} onClick={() => setTheme('system')}>跟随系统</button></div></div></section><form className="data-panel" onSubmit={save}><header className="data-panel-head"><div><h2>服务端背景配置</h2><p>配置模式、轮播间隔、过渡与媒体素材。</p></div><button className="button button--primary" disabled={busy}>{busy ? '保存中...' : '保存背景'}</button></header><div className="modal-body form-grid form-grid--three"><label className="form-field">模式<select value={settings.mode} onChange={event => setSettings(current => ({ ...current, mode: event.target.value as BackgroundSettings['mode'] }))}><option value="single">单图</option><option value="carousel">顺序轮播</option><option value="random">随机轮播</option></select></label><label className="form-field">轮播间隔（秒）<input type="number" min="1" value={settings.interval_seconds} onChange={event => setSettings(current => ({ ...current, interval_seconds: Number(event.target.value) }))} /></label><label className="form-field">过渡<select value={settings.transition_effect} onChange={event => setSettings(current => ({ ...current, transition_effect: event.target.value as BackgroundSettings['transition_effect'] }))}><option value="fade">淡入淡出</option><option value="slide">滑动</option></select></label><label className="form-field">过渡时长（毫秒）<input type="number" min="0" max="10000" step="100" value={settings.fade_duration_ms} onChange={event => setSettings(current => ({ ...current, fade_duration_ms: Number(event.target.value) }))} /></label><div className="form-field form-field--wide"><span>背景素材</span>{assets.length ? <div className="choice-grid">{assets.map(asset => <label className="choice-tile" key={asset.id}><input type="checkbox" checked={settings.selected_asset_ids.includes(asset.id)} onChange={() => toggleAsset(asset.id)} />{asset.media_type === 'image' ? <img src={mediaURL(asset.file_path)} alt="" /> : <span aria-hidden="true">视频</span>}<span>{asset.original_name}</span></label>)}</div> : <p className="table-subtext">媒体库为空，请先在媒体资源页上传图片或视频。</p>}</div></div></form></div>
}
