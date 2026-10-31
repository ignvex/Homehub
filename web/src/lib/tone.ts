// 状态语义映射：把后端返回的状态字符串归一到徽章色调。
// 单独成文件，避免与组件混在一起破坏 Fast Refresh。

export type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info'

/** 把健康检查 / 余额查询的状态字符串映射到徽章色调。 */
export function toneOf(status?: string): Tone {
  switch (status) {
    case 'ok':
    case 'success':
      return 'success'
    case 'error':
    case 'unreachable':
      return 'danger'
    case 'warn':
    case 'warning':
      return 'warning'
    default:
      return 'neutral'
  }
}
