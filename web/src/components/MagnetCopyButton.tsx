import { useEffect, useRef, useState } from 'react'
import { copyText } from '@/lib/clipboard'

/**
 * 磁力链接的复制按钮。
 *
 * 为什么是复制而不是超链接：`magnet:` 会唤起本机 BT 客户端，
 * 但实际用法通常是把链接粘到 PikPak 之类的离线下载里。
 * 打开客户端这件事用户自己能做，复制到剪贴板才是这里省事的一步。
 *
 * 复制成功后短暂换成对钩：磁力链接很长，没有反馈就分不清点没点上。
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
  /** 供调用方弹提示；不传则只有按钮自身的对钩反馈。 */
  onCopied?: (ok: boolean) => void
}) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  // 条目列表会整批重渲染，计时器必须跟着组件卸载一起清掉。
  useEffect(() => () => window.clearTimeout(timer.current), [])

  async function handleClick() {
    const ok = await copyText(url)
    if (ok) {
      setCopied(true)
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setCopied(false), 1600)
    } else {
      // 两条复制路径都失败时（非安全上下文 + execCommand 被禁），
      // 把链接摊开让人自己选。按钮化之后，否则这个链接就彻底取不到了。
      window.prompt('自动复制失败，请手动复制以下链接', url)
    }
    onCopied?.(ok)
  }

  return (
    <button
      type="button"
      className={`${className}${copied ? ' is-copied' : ''}`}
      title={copied ? '已复制' : '复制磁力链接'}
      aria-label={`复制磁力链接：${title}`}
      onClick={() => void handleClick()}
    >
      {copied ? '✓' : '🧲'}
    </button>
  )
}
