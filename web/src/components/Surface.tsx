import { useId } from 'react'
import type { ReactNode } from 'react'
import { StatusBadge } from '@/components/WorkspaceUI'
import type { Tone } from '@/lib/tone'

// ============================================================
// 界面构件 — 面板、度量行、状态灯
//
// 组件样式统一定义在 styles/components.css，这里只负责结构与语义。
// 仪表盘、RSS、系统日志三处复用同一套构件。
// ============================================================

/** 面板：带图标的标题栏 + 内容区 + 可选页脚。 */
export function Panel({ title, icon, action, footer, children, labelledBy }: {
  title: ReactNode
  icon?: ReactNode
  action?: ReactNode
  footer?: ReactNode
  children: ReactNode
  labelledBy?: string
}) {
  const autoID = useId()
  const headingID = labelledBy ?? autoID
  return <section className="data-panel" aria-labelledby={headingID}>
    <header className="data-panel-head">
      <div className="panel-title">
        {icon && <span className="panel-icon" aria-hidden="true">{icon}</span>}
        <h2 id={headingID}>{title}</h2>
      </div>
      {action && <div className="panel-action">{action}</div>}
    </header>
    {children}
    {footer && <footer className="panel-foot">{footer}</footer>}
  </section>
}

/** 度量行：左标签（可带说明），右值（可带状态徽章与备注）。 */
export function MetricRow({ label, hint, value, tone, note, mono = true }: {
  label: ReactNode
  hint?: ReactNode
  value: ReactNode
  tone?: Tone
  note?: ReactNode
  mono?: boolean
}) {
  return <div className="metric-row">
    <div className="metric-label">
      <span>{label}</span>
      {hint && <small>{hint}</small>}
    </div>
    <div className="metric-value">
      {tone ? <StatusBadge tone={tone}>{value}</StatusBadge> : <span className={mono ? 'metric-number' : undefined}>{value}</span>}
      {note && <small className="metric-note">{note}</small>}
    </div>
  </div>
}

/** 状态灯：纯色圆点，颜色由 tone 决定。 */
export function StatusDot({ tone }: { tone: 'ok' | 'error' | 'warn' | 'idle' }) {
  const cls = tone === 'ok' ? 'is-ok' : tone === 'error' ? 'is-error' : tone === 'warn' ? 'is-warn' : ''
  return <span className={`status-dot ${cls}`} aria-hidden="true" />
}
