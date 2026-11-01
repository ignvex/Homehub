import { useEffect, useState, type ReactNode } from 'react'
import { ConfirmDialog, Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import {
  buildPayload,
  emptySecrets,
  sectionLabels,
  settingsApi,
  type SafeConfig,
  type Secrets,
  type SettingsSection,
} from '@/lib/settings'
import { extensionHubApi, type ExtensionHubStatus } from '@/lib/extensionHub'

// ============================================================
// 设置页
//
// 每个模块一个面板、一个保存按钮。整页一起保存的问题是：
// 改一个 Gravatar 样式也要先把数据库连接读一遍、再写一遍，
// 而其中任何一处校验失败都会让整次保存回滚——用户分不清
// 是哪一块填错了。
//
// 后端对未提供的顶层段完全不动（指针为 nil），所以这里每个
// 面板只提交自己那一段。
// ============================================================

/** sectionFields 取出某个模块对应的配置片段。 */
function sectionFields(cfg: SafeConfig, section: SettingsSection): Partial<SafeConfig> {
  switch (section) {
    case 'connection': return { server: cfg.server, database: cfg.database }
    case 'auth': return { auth: cfg.auth }
    case 'log': return { log: cfg.log }
    case 'appearance': return { background: cfg.background }
    case 'gravatar': return { gravatar: cfg.gravatar }
    case 'bookmark': return { bookmark: cfg.bookmark }
    case 'rss': return { rss: cfg.rss }
    case 'extension': return { extension_hub: cfg.extension_hub }
  }
}

/** sectionSnapshot 把模块拍成字符串，用于判断「有没有改动」。 */
function sectionSnapshot(cfg: SafeConfig, section: SettingsSection): string {
  return JSON.stringify(sectionFields(cfg, section))
}

/** hasPendingSecret 判断该模块是否有待写入的密钥。 */
function hasPendingSecret(section: SettingsSection, secrets: Secrets): boolean {
  switch (section) {
    case 'connection': return secrets.database_password !== ''
    case 'auth': return secrets.auth_password !== '' || secrets.session_secret !== ''
    case 'gravatar': return secrets.gravatar_extension_token !== ''
    case 'rss': return secrets.rss_extension_token !== ''
    default: return false
  }
}

/** clearSecrets 清掉某模块的密钥输入框。 */
function clearSecrets(section: SettingsSection, secrets: Secrets): Secrets {
  switch (section) {
    case 'connection': return { ...secrets, database_password: '' }
    case 'auth': return { ...secrets, auth_password: '', session_secret: '' }
    case 'gravatar': return { ...secrets, gravatar_extension_token: '' }
    case 'rss': return { ...secrets, rss_extension_token: '' }
    default: return secrets
  }
}

export function SettingsPage() {
  const [mode, setMode] = useState<'form' | 'json'>('form')
  const [config, setConfig] = useState<SafeConfig | null>(null)
  const [draft, setDraft] = useState<SafeConfig | null>(null)
  const [jsonText, setJsonText] = useState('')
  const [secrets, setSecrets] = useState<Secrets>(emptySecrets)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState<SettingsSection | 'json' | ''>('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [confirmingJSON, setConfirmingJSON] = useState(false)
  // 对接状态来自后端探测（不读数据库），用来把「卡在哪一步」直接显示在面板上。
  const [hubStatus, setHubStatus] = useState<ExtensionHubStatus | null>(null)
  const [hubChecking, setHubChecking] = useState(false)

  useEffect(() => {
    settingsApi.get()
      .then(value => {
        setConfig(value)
        setDraft(structuredClone(value))
        setJsonText(JSON.stringify(value, null, 2))
      })
      .catch(cause => setError(cause instanceof Error ? cause.message : '无法读取配置'))
      .finally(() => setLoading(false))
  }, [])

  function patch(updater: (current: SafeConfig) => SafeConfig) {
    setDraft(current => (current ? updater(current) : current))
  }

  const probeHub = async () => {
    setHubChecking(true)
    try {
      setHubStatus(await extensionHubApi.status())
    } catch {
      // 探测失败不影响配置编辑；面板会停在「未知」。
      setHubStatus(null)
    } finally {
      setHubChecking(false)
    }
  }

  useEffect(() => { void probeHub() }, [])

  async function saveSection(section: SettingsSection) {
    if (!draft) return
    setSaving(section)
    try {
      await settingsApi.update(buildPayload(section, draft, secrets))
      const refreshed = await settingsApi.get()
      // 只把该模块的已保存值同步回来：其他模块里未保存的编辑不该被冲掉。
      const fresh = sectionFields(refreshed, section)
      setConfig(current => (current ? { ...current, ...fresh } : refreshed))
      setDraft(current => (current ? { ...current, ...fresh } : refreshed))
      setSecrets(current => clearSecrets(section, current))
      setMessage(`${sectionLabels[section]} 已保存并热重载`)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存失败')
    } finally {
      setSaving('')
    }
  }

  async function saveJSON() {
    setSaving('json')
    try {
      const parsed = JSON.parse(jsonText) as SafeConfig
      await settingsApi.update(parsed as unknown as Record<string, unknown>)
      const refreshed = await settingsApi.get()
      setConfig(refreshed)
      setDraft(structuredClone(refreshed))
      setJsonText(JSON.stringify(refreshed, null, 2))
      setMessage('配置已保存并热触发重载')
      setError('')
      setConfirmingJSON(false)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存失败：JSON 格式错误或字段无效')
      setConfirmingJSON(false)
    } finally {
      setSaving('')
    }
  }

  if (loading) return <PageState kind="loading" title="正在读取系统配置" />
  if (!draft || !config) return <PageState kind="error" title="无法读取系统配置" description={error} />

  const dirty = (section: SettingsSection) =>
    sectionSnapshot(draft, section) !== sectionSnapshot(config, section) || hasPendingSecret(section, secrets)

  const proxyNames = draft.proxy.map(item => item.name)

  return <div className="business-page">
    <div className="page-toolbar">
      <div>
        <p>系统配置</p>
        <h2>设置</h2>
      </div>
      <div className="toolbar-actions">
        <button className={`button ${mode === 'form' ? 'button--primary' : 'button--quiet'}`} onClick={() => setMode('form')}>表单编辑</button>
        <button className={`button ${mode === 'json' ? 'button--primary' : 'button--quiet'}`} onClick={() => setMode('json')}>JSON 编辑</button>
      </div>
    </div>

    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} onDismiss={() => setMessage('')} />

    {mode === 'json'
      ? <Section title="直接编辑 JSON" description="修改配置文件的 JSON 源，保存后立即热重载。格式错误会导致保存失败，不会写入。">
        <Notice message="秘密字段不会从服务器返回；这里看到的是脱敏后的配置，直接保存会保留原有密钥。" tone="info" />
        <textarea
          className="config-editor"
          value={jsonText}
          onChange={event => setJsonText(event.target.value)}
          spellCheck={false}
          style={{
            minHeight: 480,
            fontFamily: 'ui-monospace,monospace',
            fontSize: 13,
            lineHeight: 1.5,
            width: '100%',
            padding: 16,
            border: '1px solid var(--color-border)',
            borderRadius: 8,
            background: 'var(--color-input-bg)',
            color: 'var(--color-text)',
            resize: 'vertical',
          }}
        />
        <div className="form-actions">
          <button className="button button--quiet" onClick={() => { setJsonText(JSON.stringify(config, null, 2)); setError('') }}>放弃更改</button>
          <button className="button button--primary" disabled={saving === 'json'} onClick={() => setConfirmingJSON(true)}>
            {saving === 'json' ? '保存中...' : '保存 JSON'}
          </button>
        </div>
      </Section>
      : <div className="settings-sections">
        <Notice message="每个模块单独保存。秘密字段不会从服务器返回；留空表示保持当前值。" tone="info" />

        <Section
          section="connection"
          title="服务与数据库"
          description="修改监听地址或数据库连接可能导致当前连接中断。"
          dirty={dirty('connection')}
          saving={saving === 'connection'}
          onSave={() => void saveSection('connection')}
          onReset={() => { patch(current => ({ ...current, ...sectionFields(config, 'connection') })); setSecrets(current => clearSecrets('connection', current)) }}
        >
          <div className="form-grid form-grid--three">
            <Field label="服务主机">
              <input value={draft.server.host} onChange={event => patch(current => ({ ...current, server: { ...current.server, host: event.target.value } }))} />
            </Field>
            <Field label="服务端口">
              <input type="number" min="1" max="65535" value={draft.server.port} onChange={event => patch(current => ({ ...current, server: { ...current.server, port: Number(event.target.value) } }))} />
            </Field>
            <Field label="数据库主机">
              <input value={draft.database.host} onChange={event => patch(current => ({ ...current, database: { ...current.database, host: event.target.value } }))} />
            </Field>
            <Field label="数据库端口">
              <input type="number" min="1" max="65535" value={draft.database.port} onChange={event => patch(current => ({ ...current, database: { ...current.database, port: Number(event.target.value) } }))} />
            </Field>
            <Field label="数据库用户">
              <input value={draft.database.user} onChange={event => patch(current => ({ ...current, database: { ...current.database, user: event.target.value } }))} />
            </Field>
            <Field label="数据库名称">
              <input value={draft.database.dbname} onChange={event => patch(current => ({ ...current, database: { ...current.database, dbname: event.target.value } }))} />
            </Field>
            <Field label="SSL 模式">
              <select value={draft.database.sslmode} onChange={event => patch(current => ({ ...current, database: { ...current.database, sslmode: event.target.value } }))}>
                <option value="disable">disable</option>
                <option value="require">require</option>
                <option value="verify-ca">verify-ca</option>
                <option value="verify-full">verify-full</option>
              </select>
            </Field>
            <Field label="新数据库密码">
              <input type="password" value={secrets.database_password} placeholder="留空不修改"
                onChange={event => setSecrets(current => ({ ...current, database_password: event.target.value }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="auth"
          title="认证"
          description="登录用户名与密码。会话密钥改动会让所有现有会话失效。"
          dirty={dirty('auth')}
          saving={saving === 'auth'}
          onSave={() => void saveSection('auth')}
          onReset={() => { patch(current => ({ ...current, auth: config.auth })); setSecrets(current => clearSecrets('auth', current)) }}
        >
          <div className="form-grid form-grid--three">
            <Field label="初始用户名">
              <input value={draft.auth.username} onChange={event => patch(current => ({ ...current, auth: { username: event.target.value } }))} />
            </Field>
            <Field label="新初始密码">
              <input type="password" value={secrets.auth_password} placeholder="留空不修改"
                onChange={event => setSecrets(current => ({ ...current, auth_password: event.target.value }))} />
            </Field>
            <Field label="新会话密钥">
              <input type="password" minLength={32} value={secrets.session_secret} placeholder="留空不修改"
                onChange={event => setSecrets(current => ({ ...current, session_secret: event.target.value }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="log"
          title="日志"
          description="日志级别与输出格式，改动即时生效。"
          dirty={dirty('log')}
          saving={saving === 'log'}
          onSave={() => void saveSection('log')}
          onReset={() => patch(current => ({ ...current, log: config.log }))}
        >
          <div className="form-grid form-grid--three">
            <Field label="日志级别">
              <select value={draft.log.level} onChange={event => patch(current => ({ ...current, log: { ...current.log, level: event.target.value } }))}>
                <option value="debug">debug</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
            </Field>
            <Field label="日志格式">
              <select value={draft.log.format} onChange={event => patch(current => ({ ...current, log: { ...current.log, format: event.target.value } }))}>
                <option value="json">json</option>
                <option value="colored">colored</option>
              </select>
            </Field>
            <label className="choice-tile">
              <input type="checkbox" checked={draft.log.db_writer} onChange={event => patch(current => ({ ...current, log: { ...current.log, db_writer: event.target.checked } }))} />
              <span>日志写入数据库</span>
            </label>
          </div>
        </Section>

        <Section
          section="appearance"
          title="背景与外观"
          description="控制首页背景的显示模式、轮播间隔和过渡效果。"
          dirty={dirty('appearance')}
          saving={saving === 'appearance'}
          onSave={() => void saveSection('appearance')}
          onReset={() => patch(current => ({ ...current, background: config.background }))}
        >
          <div className="form-grid form-grid--three">
            <Field label="背景模式">
              <select value={draft.background.mode} onChange={event => patch(current => ({ ...current, background: { ...current.background, mode: event.target.value as 'single' | 'carousel' | 'random' } }))}>
                <option value="single">单图</option>
                <option value="carousel">顺序轮播</option>
                <option value="random">随机轮播</option>
              </select>
            </Field>
            <Field label="轮播间隔（秒）">
              <input type="number" min="1" value={draft.background.interval_seconds} onChange={event => patch(current => ({ ...current, background: { ...current.background, interval_seconds: Number(event.target.value) } }))} />
            </Field>
            <Field label="过渡效果">
              <select value={draft.background.transition_effect} onChange={event => patch(current => ({ ...current, background: { ...current.background, transition_effect: event.target.value as 'fade' | 'slide' } }))}>
                <option value="fade">淡入淡出</option>
                <option value="slide">滑动</option>
              </select>
            </Field>
            <Field label="过渡时长（毫秒）">
              <input type="number" min="0" max="10000" step="100" value={draft.background.fade_duration_ms} onChange={event => patch(current => ({ ...current, background: { ...current.background, fade_duration_ms: Number(event.target.value) } }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="gravatar"
          title="Gravatar 头像"
          description="头像可以直连 Gravatar，也可以让 Extension 代取（省掉本机的出网代理）。填了代取地址时优先后者。"
          dirty={dirty('gravatar')}
          saving={saving === 'gravatar'}
          onSave={() => void saveSection('gravatar')}
          onReset={() => { patch(current => ({ ...current, gravatar: config.gravatar })); setSecrets(current => clearSecrets('gravatar', current)) }}
        >
          <div className="form-grid form-grid--three">
            <label className="choice-tile">
              <input type="checkbox" checked={draft.gravatar.enabled} onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, enabled: event.target.checked } }))} />
              <span>启用 Gravatar</span>
            </label>
            <Field label="默认样式">
              <select value={draft.gravatar.default_style} onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, default_style: event.target.value } }))}>
                <option value="mp">神秘人</option>
                <option value="identicon">几何图案</option>
                <option value="monsterid">小怪物</option>
                <option value="wavatar">卡通脸</option>
                <option value="retro">复古</option>
                <option value="robohash">机器人</option>
              </select>
            </Field>
            <Field label="默认尺寸（像素）">
              <input type="number" min="16" max="512" value={draft.gravatar.default_size} onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, default_size: Number(event.target.value) } }))} />
            </Field>
            <Field label="内容评级">
              <select value={draft.gravatar.rating} onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, rating: event.target.value } }))}>
                <option value="g">G - 大众</option>
                <option value="pg">PG - 辅导</option>
                <option value="r">R - 限制</option>
                <option value="x">X - 严格</option>
              </select>
            </Field>
            <Field label="直连代理">
              <select
                value={draft.gravatar.proxy_names[0] ?? ''}
                onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, proxy_names: event.target.value ? [event.target.value] : [] } }))}
              >
                <option value="">不使用代理</option>
                {proxyNames.map(name => <option key={name} value={name}>{name}</option>)}
              </select>
            </Field>
            <Field label="请求超时（秒）">
              <input type="number" min="1" max="120" value={draft.gravatar.timeout_seconds}
                onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, timeout_seconds: Number(event.target.value) } }))} />
            </Field>
            <Field label="头像代取地址（可选）">
              <input value={draft.gravatar.extension_url} placeholder="例如 http://ext.local:8090，留空则直连 Gravatar"
                onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, extension_url: event.target.value } }))} />
            </Field>
            <Field label="代取 Token">
              <input type="password" autoComplete="off"
                value={secrets.gravatar_extension_token}
                placeholder={draft.gravatar.extension_token_set ? '已配置，留空不修改' : '未配置'}
                onChange={event => setSecrets(current => ({ ...current, gravatar_extension_token: event.target.value }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="bookmark"
          title="书签"
          description="书签图标抓取使用的代理链。"
          dirty={dirty('bookmark')}
          saving={saving === 'bookmark'}
          onSave={() => void saveSection('bookmark')}
          onReset={() => patch(current => ({ ...current, bookmark: config.bookmark }))}
        >
          <div className="form-grid form-grid--three">
            <Field label="代理链（逗号分隔）">
              <input
                value={draft.bookmark.proxy_names.join(',')}
                placeholder="router,backup"
                onChange={event => patch(current => ({
                  ...current,
                  bookmark: { proxy_names: event.target.value.split(',').map(item => item.trim()).filter(Boolean) },
                }))}
              />
            </Field>
          </div>
        </Section>

        <Section
          section="rss"
          title="RSS"
          description="订阅抓取与图片缓存。填了代取地址时抓取交给 Extension，本机只存结果。"
          dirty={dirty('rss')}
          saving={saving === 'rss'}
          onSave={() => void saveSection('rss')}
          onReset={() => { patch(current => ({ ...current, rss: config.rss })); setSecrets(current => clearSecrets('rss', current)) }}
        >
          <div className="form-grid form-grid--three">
            <Field label="保留条数">
              <input type="number" min="1" value={draft.rss.retention_count} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, retention_count: Number(event.target.value) } }))} />
            </Field>
            <Field label="后台抓取间隔（秒）">
              <input type="number" min="60" value={draft.rss.background_fetch_interval} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, background_fetch_interval: Number(event.target.value) } }))} />
            </Field>
            <Field label="抓取并发">
              <input type="number" min="1" max="32" value={draft.rss.fetch_concurrency} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, fetch_concurrency: Number(event.target.value) } }))} />
            </Field>
            <Field label="单源超时（秒）">
              <input type="number" min="5" max="600" value={draft.rss.fetch_timeout_seconds} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, fetch_timeout_seconds: Number(event.target.value) } }))} />
            </Field>
            <Field label="代理链（逗号分隔）">
              <input
                value={draft.rss.proxy_names.join(',')}
                placeholder="router,backup"
                onChange={event => patch(current => ({
                  ...current,
                  rss: {
                    ...current.rss,
                    proxy_names: event.target.value.split(',').map(item => item.trim()).filter(Boolean),
                  },
                }))}
              />
            </Field>
            <label className="choice-tile">
              <input type="checkbox" checked={draft.rss.background_fetch_enabled} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, background_fetch_enabled: event.target.checked } }))} />
              <span>启用后台抓取</span>
            </label>
            <label className="choice-tile">
              <input type="checkbox" checked={draft.rss.image_cache_enabled} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, image_cache_enabled: event.target.checked } }))} />
              <span>缓存条目图片</span>
            </label>
            <label className="choice-tile">
              <input type="checkbox" checked={draft.rss.image_cleanup_enabled} onChange={event => patch(current => ({ ...current, rss: { ...current.rss, image_cleanup_enabled: event.target.checked } }))} />
              <span>自动清理无引用图片</span>
            </label>
            <Field label="抓取代取地址（可选）">
              <input value={draft.rss.extension_url} placeholder="例如 http://ext.local:8090，留空则由本机抓取"
                onChange={event => patch(current => ({ ...current, rss: { ...current.rss, extension_url: event.target.value } }))} />
            </Field>
            <Field label="代取 Token">
              <input type="password" autoComplete="off"
                value={secrets.rss_extension_token}
                placeholder={draft.rss.extension_token_set ? '已配置，留空不修改' : '未配置'}
                onChange={event => setSecrets(current => ({ ...current, rss_extension_token: event.target.value }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="extension"
          title="订阅镜像与登录密钥（Extension 签名通道）"
          description="上面两个「代取地址」是让 Extension 帮你抓取；这里才是把订阅推给它、为用户签发登录密钥。两者互不依赖。"
          dirty={dirty('extension')}
          saving={saving === 'extension'}
          onSave={() => void saveSection('extension')}
          onReset={() => patch(current => ({ ...current, extension_hub: config.extension_hub }))}
        >
          <div className="form-grid form-grid--three">
            <label className="choice-tile">
              <input type="checkbox" checked={draft.extension_hub.enabled}
                onChange={event => patch(current => ({ ...current, extension_hub: { ...current.extension_hub, enabled: event.target.checked } }))} />
              <span>启用签名通道</span>
            </label>
            <Field label="API 根地址">
              <input value={draft.extension_hub.base_url} placeholder="https://ext.example.com/api/v1"
                onChange={event => patch(current => ({ ...current, extension_hub: { ...current.extension_hub, base_url: event.target.value } }))} />
            </Field>
            <Field label="宿主私钥路径">
              <input value={draft.extension_hub.private_key_path} placeholder="/etc/homehub/extension-hub.key"
                onChange={event => patch(current => ({ ...current, extension_hub: { ...current.extension_hub, private_key_path: event.target.value } }))} />
            </Field>
            <Field label="请求超时（秒）">
              <input type="number" min="1" max="300" value={draft.extension_hub.timeout_seconds}
                onChange={event => patch(current => ({ ...current, extension_hub: { ...current.extension_hub, timeout_seconds: Number(event.target.value) } }))} />
            </Field>
          </div>
          <div style={{ display: 'grid', gap: 12, marginTop: 4 }}>
            <div className="metric-row" style={{ alignItems: 'center' }}>
              <div className="metric-label">
                <span>当前状态</span>
                <small>{hubStatus?.base_url || '未配置对接地址'}</small>
              </div>
              <div className="metric-value" style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <StatusBadge tone={hubTone(hubStatus)}>{hubLabel(hubStatus)}</StatusBadge>
                <button type="button" className="button button--quiet" disabled={hubChecking} onClick={() => void probeHub()}>
                  {hubChecking ? '探测中...' : '重新探测'}
                </button>
              </div>
            </div>

            {hubStatus?.error_message && <Notice message={hubStatus.error_message} tone="error" />}

            {hubStatus?.public_key && <div className="metric-row">
              <div className="metric-label">
                <span>宿主公钥</span>
                <small>把这一串填进 Extension 的 auth.hub.public_key_path 对应文件</small>
              </div>
              <div className="metric-value" style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <code style={{ fontSize: 'var(--text-xs)', wordBreak: 'break-all' }}>{hubStatus.public_key}</code>
                <button
                  type="button"
                  className="button button--quiet"
                  onClick={() => {
                    // 闭包里不做类型窄化，先把值取出来。
                    const value = hubStatus.public_key ?? ''
                    void navigator.clipboard.writeText(value)
                      .then(() => { setMessage('公钥已复制'); setError('') })
                      .catch(() => setError('复制失败，请手动选中'))
                  }}
                >
                  复制
                </button>
              </div>
            </div>}

            <div style={{ background: 'var(--color-surface-muted)', borderRadius: 6, padding: 16, fontSize: 12, lineHeight: 1.7 }}>
              <p style={{ margin: '0 0 8px', fontWeight: 600 }}>接入总共三步</p>
              <ol style={{ margin: 0, paddingLeft: 18 }}>
                <li>在 HomeHub 主机生成密钥：<code>openssl genpkey -algorithm ed25519 -out /etc/homehub/extension-hub.key</code></li>
                <li>导出公钥：<code>openssl pkey -in … -pubout -out hub.pub</code>，放到 Extension 主机的 <code>auth.hub.public_key_path</code>，重启它</li>
                <li>回到这里填 <strong>API 根地址</strong>（必须带 <code>/api/v1</code>）与<strong>私钥路径</strong>，勾上启用并保存，然后点「重新探测」</li>
              </ol>
              <p style={{ margin: '8px 0 0', color: 'var(--color-text-secondary)' }}>
                Extension 是单实例：一个 HomeHub 对接一个 Extension，不支持在这里添加多个。
              </p>
            </div>
          </div>
        </Section>
      </div>}

    <ConfirmDialog
      open={confirmingJSON}
      title="保存系统配置"
      description="配置会写入 config.json 并立即热重载。监听地址、数据库、认证或代理设置错误可能导致服务不可用。"
      confirmLabel="保存并重载"
      busy={saving === 'json'}
      onClose={() => setConfirmingJSON(false)}
      onConfirm={() => void saveJSON()}
    />
  </div>
}

// ------------------------------------------------------------
// 局部组件
// ------------------------------------------------------------

/** hubLabel 把四层探测结果压成一句话。 */
function hubLabel(status: ExtensionHubStatus | null): string {
  if (!status) return '未探测'
  if (!status.enabled) return '未启用'
  if (!status.configured) return '私钥读取失败'
  if (!status.reachable) return '网络不可达'
  if (!status.authenticated) return '签名未通过'
  if (status.ready === false) return '依赖未就绪'
  if (status.rss_available) return `已连通 · ${status.subscription_count} 条订阅`
  return '已连通（RSS 模块关闭）'
}

/** hubTone 给出状态徽章色调。 */
function hubTone(status: ExtensionHubStatus | null): 'neutral' | 'warning' | 'danger' | 'success' {
  if (!status) return 'neutral'
  if (!status.enabled) return 'neutral'
  if (!status.configured) return 'danger'
  if (!status.reachable) return 'danger'
  if (!status.authenticated) return 'danger'
  if (status.ready === false) return 'warning'
  return 'success'
}

/** Section 是一个可独立保存的配置面板。 */
function Section({ title, description, children, section, dirty, saving, onSave, onReset }: {
  title: string
  description?: string
  children: ReactNode
  section?: SettingsSection
  dirty?: boolean
  saving?: boolean
  onSave?: () => void
  onReset?: () => void
}) {
  return <section className="data-panel">
    <header className="data-panel-head">
      <div>
        <h2>{title}</h2>
        {description && <p>{description}</p>}
      </div>
      {section && <div className="toolbar-actions">
        {dirty && <span style={{ fontSize: 11, color: 'var(--color-warning)' }}>未保存</span>}
        <button className="button button--quiet" type="button" disabled={!dirty || saving} onClick={onReset}>还原</button>
        <button className="button button--primary" type="button" disabled={!dirty || saving} onClick={onSave}>
          {saving ? '保存中...' : '保存'}
        </button>
      </div>}
    </header>
    <div className="modal-body">{children}</div>
  </section>
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="form-field">{label}{children}</label>
}
