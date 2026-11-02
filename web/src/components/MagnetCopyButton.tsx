import { useEffect, useRef, useState } from 'react'
import type { MouseEvent } from 'react'
import { copyText } from '@/lib/clipboard'

/**
 * 磁力链接的复制按钮。
 *
 * 为什么是复制而不是超链接：`magnet:` 会唤起本机 BT 客户端，
 * 但实际用法通常是把链接粘到 PikPak 之类的离线下载里。
 *
 * 但仍然用 `<a href="magnet:...">` 承载，原因有两个：
 *
 *   1. 右键菜单里的「复制链接地址」始终可用。即使脚本被禁用、
 *      Clipboard API 被策略拦掉，用户还有这条路可走。
 *   2. PikPak 一类浏览器扩展监听的是 magnet 链接本身。用 `<button>`
 *      它认不出来；用 `<a href>` 即使默认行为被拦下，扩展仍能在
 *      捕获阶段拿到这次点击。
 *
 * 点击成功复制后短暂换成对钩：磁力链接很长，没有反馈就分不清点没点上。
 *
 * 复制失败时就地展开一个已全选的输入框。不用 `window.prompt`：浏览器会
 * 静默拦截它（用户勾选过「阻止此页面创建更多对话框」后尤其如此），
 * 表现就是「点了没反应」。输入框也不做失焦自动收起——聚焦失败或布局
 * 抖动会让它一闪而过，看起来同样像"没反应"。
 */
export function MagnetCopyButton({
  url,
  title,
  className,
  onCopied,
}: {
  url: string
  title: string
  /** 由调用方决定外观（RSS 页与首页的尺寸不同）。 */
  className: string
  /** 供调用方弹提示；不传则只有按钮自身的状态反馈。 */
  onCopied?: (ok: boolean) => void
}) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  const timer = useRef<number | undefined>(undefined)
  const inputRef = useRef<HTMLInputElement>(null)

  // 计时器必须跟着组件卸载一起清掉：条目列表会整批重渲染。
  useEffect(() => () => window.clearTimeout(timer.current), [])

  // 展开后立即全选，用户下一步只会按 Ctrl+C。
  useEffect(() => {
    if (!failed) return
    const input = inputRef.current
    if (!input) return
    input.focus({ preventScroll: true })
    input.select()
  }, [failed])

  async function handleClick(event: MouseEvent<HTMLAnchorElement>) {
    // 拦下默认行为，避免点一下就唤起 BT 客户端。
    event.preventDefault()

    const ok = await copyText(url)
    onCopied?.(ok)

    if (!ok) {
      setFailed(true)
      return
    }
    setCopied(true)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setCopied(false), 1600)
  }

  if (failed) {
    return (
      <span className="magnet-manual">
        <input
          ref={inputRef}
          className="magnet-manual-input"
          value={url}
          readOnly
          aria-label={`磁力链接：${title}`}
          onClick={event => event.currentTarget.select()}
          onKeyDown={event => {
            if (event.key === 'Escape' || event.key === 'Enter') setFailed(false)
          }}
        />
        <button
          type="button"
          className="magnet-manual-close"
          title="收起"
          aria-label="收起手动复制框"
          onClick={() => setFailed(false)}
        >
          ×
        </button>
      </span>
    )
  }

  return (
    <a
      href={url}
      className={`${className}${copied ? ' is-copied' : ''}`}
      title={copied ? '已复制' : '复制磁力链接（右键可复制链接地址）'}
      aria-label={`复制磁力链接：${title}`}
      onClick={event => void handleClick(event)}
    >
      {copied ? '✓' : '🧲'}
    </a>
  )
}
