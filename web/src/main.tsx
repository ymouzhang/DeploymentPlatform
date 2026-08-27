import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ConfigProvider, App as AntApp, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import 'antd/dist/reset.css'
import { App } from './app/App'
import './styles/global.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 3_000,
      retry: 1,
      refetchOnWindowFocus: true,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        algorithm: theme.defaultAlgorithm,
        token: {
          colorPrimary: '#4f5bd5',
          colorInfo: '#4f5bd5',
          colorSuccess: '#21845a',
          colorWarning: '#b56a00',
          colorError: '#c73e4d',
          colorText: '#202127',
          colorTextSecondary: '#696c76',
          colorBorder: '#d9dce5',
          colorBorderSecondary: '#e7e9ef',
          colorFillSecondary: '#f0f1f5',
          colorFillTertiary: '#f5f6f8',
          borderRadius: 12,
          borderRadiusLG: 20,
          colorBgLayout: '#f4f5f8',
          colorBgContainer: '#ffffff',
          colorBgElevated: '#ffffff',
          fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", "PingFang SC", "Microsoft YaHei", sans-serif',
        },
        components: {
          Button: { controlHeight: 40, fontWeight: 600, borderRadius: 12 },
          Input: { controlHeight: 40, activeShadow: '0 0 0 4px rgba(79,91,213,.12)' },
          Select: { controlHeight: 40, activeOutlineColor: 'rgba(79,91,213,.12)' },
          Table: { headerBg: '#f6f7fa', headerColor: '#696c76', rowHoverBg: '#f7f8ff' },
          Modal: { titleFontSize: 20 },
        },
      }}
    >
      <AntApp>
        <QueryClientProvider client={queryClient}>
          <App />
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  </StrictMode>,
)
