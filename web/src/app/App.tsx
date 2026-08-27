import {
  CloudServerOutlined,
  DeploymentUnitOutlined,
  FileZipOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  SafetyCertificateOutlined,
  TeamOutlined,
  LogoutOutlined,
  KeyOutlined,
  AuditOutlined,
  DashboardOutlined,
  BellOutlined,
  HistoryOutlined,
  LaptopOutlined,
} from '@ant-design/icons'
import { lazy, Suspense, useEffect, useState } from 'react'
import { Alert, App as AntApp, Badge, Button, Dropdown, Form, Input, Layout, Menu, Modal, Select, Space, Table, Tag, Tooltip, Typography } from 'antd'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { AuthContext } from './AuthContext'
import { LoginPage } from '../features/auth/LoginPage'
import { communicationKeys } from '../features/communications/queryKeys'
import { RealtimeEvents } from '../realtime/RealtimeEvents'
import { HeaderMessageEntry, SidebarMessageIcon, SidebarMessageLabel } from '../features/communications/MessageEntrypoints'

const { Header, Sider, Content } = Layout
const EnvironmentsPage = lazy(() =>
  import('../features/environments/EnvironmentsPage').then((module) => ({
    default: module.EnvironmentsPage,
  })),
)
const ServicesPage = lazy(() =>
  import('../features/services/ServicesPage').then((module) => ({
    default: module.ServicesPage,
  })),
)
const PackagesPage = lazy(() =>
  import('../features/packages/PackagesPage').then((module) => ({
    default: module.PackagesPage,
  })),
)
const UsersPage = lazy(() => import('../features/users/UsersPage').then((module) => ({ default: module.UsersPage })))
const AuditPage = lazy(() => import('../features/audit/AuditPage').then((module) => ({ default: module.AuditPage })))
const DashboardPage = lazy(() => import('../features/admin/DashboardPage').then((module) => ({ default: module.DashboardPage })))
const OperationsPage = lazy(() => import('../features/operations/OperationsPage').then((module) => ({ default: module.OperationsPage })))
const NotificationsPage = lazy(() => import('../features/notifications/NotificationsPage').then((module) => ({ default: module.NotificationsPage })))
const CommunicationsPage = lazy(() => import('../features/communications/CommunicationsPage').then((module) => ({ default: module.CommunicationsPage })))

function Shell() {
  const { message } = AntApp.useApp()
  const queryClient = useQueryClient()
  const meQuery = useQuery({ queryKey: ['me'], queryFn: api.me })
  const usersQuery = useQuery({ queryKey: ['users'], queryFn: api.listUsers, enabled: meQuery.data?.role === 'admin' && !meQuery.data.must_change_password })
  const notificationQuery = useQuery({ queryKey: ['notification-summary'], queryFn: api.notificationSummary, enabled: meQuery.data?.role === 'admin' && !meQuery.data.must_change_password, refetchInterval: 30_000 })
  const communicationQuery = useQuery({ queryKey: communicationKeys.summary, queryFn: api.communicationSummary, enabled: Boolean(meQuery.data) && !meQuery.data?.must_change_password, refetchInterval: 30_000 })
  const [ownerId, setOwnerId] = useState<string | undefined>()
  const [loggedOut, setLoggedOut] = useState(false)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [sessionsOpen, setSessionsOpen] = useState(false)
  const [passwordForm] = Form.useForm()
  const sessionsQuery = useQuery({ queryKey: ['own-sessions'], queryFn: api.listOwnSessions, enabled: sessionsOpen })
  const location = useLocation()
  const navigate = useNavigate()
  const [siderCollapsed, setSiderCollapsed] = useState(
    () => window.localStorage.getItem('dp:sider-collapsed') === 'true',
  )
  useEffect(() => {
    if (meQuery.data?.role !== 'admin' || !['/packages', '/environments', '/services'].includes(location.pathname)) return
    const requested = new URLSearchParams(location.search).get('owner_id') ?? undefined
    if (!requested || usersQuery.data?.some((item) => item.id === requested)) setOwnerId(requested)
  }, [location.pathname, location.search, meQuery.data?.role, usersQuery.data])
  if (meQuery.isLoading && !loggedOut) return <div className="page-loading">正在验证登录状态…</div>
  if (loggedOut || !meQuery.data) return <LoginPage onLogin={(user) => { queryClient.setQueryData(['me'], user); setLoggedOut(false); navigate(user.role === 'admin' ? '/dashboard' : '/packages', { replace: true }) }} />
  const user = meQuery.data
  const users = usersQuery.data ?? []
  const communicationUnread = communicationQuery.data?.unread ?? 0
  const finishLogout = () => { setLoggedOut(true); setOwnerId(undefined); queryClient.clear(); navigate('/login', { replace: true }) }
  const submitPassword = async (values: { current_password: string; new_password: string }) => {
    try {
      await api.changePassword(values)
      message.success('密码已修改，请使用新密码重新登录')
      setPasswordOpen(false)
      passwordForm.resetFields()
      finishLogout()
    } catch (error) {
      message.error((error as Error).message)
    }
  }
  if (user.must_change_password) return <div className="forced-password-page"><div className="forced-password-card"><div className="forced-password-mark"><KeyOutlined /></div><Typography.Title level={2}>设置你的新密码</Typography.Title><Typography.Paragraph type="secondary">当前账号正在使用系统初始化或管理员分配的临时密码。完成修改后，才能继续访问部署资源。</Typography.Paragraph><Alert type="info" showIcon message="修改成功后将退出当前会话，请使用新密码重新登录。" /><Form form={passwordForm} layout="vertical" onFinish={submitPassword}><Form.Item name="current_password" label="当前临时密码" rules={[{ required: true, message: '请输入当前临时密码' }]}><Input.Password autoFocus /></Form.Item><Form.Item name="new_password" label="新密码" rules={[{ required: true, message: '请输入新密码' }, { min: 8, max: 128 }]}><Input.Password /></Form.Item><Form.Item name="confirm_password" label="确认新密码" dependencies={['new_password']} rules={[{ required: true, message: '请再次输入新密码' }, ({ getFieldValue }) => ({ validator(_, value) { return !value || getFieldValue('new_password') === value ? Promise.resolve() : Promise.reject(new Error('两次输入的密码不一致')) } })]}><Input.Password /></Form.Item><Button block type="primary" htmlType="submit">完成密码修改</Button><Button block type="text" icon={<LogoutOutlined />} onClick={async () => { await api.logout(); finishLogout() }}>退出登录</Button></Form></div></div>
  const pageName =
    location.pathname === '/dashboard'
      ? '管理总览'
      : location.pathname === '/packages'
      ? '安装包管理'
      : location.pathname === '/services'
        ? '服务管理'
        : location.pathname === '/users'
          ? '账号管理'
          : location.pathname === '/operations'
            ? '操作中心'
            : location.pathname === '/notifications'
              ? '通知中心'
              : location.pathname === '/communications'
                ? '消息中心'
              : location.pathname === '/audit' ? '审计日志' : '环境管理'

  const toggleSider = () => {
    setSiderCollapsed((collapsed) => {
      const next = !collapsed
      window.localStorage.setItem('dp:sider-collapsed', String(next))
      return next
    })
  }

  return (
    <Layout className="app-shell">
      <Sider
        width={248}
        collapsedWidth={82}
        collapsed={siderCollapsed}
        trigger={null}
        className={`app-sider${siderCollapsed ? ' is-collapsed' : ''}`}
      >
        <div className="brand">
          <div className="brand-mark">
            <span>IF</span>
          </div>
          <div className="brand-copy">
            <div className="brand-name">DP Console</div>
            <div className="brand-subtitle">DEPLOYMENT HUB</div>
          </div>
        </div>
        <div className="nav-section-label">工作台</div>
        <Menu
          mode="inline"
          inlineCollapsed={siderCollapsed}
          selectedKeys={[location.pathname]}
          onClick={({ key }) => navigate(key)}
          items={[
            ...(user.role === 'admin' ? [{ key: '/dashboard', icon: <DashboardOutlined />, label: '管理总览' }] : []),
            { key: '/packages', icon: <FileZipOutlined />, label: '安装包管理' },
            { key: '/environments', icon: <CloudServerOutlined />, label: '环境管理' },
            { key: '/services', icon: <DeploymentUnitOutlined />, label: '服务管理' },
            { key: '/communications', icon: <SidebarMessageIcon unread={communicationUnread} />, label: <SidebarMessageLabel unread={communicationUnread} /> },
            ...(user.role === 'admin' ? [{ key: '/users', icon: <TeamOutlined />, label: '账号管理' }] : []),
            ...(user.role === 'admin' ? [{ key: '/operations', icon: <HistoryOutlined />, label: '操作中心' }] : []),
            ...(user.role === 'admin' ? [{ key: '/audit', icon: <AuditOutlined />, label: '审计日志' }] : []),
            ...(user.role === 'admin' ? [{ key: '/notifications', icon: <BellOutlined />, label: <span>通知中心 <Badge size="small" count={notificationQuery.data?.unread ?? 0} overflowCount={99} /></span> }] : []),
          ]}
        />
        <Tooltip title={siderCollapsed ? 'SSH 凭据加密存储' : undefined} placement="right">
          <div className="sider-security">
            <span className="security-dot" />
            <div><strong>凭据安全</strong><span>AES 加密 · 本地存储</span></div>
          </div>
        </Tooltip>
      </Sider>
      <Layout className={`app-main${siderCollapsed ? ' is-sider-collapsed' : ''}`}>
        <Header className="app-header">
          <div className="header-leading">
            <Tooltip title={siderCollapsed ? '展开侧边栏' : '折叠侧边栏'}>
              <Button
                type="text"
                className="sider-toggle"
                icon={siderCollapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
                aria-label={siderCollapsed ? '展开侧边栏' : '折叠侧边栏'}
                onClick={toggleSider}
              />
            </Tooltip>
            <div className="header-context">
              <span>控制台</span>
              <i>/</i>
              <strong>{pageName}</strong>
            </div>
          </div>
          <Space>
            {user.role === 'admin' && ['/packages', '/environments', '/services'].includes(location.pathname) && <Space size={6}><Typography.Text type="secondary">数据范围</Typography.Text><Select allowClear placeholder="全部账号" value={ownerId} style={{ width: 180 }} options={users.map((item) => ({ value: item.id, label: item.username }))} onChange={(value) => setOwnerId(value)} /></Space>}
            <div className="system-state" aria-label="系统连接正常"><span className="system-state-dot" />系统在线</div>
            <HeaderMessageEntry key={`message-${communicationUnread}`} unread={communicationUnread} onClick={() => navigate('/communications')} />
            <Dropdown menu={{ items: [{ key: 'sessions', icon: <LaptopOutlined />, label: '登录会话' }, { key: 'password', icon: <KeyOutlined />, label: '修改密码' }, { key: 'logout', icon: <LogoutOutlined />, label: '退出登录' }], onClick: async ({ key }) => { if (key === 'sessions') setSessionsOpen(true); else if (key === 'password') setPasswordOpen(true); else { await api.logout(); finishLogout() } } }}>
              <Button className="account-button"><span className="account-avatar">{user.username.slice(0, 1).toUpperCase()}</span><span>{user.username}</span><span className="account-role">{user.role === 'admin' ? '管理员' : '成员'}</span></Button>
            </Dropdown>
          </Space>
        </Header>
        <Content className="app-content">
          <AuthContext.Provider value={{ user, ownerId, setOwnerId, users, logout: () => void 0 }}>
            <RealtimeEvents />
            <Suspense fallback={<div className="page-loading">正在加载…</div>}>
              <Routes>
                {user.role === 'admin' && <Route path="/dashboard" element={<DashboardPage />} />}
                <Route path="/packages" element={<PackagesPage />} />
                <Route path="/environments" element={<EnvironmentsPage />} />
                <Route path="/services" element={<ServicesPage />} />
                <Route path="/communications" element={<CommunicationsPage />} />
                {user.role === 'admin' && <Route path="/users" element={<UsersPage />} />}
                {user.role === 'admin' && <Route path="/audit" element={<AuditPage />} />}
                {user.role === 'admin' && <Route path="/operations" element={<OperationsPage />} />}
                {user.role === 'admin' && <Route path="/notifications" element={<NotificationsPage />} />}
                <Route path="*" element={<Navigate to={user.role === 'admin' ? '/dashboard' : '/packages'} replace />} />
              </Routes>
            </Suspense>
          </AuthContext.Provider>
        </Content>
      </Layout>
      <Modal title="修改密码" open={passwordOpen} onCancel={() => setPasswordOpen(false)} onOk={() => passwordForm.validateFields().then(submitPassword)}><Form form={passwordForm} layout="vertical"><Form.Item name="current_password" label="当前密码" rules={[{ required: true }]}><Input.Password /></Form.Item><Form.Item name="new_password" label="新密码" rules={[{ required: true }, { min: 8, max: 128 }]}><Input.Password /></Form.Item></Form></Modal>
      <Modal width={1080} title="登录会话" open={sessionsOpen} footer={null} onCancel={() => setSessionsOpen(false)}><Typography.Paragraph type="secondary">查看账号当前登录位置并撤销不再使用的会话。撤销当前会话会立即退出登录。</Typography.Paragraph><Table rowKey="id" size="small" pagination={false} scroll={{ x: 980 }} loading={sessionsQuery.isLoading} dataSource={sessionsQuery.data ?? []} columns={[{ title: '客户端', dataIndex: 'user_agent', width: 210, ellipsis: true, render: (value: string) => value || '未知客户端' }, { title: '来源 IP', dataIndex: 'source_ip', width: 130, render: (value: string) => value || '-' }, { title: '登录时间', dataIndex: 'created_at', width: 170, render: (value: string) => new Date(value).toLocaleString('zh-CN') }, { title: '最近活动', dataIndex: 'last_seen_at', width: 170, render: (value: string) => new Date(value).toLocaleString('zh-CN') }, { title: '过期时间', dataIndex: 'expires_at', width: 170, render: (value: string) => new Date(value).toLocaleString('zh-CN') }, { title: '状态', width: 100, render: (_: unknown, session) => session.current ? <Tag color="green">当前会话</Tag> : <Tag>其他会话</Tag> }, { title: '操作', width: 80, fixed: 'right', render: (_: unknown, session) => <Button danger size="small" onClick={() => void api.revokeOwnSession(session.id).then((result) => { message.success('会话已撤销'); if (result.current) finishLogout(); else void sessionsQuery.refetch() }).catch((error: Error) => message.error(error.message))}>撤销</Button> }]} /></Modal>
    </Layout>
  )
}

export function App() {
  return (
    <BrowserRouter>
      <Shell />
    </BrowserRouter>
  )
}
