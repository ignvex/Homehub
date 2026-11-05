import { useEffect, useRef, useState, type ReactNode } from 'react'

export function PageState({ kind, title, description, action }: { kind: 'loading' | 'empty' | 'error'; title: string; description?: string; action?: ReactNode }) {
  return (
    <div className={`page-state page-state--${kind}`} role={kind === 'error' ? 'alert' : 'status'} aria-busy={kind === 'loading'}>
      {kind === 'loading' && <span className="page-spinner" aria-hidden="true" />}
      <strong>{title}</strong>
      {description && <p>{description}</p>}
      {action}
    </div>
  )
}

export function Notice({ message, tone = 'success', onDismiss }: { message?: string; tone?: 'success' | 'error' | 'info'; onDismiss?: () => void }) {
  if (!message) return null
  return (
    <div className={`notice notice--${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      <span>{message}</span>
      {onDismiss && <button type="button" className="icon-button" onClick={onDismiss} aria-label="关闭消息">×</button>}
    </div>
  )
}

export interface ModalProps {
  open: boolean
  title: string
  description?: string
  children?: ReactNode
  onClose: () => void
  footer?: ReactNode
  size?: 'small' | 'medium' | 'large'
  /** 弹窗内的错误提示。弹窗处于 top layer，页面上的提示会被遮罩挡住，所以错误要画在这里。 */
  error?: string
  /** 提交中：Esc、遮罩、× 都不关闭。 */
  busy?: boolean
  /** 有未保存内容：关闭前先确认。 */
  dirty?: boolean
  /** false 时只能通过 footer 里的按钮关闭（Esc、遮罩、× 均无效，也不渲染 ×）。 */
  dismissible?: boolean
}

const DIRTY_CLOSE_MESSAGE = '有未保存的更改，确定要关闭吗？'

export function Modal({ open, title, description, children, onClose, footer, size = 'medium', error, busy = false, dirty = false, dismissible = true }: ModalProps) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const returnFocusRef = useRef<HTMLElement | null>(null)
  const pointerDownOnDialog = useRef(false)
  // 原生事件监听只注册一次，通过 ref 读取最新的 props。
  const latest = useRef({ open, onClose, busy, dirty, dismissible })
  latest.current = { open, onClose, busy, dirty, dismissible }

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) {
      returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
      dialog.showModal()
      // showModal 会聚焦第一个可聚焦元素；如果内容里有 autoFocus 元素，以它为准。
      const preferred = dialog.querySelector<HTMLElement>('[autofocus], .modal-body :is(input, select, textarea, button):not(:disabled)')
      preferred?.focus()
    }
    if (!open && dialog.open) {
      dialog.close()
      const target = returnFocusRef.current
      returnFocusRef.current = null
      if (target?.isConnected) target.focus()
    }
  }, [open])

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    const cancel = (event: Event) => { event.preventDefault(); requestClose(latest.current) }
    // 浏览器可能无视 preventDefault 强制关闭（连续按 Esc 的防滥用策略），此时与 props 重新对齐。
    const closed = () => {
      const state = latest.current
      if (!state.open) return
      if (state.dismissible && !state.busy && !state.dirty) state.onClose()
      else dialog.showModal()
    }
    dialog.addEventListener('cancel', cancel)
    dialog.addEventListener('close', closed)
    return () => { dialog.removeEventListener('cancel', cancel); dialog.removeEventListener('close', closed) }
  }, [])

  return (
    <dialog
      ref={dialogRef}
      className={`workspace-modal workspace-modal--${size}`}
      aria-busy={busy || undefined}
      // 只有按下与松开都落在遮罩（dialog 自身）上才关闭，避免在输入框里拖选文字、松手到外面时误关。
      onPointerDown={event => { pointerDownOnDialog.current = event.target === dialogRef.current }}
      onClick={event => {
        const fromBackdrop = pointerDownOnDialog.current && event.target === dialogRef.current
        pointerDownOnDialog.current = false
        if (fromBackdrop) requestClose(latest.current)
      }}
    >
      <div className="modal-head">
        <div><h2>{title}</h2>{description && <p>{description}</p>}</div>
        {dismissible && <button type="button" className="icon-button" onClick={() => requestClose(latest.current)} disabled={busy} aria-label="关闭对话框">×</button>}
      </div>
      {(error || children) && <div className="modal-body">
        {error && <div className="notice notice--error modal-error" role="alert"><span>{error}</span></div>}
        {children}
      </div>}
      {footer && <div className="modal-footer">{footer}</div>}
    </dialog>
  )
}

function requestClose({ onClose, busy, dirty, dismissible }: { onClose: () => void; busy: boolean; dirty: boolean; dismissible: boolean }) {
  if (busy || !dismissible) return
  if (dirty && !window.confirm(DIRTY_CLOSE_MESSAGE)) return
  onClose()
}

/** 确认弹窗。description 需要自己说清楚后果，组件不再附加固定文案。 */
export function ConfirmDialog({ open, title, description, confirmLabel = '确认', busy = false, danger = false, error, onConfirm, onClose }: { open: boolean; title: string; description: string; confirmLabel?: string; busy?: boolean; danger?: boolean; error?: string; onConfirm: () => void; onClose: () => void }) {
  return (
    <Modal open={open} title={title} onClose={onClose} size="small" busy={busy} error={error} footer={<><button type="button" className="button button--quiet" onClick={onClose} disabled={busy}>取消</button><button type="button" className={`button ${danger ? 'button--danger' : 'button--primary'}`} onClick={onConfirm} disabled={busy}>{busy ? '处理中…' : confirmLabel}</button></>}>
      <p className="confirm-copy">{description}</p>
    </Modal>
  )
}

export function Pagination({ page, perPage, total, onChange, disabled = false }: { page: number; perPage: number; total: number; onChange: (page: number) => void; disabled?: boolean }) {
  const pages = Math.max(1, Math.ceil(total / perPage))
  const [jump, setJump] = useState('')
  const doJump = () => { const n = parseInt(jump, 10); if (n >= 1 && n <= pages) { onChange(n); setJump('') } }
  return (
    <nav className="pager" aria-label="分页">
      <button type="button" className="button button--quiet" disabled={disabled || page <= 1} onClick={() => onChange(page - 1)}>上一页</button>
      <span>第 {page} / {pages} 页，共 {total.toLocaleString()} 项</span>
      <button type="button" className="button button--quiet" disabled={disabled || page >= pages} onClick={() => onChange(page + 1)}>下一页</button>
      {pages > 1 && (
        <span className="pager-jump">
          跳至 <input type="number" className="pager-jump-input" aria-label="跳转到页码" min={1} max={pages} value={jump} onChange={e => setJump(e.target.value)} onKeyDown={e => e.key === 'Enter' && doJump()} disabled={disabled} /> 页
          <button type="button" className="button button--quiet" disabled={disabled} onClick={doJump}>跳转</button>
        </span>
      )}
    </nav>
  )
}

export function StatusBadge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' }) {
  return <span className={`status-badge status-badge--${tone}`}>{children}</span>
}
