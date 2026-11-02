import { useCallback, useEffect, useState } from 'react'
import { ConfirmDialog, Modal, Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import {
  extensionHubApi,
  type AccessKey,
  type CreatedKey,
  type ExtensionHubStatus,
  type RemoteSubscription,
  type SyncResult,
} from '@/lib/extensionHub'

// ============================================================
// Extension 对接
//
// 两块内容：订阅镜像（本地 rss_feeds → Extension）与访问密钥
// （宿主签发，用户在 Extension 的 /pair 页面兑换登录）。
//
// 这一页刻意不提供“删除对方全部订阅”：删除会连带清掉 Extension
// 已经抓到的条目与 blob，必须逐条确认。
// ============================================================

/** unixSeconds 把协议里的 Unix 秒转成本地时间；0 与 null 都表示“还没发生”。 */
function unixSeconds(value: number | null | undefined): string {
  if (!value) return '—'
  return new Date(value * 1000).toLocaleString('zh-CN', { hour12: false })
}

/** statusTone 把探测结果压成徽章色调。 */
function statusTone(ok: boolean, problem: boolean): 'success' | 'danger' | 'warning' {
  if (ok) return 'success'
  return problem ? 'danger' : 'warning'
}

const expiresOptions = [
  { label: '7 天', value: '168h' },
  { label: '30 天', value: '720h' },
  { label: '90 天', value: '2160h' },
  { label: '永久有效', value: '' },
]

/**
 * syncMessage 把对账结果拼成一句人话。
 *
 * 对账只推归属 Extension 的源，所以 total=0 有两种含义：本地没有订阅源，
 * 或者有源但全归本机。不把后者说出来的话，使用者会以为对账什么都没做。
 */
function syncMessage(result: SyncResult): string {
  const skipped = result.skipped_local > 0 ? `；${result.skipped_local} 条由本机抓取，未推送` : ''
  if (result.total === 0) {
    return result.skipped_local > 0
      ? `没有需要同步的源：${result.skipped_local} 条都由本机抓取`
      : '本地还没有订阅源'
  }
  return `对账完成：新建 ${result.created}、更新 ${result.updated}、失败 ${result.failed}（共 ${result.total} 条）${skipped}`
}

export function ExtensionPage() {
  const [status, setStatus] = useState<ExtensionHubStatus | null>(null)
  const [subscriptions, setSubscriptions] = useState<RemoteSubscription[]>([])
  const [keys, setKeys] = useState<AccessKey[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [syncResult, setSyncResult] = useState<SyncResult | null>(null)

  const [pendingRemove, setPendingRemove] = useState<RemoteSubscription | null>(null)
  const [keyDraft, setKeyDraft] = useState<{ name: string; expires: string } | null>(null)
  const [issuedKey, setIssuedKey] = useState<CreatedKey | null>(null)
  const [pendingRevoke, setPendingRevoke] = useState<AccessKey | null>(null)

  const describeError = (cause: unknown) =>
    cause instanceof Error ? cause.message : '操作失败'

  /** loadStatus 只探测状态；订阅与密钥按状态再决定是否读取。 */
  const loadStatus = useCallback(async () => {
    try {
      const next = await extensionHubApi.status()
      setStatus(next)
      return next
    } catch (cause) {
      setError(describeError(cause))
      return null
    }
  }, [])

  const loadSubscriptions = useCallback(async () => {
    try {
      setSubscriptions(await extensionHubApi.listSubscriptions())
    } catch (cause) {
      setError(describeError(cause))
    }
  }, [])

  const loadKeys = useCallback(async () => {
    try {
      setKeys(await extensionHubApi.listKeys())
    } catch (cause) {
      setError(describeError(cause))
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      const next = await loadStatus()
      if (cancelled) return
      if (next?.authenticated) {
        await Promise.all([next.rss_available ? loadSubscriptions() : Promise.resolve(), loadKeys()])
      }
      if (!cancelled) setLoading(false)
    })()
    return () => { cancelled = true }
  }, [loadStatus, loadSubscriptions, loadKeys])

  async function refresh() {
    setBusy(true)
    try {
      const next = await loadStatus()
      if (next?.authenticated) {
        await Promise.all([next.rss_available ? loadSubscriptions() : Promise.resolve(), loadKeys()])
      }
      // 只在探测真的成功时清错误。无条件清除会把 loadStatus 刚写下的
      // 失败原因抹掉，按钮看上去像点了没反应。
      if (next) setError('')
    } finally {
      setBusy(false)
    }
  }

  async function runSync() {
    setBusy(true)
    try {
      const result = await extensionHubApi.sync()
      setSyncResult(result)
      // 对账只推归属 Extension 的源。total=0 有两种含义（本地没源 /
      // 有源但都归本机），不区分的话使用者会以为对账没干活。
      setMessage(syncMessage(result))
      setError('')
      await loadSubscriptions()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function pushOne(item: RemoteSubscription) {
    if (!item.local_feed_id) return
    setBusy(true)
    try {
      // fetch_now=false：重推的目的是刷新元数据（标题、间隔），
      // 不应该顺手触发一次抓取。
      await extensionHubApi.push(item.local_feed_id, { fetchNow: false })
      setMessage(`已重推「${item.title || item.url}」`)
      setError('')
      await loadSubscriptions()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function removeSubscription() {
    if (!pendingRemove) return
    setBusy(true)
    try {
      const result = await extensionHubApi.removeSubscription(pendingRemove.id)
      setMessage(result.already_deleted ? '对方本来就没有这条订阅' : '已从 Extension 移除该订阅')
      setError('')
      setPendingRemove(null)
      await loadSubscriptions()
    } catch (cause) {
      setError(describeError(cause))
      setPendingRemove(null)
    } finally {
      setBusy(false)
    }
  }

  async function issueKey() {
    if (!keyDraft) return
    setBusy(true)
    try {
      const created = await extensionHubApi.issueKey(
        keyDraft.name.trim(),
        keyDraft.expires || undefined,
      )
      setIssuedKey(created)
      setKeyDraft(null)
      setError('')
      await loadKeys()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function revokeKey() {
    if (!pendingRevoke) return
    setBusy(true)
    try {
      await extensionHubApi.revokeKey(pendingRevoke.id)
      setMessage(`已撤销密钥「${pendingRevoke.name}」，它换出去的会话同时失效`)
      setError('')
      setPendingRevoke(null)
      await loadKeys()
    } catch (cause) {
      setError(describeError(cause))
      setPendingRevoke(null)
    } finally {
      setBusy(false)
    }
  }

  async function copyKey() {
    if (!issuedKey) return
    try {
      await navigator.clipboard.writeText(issuedKey.key)
      setMessage('密钥已复制到剪贴板')
    } catch {
      setError('复制失败，请手动选中上面的密钥文本')
    }
  }

  if (loading) return <PageState kind="loading" title="正在探测 Extension 状态" />
  if (!status) return <PageState kind="error" title="无法读取对接状态" description={error} />

  // 未启用：给出配置指引，而不是一个空白页。
  if (!status.enabled || status.error_code === 'disabled') {
    return <div className="business-page">
      <PageState
        kind="empty"
        title="Extension 对接未启用"
        description="在 config.json 的 extension 段填写 base_url 与 private_key_pem，把 enabled 设为 true，保存后配置会自动热重载。"
        action={<button className="button button--primary" disabled={busy} onClick={() => void refresh()}>重新探测</button>}
      />
      <Notice message={status.error_message} tone="info" />
    </div>
  }

  const configured = status.configured
  const healthy = status.reachable && status.authenticated

  return <div className="business-page">
    <div className="page-toolbar">
      <div>
        <p>跨实例集成</p>
        <h2>Extension 对接</h2>
      </div>
      <div className="toolbar-actions">
        <button className="button button--quiet" disabled={busy} onClick={() => void refresh()}>
          {busy ? '探测中...' : '刷新状态'}
        </button>
      </div>
    </div>

    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} onDismiss={() => setMessage('')} />

    {/* ---------- 状态 ---------- */}
    <section className="data-panel">
      <header className="data-panel-head">
        <div>
          <h2>通道状态</h2>
          <p>{status.base_url}</p>
        </div>
      </header>
      <div className="modal-body">
        <div className="metric-row">
          <div className="metric-label"><span>网络可达</span><small>能否建立 HTTP 连接</small></div>
          <div className="metric-value">
            <StatusBadge tone={statusTone(status.reachable, configured && !status.reachable)}>
              {status.reachable ? '可达' : '不可达'}
            </StatusBadge>
          </div>
        </div>
        <div className="metric-row">
          <div className="metric-label"><span>签名通过</span><small>Extension 是否认可这把宿主私钥</small></div>
          <div className="metric-value">
            <StatusBadge tone={statusTone(status.authenticated, status.reachable && !status.authenticated)}>
              {status.authenticated ? '已通过' : '未通过'}
            </StatusBadge>
          </div>
        </div>
        <div className="metric-row">
          <div className="metric-label"><span>RSS 模块</span><small>对方是否开启了阅读器</small></div>
          <div className="metric-value">
            <StatusBadge tone={statusTone(status.rss_available, status.authenticated && !status.rss_available)}>
              {status.rss_available ? `已启用 · ${status.subscription_count} 条订阅` : '未启用'}
            </StatusBadge>
          </div>
        </div>
        {status.public_key && <div className="metric-row">
          <div className="metric-label">
            <span>宿主公钥</span>
            <small>与 Extension 的 auth.hub.public_key 内容比对</small>
          </div>
          <div className="metric-value">
            <code style={{ wordBreak: 'break-all', fontSize: 'var(--text-xs)' }}>{status.public_key}</code>
          </div>
        </div>}
        {status.error_message && <Notice message={`${status.error_code}：${status.error_message}`} tone="error" />}
        {status.error_code === 'unauthenticated' && <Notice
          message="签名未通过。检查两侧时钟偏差是否小于 30 秒，以及 Extension 侧配置的公钥是否与上面这把一致。"
          tone="info"
        />}
        {status.error_code === 'unavailable' && <Notice
          message="Extension 启动了但签名通道不可用：通常是 auth.hub.public_key 为空或解析失败，看它日志里的 hub signature channel disabled。"
          tone="info"
        />}
      </div>
    </section>

    {/* ---------- 订阅镜像 ---------- */}
    <section className="data-panel">
      <header className="data-panel-head">
        <div>
          <h2>订阅镜像</h2>
          <p>把本地订阅源推送到 Extension。只推不删：删除会连带清掉对方已抓取的条目。</p>
        </div>
        <div className="toolbar-actions">
          <button className="button button--primary" disabled={busy || !status.rss_available} onClick={() => void runSync()}>
            全量对账
          </button>
        </div>
      </header>
      <div className="modal-body">
        {!status.configured
          ? <PageState kind="empty" title="配置不完整" description={status.error_message || '检查 extension.base_url 与 extension.private_key_pem。'} />
          : !status.authenticated
            ? <PageState kind="empty" title="签名未通过" description="Extension 没认可这把宿主私钥。检查两侧时钟偏差与公钥是否配对。" />
            : !status.rss_available
              ? <PageState kind="empty" title="对方的 RSS 模块未启用" description="在 Extension 的 config.json 里把 rss.enabled 设为 true 并重启后再试。" />
              : !subscriptions.length
                ? <PageState
                  kind="empty"
                  title="Extension 侧还没有订阅"
                  description="点击「全量对账」把本地订阅源推过去。"
                  action={<button className="button button--primary" disabled={busy} onClick={() => void runSync()}>全量对账</button>}
                />
                : <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>订阅</th>
                    <th>抓取状态</th>
                    <th>未读</th>
                    <th>本地</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {subscriptions.map(item => <tr key={item.id}>
                    <td data-label="订阅">
                      <strong>{item.title || item.url}</strong>
                      <p className="table-subtext">{item.url}</p>
                      <p className="table-subtext">间隔 {item.fetch_interval} · 最近抓取 {unixSeconds(item.last_fetch_at)}</p>
                    </td>
                    <td data-label="抓取状态">
                      <StatusBadge tone={item.enabled ? (item.error_count > 0 ? 'warning' : 'success') : 'danger'}>
                        {item.enabled ? (item.error_count > 0 ? `连续失败 ${item.error_count} 次` : '正常') : '已停用'}
                      </StatusBadge>
                      {item.last_error && <p className="table-subtext">{item.last_error}</p>}
                      {item.last_status > 0 && <p className="table-subtext">HTTP {item.last_status}</p>}
                    </td>
                    <td data-label="未读">{item.unread_count}</td>
                    <td data-label="本地">
                      <StatusBadge tone={item.in_local ? 'success' : 'neutral'}>
                        {item.in_local ? `#${item.local_feed_id}` : '仅远端'}
                      </StatusBadge>
                    </td>
                    <td data-label="操作">
                      <div className="table-actions">
                        {item.in_local && <button
                          className="button button--quiet"
                          disabled={busy}
                          onClick={() => void pushOne(item)}
                        >
                          重推
                        </button>}
                        <button className="icon-button" aria-label="从 Extension 删除该订阅" onClick={() => setPendingRemove(item)}>×</button>
                      </div>
                    </td>
                  </tr>)}
                </tbody>
              </table>
            </div>}

        {syncResult && syncResult.failed > 0 && <div style={{ marginTop: 16 }}>
          <h3 style={{ fontSize: 'var(--text-sm)', marginBottom: 8 }}>失败明细</h3>
          <div className="table-wrap">
            <table className="data-table">
              <thead><tr><th>订阅源</th><th>原因</th></tr></thead>
              <tbody>
                {syncResult.items.filter(item => item.status === 'failed').map(item => <tr key={item.feed_id}>
                  <td data-label="订阅源"><strong>#{item.feed_id}</strong><p className="table-subtext">{item.url}</p></td>
                  <td data-label="原因"><p className="table-subtext">{item.error}</p></td>
                </tr>)}
              </tbody>
            </table>
          </div>
        </div>}
      </div>
    </section>

    {/* ---------- 访问密钥 ---------- */}
    <section className="data-panel">
      <header className="data-panel-head">
        <div>
          <h2>访问密钥</h2>
          <p>用户在 Extension 的 /pair 页面输入密钥即可登录。密钥可重复使用，撤销后立即生效。</p>
        </div>
        <div className="toolbar-actions">
          <button className="button button--primary" disabled={busy || !healthy} onClick={() => setKeyDraft({ name: '', expires: '720h' })}>
            签发密钥
          </button>
        </div>
      </header>
      <div className="modal-body">
        {!healthy
          ? <PageState kind="empty" title="通道未就绪" description="需要网络可达且签名通过才能管理访问密钥。" />
          : !keys.length
            ? <PageState
              kind="empty"
              title="还没有访问密钥"
              description="签发一把交给用户，它在 Extension 侧等同于一次登录。"
              action={<button className="button button--primary" disabled={busy} onClick={() => setKeyDraft({ name: '', expires: '720h' })}>签发密钥</button>}
            />
            : <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>名称</th>
                    <th>状态</th>
                    <th>创建时间</th>
                    <th>最近使用</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {keys.map(item => <tr key={item.id}>
                    <td data-label="名称">
                      <strong>{item.name}</strong>
                      <p className="table-subtext">{item.id}</p>
                    </td>
                    <td data-label="状态">
                      <StatusBadge tone={item.live ? 'success' : 'danger'}>
                        {item.live ? '有效' : item.revoked ? '已撤销' : '已过期'}
                      </StatusBadge>
                    </td>
                    <td data-label="创建时间">{unixSeconds(item.created_at)}</td>
                    <td data-label="最近使用">{unixSeconds(item.last_used_at)}</td>
                    <td data-label="操作">
                      <div className="table-actions">
                        <button
                          className="button button--quiet"
                          disabled={busy || !item.live}
                          onClick={() => setPendingRevoke(item)}
                        >
                          撤销
                        </button>
                      </div>
                    </td>
                  </tr>)}
                </tbody>
              </table>
            </div>}
      </div>
    </section>

    {/* ---------- 签发密钥 ---------- */}
    <Modal
      open={keyDraft !== null}
      title="签发访问密钥"
      description="密钥只在这个流程里显示一次，Extension 侧只存哈希。之后任何接口都拿不回明文。"
      onClose={() => !busy && setKeyDraft(null)}
      footer={<>
        <button className="button button--quiet" type="button" disabled={busy} onClick={() => setKeyDraft(null)}>取消</button>
        <button
          className="button button--primary"
          type="submit"
          form="extension-key-form"
          disabled={busy || !keyDraft?.name.trim()}
        >
          {busy ? '签发中...' : '签发'}
        </button>
      </>}
    >
      <form id="extension-key-form" className="form-grid" onSubmit={event => { event.preventDefault(); void issueKey() }}>
        <label className="form-field form-field--wide">
          名称
          <input
            autoFocus
            required
            maxLength={100}
            value={keyDraft?.name ?? ''}
            placeholder="例如 alice-手机"
            onChange={event => setKeyDraft(current => current ? { ...current, name: event.target.value } : current)}
          />
        </label>
        <label className="form-field form-field--wide">
          有效期
          <select
            value={keyDraft?.expires ?? ''}
            onChange={event => setKeyDraft(current => current ? { ...current, expires: event.target.value } : current)}
          >
            {expiresOptions.map(option => <option key={option.label} value={option.value}>{option.label}</option>)}
          </select>
        </label>
      </form>
    </Modal>

    {/* ---------- 明文只出现一次 ---------- */}
    <Modal
      open={issuedKey !== null}
      title="密钥已签发"
      description="现在复制它。关闭这个窗口后，任何接口都拿不回明文，只能重新签发。"
      onClose={() => setIssuedKey(null)}
      footer={<>
        <button className="button button--quiet" onClick={() => void copyKey()}>复制密钥</button>
        <button className="button button--primary" onClick={() => setIssuedKey(null)}>我已保存</button>
      </>}
    >
      <div className="form-grid">
        <label className="form-field form-field--wide">
          密钥
          <input readOnly value={issuedKey?.key ?? ''} onFocus={event => event.currentTarget.select()} style={{ fontFamily: 'ui-monospace,monospace' }} />
        </label>
        <label className="form-field form-field--wide">
          有效期
          <input readOnly value={issuedKey?.expires_at ? unixSeconds(issuedKey.expires_at) : '永不过期'} />
        </label>
      </div>
      <Notice message="这串密钥等同于一次登录。不要贴进公开渠道，也不要写进 URL。" tone="info" />
    </Modal>

    <ConfirmDialog
      open={pendingRemove !== null}
      title="从 Extension 删除订阅"
      description={`将删除「${pendingRemove?.title || pendingRemove?.url || ''}」，并连带清理 Extension 已抓取的条目与媒体文件，不可恢复。本地订阅源不受影响。`}
      confirmLabel="确认删除"
      danger
      busy={busy}
      onClose={() => setPendingRemove(null)}
      onConfirm={() => void removeSubscription()}
    />

    <ConfirmDialog
      open={pendingRevoke !== null}
      title="撤销访问密钥"
      description={`撤销「${pendingRevoke?.name ?? ''}」后，它换出去的登录会话会立即失效。该密钥名下创建的私有片段仍可被同一主人的其他凭证读取。`}
      confirmLabel="撤销"
      danger
      busy={busy}
      onClose={() => setPendingRevoke(null)}
      onConfirm={() => void revokeKey()}
    />
  </div>
}
