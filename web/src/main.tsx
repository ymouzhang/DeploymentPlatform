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
          colorPrimary: '#166b48',
          colorSuccess: '#208256',
          colorWarning: '#c57b08',
          colorError: '#d94747',
          colorText: '#17211b',
          colorTextSecondary: '#778078',
          colorBorder: '#dfe4dc',
          borderRadius: 10,
          borderRadiusLG: 16,
          colorBgLayout: '#f4f6f1',
          fontFamily: '"DM Sans", "Noto Sans SC", "PingFang SC", "Microsoft YaHei", sans-serif',
        },
        components: {
          Button: { controlHeight: 38, fontWeight: 600 },
          Table: { headerBg: '#fafbf9', headerColor: '#778078' },
          Modal: { titleFontSize: 18 },
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
