import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { PageState } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useAuth } from '@/lib/auth'
import { CONTENT_WRITE_LEVEL, hasLevel } from '@/lib/permissions'
import { useTheme, type ThemePreference } from '@/hooks/useTheme'
import type { BackgroundSettings } from '@/lib/home'
import { mediaURL } from '@/lib/media'
import { mediaAssetsApi, type MediaAsset } from '@/lib/mediaAssets'
import { settingsApi } from '@/lib/settings'

// 背景配置读取失败时不再用默认值渲染可保存的表单：那样一点保存就会
// 把服务端的真实配置覆盖成默认值。表单只在读取成功后出现。

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
  const { auth } = useAuth()
  const canWrite = hasLevel(auth, CONTENT_WRITE_LEVEL)
  const notice = useNotice()
  const [loaded, setLoaded] = useState<BackgroundSettings | null>(null)
  const [draft, setSettings] = useState<BackgroundSettings | null>(null)
  const [assets, setAssets] = useState<MediaAsset[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setLoadError('')
    Promise.all([settingsApi.getBackground(), mediaAssetsApi.list()])
      .then(([background, media]) => {
        // 旧后端在 config.json 缺字段时会下发 null；归一后下面所有 .length/.includes 才安全。
        const normalized = { ...background, selected_asset_ids: background.selected_asset_ids ?? [] }
        setLoaded(normalized)
        setSettings(normalized)
        setAssets(media.filter(asset => asset.media_type === 'image' || asset.media_type === 'video'))
      })
      .catch(cause => setLoadError(errorMessage(cause, '无法读取外观配置')))
      .finally(() => setLoading(false))
  }, [])
  useEffect(load, [load])

  // 渲染处已保证 draft 非空才使用 settings；这里给一个只读占位避免到处判空。
  const settings: BackgroundSettings = draft ?? { mode: 'single', interval_seconds: 30, transition_effect: 'fade', fade_duration_ms: 1000, selected_asset_ids: [] }
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
    setSettings(current => current && ({
      ...current,
      selected_asset_ids: current.selected_asset_ids.includes(id)
        ? current.selected_asset_ids.filter(value => value !== id)
        : [...current.selected_asset_ids, id],
    }))
  }

  function selectAllAssets() {
    setSettings(current => current && ({ ...current, selected_asset_ids: current.selected_asset_ids.length === assets.length ? [] : assets.map(asset => asset.id) }))
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!draft || !loaded) return
    setBusy(true)
    try {
      await settingsApi.updateBackground(draft)
      setLoaded(draft)
      notice.success('首页背景已保存。登录页背景在登录页的独立配置中维护，不受此处影响。')
    } catch (cause) {
      notice.error(errorMessage(cause, '保存失败'))
    } finally {
      setBusy(false)
    }
  }

  return <div className="business-page appearance-page">
    {notice.element}

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
    {loading && !draft
      ? <PageState kind="loading" title="正在读取背景配置" />
      : !draft
      ? <PageState kind="error" title="无法读取背景配置" description={`${loadError}。为避免用默认值覆盖服务端配置，读取成功前不能保存。`} action={<button type="button" className="button button--primary" onClick={load}>重试</button>} />
      : <form className="data-panel" onSubmit={save}>
      <header className="data-panel-head">
        <div>
          <h2>服务端背景</h2>
          <p>{canWrite ? '写入服务端配置，首页与登录页共享。' : '写入服务端配置，首页与登录页共享。当前等级只能查看。'}</p>
        </div>
        {canWrite && <button type="submit" className="button button--primary" disabled={busy}>{busy ? '保存中…' : '保存背景'}</button>}
      </header>
      <fieldset className="appearance-fieldset" disabled={!canWrite}>

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
                  onChange={() => setSettings(current => current && ({ ...current, mode: option.value }))}
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
              onChange={event => setSettings(current => current && ({ ...current, interval_seconds: Number(event.target.value) }))}
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
                  onChange={() => setSettings(current => current && ({ ...current, transition_effect: 'fade' }))}
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
                  onChange={() => setSettings(current => current && ({ ...current, transition_effect: 'slide' }))}
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
              onChange={event => setSettings(current => current && ({ ...current, fade_duration_ms: Number(event.target.value) }))}
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
      </fieldset>
    </form>}
  </div>
}
