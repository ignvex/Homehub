import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Pie } from 'react-chartjs-2'
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js'
import { DeleteButton } from '@/components/Icon'
import { ConfirmDialog, Modal, Notice, PageState, StatusBadge } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useAuth } from '@/lib/auth'
import { CONTENT_WRITE_LEVEL, hasLevel } from '@/lib/permissions'
import { balanceApi, type BalanceStatus, type BalanceTarget, type BalanceTargetInput } from '@/lib/balance'
import { formatDateTime } from '@/lib/types'

ChartJS.register(ArcElement, Tooltip, Legend)

// 图表色板来自 variables.css 的 --chart-1..6 令牌。Chart.js 画在 canvas 上，
// 不认 var(...)，所以在渲染时用 getComputedStyle 读出实际颜色值。
const CHART_TOKENS = ['--chart-1', '--chart-2', '--chart-3', '--chart-4', '--chart-5', '--chart-6']
/** 每种供应商固定用色板里的哪一档，其余按顺序轮换。 */
const typeColorIndex: Record<string, number> = { deepseek: 0 }

function readToken(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

function readChartPalette(): { colors: string[]; surface: string } {
  return { colors: CHART_TOKENS.map(readToken), surface: readToken('--color-surface') }
}

/** useChartPalette 读取色板，并在切换明暗主题（html[data-theme] 变化）时重读。 */
function useChartPalette() {
  const [palette, setPalette] = useState(readChartPalette)
  useEffect(() => {
    const observer = new MutationObserver(() => setPalette(readChartPalette()))
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class', 'style'] })
    return () => observer.disconnect()
  }, [])
  return palette
}

function colorIndex(type: string, index: number): number {
  return (typeColorIndex[type] ?? index) % CHART_TOKENS.length
}

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
  /** 字符串草稿：清空输入框时不会被 Number('') 变成 0 静默保存。 */
  sort_order: string
}

const emptyDraft: Draft = {
  name: '',
  type: 'deepseek',
  base_url: 'https://api.deepseek.com',
  api_key: '',
  clearKey: false,
  enabled: true,
  sort_order: '0',
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
  const [loadError, setLoadError] = useState('')
  const [formError, setFormError] = useState('')
  const notice = useNotice()
  const { auth } = useAuth()
  const canWrite = hasLevel(auth, CONTENT_WRITE_LEVEL)
  const palette = useChartPalette()

  const [draft, setDraft] = useState<Draft | null>(null)
  const [pendingDelete, setPendingDelete] = useState<BalanceTarget | null>(null)

  const describeError = (cause: unknown) => errorMessage(cause, '操作失败')

  /** silent=true 时保留当前列表，失败只给提示。 */
  async function load(silent = false) {
    if (!silent) setLoading(true)
    try {
      const [list, stat] = await Promise.all([balanceApi.list(), balanceApi.status()])
      setItems(list ?? [])
      setStatus(stat)
      setLoadError('')
    } catch (cause) {
      if (silent) notice.error(describeError(cause))
      else setLoadError(describeError(cause))
    } finally {
      setLoading(false)
    }
  }

  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void load() }, [])

  async function refreshAll() {
    setBusy(true)
    try {
      await balanceApi.refresh()
      notice.success('已刷新全部监控点')
      await load(true)
    } catch (cause) {
      notice.error(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function refreshOne(item: BalanceTarget) {
    setBusy(true)
    try {
      const updated = await balanceApi.refreshOne(item.id)
      if (updated.status === 'ok') {
        notice.success(`「${item.name}」余额 ${updated.balance.toFixed(2)} ${updated.currency ?? ''}`)
      } else {
        // 查询失败是业务结果，不是请求失败：用 error 提示但保留列表。
        notice.error(updated.last_error || '查询失败')
      }
      await load(true)
    } catch (cause) {
      notice.error(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!draft) return

    const isCreate = draft.id === undefined
    if (isCreate && !draft.api_key.trim()) {
      setFormError('新建监控点必须填写 API Key')
      return
    }
    if (draft.clearKey && draft.api_key.trim()) {
      setFormError('「清除密钥」与填写新密钥只能选一个')
      return
    }
    const sortText = draft.sort_order.trim()
    if (!sortText || !Number.isInteger(Number(sortText))) {
      setFormError('排序需要填写整数')
      return
    }

    const input: BalanceTargetInput = {
      name: draft.name.trim(),
      type: draft.type,
      base_url: draft.base_url.trim(),
      enabled: draft.enabled,
      sort_order: Number(sortText),
    }
    // 三态：留空 = 不发（保留原值）；填了 = 覆盖；勾选清除 = 发空串。
    if (draft.clearKey) input.api_key = ''
    else if (draft.api_key.trim()) input.api_key = draft.api_key.trim()

    setBusy(true)
    setFormError('')
    try {
      if (isCreate) {
        await balanceApi.create(input)
        notice.success(`已创建「${input.name}」`)
      } else {
        await balanceApi.update(draft.id as number, input)
        notice.success(`已更新「${input.name}」`)
      }
      setDraft(null)
      await load(true)
    } catch (cause) {
      setFormError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!pendingDelete) return
    setBusy(true)
    setFormError('')
    try {
      await balanceApi.remove(pendingDelete.id)
      notice.success(`已删除「${pendingDelete.name}」`)
      setPendingDelete(null)
      await load(true)
    } catch (cause) {
      setFormError(describeError(cause))
    } finally {
      setBusy(false)
    }
  }

  async function toggle(item: BalanceTarget) {
    setBusy(true)
    try {
      await balanceApi.update(item.id, { enabled: !item.enabled })
      notice.success(`「${item.name}」已${item.enabled ? '停用' : '启用'}`)
      await load(true)
    } catch (cause) {
      notice.error(describeError(cause))
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
      backgroundColor: active.length ? active.map((item, i) => palette.colors[colorIndex(item.type, i)]) : [palette.colors[0]],
      borderColor: palette.surface,
      borderWidth: 3,
    }],
  }), [active, palette])

  if (loading && !items.length) return <PageState kind="loading" title="正在读取余额信息" />
  if (loadError && !items.length) return <PageState kind="error" title="无法读取余额信息" description={loadError} action={<button type="button" className="button button--primary" onClick={() => void load()}>重试</button>} />

  const editing = draft?.id !== undefined
  const closeForm = () => { setDraft(null); setPendingDelete(null); setFormError('') }
  const openCreate = () => setDraft({ ...emptyDraft, sort_order: String(items.length) })

  return <div className="business-page">
    <div className="page-toolbar page-toolbar--end">
      <div className="toolbar-actions">
        {status && <span className="toolbar-summary">
          {status.ok_count}/{status.target_count} 可用 · {isZeroTime(status.last_refresh_at) ? '未刷新' : formatDateTime(status.last_refresh_at)}
        </span>}
        {canWrite && <>
          <button type="button" className="button button--quiet" disabled={busy} onClick={() => void refreshAll()}>刷新全部</button>
          <button type="button" className="button button--primary" disabled={busy} onClick={openCreate}>新增监控点</button>
        </>}
      </div>
    </div>

    {notice.element}

    {status && !status.enabled && <Notice
      message="余额监控模块未启用：在 config.json 里把 balance_monitor.enabled 设为 true。监控点本身已经存在数据库里，不受这个开关影响。"
      tone="info"
    />}

    {!items.length
      ? <PageState
        kind="empty"
        title="还没有监控点"
        description={canWrite ? '添加一个供应商账号即可开始追踪余额。密钥只保存在服务端，读取时只显示掩码。' : '管理员或编辑添加监控点后会显示在这里。'}
        action={canWrite ? <button type="button" className="button button--primary" onClick={openCreate}>新增监控点</button> : undefined}
      />
      : <div className="balance-grid">
        <section className="data-panel balance-panel--wide">
          <header className="data-panel-head">
            <div><h2>数据资产</h2><p>启用中的监控点总余额与占比</p></div>
            <strong className="balance-total">{total.toFixed(2)}</strong>
          </header>
          <div className="balance-overview">
            <div className="balance-chart">
              <Pie data={chartData} options={{ plugins: { legend: { display: false } }, maintainAspectRatio: false }} />
            </div>
            <ul className="balance-legend">
              {active.map((item, i) => <li key={item.id}>
                <span className="balance-legend-name">
                  <span className={`balance-swatch balance-swatch--${colorIndex(item.type, i) + 1}`} aria-hidden="true" />
                  <span>{item.name}</span>
                </span>
                <strong>{item.balance?.toFixed(2) ?? '—'} {item.currency || ''}</strong>
              </li>)}
              {!active.length && <li className="balance-legend-empty">没有启用的监控点</li>}
            </ul>
          </div>
        </section>

        <section className="data-panel balance-panel--wide">
          <header className="data-panel-head">
            <div><h2>监控点</h2><p>密钥只显示掩码；编辑时留空表示保持原值</p></div>
          </header>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr><th>名称</th><th>类型</th><th>余额</th><th>状态</th><th>最后检查</th>{canWrite && <th>操作</th>}</tr>
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
                  {canWrite && <td data-label="操作">
                    <div className="table-actions">
                      <button type="button" className="button button--quiet" disabled={busy || !item.enabled} onClick={() => void refreshOne(item)}>查询</button>
                      <button type="button" className="button button--quiet" disabled={busy} onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button>
                      <button
                        type="button"
                        className="button button--quiet"
                        onClick={() => setDraft({
                          id: item.id,
                          name: item.name,
                          type: item.type,
                          base_url: item.base_url,
                          api_key: '',
                          clearKey: false,
                          enabled: item.enabled,
                          sort_order: String(item.sort_order),
                        })}
                      >
                        编辑
                      </button>
                      <DeleteButton label={`删除监控点「${item.name}」`} onClick={() => setPendingDelete(item)} />
                    </div>
                  </td>}
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
      error={formError}
      busy={busy}
      onClose={closeForm}
      footer={<>
        <button className="button button--quiet" type="button" disabled={busy} onClick={closeForm}>取消</button>
        <button className="button button--primary" type="submit" form="balance-form" disabled={busy}>
          {busy ? '保存中…' : '保存'}
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
            step={1}
            required
            value={draft?.sort_order ?? ''}
            onChange={event => setDraft(current => current ? { ...current, sort_order: event.target.value } : current)}
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
      error={formError}
      onClose={closeForm}
      onConfirm={() => void remove()}
    />
  </div>
}
