import { AlertOutlined, BellOutlined, CloudServerOutlined, DeploymentUnitOutlined, FileZipOutlined, HistoryOutlined, MessageOutlined, SafetyCertificateOutlined, TeamOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { Badge, Button, Card, Col, Empty, Row, Skeleton, Tag, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { useState } from 'react'
import { api } from '../../api/client'
import { TagFilter } from '../tags/TagControls'

export function DashboardPage() {
  const navigate = useNavigate()
  const [tagFilter, setTagFilter] = useState<string[]>([])
  const query = useQuery({ queryKey: ['admin-dashboard', tagFilter], queryFn: () => api.adminDashboard(tagFilter), refetchInterval: 30_000 })
  const tagsQuery = useQuery({ queryKey: ['tags', 'all'], queryFn: () => api.listTags() })
  const metrics = query.data?.metrics
  const cards = metrics ? [
    ['账号', metrics.users, `${metrics.enabled_users} 启用 · ${metrics.disabled_users} 禁用`, TeamOutlined, '/users'],
    ['安装包', metrics.packages, '当前服务类型', FileZipOutlined, '/packages'],
	['主机', metrics.hosts, `${metrics.unvalidated_hosts} 未校验 · ${metrics.stale_validation_hosts} 已过期`, CloudServerOutlined, '/hosts'],
    ['已安装服务', metrics.installed_services, `${metrics.running_services} 运行 · ${metrics.unhealthy_installed_services} 异常`, DeploymentUnitOutlined, withTags('/services', tagFilter)],
    ['执行中操作', metrics.active_operations, `24 小时失败 ${metrics.failed_operations_24h}`, HistoryOutlined, withTags('/operations', tagFilter)],
    ['高风险审计', metrics.high_risk_audits_24h, '最近 24 小时', AlertOutlined, '/audit'],
    ['登录失败', metrics.login_failures_24h, '最近 24 小时', SafetyCertificateOutlined, '/audit?category=authentication&outcome=failure'],
    ['未读通知', metrics.unread_notifications, '管理员共享状态', BellOutlined, '/notifications?unread=true'],
  ] as const : []
  const communications = query.data?.communications ?? []
  const notifications = query.data?.notifications.slice(0, 5) ?? []
  return <div className="page">
    <div className="page-heading"><div><div className="page-eyebrow">Administrator overview</div><Typography.Title level={2}>管理总览</Typography.Title><Typography.Paragraph type="secondary">聚焦系统健康、失败操作和需要处理的风险。</Typography.Paragraph></div><TagFilter tags={tagsQuery.data ?? []} value={tagFilter} onChange={setTagFilter} width={340} /></div>
	{tagFilter.length > 0 && <Typography.Paragraph type="secondary">当前标签仅收窄服务实例和操作指标；主机、账号、安装包、安全审计、通知与通讯仍为全局口径。</Typography.Paragraph>}
    {query.isLoading ? <Skeleton active /> : <Row gutter={[14, 14]}>{cards.map(([label, value, caption, Icon, link]) => <Col xs={24} sm={12} xl={6} key={label}><Card hoverable className="dashboard-card" onClick={() => navigate(link)}><div className="metric-label"><Icon /> {label}</div><div className="metric-value">{value}</div><Typography.Text type="secondary">{caption}</Typography.Text></Card></Col>)}</Row>}
    <Card className="content-card dashboard-notifications" title="待处理事项">
      {query.isLoading ? <Skeleton active paragraph={{ rows: 4 }} /> : <div className="dashboard-pending-grid">
        <section className="dashboard-pending-section">
          <div className="dashboard-pending-head">
            <div><MessageOutlined /><strong>待处理消息</strong><Badge count={metrics?.unread_communications ?? 0} overflowCount={99} /></div>
            <Button type="link" size="small" onClick={() => navigate('/communications?unread=true')}>查看全部</Button>
          </div>
          <div className="dashboard-pending-list">
            {communications.length ? communications.map((item) => <button type="button" className="dashboard-pending-item" key={item.id} onClick={() => navigate(`/communications?thread_id=${encodeURIComponent(item.id)}`)}>
              <div className="dashboard-pending-title"><span>{item.title}</span><Tag color="green">{item.unread_count} 条未读</Tag></div>
              <div className="dashboard-pending-copy">{item.last_message || '暂无消息摘要'}</div>
              <div className="dashboard-pending-meta"><span>用户：{item.target_username}</span><time>{formatTime(item.updated_at)}</time></div>
            </button>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有待处理消息" />}
          </div>
        </section>
        <section className="dashboard-pending-section">
          <div className="dashboard-pending-head">
            <div><BellOutlined /><strong>风险通知</strong><Badge count={metrics?.unread_notifications ?? 0} overflowCount={99} /></div>
            <Button type="link" size="small" onClick={() => navigate('/notifications')}>查看全部</Button>
          </div>
          <div className="dashboard-pending-list">
            {notifications.length ? notifications.map((item) => <button type="button" className="dashboard-pending-item" key={item.id} onClick={() => navigate(item.link)}>
              <div className="dashboard-pending-title"><span>{item.title}</span><Tag color={item.risk_level === 'high' ? 'error' : 'warning'}>{item.risk_level === 'high' ? '高风险' : '关注'}</Tag></div>
              <div className="dashboard-pending-copy">{item.message}</div>
              <div className="dashboard-pending-meta"><span>{item.category}</span><time>{formatTime(item.created_at)}</time></div>
            </button>) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有待处理风险" />}
          </div>
        </section>
      </div>}
    </Card>
  </div>
}

function formatTime(value: string) { return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value)) }
function withTags(path: string, tagIds: string[]) { const query = new URLSearchParams(); for (const id of tagIds) query.append('tag_id', id); return query.size ? `${path}?${query}` : path }
