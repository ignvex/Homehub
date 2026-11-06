import { Component, type ErrorInfo, type ReactNode } from 'react'
import { PageState } from '@/components/WorkspaceUI'

interface Props {
  children: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
}

/**
 * ErrorBoundary 捕获子树渲染时的异常，显示友好提示而不是白屏。
 *
 * React 的错误边界只能写成 class 组件。捕获后卸载整棵子树，
 * 用户刷新页面可以恢复（如果错误是偶发的）。
 */
export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    // 生产环境下只记录到控制台；开发环境下 React DevTools 会显示完整堆栈
    console.error('ErrorBoundary caught an error:', error, errorInfo)
  }

  render() {
    if (this.state.hasError) {
      const message = this.state.error?.message || '未知错误'
      return (
        <PageState
          kind="error"
          title="页面渲染失败"
          description={`${message}。刷新页面可能恢复。`}
          action={
            <button
              type="button"
              className="button button--primary"
              onClick={() => window.location.reload()}
            >
              刷新页面
            </button>
          }
        />
      )
    }

    return this.props.children
  }
}
