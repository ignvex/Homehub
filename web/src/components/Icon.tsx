import type { ReactNode } from 'react'

// ============================================================
// 线框图标：统一 24×24 视口，描边色跟随 currentColor。
// 尺寸与线宽由 components.css 的 .icon 规则决定。
// ============================================================
function Svg({ children }: { children: ReactNode }) {
  return <svg className="icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">{children}</svg>
}

export function TrashIcon() {
  return <Svg><path d="M4 7h16M9 7V4.5h6V7M6.5 7l1 12.5h9l1-12.5M10 11v5.5M14 11v5.5" /></Svg>
}

export function CopyIcon() {
  return <Svg><rect x="8" y="8" width="11" height="12" rx="1" /><path d="M5 15V4h10" /></Svg>
}

/** 删除按钮：垃圾桶图标 + 危险色。label 应包含对象名，如「删除书签“GitHub”」。 */
export function DeleteButton({ label, onClick, disabled = false }: { label: string; onClick: () => void; disabled?: boolean }) {
  return <button type="button" className="icon-button icon-button--danger" aria-label={label} title={label} disabled={disabled} onClick={onClick}><TrashIcon /></button>
}
