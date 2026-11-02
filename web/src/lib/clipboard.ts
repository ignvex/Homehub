/**
 * 复制文本到剪贴板。
 *
 * 优先用 Clipboard API；不可用时退回 execCommand。
 *
 * 为什么要降级：`navigator.clipboard` 只在安全上下文（HTTPS 或 localhost）
 * 里存在。HomeHub 常见部署是局域网里的 `http://192.168.x.x:8080`，
 * 那种情况下它是 undefined，直接调用会抛 TypeError，按钮看起来"没反应"。
 *
 * 返回是否成功，调用方据此决定提示文案。
 */
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 权限被拒或非安全上下文，继续尝试降级方案。
    }
  }

  try {
    const area = document.createElement('textarea')
    area.value = text
    // 固定定位并移出视口，避免复制瞬间页面滚动。
    area.style.position = 'fixed'
    area.style.top = '-9999px'
    area.style.opacity = '0'
    area.setAttribute('readonly', '')
    document.body.appendChild(area)
    area.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(area)
    return ok
  } catch {
    return false
  }
}
