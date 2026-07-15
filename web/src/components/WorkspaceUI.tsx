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

export function Modal({ open, title, description, children, onClose, footer, size = 'medium' }: { open: boolean; title: string; description?: string; children: ReactNode; onClose: () => void; footer?: ReactNode; size?: 'small' | 'medium' | 'large' }) {
  const dialogRef = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    const cancel = (event: Event) => { event.preventDefault(); onClose() }
    dialog.addEventListener('cancel', cancel)
    return () => dialog.removeEventListener('cancel', cancel)
  }, [onClose])

  return (
    <dialog ref={dialogRef} className={`workspace-modal workspace-modal--${size}`} onClick={event => { if (event.target === dialogRef.current) onClose() }}>
      <div className="modal-head">
        <div><h2>{title}</h2>{description && <p>{description}</p>}</div>
        <button type="button" className="icon-button" onClick={onClose} aria-label="关闭对话框">×</button>
      </div>
      <div className="modal-body">{children}</div>
      {footer && <div className="modal-footer">{footer}</div>}
    </dialog>
  )
}

export function ConfirmDialog({ open, title, description, confirmLabel = '确认', busy = false, danger = false, onConfirm, onClose }: { open: boolean; title: string; description: string; confirmLabel?: string; busy?: boolean; danger?: boolean; onConfirm: () => void; onClose: () => void }) {
  return (
    <Modal open={open} title={title} description={description} onClose={onClose} size="small" footer={<><button type="button" className="button button--quiet" onClick={onClose} disabled={busy}>取消</button><button type="button" className={`button ${danger ? 'button--danger' : 'button--primary'}`} onClick={onConfirm} disabled={busy}>{busy ? '处理中...' : confirmLabel}</button></>}>
      <p className="confirm-copy">此操作会立即生效。</p>
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
          跳至 <input type="number" className="pager-jump-input" min={1} max={pages} value={jump} onChange={e => setJump(e.target.value)} onKeyDown={e => e.key === 'Enter' && doJump()} disabled={disabled} /> 页
          <button type="button" className="button button--quiet" disabled={disabled} onClick={doJump}>跳转</button>
        </span>
      )}
    </nav>
  )
}

export function StatusBadge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' }) {
  return <span className={`status-badge status-badge--${tone}`}>{children}</span>
}
