import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Notice, PageState } from '@/components/WorkspaceUI'
import { useTheme, type ThemePreference } from '@/hooks/useTheme'
import type { BackgroundSettings } from '@/lib/home'
import { mediaURL } from '@/lib/media'
import { mediaAssetsApi, type MediaAsset } from '@/lib/mediaAssets'
import { settingsApi } from '@/lib/settings'

const fallback: BackgroundSettings = {
  mode: 'single',
  interval_seconds: 30,
  transition_effect: 'fade',
  fade_duration_ms: 1000,
  selected_asset_ids: [],
}

// ============================================================
// 外观 — 主题偏好 + 服务端背景配置
//
// 主题只影响当前浏览器；背景配置写入服务端，影响所有人。
// 两者用不同的视觉区块区分，避免误以为主题也会同步。
// ============================================================

const themes: { value: ThemePreference; label: string; hint: string; swatches: string[] }[] = [
  { value: 'light', label: '浅色', hint: '米白底 · 墨绿主色', swatches: ['#f5f5dc', '#4a7729', '#fffdf4'] },
  { value: 'dark', label: '深色', hint: '墨石灰底 · 提亮主色', swatches: ['#1b2927', '#8eb46b', '#263633'] },
  { value: 'system', label: '跟随系统', hint: '随操作系统自动切换', swatches: ['#f5f5dc', '#64716c', '#1b2927'] },
]

const modes: { value: BackgroundSettings['mode']; label: string; hint: string }[] = [
  { value: 'single', label: '单图', hint: '固定展示一张' },
  { value: 'carousel', label: '顺序轮播', hint: '按选中顺序循环' },
  { value: 'random', label: '随机轮播', hint: '每次随机切换' },
]

export function AppearancePage() {
  const { theme, setTheme } = useTheme()
  const [settings, setSettings] = useState<BackgroundSettings>(fallback)
  const [assets, setAssets] = useState<MediaAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  useEffect(() => {
    Promise.all([settingsApi.getBackground(), mediaAssetsApi.list()])
      .then(([background, media]) => {
        setSettings(background)
        setAssets(media.filter(asset => asset.media_type === 'image' || asset.media_type === 'video'))
      })
      .catch(cause => setError(cause instanceof Error ? cause.message : '无法读取外观配置'))
      .finally(() => setLoading(false))
  }, [])

  const selectedCount = settings.selected_asset_ids.length
  const carousel = settings.mode !== 'single'

  // 按选中顺序展示预览，未选中时退化为素材前几张
  const previewAssets = useMemo(() => {
    const picked = settings.selected_asset_ids
      .map(id => assets.find(asset => asset.id === id))
      .filter((asset): asset is MediaAsset => Boolean(asset))
    return picked.length ? picked : assets.slice(0, 4)
  }, [assets, settings.selected_asset_ids])

  function toggleAsset(id: number) {
    setSettings(current => ({
      ...current,
      selected_asset_ids: current.selected_asset_ids.includes(id)
        ? current.selected_asset_ids.filter(value => value !== id)
        : [...current.selected_asset_ids, id],
    }))
  }

  function selectAllAssets() {
    setSettings(current => ({ ...current, selected_asset_ids: current.selected_asset_ids.length === assets.length ? [] : assets.map(asset => asset.id) }))
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    try {
      await settingsApi.updateBackground(settings)
      setMessage('首页背景已保存。登录页背景在登录页的独立配置中维护，不受此处影响。')
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <PageState kind="loading" title="正在读取外观设置" />

  return <div className="business-page appearance-page">
    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} tone="info" onDismiss={() => setMessage('')} />

    {/* ---------- 浏览器主题 ---------- */}
    <section className="data-panel">
      <header className="data-panel-head">
        <div>
          <h2>浏览器主题</h2>
          <p>只保存在当前浏览器，不影响其他设备。</p>
        </div>
        <span className="status-badge status-badge--info">{themes.find(item => item.value === theme)?.label}</span>
      </header>
      <div className="appearance-body">
        <div className="theme-options" role="radiogroup" aria-label="主题偏好">
          {themes.map(option => <label
            key={option.value}
            className={`theme-option${theme === option.value ? ' is-active' : ''}`}
          >
            <input
              type="radio"
              name="theme-preference"
              value={option.value}
              checked={theme === option.value}
              onChange={() => setTheme(option.value)}
              className="visually-hidden"
            />
            <span className="theme-swatches" aria-hidden="true">
              {option.swatches.map(color => <i key={color} style={{ background: color }} />)}
            </span>
            <span className="theme-option-copy">
              <strong>{option.label}</strong>
              <small>{option.hint}</small>
            </span>
          </label>)}
        </div>
      </div>
    </section>

    {/* ---------- 服务端背景 ---------- */}
    <form className="data-panel" onSubmit={save}>
      <header className="data-panel-head">
        <div>
          <h2>服务端背景</h2>
          <p>写入服务端配置，首页与登录页共享。</p>
        </div>
        <button className="button button--primary" disabled={busy}>{busy ? '保存中…' : '保存背景'}</button>
      </header>

      <div className="appearance-body appearance-split">
        {/* 参数 */}
        <div className="appearance-fields">
          <div>
            <span className="appearance-label">显示模式</span>
            <div className="mode-options" role="radiogroup" aria-label="背景显示模式">
              {modes.map(option => <label
                key={option.value}
                className={`mode-option${settings.mode === option.value ? ' is-active' : ''}`}
              >
                <input
                  type="radio"
                  name="background-mode"
                  value={option.value}
                  checked={settings.mode === option.value}
                  onChange={() => setSettings(current => ({ ...current, mode: option.value }))}
                  className="visually-hidden"
                />
                <strong>{option.label}</strong>
                <small>{option.hint}</small>
              </label>)}
            </div>
          </div>

          <label className="form-field">
            轮播间隔（秒）
            <input
              type="number"
              min="1"
              value={settings.interval_seconds}
              disabled={!carousel}
              onChange={event => setSettings(current => ({ ...current, interval_seconds: Number(event.target.value) }))}
            />
          </label>

          <div className="form-field">
            过渡效果
            <div className="segmented" role="radiogroup" aria-label="过渡效果">
              <label className={settings.transition_effect === 'fade' ? 'is-on' : ''}>
                <input
                  type="radio"
                  name="transition-effect"
                  value="fade"
                  checked={settings.transition_effect === 'fade'}
                  onChange={() => setSettings(current => ({ ...current, transition_effect: 'fade' }))}
                  className="visually-hidden"
                />
                淡入淡出
              </label>
              <label className={settings.transition_effect === 'slide' ? 'is-on' : ''}>
                <input
                  type="radio"
                  name="transition-effect"
                  value="slide"
                  checked={settings.transition_effect === 'slide'}
                  onChange={() => setSettings(current => ({ ...current, transition_effect: 'slide' }))}
                  className="visually-hidden"
                />
                滑动
              </label>
            </div>
          </div>

          <label className="form-field">
            过渡时长（毫秒）
            <input
              type="number"
              min="0"
              max="10000"
              step="100"
              value={settings.fade_duration_ms}
              onChange={event => setSettings(current => ({ ...current, fade_duration_ms: Number(event.target.value) }))}
            />
          </label>
        </div>

        {/* 素材 */}
        <div className="appearance-assets">
          <div className="appearance-assets-head">
            <span className="appearance-label">背景素材</span>
            <div className="appearance-assets-tools">
              <span>{selectedCount > 0 ? `已选 ${selectedCount} / ${assets.length}` : `未选择（共 ${assets.length}）`}</span>
              {assets.length > 0 && <button type="button" onClick={selectAllAssets}>{selectedCount === assets.length ? '取消全选' : '全选'}</button>}
            </div>
          </div>

          {assets.length === 0
            ? <div className="appearance-empty">
              <p>媒体库里还没有图片或视频。</p>
              <a className="button button--quiet" href="/media">去上传 →</a>
            </div>
            : <>
              <div className="asset-grid">
                {assets.map(asset => {
                  const active = settings.selected_asset_ids.includes(asset.id)
                  return <button
                    key={asset.id}
                    type="button"
                    className={`asset-tile${active ? ' is-active' : ''}`}
                    aria-pressed={active}
                    onClick={() => toggleAsset(asset.id)}
                    title={asset.original_name}
                  >
                    {asset.media_type === 'image'
                      ? <img src={mediaURL(asset.file_path)} alt="" loading="lazy" />
                      : <span className="asset-video" aria-hidden="true">▶</span>}
                    <span className="asset-name">{asset.original_name}</span>
                    {active && <span className="asset-check" aria-hidden="true">✓</span>}
                  </button>
                })}
              </div>
              {selectedCount > 0 && <div className="appearance-preview">
                <span className="appearance-label">轮播顺序预览</span>
                <ol className="preview-list">
                  {previewAssets.map((asset, index) => <li key={asset.id}>
                    <span className="preview-index">{index + 1}</span>
                    <span className="preview-name">{asset.original_name}</span>
                  </li>)}
                </ol>
              </div>}
            </>}
        </div>
      </div>
    </form>
  </div>
}
