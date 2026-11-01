import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Pie } from 'react-chartjs-2'
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js'
import { ConfirmDialog, Modal, Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { balanceApi, type BalanceStatus, type BalanceTarget, type BalanceTargetInput } from '@/lib/balance'
import { formatDateTime } from '@/lib/types'

ChartJS.register(ArcElement, Tooltip, Legend)

const typeColors: Record<string, string> = {
  deepseek: '#4F8B89',
}
const fallbackColors = ['#4F8B89', '#E8804F', '#6B8AC4', '#A07BB8', '#C4A35A', '#D4737A']

const supportedTypes = [{ value: 'deepseek', label: 'DeepSeek' }]

/** isZeroTime 判断后端返回的零值时间（未刷新过）。 */
function isZeroTime(value?: string): boolean {
  return !value || value.startsWith('0001-01-01')
}

/** Draft 是表单状态。id 为空表示新建。 */
interface Draft {
  id?: number
  name: string
  type: string
  base_url: string
  /** 新建时必填；编辑时留空表示不改，填了才覆盖。 */
  api_key: string
  /** 显式清空已存的密钥。只有编辑态且原来有密钥时才有意义。 */
  clearKey: boolean
  enabled: boolean
  sort_order: number
}

const emptyDraft: Draft = {
  name: '',
  type: 'deepseek',
  base_url: 'https://api.deepseek.com',
  api_key: '',
  clearKey: false,
  enabled: true,
  sort_order: 0,
}

/** statusTone 把监控点状态映射成徽章色调。 */
function statusTone(status: BalanceTarget['status']): 'success' | 'danger' | 'warning' | 'neutral' {
  switch (status) {
    case 'ok': return 'success'
    case 'error': return 'danger'
    case 'checking': return 'warning'
    default: return 'neutral'
  }
}

function statusLabel(item: BalanceTarget): string {
  if (!item.enabled) return '已停用'
  switch (item.status) {
    case 'ok': return '正常'
    case 'error': return '异常'
    case 'checking': return '查询中'
    default: return '未查询'
  }
}

export function BalancePage() {
  const [items, setItems] = useState<BalanceTarget[]>([])
  const [status, setStatus] = useState<BalanceStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const [draft, setDraft] = useState<Draft | null>(null)
  const [pendingDelete, setPendingDelete] = useState<BalanceTarget | null>(null)

  const describeError = (cause: unknown) => cause instanceof Error ? cause.message : '操作失败'

  async function load() {
    setLoading(true)
    try {
      const [list, stat] = await Promise.all([balanceApi.list(), balanceApi.status()])
      setItems(list ?? [])
      setStatus(stat)
      setError('')
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  async function refreshAll() {
    setBusy(true)
    try {
      await balanceApi.refresh()
      setMessage('已刷新全部监控点')
      setError('')
      await load()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function refreshOne(item: BalanceTarget) {
    setBusy(true)
    try {
      const updated = await balanceApi.refreshOne(item.id)
      if (updated.status === 'ok') {
        setMessage(`「${item.name}」余额 ${updated.balance.toFixed(2)} ${updated.currency ?? ''}`)
        setError('')
      } else {
        // 查询失败是业务结果，不是请求失败：用 error 提示但保留列表。
        setError(updated.last_error || '查询失败')
      }
      await load()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!draft) return

    const isCreate = draft.id === undefined
    if (isCreate && !draft.api_key.trim()) {
      setError('新建监控点必须填写 API Key')
      return
    }
    if (draft.clearKey && draft.api_key.trim()) {
      setError('「清除密钥」与填写新密钥只能选一个')
      return
    }

    const input: BalanceTargetInput = {
      name: draft.name.trim(),
      type: draft.type,
      base_url: draft.base_url.trim(),
      enabled: draft.enabled,
      sort_order: draft.sort_order,
    }
    // 三态：留空 = 不发（保留原值）；填了 = 覆盖；勾选清除 = 发空串。
    if (draft.clearKey) input.api_key = ''
    else if (draft.api_key.trim()) input.api_key = draft.api_key.trim()

    setBusy(true)
    try {
      if (isCreate) {
        await balanceApi.create(input)
        setMessage(`已创建「${input.name}」`)
      } else {
        await balanceApi.update(draft.id as number, input)
        setMessage(`已更新「${input.name}」`)
      }
      setDraft(null)
      setError('')
      await load()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!pendingDelete) return
    setBusy(true)
    try {
      await balanceApi.remove(pendingDelete.id)
      setMessage(`已删除「${pendingDelete.name}」`)
      setError('')
      setPendingDelete(null)
      await load()
    } catch (cause) {
      setError(describeError(cause))
      setPendingDelete(null)
    } finally {
      setBusy(false)
    }
  }

  async function toggle(item: BalanceTarget) {
    setBusy(true)
    try {
      await balanceApi.update(item.id, { enabled: !item.enabled })
      await load()
    } catch (cause) {
      setError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  // 图表只统计启用的监控点：停用的那条余额不是 0，而是「不该看」。
  const active = useMemo(() => items.filter(item => item.enabled), [items])
  const total = useMemo(() => active.reduce((sum, item) => sum + (item.balance || 0), 0), [active])
  const chartData = useMemo(() => ({
    labels: active.length ? active.map(item => item.name) : ['无数据'],
    datasets: [{
      data: active.length ? active.map(item => item.balance || 0) : [1],
      backgroundColor: active.map((item, i) => typeColors[item.type] || fallbackColors[i % fallbackColors.length]),
      borderColor: 'var(--color-surface)',
      borderWidth: 3,
    }],
  }), [active])

  if (loading && !items.length) return <PageState kind="loading" title="正在读取余额信息" />

  const editing = draft?.id !== undefined

  return <div className="business-page">
    <div className="page-toolbar">
      <div>
        <p>余额监控</p>
        <h2>LLM 余额</h2>
      </div>
      <div className="toolbar-actions">
        {status && <span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>
          {status.ok_count}/{status.target_count} 可用 · {isZeroTime(status.last_refresh_at) ? '未刷新' : formatDateTime(status.last_refresh_at)}
        </span>}
        <button className="button button--quiet" disabled={busy} onClick={() => void refreshAll()}>刷新全部</button>
        <button className="button button--primary" disabled={busy} onClick={() => setDraft({ ...emptyDraft, sort_order: items.length })}>新增监控点</button>
      </div>
    </div>

    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} onDismiss={() => setMessage('')} />

    {status && !status.enabled && <Notice
      message="余额监控模块未启用：在 config.json 里把 balance_monitor.enabled 设为 true。监控点本身已经存在数据库里，不受这个开关影响。"
      tone="info"
    />}

    {!items.length
      ? <PageState
        kind="empty"
        title="还没有监控点"
        description="添加一个供应商账号即可开始追踪余额。密钥只保存在服务端，读取时只显示掩码。"
        action={<button className="button button--primary" onClick={() => setDraft({ ...emptyDraft })}>新增监控点</button>}
      />
      : <div className="balance-grid">
        <section className="data-panel" style={{ gridColumn: '1 / -1' }}>
          <header className="data-panel-head">
            <div><h2>数据资产</h2><p>启用中的监控点总余额与占比</p></div>
            <strong style={{ fontSize: 26, color: 'var(--color-primary)' }}>{total.toFixed(2)}</strong>
          </header>
          <div style={{ display: 'flex', alignItems: 'center', gap: 28, padding: 20, flexWrap: 'wrap' }}>
            <div style={{ width: 200, height: 200, flexShrink: 0 }}>
              <Pie data={chartData} options={{ plugins: { legend: { display: false } }, maintainAspectRatio: false }} />
            </div>
            <div style={{ display: 'grid', gap: 8, flex: 1, minWidth: 200 }}>
              {active.map((item, i) => <div key={item.id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ width: 10, height: 10, borderRadius: 4, background: typeColors[item.type] || fallbackColors[i % fallbackColors.length], flexShrink: 0 }} />
                  <span>{item.name}</span>
                </div>
                <strong>{item.balance?.toFixed(2) ?? '—'} {item.currency || ''}</strong>
              </div>)}
              {!active.length && <p style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>没有启用的监控点</p>}
            </div>
          </div>
        </section>

        <section className="data-panel" style={{ gridColumn: '1 / -1' }}>
          <header className="data-panel-head">
            <div><h2>监控点</h2><p>密钥只显示掩码；编辑时留空表示保持原值</p></div>
          </header>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>名称</th><th>类型</th><th>余额</th><th>状态</th><th>最后检查</th><th>操作</th></tr>
              </thead>
              <tbody>
                {items.map(item => <tr key={item.id}>
                  <td data-label="名称">
                    <strong>{item.name}</strong>
                    <p className="table-subtext">{item.base_url}</p>
                    {item.api_key_masked && <p className="table-subtext">密钥 {item.api_key_masked}</p>}
                  </td>
                  <td data-label="类型"><StatusBadge tone="neutral">{item.type}</StatusBadge></td>
                  <td data-label="余额"><strong>{item.balance?.toFixed(2) ?? '—'}</strong> {item.currency || ''}</td>
                  <td data-label="状态">
                    <StatusBadge tone={statusTone(item.status)}>{statusLabel(item)}</StatusBadge>
                    {item.last_error && <p className="table-subtext">{item.last_error}</p>}
                  </td>
                  <td data-label="最后检查">{isZeroTime(item.last_checked_at) ? '—' : formatDateTime(item.last_checked_at)}</td>
                  <td data-label="操作">
                    <div className="table-actions">
                      <button className="button button--quiet" disabled={busy || !item.enabled} onClick={() => void refreshOne(item)}>查询</button>
                      <button className="button button--quiet" disabled={busy} onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button>
                      <button
                        className="button button--quiet"
                        onClick={() => setDraft({
                          id: item.id,
                          name: item.name,
                          type: item.type,
                          base_url: item.base_url,
                          api_key: '',
                          clearKey: false,
                          enabled: item.enabled,
                          sort_order: item.sort_order,
                        })}
                      >
                        编辑
                      </button>
                      <button className="icon-button" aria-label={`删除 ${item.name}`} onClick={() => setPendingDelete(item)}>×</button>
                    </div>
                  </td>
                </tr>)}
              </tbody>
            </table>
          </div>
        </section>
      </div>}

    <Modal
      open={draft !== null}
      title={editing ? '编辑监控点' : '新增监控点'}
      description={editing
        ? 'API Key 留空表示保持原值；要彻底删除已存的密钥请勾选下面的「清除密钥」。'
        : 'API Key 只保存在服务端，之后任何接口都只返回掩码。'}
      onClose={() => !busy && setDraft(null)}
      footer={<>
        <button className="button button--quiet" type="button" disabled={busy} onClick={() => setDraft(null)}>取消</button>
        <button className="button button--primary" type="submit" form="balance-form" disabled={busy}>
          {busy ? '保存中...' : '保存'}
        </button>
      </>}
    >
      <form id="balance-form" className="form-grid" onSubmit={save}>
        <label className="form-field">
          名称
          <input
            autoFocus
            required
            maxLength={120}
            value={draft?.name ?? ''}
            placeholder="例如 DeepSeek 主号"
            onChange={event => setDraft(current => current ? { ...current, name: event.target.value } : current)}
          />
        </label>
        <label className="form-field">
          供应商
          <select
            value={draft?.type ?? 'deepseek'}
            onChange={event => setDraft(current => current ? { ...current, type: event.target.value } : current)}
          >
            {supportedTypes.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label className="form-field form-field--wide">
          API 地址
          <input
            required
            value={draft?.base_url ?? ''}
            placeholder="https://api.deepseek.com"
            onChange={event => setDraft(current => current ? { ...current, base_url: event.target.value } : current)}
          />
        </label>
        <label className="form-field form-field--wide">
          API Key
          <input
            type="password"
            autoComplete="off"
            disabled={draft?.clearKey}
            value={draft?.api_key ?? ''}
            placeholder={editing ? '留空表示不修改' : '必填'}
            onChange={event => setDraft(current => current ? { ...current, api_key: event.target.value } : current)}
          />
        </label>
        <label className="form-field">
          排序
          <input
            type="number"
            value={draft?.sort_order ?? 0}
            onChange={event => setDraft(current => current ? { ...current, sort_order: Number(event.target.value) } : current)}
          />
        </label>
        <label className="choice-tile">
          <input
            type="checkbox"
            checked={draft?.enabled ?? true}
            onChange={event => setDraft(current => current ? { ...current, enabled: event.target.checked } : current)}
          />
          <span>启用（参与后台刷新）</span>
        </label>
        {editing && <label className="choice-tile">
          <input
            type="checkbox"
            checked={draft?.clearKey ?? false}
            onChange={event => setDraft(current => current
              ? { ...current, clearKey: event.target.checked, api_key: event.target.checked ? '' : current.api_key }
              : current)}
          />
          <span>清除已保存的密钥</span>
        </label>}
      </form>
    </Modal>

    <ConfirmDialog
      open={pendingDelete !== null}
      title="删除监控点"
      description={`将删除「${pendingDelete?.name ?? ''}」及其密钥，不可恢复。已查到的余额数字会一并消失。`}
      confirmLabel="删除"
      danger
      busy={busy}
      onClose={() => setPendingDelete(null)}
      onConfirm={() => void remove()}
    />
  </div>
}
