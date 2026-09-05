// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { DashboardPage } from './DashboardPage'

vi.mock('../../api/client', () => ({ api: { adminDashboard: vi.fn(), listTags: vi.fn() } }))

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })),
})

describe('DashboardPage', () => {
  afterEach(() => vi.clearAllMocks())

  it('shows package and recent login failure metrics', async () => {
    vi.mocked(api.listTags).mockResolvedValue([])
    vi.mocked(api.adminDashboard).mockResolvedValue({
      metrics: {
        users: 3, enabled_users: 2, disabled_users: 1, packages: 7, hosts: 3,
        models: 6, ready_models: 3, processing_models: 2, failed_models: 1, service_instances: 4,
        installed_services: 2, running_services: 1, active_operations: 0,
        failed_operations_24h: 1, login_failures_24h: 5, unvalidated_hosts: 0,
        stale_validation_hosts: 0, unhealthy_installed_services: 0,
        high_risk_audits_24h: 2, unread_notifications: 3, unread_communications: 2,
      },
      communications: [{
        id: 'thread-1', target_user_id: 'user-1', target_username: 'alice', title: '请确认部署结果', status: 'open',
        reopen_count: 0, created_by: 'admin-1', created_by_username: 'admin', closed_by_username: '',
        last_reopened_by_username: '', created_at: '2026-08-14T01:00:00Z', updated_at: '2026-08-14T02:00:00Z',
        unread_count: 2, last_message: '服务已启动，请查看', resources: [],
      }],
      notifications: [],
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(<QueryClientProvider client={client}><MemoryRouter><DashboardPage /></MemoryRouter></QueryClientProvider>)
    await waitFor(() => expect(view.getByText('安装包')).toBeTruthy())
	expect(view.getByText('模型')).toBeTruthy()
	expect(view.getByText('3 可用 · 2 处理中 · 1 失败')).toBeTruthy()
    expect(view.getByText('登录失败')).toBeTruthy()
    expect(view.getByText('5')).toBeTruthy()
    expect(view.getByText('待处理消息')).toBeTruthy()
    expect(view.getByText('请确认部署结果')).toBeTruthy()
    expect(view.getByText('服务已启动，请查看')).toBeTruthy()
  })
})
