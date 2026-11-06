import { useCallback, useEffect, useState, type FormEvent, type InputHTMLAttributes, type ReactNode } from 'react'
import { ConfirmDialog, Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { copyText } from '@/lib/clipboard'
import { useLeaveGuard } from '@/lib/navigation'
import {
  buildPayload,
  emptySecrets,
  sectionLabels,
  settingsApi,
  type SafeConfig,
  type Secrets,
  type SettingsSection,
} from '@/lib/settings'
import { extensionHubApi, hubSummary, type ExtensionHubStatus, type KeyInfo } from '@/lib/extensionHub'

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

/** 设置页上实际渲染的模块（bookmark 段目前没有面板）。 */
const allSections: SettingsSection[] = ['connection', 'auth', 'log', 'appearance', 'gravatar', 'rss', 'balance', 'extension']

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
    case 'balance': return { balance_monitor: cfg.balance_monitor }
    case 'extension': return { extension: cfg.extension }
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
    default: return false
  }
}

/** clearSecrets 清掉某模块的密钥输入框。 */
function clearSecrets(section: SettingsSection, secrets: Secrets): Secrets {
  switch (section) {
    case 'connection': return { ...secrets, database_password: '' }
    case 'auth': return { ...secrets, auth_password: '', session_secret: '' }
    default: return secrets
  }
}

export function SettingsPage() {
  const [mode, setMode] = useState<'form' | 'json'>('form')
  const [config, setConfig] = useState<SafeConfig | null>(null)
  const [draft, setDraft] = useState<SafeConfig | null>(null)
  const [jsonText, setJsonText] = useState('')
  const [secrets, setSecrets] = useState<Secrets>(emptySecrets)
  // jsonBase 是进入 JSON 模式时生成的文本，用来判断 JSON 草稿有没有改动。
  const [jsonBase, setJsonBase] = useState('')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [saving, setSaving] = useState<SettingsSection | 'json' | ''>('')
  const notice = useNotice()
  const [confirmingJSON, setConfirmingJSON] = useState(false)
  const [jsonError, setJsonError] = useState('')
  const [keyDeleteError, setKeyDeleteError] = useState('')
  // 对接状态来自后端探测（不读数据库），用来把「卡在哪一步」直接显示在面板上。
  const [hubStatus, setHubStatus] = useState<ExtensionHubStatus | null>(null)
  const [hubChecking, setHubChecking] = useState(false)
  const [keyInfo, setKeyInfo] = useState<KeyInfo | null>(null)
  const [keyDraft, setKeyDraft] = useState('')
  const [keyBusy, setKeyBusy] = useState(false)
  const [confirmingKeyDelete, setConfirmingKeyDelete] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setLoadError('')
    settingsApi.get()
      .then(value => {
        setConfig(value)
        setDraft(structuredClone(value))
      })
      .catch(cause => setLoadError(errorMessage(cause, '无法读取配置')))
      .finally(() => setLoading(false))
  }, [])
  useEffect(load, [load])

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

  const loadKeyInfo = async () => {
    try {
      setKeyInfo(await extensionHubApi.getKey())
    } catch {
      setKeyInfo(null)
    }
  }

  async function saveKey() {
    if (!keyDraft.trim()) return
    setKeyBusy(true)
    try {
      const info = await extensionHubApi.setKey(keyDraft)
      setKeyInfo(info)
      setKeyDraft('')
      notice.success('私钥已写入 config.json（单行 base64）')
      await probeHub()
    } catch (cause) {
      notice.error(errorMessage(cause, '保存私钥失败'))
    } finally {
      setKeyBusy(false)
    }
  }

  async function deleteKey() {
    setKeyBusy(true)
    setKeyDeleteError('')
    try {
      const result = await extensionHubApi.deleteKey()
      notice.success(result.deleted ? '已移除 config.json 里的私钥' : 'config.json 里本来就没有私钥')
      setConfirmingKeyDelete(false)
      await loadKeyInfo()
      await probeHub()
    } catch (cause) {
      setKeyDeleteError(errorMessage(cause, '删除私钥失败'))
    } finally {
      setKeyBusy(false)
    }
  }

  useEffect(() => { void probeHub(); void loadKeyInfo() }, [])

  async function saveSection(section: SettingsSection, event?: FormEvent) {
    event?.preventDefault()
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
      notice.success(`${sectionLabels[section]} 已保存并热重载`)
    } catch (cause) {
      notice.error(errorMessage(cause, '保存失败'))
    } finally {
      setSaving('')
    }
  }

  async function saveJSON() {
    setSaving('json')
    setJsonError('')
    try {
      const parsed = parseConfigJSON(jsonText)
      // 编辑器里是前端形状（background、private_key_set…），不能原样 PUT：
      // 后端不认识 background，会把背景改动静默丢掉。按模块转换成后端形状再合并。
      const merged: SafeConfig = draft ? { ...draft, ...parsed } : parsed
      // proxy 没有表单面板，只能在这里改；后端按名字合并条目，脱敏掉的代理地址会保留。
      const payload = Object.assign({ proxy: merged.proxy }, ...allSections.map(section => buildPayload(section, merged, emptySecrets)))
      await settingsApi.update(payload)
      const refreshed = await settingsApi.get()
      setConfig(refreshed)
      setDraft(structuredClone(refreshed))
      const text = JSON.stringify(refreshed, null, 2)
      setJsonText(text)
      setJsonBase(text)
      notice.success('配置已保存并热触发重载')
      setConfirmingJSON(false)
    } catch (cause) {
      // 确认弹窗处于 top layer，错误画在弹窗里，用户可以关掉弹窗继续改 JSON。
      setJsonError(errorMessage(cause, '保存失败：JSON 格式错误或字段无效'))
    } finally {
      setSaving('')
    }
  }

  const dirty = (section: SettingsSection) =>
    !!draft && !!config && (sectionSnapshot(draft, section) !== sectionSnapshot(config, section) || hasPendingSecret(section, secrets))
  const jsonDirty = mode === 'json' && jsonText !== jsonBase
  const anyDirty = jsonDirty || keyDraft.trim() !== '' || allSections.some(dirty)
  // 有未保存内容时：客户端导航前确认，刷新或关闭标签页时由浏览器提示。
  useLeaveGuard(anyDirty, '设置有未保存的更改，确定要离开吗？')

  /** 切换编辑模式：始终以当前草稿为准，未保存的编辑不会丢失。 */
  function switchMode(next: 'form' | 'json') {
    if (next === mode || !draft) return
    if (next === 'json') {
      const text = JSON.stringify(draft, null, 2)
      setJsonText(text)
      setJsonBase(text)
      setMode('json')
      return
    }
    if (jsonText === jsonBase) { setMode('form'); return }
    try {
      const parsed = parseConfigJSON(jsonText)
      setDraft(current => (current ? { ...current, ...parsed } : current))
      setMode('form')
    } catch (cause) {
      if (window.confirm(`JSON 无法解析（${errorMessage(cause, '格式错误')}）。放弃 JSON 中的修改并回到表单吗？`)) setMode('form')
    }
  }

  /**
   * 「还原」本身就是明确的放弃操作，不再弹 window.confirm：浏览器会静默拦截
   * 原生对话框（用户勾过「阻止此页面创建更多对话框」后尤其如此），表现为
   * 「点了还原没反应」。还原只影响本模块的草稿，提示一下即可。
   */
  function confirmDiscard(action: () => void) {
    action()
    notice.info('已还原为当前生效的配置')
  }

  if (loading && !config) return <PageState kind="loading" title="正在读取系统配置" />
  if (!draft || !config) return <PageState kind="error" title="无法读取系统配置" description={loadError} action={<button type="button" className="button button--primary" onClick={load}>重试</button>} />

  const proxyNames = draft.proxy.entries.map(item => item.name)

  return <div className="business-page">
    <div className="page-toolbar page-toolbar--end">
      <div className="toolbar-actions" role="group" aria-label="编辑方式">
        <button type="button" aria-pressed={mode === 'form'} className={`button ${mode === 'form' ? 'button--primary' : 'button--quiet'}`} onClick={() => switchMode('form')}>表单编辑</button>
        <button type="button" aria-pressed={mode === 'json'} className={`button ${mode === 'json' ? 'button--primary' : 'button--quiet'}`} onClick={() => switchMode('json')}>JSON 编辑</button>
      </div>
    </div>

    {notice.element}

    {mode === 'json'
      ? <Section title="直接编辑 JSON" description="修改配置文件的 JSON 源，保存后立即热重载。格式错误会导致保存失败，不会写入。">
        <Notice message="秘密字段不会从服务器返回；这里看到的是脱敏后的配置，直接保存会保留原有密钥。" tone="info" />
        <label className="form-field settings-json">
          <span className="visually-hidden">配置 JSON</span>
          <textarea
            className="code-textarea code-textarea--tall"
            value={jsonText}
            onChange={event => setJsonText(event.target.value)}
            spellCheck={false}
          />
        </label>
        <div className="form-actions">
          <button type="button" className="button button--quiet" disabled={!jsonDirty} onClick={() => confirmDiscard(() => setJsonText(jsonBase))}>放弃更改</button>
          <button type="button" className="button button--primary" disabled={saving === 'json'} onClick={() => { setJsonError(''); setConfirmingJSON(true) }}>
            {saving === 'json' ? '保存中…' : '保存 JSON'}
          </button>
        </div>
      </Section>
      : <div className="settings-sections">
        <Notice message="每个模块单独保存。秘密字段不会从服务器返回；留空表示保持当前值。" tone="info" />

        <Section
          section="connection"
          title="服务与数据库"
          dirty={dirty('connection')}
          saving={saving === 'connection'}
          onSave={event => void saveSection('connection', event)}
          onReset={() => confirmDiscard(() => { patch(current => ({ ...current, ...sectionFields(config, 'connection') })); setSecrets(current => clearSecrets('connection', current)) })}
        >
          <div className="form-grid form-grid--three">
            <Field label="服务主机">
              <input value={draft.server.host} onChange={event => patch(current => ({ ...current, server: { ...current.server, host: event.target.value } }))} />
            </Field>
            <Field label="服务端口">
              <NumberInput min="1" max="65535" value={draft.server.port} onChange={value => patch(current => ({ ...current, server: { ...current.server, port: value } }))} />
            </Field>
            <Field label="数据库主机">
              <input value={draft.database.host} onChange={event => patch(current => ({ ...current, database: { ...current.database, host: event.target.value } }))} />
            </Field>
            <Field label="数据库端口">
              <NumberInput min="1" max="65535" value={draft.database.port} onChange={value => patch(current => ({ ...current, database: { ...current.database, port: value } }))} />
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
            <Field label="可信反向代理" wide>
              <LineListInput value={draft.server.trusted_proxies} placeholder={'172.17.0.1\n10.0.0.0/8'}
                onChange={value => patch(current => ({ ...current, server: { ...current.server, trusted_proxies: value } }))} />
              <span className="settings-hint">部署在反向代理后面时填写代理的 IP 或 CIDR，一行一个；留空表示不信任任何转发头。服务地址、端口与此项修改后需重启生效。</span>
            </Field>
          </div>
        </Section>

        <Section
          section="auth"
          title="认证"
          description="登录用户名与密码。会话密钥改动会让所有现有会话失效。"
          dirty={dirty('auth')}
          saving={saving === 'auth'}
          onSave={event => void saveSection('auth', event)}
          onReset={() => confirmDiscard(() => { patch(current => ({ ...current, auth: config.auth })); setSecrets(current => clearSecrets('auth', current)) })}
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
          description="日志级别、输出格式与数据库日志保留期，改动即时生效。"
          dirty={dirty('log')}
          saving={saving === 'log'}
          onSave={event => void saveSection('log', event)}
          onReset={() => confirmDiscard(() => patch(current => ({ ...current, log: config.log })))}
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
            <Field label="数据库日志保留天数">
              <NumberInput min="0" max="3650" step="1" value={draft.log.retention_days}
                onChange={value => patch(current => ({ ...current, log: { ...current.log, retention_days: value } }))} />
              <span className="settings-hint">超过天数的数据库日志会被自动删除；0 表示永久保留。</span>
            </Field>
          </div>
        </Section>

        <Section
          section="appearance"
          title="背景与外观"
          description="控制首页背景的显示模式、轮播间隔和过渡效果。"
          dirty={dirty('appearance')}
          saving={saving === 'appearance'}
          onSave={event => void saveSection('appearance', event)}
          onReset={() => confirmDiscard(() => patch(current => ({ ...current, background: config.background })))}
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
              <NumberInput min="1" value={draft.background.interval_seconds} onChange={value => patch(current => ({ ...current, background: { ...current.background, interval_seconds: value } }))} />
            </Field>
            <Field label="过渡效果">
              <select value={draft.background.transition_effect} onChange={event => patch(current => ({ ...current, background: { ...current.background, transition_effect: event.target.value as 'fade' | 'slide' } }))}>
                <option value="fade">淡入淡出</option>
                <option value="slide">滑动</option>
              </select>
            </Field>
            <Field label="过渡时长（毫秒）">
              <NumberInput min="0" max="10000" step="100" value={draft.background.fade_duration_ms} onChange={value => patch(current => ({ ...current, background: { ...current.background, fade_duration_ms: value } }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="gravatar"
          title="Gravatar 头像"
          description="头像直接向 Gravatar 取（需要代理时用下面的代理链），结果缓存到本地。"
          dirty={dirty('gravatar')}
          saving={saving === 'gravatar'}
          onSave={event => void saveSection('gravatar', event)}
          onReset={() => confirmDiscard(() => { patch(current => ({ ...current, gravatar: config.gravatar })); setSecrets(current => clearSecrets('gravatar', current)) })}
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
              <NumberInput min="16" max="512" value={draft.gravatar.default_size} onChange={value => patch(current => ({ ...current, gravatar: { ...current.gravatar, default_size: value } }))} />
            </Field>
            <Field label="内容评级">
              <select value={draft.gravatar.rating} onChange={event => patch(current => ({ ...current, gravatar: { ...current.gravatar, rating: event.target.value } }))}>
                <option value="g">G - 大众</option>
                <option value="pg">PG - 辅导</option>
                <option value="r">R - 限制</option>
                <option value="x">X - 严格</option>
              </select>
            </Field>
            <Field label="出网方式">
              <small>
                {draft.extension.enabled
                  ? '经 Extension 代理，使用 extension 段配置'
                  : `直连，走全局代理「${draft.proxy.default.join(',') || '无'}」`}
              </small>
            </Field>
            <Field label="请求超时（秒）">
              <NumberInput min="1" max="120" value={draft.gravatar.timeout_seconds}
                onChange={value => patch(current => ({ ...current, gravatar: { ...current.gravatar, timeout_seconds: value } }))} />
            </Field>
          </div>
        </Section>

        <Section
          section="rss"
          title="RSS"
          description="订阅由本机按间隔抓取，条目与图片都缓存在这里。要让 Extension 抓取，请用它自己的界面或在「订阅镜像」里推送。"
          dirty={dirty('rss')}
          saving={saving === 'rss'}
          onSave={event => void saveSection('rss', event)}
          onReset={() => confirmDiscard(() => { patch(current => ({ ...current, rss: config.rss })); setSecrets(current => clearSecrets('rss', current)) })}
        >
          <div className="form-grid form-grid--three">
            <label className="choice-tile">
              <input type="checkbox" checked={draft.rss.retention_unlimited}
                onChange={event => patch(current => ({ ...current, rss: { ...current.rss, retention_unlimited: event.target.checked } }))} />
              <span>无限存储（不自动删旧条目）</span>
            </label>
            <Field label="保留条数">
              <NumberInput min="1" value={draft.rss.retention_count}
                disabled={draft.rss.retention_unlimited}
                onChange={value => patch(current => ({ ...current, rss: { ...current.rss, retention_count: value } }))} />
              <small>{draft.rss.retention_unlimited
                ? '无限存储已开启，这一项不生效'
                : '每个订阅源最多保留的条目数，超出的从最旧的开始删'}</small>
            </Field>
            <Field label="后台抓取间隔（秒）">
              <NumberInput min="60" value={draft.rss.background_fetch_interval} onChange={value => patch(current => ({ ...current, rss: { ...current.rss, background_fetch_interval: value } }))} />
            </Field>
            <Field label="抓取并发">
              <NumberInput min="1" max="32" value={draft.rss.fetch_concurrency} onChange={value => patch(current => ({ ...current, rss: { ...current.rss, fetch_concurrency: value } }))} />
            </Field>
            <Field label="单源超时（秒）">
              <NumberInput min="5" max="600" value={draft.rss.fetch_timeout_seconds} onChange={value => patch(current => ({ ...current, rss: { ...current.rss, fetch_timeout_seconds: value } }))} />
            </Field>
            <Field label="出网方式">
              <small>
                {draft.extension.enabled && draft.extension.base_url
                  ? '经 Extension 抓取'
                  : `本机抓取，走全局代理「${draft.proxy.default.join(',') || '无'}」`}
              </small>
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
            <Field label="User-Agent">
              <input type="text" placeholder="留空则用内置 Chrome UA" value={draft.rss.user_agent}
                onChange={event => patch(current => ({ ...current, rss: { ...current.rss, user_agent: event.target.value } }))} />
              <small>部分源会按 UA 返回不同内容；留空即可</small>
            </Field>
            <Field label="图片并发">
              <NumberInput min="1" max="32" value={draft.rss.image_cache_concurrency}
                disabled={!draft.rss.image_cache_enabled}
                onChange={value => patch(current => ({ ...current, rss: { ...current.rss, image_cache_concurrency: value } }))} />
            </Field>
            <Field label="单张图片上限（MB）">
              <NumberInput min="1" max="2048" value={Math.round(draft.rss.image_max_bytes / 1048576)}
                disabled={!draft.rss.image_cache_enabled}
                onChange={value => patch(current => ({ ...current, rss: { ...current.rss, image_max_bytes: value * 1048576 } }))} />
              <small>超过上限的图直接丢弃，不截断</small>
            </Field>
            <Field label="无引用图片保留（天）">
              <NumberInput min="1" max="3650" value={draft.rss.image_retention_days}
                disabled={!draft.rss.image_cleanup_enabled}
                onChange={value => patch(current => ({ ...current, rss: { ...current.rss, image_retention_days: value } }))} />
            </Field>
            <Field label="图片清理间隔（小时）">
              <NumberInput min="1" max="168" value={draft.rss.image_cleanup_interval_hours}
                disabled={!draft.rss.image_cleanup_enabled}
                onChange={value => patch(current => ({ ...current, rss: { ...current.rss, image_cleanup_interval_hours: value } }))} />
            </Field>
            <label className="choice-tile">
              <input type="checkbox" checked={draft.rss.image_allow_private_hosts}
                onChange={event => patch(current => ({ ...current, rss: { ...current.rss, image_allow_private_hosts: event.target.checked } }))} />
              <span>允许缓存内网图片（降低 SSRF 防护）</span>
            </label>
          </div>
        </Section>

        <Section
          section="balance"
          title="余额监控"
          description="后台按间隔查询各监控点的余额。监控点本身在「余额监控」页面管理。"
          dirty={dirty('balance')}
          saving={saving === 'balance'}
          onSave={event => void saveSection('balance', event)}
          onReset={() => confirmDiscard(() => patch(current => ({ ...current, balance_monitor: config.balance_monitor })))}
        >
          <div className="form-grid form-grid--three">
            <label className="choice-tile">
              <input type="checkbox" checked={draft.balance_monitor.enabled}
                onChange={event => patch(current => ({ ...current, balance_monitor: { ...current.balance_monitor, enabled: event.target.checked } }))} />
              <span>启用后台刷新</span>
            </label>
            <Field label="刷新间隔（秒）">
              <NumberInput min="10" max="3600" value={draft.balance_monitor.refresh_interval_seconds}
                onChange={value => patch(current => ({ ...current, balance_monitor: { ...current.balance_monitor, refresh_interval_seconds: value } }))} />
            </Field>
            <label className="choice-tile form-field--wide">
              <input type="checkbox" checked={draft.balance_monitor.allow_private_hosts}
                onChange={event => patch(current => ({ ...current, balance_monitor: { ...current.balance_monitor, allow_private_hosts: event.target.checked } }))} />
              <span>允许余额监控访问内网/回环地址。仅在自建中转服务时开启。</span>
            </label>
          </div>
        </Section>

        <Section
          section="extension"
          title="Extension 连接"
          description="订阅镜像、抓取代理、头像代理与登录密钥共用这一份地址与密钥。"
          dirty={dirty('extension')}
          saving={saving === 'extension'}
          onSave={event => void saveSection('extension', event)}
          onReset={() => confirmDiscard(() => patch(current => ({ ...current, extension: config.extension })))}
        >
          {/* 连接：地址与网络路径 */}
          <div className="form-block">
            <div className="form-block-head">
              <h3>连接</h3>
              <p>/hub/*、/fetch、/avatar 共用这一份地址与密钥。</p>
            </div>
            <div className="form-grid form-grid--three">
              <label className="choice-tile">
                <input type="checkbox" checked={draft.extension.enabled}
                  onChange={event => patch(current => setExtensionEnabled(current, event.target.checked))} />
                <span>启用 Extension</span>
              </label>
              <Field label="API 根地址">
                <input value={draft.extension.base_url} placeholder="https://ext.example.com/api/v1"
                  onChange={event => patch(current => ({ ...current, extension: { ...current.extension, base_url: event.target.value } }))} />
              </Field>
              <Field label="连接代理">
                <select
                  value={draft.extension.proxy_names[0] ?? ''}
                  onChange={event => patch(current => ({ ...current, extension: { ...current.extension, proxy_names: event.target.value ? [event.target.value] : [] } }))}
                >
                  <option value="">直连（默认）</option>
                  {proxyNames.map(name => <option key={name} value={name}>{name}</option>)}
                </select>
              </Field>
              <Field label="请求超时（秒）">
                <NumberInput min="1" max="300" value={draft.extension.timeout_seconds}
                  onChange={value => patch(current => ({ ...current, extension: { ...current.extension, timeout_seconds: value } }))} />
              </Field>
            </div>
          </div>

          {/* 能力：每一条都会改变流量去哪 */}
          <div className="form-block">
            <div className="form-block-head">
              <h3>能力</h3>
              <p>三条通路可以分别开关。对端没实现的不要打开——右侧会显示探测结果。</p>
            </div>
            <div className="settings-stack settings-stack--tight">
              <label className="feature-row">
                <input type="checkbox" checked={draft.extension.features.hub}
                  onChange={event => patch(current => setExtensionFeature(current, 'hub', event.target.checked))} />
                <span className="feature-row-body">
                  <strong>订阅镜像与访问密钥（/hub/*）
                    <CapabilityHint available={null} />
                  </strong>
                  <small>把订阅推给 Extension、签发登录密钥。条目落在对端，宿主这侧不再抓。</small>
                </span>
              </label>
              <label className="feature-row">
                <input type="checkbox" checked={draft.extension.features.fetch}
                  onChange={event => patch(current => setExtensionFeature(current, 'fetch', event.target.checked))} />
                <span className="feature-row-body">
                  <strong>RSS 抓取代理（/fetch）
                    <CapabilityHint available={hubStatus?.fetch_available} />
                  </strong>
                  <small>只把出网这一步外包，条目仍解析回本地库。会占用 rss.fetch_timeout_seconds 的预算。</small>
                </span>
              </label>
              <label className="feature-row">
                <input type="checkbox" checked={draft.extension.features.avatar}
                  onChange={event => patch(current => setExtensionFeature(current, 'avatar', event.target.checked))} />
                <span className="feature-row-body">
                  <strong>头像代理（/avatar）
                    <CapabilityHint available={hubStatus?.avatar_available} />
                  </strong>
                  <small>头像下载交给对端。关闭时直连 Gravatar，走全局代理。</small>
                </span>
              </label>
            </div>
          </div>

          {/* 状态：一次探测的四层结论 */}
          <div className="form-block">
            <div className="form-block-head">
              <h3>当前状态</h3>
              <p>四层依次是：配置齐全 → HTTP 可达 → 签名通过 → RSS 模块可用。</p>
            </div>
            <div className="settings-stack">
            <div className="metric-row">
              <div className="metric-label">
                <span>探测结果</span>
                <small>{hubStatus?.base_url || '未配置对接地址'}</small>
              </div>
              <div className="metric-value settings-inline">
                <StatusBadge tone={hubTone(hubStatus)}>{hubLabel(hubStatus)}</StatusBadge>
                <button type="button" className="button button--quiet" disabled={hubChecking} onClick={() => void probeHub()}>
                  {hubChecking ? '探测中…' : '重新探测'}
                </button>
              </div>
            </div>

            {hubStatus?.error_message && <Notice message={hubStatus.error_message} tone="error" />}

            {hubStatus?.public_key_pem && <div className="settings-stack settings-stack--tight">
              <div className="metric-row">
                <div className="metric-label">
                  <span>宿主公钥</span>
                  <small>粘进 Extension 的 auth.hub.public_key（内容是公钥本身，不是路径）</small>
                </div>
                <div className="metric-value settings-inline">
                  <StatusBadge tone="success">与私钥配对</StatusBadge>
                  {hubStatus.key_fingerprint && <code className="settings-fingerprint">{hubStatus.key_fingerprint}</code>}
                </div>
              </div>
              <label className="form-field">
                <span className="visually-hidden">宿主公钥 PEM</span>
                <textarea
                  className="code-textarea code-textarea--short"
                  readOnly
                  value={hubStatus.public_key_pem}
                  spellCheck={false}
                  onFocus={event => event.currentTarget.select()}
                />
              </label>
              <div className="settings-inline">
                <button
                  type="button"
                  className="button button--primary"
                  onClick={() => void copyText(hubStatus.public_key_pem ?? '').then(ok => ok ? notice.success('公钥 PEM 已复制') : notice.error('复制失败，请手动选中上面的文本'))}
                >
                  复制公钥 PEM
                </button>
                {hubStatus.public_key && <button
                  type="button"
                  className="button button--quiet"
                  onClick={() => void copyText(hubStatus.public_key ?? '').then(ok => ok ? notice.success('单行 base64 已复制，可直接粘进 auth.hub.public_key') : notice.error('复制失败，请手动选中上面的文本'))}
                >
                  复制单行 base64
                </button>}
                <span className="settings-hint">
                  单行 base64 可直接粘进 config.json；用 PEM 则要把换行写成 \n，否则 JSON 字符串不合法。
                </span>
              </div>
            </div>}

            </div>
          </div>

          {/* 密钥：身份，与连接和能力分开 */}
          <div className="form-block">
            <div className="form-block-head">
              <h3>签名私钥</h3>
              <p>宿主的身份。换一把就必须同步更新 Extension 侧的公钥，否则所有 /hub/* 调用都会 401。</p>
            </div>
            <div className="metric-row">
              <div className="metric-label">
                <span>当前私钥</span>
                <small>{keySourceHint(keyInfo)}</small>
              </div>
              <div className="metric-value settings-inline">
                <StatusBadge tone={keyInfo?.broken ? 'danger' : keyInfo?.source === 'config' ? 'success' : 'warning'}>
                  {keyInfo?.broken ? '私钥无法解析' : keyInfo?.source === 'config' ? '已配置' : '缺失'}
                </StatusBadge>
                {keyInfo?.fingerprint && <code className="settings-fingerprint">{keyInfo.fingerprint}</code>}
                {keyInfo?.configured && <button
                  type="button"
                  className="button button--quiet"
                  disabled={keyBusy}
                  onClick={() => setConfirmingKeyDelete(true)}
                >
                  移除
                </button>}
              </div>
            </div>

            <div className="settings-stack settings-stack--tight">
              <label className="form-field">
                粘贴私钥
                <textarea
                  className="code-textarea code-textarea--short"
                  value={keyDraft}
                  onChange={event => setKeyDraft(event.target.value)}
                  spellCheck={false}
                  placeholder="一行 base64（44 字符），或直接的 PKCS#8 PEM"
                />
              </label>
              <div className="settings-inline">
                <button
                  type="button"
                  className="button button--primary"
                  disabled={keyBusy || !keyDraft.trim()}
                  onClick={() => void saveKey()}
                >
                  {keyBusy ? '保存中…' : '保存私钥'}
                </button>
                <button
                  type="button"
                  className="button button--quiet"
                  disabled={keyBusy}
                  onClick={() => setKeyDraft('')}
                >
                  清空输入
                </button>
                <span className="settings-hint">
                  接受 base64 种子、完整私钥的 base64，或 PKCS#8 PEM；保存后会规范成一行 44 字符。
                  写入前自签自验，读接口永不返回私钥。
                </span>
              </div>
            </div>

            <div className="settings-guide">
              <p className="settings-guide-title">接入总共三步</p>
              <ol>
                <li>生成密钥：<code>openssl genpkey -algorithm ed25519 -out hub.key</code></li>
                <li>把公钥粘进 Extension 主机的 <code>auth.hub.public_key</code>（内容是公钥本身，不是路径），重启它</li>
                <li>把私钥粘进下面的输入框并保存（会自动规范成一行 44 字符），填好 <strong>API 根地址</strong>，勾上启用，然后点「重新探测」</li>
              </ol>
              <p className="settings-guide-note">
                Extension 是单实例：一个 HomeHub 对接一个 Extension，不支持在这里添加多个。
              </p>
            </div>
          </div>
        </Section>
      </div>}

    <ConfirmDialog
      open={confirmingKeyDelete}
      title="移除签名私钥"
      description="会把 config.json 里的 private_key_pem 清空。清空后无法再签名，所有 /hub/* 调用都会失败。需要先关闭 extension.enabled 才能执行。"
      confirmLabel="移除"
      danger
      busy={keyBusy}
      error={keyDeleteError}
      onClose={() => { setConfirmingKeyDelete(false); setKeyDeleteError('') }}
      onConfirm={() => void deleteKey()}
    />

    <ConfirmDialog
      open={confirmingJSON}
      title="保存系统配置"
      description="配置会写入 config.json 并立即热重载。监听地址、数据库、认证或代理设置错误可能导致服务不可用。"
      confirmLabel="保存并重载"
      busy={saving === 'json'}
      error={jsonError}
      onClose={() => { setConfirmingJSON(false); setJsonError('') }}
      onConfirm={() => void saveJSON()}
    />
  </div>
}

// ------------------------------------------------------------
// Extension 开关联动
//
// 后端校验拒绝「能力开着、总开关关着」。若两者各自独立，取消「启用」后
// 保存必然失败，用户只能先逐个取消能力，表现为开关关不掉。
// ------------------------------------------------------------

type ExtensionFeature = keyof SafeConfig['extension']['features']

/** 关闭总开关时一并关闭全部能力；打开时保持能力原样。 */
function setExtensionEnabled(cfg: SafeConfig, enabled: boolean): SafeConfig {
  const features = enabled ? cfg.extension.features : { hub: false, fetch: false, avatar: false }
  return { ...cfg, extension: { ...cfg.extension, enabled, features } }
}

/** 打开任一能力时自动打开总开关，否则保存会被后端拒绝。 */
function setExtensionFeature(cfg: SafeConfig, feature: ExtensionFeature, on: boolean): SafeConfig {
  const features = { ...cfg.extension.features, [feature]: on }
  return { ...cfg, extension: { ...cfg.extension, enabled: on || cfg.extension.enabled, features } }
}

// ------------------------------------------------------------
// 局部组件
// ------------------------------------------------------------

/**
 * CapabilityHint 在对端探测结果旁给一个小标记。
 *
 * available 为 null 表示还没探测到（未配置、不可达、或该能力不在探测范围）。
 * 不显示比显示「未知」更好：一个常驻的灰色「未知」会让整页看起来像坏了。
 */
function CapabilityHint({ available }: { available: boolean | null | undefined }) {
  if (available === null || available === undefined) return null
  return available
    ? <StatusBadge tone="success">对端已实现</StatusBadge>
    : <StatusBadge tone="danger">对端未实现</StatusBadge>
}

/** keySourceHint 说明密钥来自哪里，以及该去哪改。 */
function keySourceHint(info: KeyInfo | null): string {
  if (!info) return '无法读取密钥状态'
  if (info.broken) return 'config.json 里的 private_key_pem 解析失败，请重新粘贴'
  if (info.source === 'config') return '来自 config.json 的 extension.private_key_pem'
  return '尚未配置私钥：粘贴一把 PKCS#8 PEM，或直接写 config.json'
}

const hubToneMap = { idle: 'neutral', ok: 'success', warn: 'warning', error: 'danger' } as const
const hubLabel = (status: ExtensionHubStatus | null) => hubSummary(status).label
const hubTone = (status: ExtensionHubStatus | null) => hubToneMap[hubSummary(status).tone]

/** Section 是一个可独立保存的配置面板。 */
function Section({ title, description, children, section, dirty, saving, onSave, onReset }: {
  title: string
  description?: string
  children: ReactNode
  section?: SettingsSection
  dirty?: boolean
  saving?: boolean
  onSave?: (event: FormEvent) => void
  onReset?: () => void
}) {
  const formId = section ? `settings-${section}` : undefined
  return <section className="data-panel">
    <header className="data-panel-head">
      <div>
        <h2>{title}</h2>
        {description && <p>{description}</p>}
      </div>
      {section && <div className="toolbar-actions">
        {dirty && <span className="settings-dirty">未保存</span>}
        <button className="button button--quiet" type="button" disabled={!dirty || saving} onClick={onReset}>还原</button>
        {/* 提交按钮在 form 外，通过 form 属性关联：浏览器校验（min/max/required）与回车提交都生效。 */}
        <button className="button button--primary" type="submit" form={formId} disabled={!dirty || saving}>
          {saving ? '保存中…' : '保存'}
        </button>
      </div>}
    </header>
    {/* 回车提交但没有改动时不发请求。 */}
    {section
      ? <form id={formId} className="data-panel-body" onSubmit={event => { if (dirty && !saving) onSave?.(event); else event.preventDefault() }}>{children}</form>
      : <div className="data-panel-body">{children}</div>}
  </section>
}

/**
 * NumberInput 用字符串保存输入中的文本。
 *
 * 直接写 Number(event.target.value) 会把清空的输入框变成 0 写进草稿并被保存。
 * 这里只在文本是合法数字时才回调；清空时草稿保持原值，而 required 让表单提交时拦下空值。
 */
function NumberInput({ value, onChange, ...rest }: Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange' | 'type'> & { value: number; onChange: (value: number) => void }) {
  const [text, setText] = useState(String(value))
  // 外部值变化（还原、保存后刷新、JSON 回填）时同步显示文本。
  useEffect(() => {
    setText(current => (current !== '' && Number(current) === value ? current : String(value)))
  }, [value])
  return <input
    {...rest}
    type="number"
    required
    value={text}
    onChange={event => {
      const raw = event.target.value
      setText(raw)
      if (raw.trim() !== '' && Number.isFinite(Number(raw))) onChange(Number(raw))
    }}
  />
}

/** parseConfigJSON 解析 JSON 编辑器里的文本，顶层必须是对象。 */
function parseConfigJSON(text: string): SafeConfig {
  const parsed: unknown = JSON.parse(text)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('顶层必须是一个 JSON 对象')
  return parsed as SafeConfig
}

function Field({ label, children, wide = false }: { label: string; children: ReactNode; wide?: boolean }) {
  return <label className={wide ? 'form-field form-field--wide' : 'form-field'}>{label}{children}</label>
}

/**
 * LineListInput 编辑「一行一项」的字符串列表。
 *
 * 草稿保存原始文本，失焦或提交时才不会吃掉用户正在输入的空行；
 * 对外只回传去空白、去空行后的列表。
 */
function LineListInput({ value, onChange, placeholder }: { value: string[]; onChange: (value: string[]) => void; placeholder?: string }) {
  const [text, setText] = useState(value.join('\n'))
  useEffect(() => {
    setText(current => (sameLines(current, value) ? current : value.join('\n')))
  }, [value])
  return <textarea
    className="code-textarea code-textarea--short"
    spellCheck={false}
    placeholder={placeholder}
    value={text}
    onChange={event => { setText(event.target.value); onChange(splitLines(event.target.value)) }}
  />
}

function splitLines(text: string): string[] {
  return text.split('\n').map(line => line.trim()).filter(Boolean)
}

function sameLines(text: string, value: string[]): boolean {
  return JSON.stringify(splitLines(text)) === JSON.stringify(value)
}
