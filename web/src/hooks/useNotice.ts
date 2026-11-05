import { createElement, useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from 'react'
import { Notice } from '@/components/WorkspaceUI'

export type NoticeTone = 'success' | 'error' | 'info'
export interface NoticeState { tone: NoticeTone; message: string; id: number }

/** success / info 自动消失的时长；error 保留到手动关闭。 */
const AUTO_DISMISS_MS = 4500

export interface NoticeController {
  notice: NoticeState | null
  success: (message: string) => void
  error: (message: string) => void
  info: (message: string) => void
  clear: () => void
  /** 渲染位置固定的提示区（aria-live），没有提示时也保留容器以便读屏器播报。 */
  element: ReactElement
}

/**
 * 页面级单条提示：同一时刻只保留一条，新的覆盖旧的。
 * 取代页面里 message / error 两个独立状态（会出现成功与失败同时显示）。
 */
export function useNotice(): NoticeController {
  const [notice, setNotice] = useState<NoticeState | null>(null)
  const seq = useRef(0)

  const show = useCallback((tone: NoticeTone, message: string) => {
    if (!message) { setNotice(null); return }
    seq.current += 1
    setNotice({ tone, message, id: seq.current })
  }, [])

  useEffect(() => {
    if (!notice || notice.tone === 'error') return
    const timer = window.setTimeout(() => setNotice(current => current?.id === notice.id ? null : current), AUTO_DISMISS_MS)
    return () => window.clearTimeout(timer)
  }, [notice])

  const success = useCallback((message: string) => show('success', message), [show])
  const error = useCallback((message: string) => show('error', message), [show])
  const info = useCallback((message: string) => show('info', message), [show])
  const clear = useCallback(() => setNotice(null), [])

  const element = useMemo(() => createElement('div', { className: 'notice-region', 'aria-live': 'polite', 'aria-atomic': true },
    notice ? createElement(Notice, { key: notice.id, message: notice.message, tone: notice.tone, onDismiss: clear, live: false }) : null,
  ), [notice, clear])

  return { notice, success, error, info, clear, element }
}

/** 把 catch 到的异常转成可展示的文案。 */
export function errorMessage(cause: unknown, fallback: string): string {
  return cause instanceof Error && cause.message ? cause.message : fallback
}
