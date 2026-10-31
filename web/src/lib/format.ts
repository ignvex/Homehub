// 展示层格式化工具：URL 主机名、时间展示与摘要提取。
// 只做纯函数转换，不触碰 API 状态。

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
const WEEK = 7 * DAY

/** 取 URL 的主机名并去掉 www.；非法或无法解析出主机时退化为原始字符串的首段。 */
export function hostnameOf(rawURL: string): string {
  if (!rawURL) return ''
  try {
    // 「internal-host:3000/x」会被解析成 protocol=internal-host:，hostname 为空
    const { hostname } = new URL(rawURL)
    if (hostname) return hostname.replace(/^www\./, '')
  } catch {
    // 落到下方的手工解析
  }
  return rawURL.replace(/^https?:\/\//, '').split('/')[0]
}

/** 判断时间戳是否非零且可解析，调用方据此决定是否输出 dateTime。 */
export function isUsableTimestamp(value?: string): boolean {
  return usableTimestamp(value) !== null
}

/** 判断时间戳是否可用：Go 的零值时间会序列化成 0001-01-01。 */
function usableTimestamp(value?: string): number | null {
  if (!value) return null
  const timestamp = new Date(value).getTime()
  if (Number.isNaN(timestamp)) return null
  if (new Date(timestamp).getFullYear() < 1970) return null
  return timestamp
}

/** 相对时间：刚刚 / 12 分钟前 / 3 小时前 / 2 天前 / 3 周前 / 具体日期。 */
export function relativeTime(value?: string): string {
  const timestamp = usableTimestamp(value)
  if (timestamp === null) return ''
  const diff = Date.now() - timestamp
  if (diff < MINUTE) return '刚刚'
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)} 分钟前`
  if (diff < DAY) return `${Math.floor(diff / HOUR)} 小时前`
  if (diff < WEEK) return `${Math.floor(diff / DAY)} 天前`
  if (diff < 4 * WEEK) return `${Math.floor(diff / WEEK)} 周前`
  return new Date(timestamp).toLocaleDateString('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' })
}

/** 完整日期，用于 time 标签的 title 提示。 */
export function absoluteDate(value?: string): string {
  const timestamp = usableTimestamp(value)
  if (timestamp === null) return ''
  return new Date(timestamp).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' })
}

/**
 * 从条目正文 HTML 中提取纯文本摘要，用于列表预览。
 *
 * 用 DOMParser 而不是正则：正则容易在嵌套标签、HTML 实体和属性里的
 * 尖括号上出错，留下 “<span” 之类的碎片。解析失败时退回空串，
 * 让调用方直接不显示摘要。
 *
 * maxLength 是字符数上限而非行数——显示时不做行数截断，窄栏下
 * 由行数自然撑开，保证摘要读完。
 */
export function plainSummary(html?: string, maxLength = 300): string {
  if (!html) return ''
  try {
    const doc = new DOMParser().parseFromString(html, 'text/html')
    // 图片与脚本对摘要没有意义，直接丢弃
    doc.querySelectorAll('script, style, img, video, audio, figure').forEach(node => node.remove())
    const text = (doc.body.textContent ?? '').replace(/\s+/g, ' ').trim()
    return text.length > maxLength ? `${text.slice(0, maxLength).trimEnd()}…` : text
  } catch {
    return ''
  }
}
