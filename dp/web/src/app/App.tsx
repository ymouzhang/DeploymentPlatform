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
  DatabaseOutlined,
} from '@ant-design/icons'
import { lazy, Suspense, useEffect, useState } from 'react'
import { Alert, App as AntApp, Badge, Button, Dropdown, Form, Input, Layout, Menu, Modal, Select, Space, Table, Tag, Tooltip, Typography } from 'antd'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { AuthContext, canAccess, hasAllAccess } from './AuthContext'
import type { Permission } from '../types'
import { LoginPage } from '../features/auth/LoginPage'
import { communicationKeys } from '../features/communications/queryKeys'
import { RealtimeEvents } from '../realtime/RealtimeEvents'
import { HeaderMessageEntry, SidebarMessageIcon, SidebarMessageLabel } from '../features/communications/MessageEntrypoints'
import { ModelUploadProvider } from '../features/models/ModelUploadContext'
import { firstAllowedPath } from './navigation'

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
const ModelsPage = lazy(() => import('../features/models/ModelsPage').then((module) => ({ default: module.ModelsPage })))
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
const RolesPage = lazy(() => import('../features/roles/RolesPage').then((module) => ({ default: module.RolesPage })))

const scopedPagePermissions: Partial<Record<string, Permission>> = {
  '/packages': 'package.read',
  '/environments': 'environment.read',
  '/models': 'model.read',
  '/services': 'service.read',
}

function ForbiddenPage() {
  return <Alert type="error" showIcon message="无权访问" description="当前账号没有访问该页面所需的权限。" />
}

function Shell() {
  const { message } = AntApp.useApp()
  const queryClient = useQueryClient()
  const meQuery = useQuery({ queryKey: ['me'], queryFn: api.me })
  const usersQuery = useQuery({ queryKey: ['users'], queryFn: api.listUsers, enabled: canAccess(meQuery.data, 'account.read') && !meQuery.data?.must_change_password })
  const notificationQuery = useQuery({ queryKey: ['notification-summary'], queryFn: api.notificationSummary, enabled: canAccess(meQuery.data, 'notification.read') && !meQuery.data?.must_change_password, refetchInterval: 30_000 })
  const communicationQuery = useQuery({ queryKey: communicationKeys.summary, queryFn: api.communicationSummary, enabled: canAccess(meQuery.data, 'communication.read') && !meQuery.data?.must_change_password, refetchInterval: 30_000 })
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
    const permission = scopedPagePermissions[location.pathname]
    if (!permission || !hasAllAccess(meQuery.data, permission)) return
    const requested = new URLSearchParams(location.search).get('owner_id') ?? undefined
    if (!requested || usersQuery.data?.some((item) => item.id === requested)) setOwnerId(requested)
  }, [location.pathname, location.search, meQuery.data, usersQuery.data])
  if (meQuery.isLoading && !loggedOut) return <div className="page-loading">正在验证登录状态…</div>
  if (loggedOut || !meQuery.data) return <LoginPage onLogin={(user) => { queryClient.setQueryData(['me'], user); setLoggedOut(false); navigate(firstAllowedPath(user), { replace: true }) }} />
  const user = meQuery.data
  const users = usersQuery.data ?? []
  const communicationUnread = communicationQuery.data?.unread ?? 0
  const can = (permission: Permission, resourceOwnerId?: string) => canAccess(user, permission, resourceOwnerId)
  const hasAll = (permission: Permission) => hasAllAccess(user, permission)
  const scopedPermission = scopedPagePermissions[location.pathname]
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
          : location.pathname === '/models'
            ? '模型管理'
        : location.pathname === '/users'
          ? '账号管理'
		  : location.pathname === '/roles'
			? '角色与权限'
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
            <span>DP</span>
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
            ...(can('dashboard.read') ? [{ key: '/dashboard', icon: <DashboardOutlined />, label: '管理总览' }] : []),
            ...(can('package.read') ? [{ key: '/packages', icon: <FileZipOutlined />, label: '安装包管理' }] : []),
            ...(can('environment.read') ? [{ key: '/environments', icon: <CloudServerOutlined />, label: '环境管理' }] : []),
            ...(can('model.read') ? [{ key: '/models', icon: <DatabaseOutlined />, label: '模型管理' }] : []),
            ...(can('service.read') ? [{ key: '/services', icon: <DeploymentUnitOutlined />, label: '服务管理' }] : []),
            ...(can('communication.read') ? [{ key: '/communications', icon: <SidebarMessageIcon unread={communicationUnread} />, label: <SidebarMessageLabel unread={communicationUnread} /> }] : []),
            ...(can('account.read') ? [{ key: '/users', icon: <TeamOutlined />, label: '账号管理' }] : []),
			...(can('role.read') ? [{ key: '/roles', icon: <SafetyCertificateOutlined />, label: '角色与权限' }] : []),
            ...(can('operation.read') ? [{ key: '/operations', icon: <HistoryOutlined />, label: '操作中心' }] : []),
            ...(can('audit.read') ? [{ key: '/audit', icon: <AuditOutlined />, label: '审计日志' }] : []),
            ...(can('notification.read') ? [{ key: '/notifications', icon: <BellOutlined />, label: <span>通知中心 <Badge size="small" count={notificationQuery.data?.unread ?? 0} overflowCount={99} /></span> }] : []),
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
            {scopedPermission && hasAll(scopedPermission) && can('account.read') && <Space size={6}><Typography.Text type="secondary">数据范围</Typography.Text><Select allowClear placeholder="全部账号" value={ownerId} style={{ width: 180 }} options={users.map((item) => ({ value: item.id, label: item.username }))} onChange={(value) => setOwnerId(value)} /></Space>}
            <div className="system-state" aria-label="系统连接正常"><span className="system-state-dot" />系统在线</div>
            <HeaderMessageEntry key={`message-${communicationUnread}`} unread={communicationUnread} onClick={() => navigate('/communications')} />
            <Dropdown menu={{ items: [{ key: 'sessions', icon: <LaptopOutlined />, label: '登录会话' }, { key: 'password', icon: <KeyOutlined />, label: '修改密码' }, { key: 'logout', icon: <LogoutOutlined />, label: '退出登录' }], onClick: async ({ key }) => { if (key === 'sessions') setSessionsOpen(true); else if (key === 'password') setPasswordOpen(true); else { await api.logout(); finishLogout() } } }}>
              <Button className="account-button"><span className="account-avatar">{user.username.slice(0, 1).toUpperCase()}</span><span>{user.username}</span><span className="account-role">{user.roles.map((role) => role.name).join('、') || '无角色'}</span></Button>
            </Dropdown>
          </Space>
        </Header>
        <Content className="app-content">
          <AuthContext.Provider value={{ user, ownerId, setOwnerId, users, can, hasAll, logout: () => void 0 }}>
            <ModelUploadProvider key={user.id} userId={user.id}>
              <RealtimeEvents />
              <Suspense fallback={<div className="page-loading">正在加载…</div>}>
                <Routes>
                <Route path="/dashboard" element={can('dashboard.read') ? <DashboardPage /> : <ForbiddenPage />} />
                <Route path="/packages" element={can('package.read') ? <PackagesPage /> : <ForbiddenPage />} />
                <Route path="/environments" element={can('environment.read') ? <EnvironmentsPage /> : <ForbiddenPage />} />
                <Route path="/models" element={can('model.read') ? <ModelsPage /> : <ForbiddenPage />} />
                <Route path="/services" element={can('service.read') ? <ServicesPage /> : <ForbiddenPage />} />
                <Route path="/communications" element={can('communication.read') ? <CommunicationsPage /> : <ForbiddenPage />} />
                <Route path="/users" element={can('account.read') ? <UsersPage /> : <ForbiddenPage />} />
				<Route path="/roles" element={can('role.read') ? <RolesPage /> : <ForbiddenPage />} />
                <Route path="/audit" element={can('audit.read') ? <AuditPage /> : <ForbiddenPage />} />
                <Route path="/operations" element={can('operation.read') ? <OperationsPage /> : <ForbiddenPage />} />
                <Route path="/notifications" element={can('notification.read') ? <NotificationsPage /> : <ForbiddenPage />} />
                <Route path="/forbidden" element={<ForbiddenPage />} />
                <Route path="*" element={<Navigate to={firstAllowedPath(user)} replace />} />
                </Routes>
              </Suspense>
            </ModelUploadProvider>
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
