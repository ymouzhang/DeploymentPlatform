import { Alert, Button, Space } from 'antd'
import { Component, type ErrorInfo, type ReactNode } from 'react'

type PageErrorBoundaryProps = {
  children: ReactNode
  onHome: () => void
  resetKey: string
}

type PageErrorBoundaryState = {
  error: Error | null
}

export class PageErrorBoundary extends Component<PageErrorBoundaryProps, PageErrorBoundaryState> {
  state: PageErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): PageErrorBoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('page render failed', error, info.componentStack)
  }

  componentDidUpdate(previous: PageErrorBoundaryProps) {
    if (previous.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null })
    }
  }

  render() {
    if (!this.state.error) return this.props.children

    return (
      <div className="page-error">
        <Alert
          type="error"
          showIcon
          message="当前页面加载失败"
          description="页面数据或组件渲染出现异常，其他管理功能不受影响。你可以重试，或返回首页。"
        />
        <Space>
          <Button type="primary" onClick={() => this.setState({ error: null })}>重试</Button>
          <Button onClick={() => { this.setState({ error: null }); this.props.onHome() }}>返回首页</Button>
        </Space>
      </div>
    )
  }
}
