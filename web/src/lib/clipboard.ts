/**
 * 复制文本到剪贴板。
 *
 * 策略：**先在用户手势的同步上下文里用 execCommand，失败再试 Clipboard API。**
 *
 * 顺序为什么是这样：
 *
 *   - `execCommand('copy')` 必须在用户手势的同步执行上下文里调用。一旦
 *     跨过 `await`，浏览器就不再认为这是用户触发，直接拒绝。所以它必须
 *     排在最前面，且不能被任何异步操作挡在中间。
 *   - `navigator.clipboard.writeText` 是异步的，能跨手势，但在非安全上下文
 *     （局域网 http://）里是 undefined，且需要文档有焦点。
 *
 * 两个都试一遍没有副作用：成功即停。
 *
 * 返回是否成功，调用方据此决定呈现方式。
 */
export async function copyText(text: string): Promise<boolean> {
  if (legacyCopy(text)) return true

  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 权限被拒或文档未聚焦，落到下面的 false。
    }
  }
  return false
}

/**
 * execCommand 降级实现。
 *
 * 三个细节决定它在真实浏览器里能不能成：
 *
 *   1. textarea 必须"可见"。完全透明（opacity:0）或移出视口会让部分
 *      浏览器判定元素不可见而拒绝复制。1px 见方 + clip 是安全的隐藏方式。
 *   2. 不用 `readonly`：某些引擎里只读 textarea 的 select() 不会真正
 *      建立选区。改用 setSelectionRange 显式指定范围。
 *   3. 必须 focus()，否则选区不落在文档上。
 */
function legacyCopy(text: string): boolean {
  if (typeof document === 'undefined') return false

  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('aria-hidden', 'true')
  area.setAttribute('tabindex', '-1')
  // 注意：不要写 opacity:0，见上面第 1 条。
  area.style.cssText =
    'position:fixed;top:0;left:0;width:1px;height:1px;padding:0;border:0;' +
    'outline:0;box-shadow:none;background:transparent;clip:rect(0 0 0 0);'

  document.body.appendChild(area)
  try {
    area.focus({ preventScroll: true })
    area.setSelectionRange(0, text.length)
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(area)
  }
}
