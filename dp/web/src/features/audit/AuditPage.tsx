import { useMemo, useState } from 'react'
import {
  DownloadOutlined,
  ExclamationCircleOutlined,
  EyeOutlined,
  FileSearchOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { App, Button, Card, Descriptions, Drawer, Input, Select, Space, Table, Tag, Typography } from 'antd'
import { api } from '../../api/client'
import { useNavigate, useSearchParams } from 'react-router-dom'
import type { AuditEvent, AuditFilter } from '../../types'
import { LoadMoreFooter } from '../../components/ListPagination'

type DraftFilter = {
  from: string
  to: string
  actor_id?: string
  owner_id?: string
  category?: string
  action?: string
  outcome?: string
  source_ip?: string
  keyword?: string
}

export function AuditPage() {
  const { message } = App.useApp()
  const navigate = useNavigate()
  const [search] = useSearchParams()
  const defaults = useMemo(() => {
    const keyword = search.get('operation_id') || search.get('request_id') || undefined
    const base = defaultDraft(keyword ? 180 : 1)
    return { ...base, owner_id: search.get('owner_id') || undefined, category: search.get('category') || undefined, outcome: search.get('outcome') || undefined, keyword }
  }, [search])
  const [draft, setDraft] = useState<DraftFilter>(defaults)
  const [filter, setFilter] = useState<AuditFilter>(() => toFilter(defaults))
  const [selected, setSelected] = useState<string | null>(null)
  const [exporting, setExporting] = useState(false)
  const users = useQuery({ queryKey: ['users'], queryFn: api.listUsers })
  const summary = useQuery({ queryKey: ['audit-summary', filter], queryFn: () => api.auditSummary(filter) })
  const events = useInfiniteQuery({
    queryKey: ['audit-events', filter],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api.listAuditEvents({ ...filter, cursor: pageParam || undefined, limit: 50 }),
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
  const detail = useQuery({
    queryKey: ['audit-event', selected],
    queryFn: () => api.getAuditEvent(selected!),
    enabled: Boolean(selected),
  })
  const rows = events.data?.pages.flatMap((page) => page.items) ?? []

  const apply = () => {
    const next = toFilter(draft)
    if (new Date(next.to).getTime() < new Date(next.from).getTime()) {
      message.error('结束时间不能早于开始时间')
      return
    }
    setFilter(next)
  }
  const reset = () => { const next = defaultDraft(); setDraft(next); setFilter(toFilter(next)) }
  const exportCSV = async () => {
    setExporting(true)
    try {
      const { blob, filename } = await api.exportAuditEvents(filter)
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url; link.download = filename; link.click()
      URL.revokeObjectURL(url)
      message.success('审计日志已导出')
    } catch (error) { message.error((error as Error).message) } finally { setExporting(false) }
  }

  return <div className="page audit-page">
    <div className="page-heading">
      <div><div className="page-eyebrow">Security observability</div><Typography.Title level={2}>审计日志</Typography.Title><Typography.Paragraph type="secondary">追踪全部账号的认证、资源变更与远程服务操作，定位每一次跨账号管理行为。</Typography.Paragraph></div>
      <Button icon={<DownloadOutlined />} loading={exporting} onClick={exportCSV}>导出当前范围</Button>
    </div>

    <div className="metric-strip">
      <Metric label="事件总数" value={summary.data?.total ?? 0} icon={<FileSearchOutlined />} />
      <Metric label="失败与拒绝" value={summary.data?.failures ?? 0} icon={<ExclamationCircleOutlined />} tone="danger" />
      <Metric label="登录失败" value={summary.data?.login_failures ?? 0} icon={<SafetyCertificateOutlined />} tone="warning" />
      <Metric label="高风险事件" value={summary.data?.high_risk ?? 0} icon={<WarningOutlined />} tone="danger" />
    </div>

    <Card className="content-card audit-filter-card">
      <div className="audit-filter-grid">
        <label><span>开始时间</span><Input type="datetime-local" value={draft.from} onChange={(event) => setDraft({ ...draft, from: event.target.value })} /></label>
        <label><span>结束时间</span><Input type="datetime-local" value={draft.to} onChange={(event) => setDraft({ ...draft, to: event.target.value })} /></label>
        <label><span>操作账号</span><Select allowClear value={draft.actor_id} placeholder="全部账号" options={(users.data ?? []).map((user) => ({ value: user.id, label: user.username }))} onChange={(value) => setDraft({ ...draft, actor_id: value })} /></label>
        <label><span>资源所属账号</span><Select allowClear value={draft.owner_id} placeholder="全部账号" options={(users.data ?? []).map((user) => ({ value: user.id, label: user.username }))} onChange={(value) => setDraft({ ...draft, owner_id: value })} /></label>
        <label><span>事件分类</span><Select allowClear value={draft.category} placeholder="全部分类" options={categoryOptions} onChange={(value) => setDraft({ ...draft, category: value })} /></label>
        <label><span>具体操作</span><Select allowClear showSearch value={draft.action} placeholder="全部操作" options={actionOptions} onChange={(value) => setDraft({ ...draft, action: value })} /></label>
        <label><span>结果</span><Select allowClear value={draft.outcome} placeholder="全部结果" options={[{ value: 'success', label: '成功' }, { value: 'failure', label: '失败' }, { value: 'denied', label: '拒绝' }]} onChange={(value) => setDraft({ ...draft, outcome: value })} /></label>
        <label><span>来源 IP</span><Input value={draft.source_ip} placeholder="精确 IP" onChange={(event) => setDraft({ ...draft, source_ip: event.target.value })} /></label>
        <label className="audit-keyword"><span>检索</span><Input value={draft.keyword} placeholder="用户名、资源、服务类型、请求或操作 ID" onPressEnter={apply} onChange={(event) => setDraft({ ...draft, keyword: event.target.value })} /></label>
      </div>
      <div className="audit-filter-actions"><Button icon={<ReloadOutlined />} onClick={reset}>重置</Button><Button type="primary" onClick={apply}>应用筛选</Button></div>
    </Card>

    <Card className="content-card table-card audit-table-card" styles={{ body: { padding: 0 } }}>
      <Table<AuditEvent>
        rowKey="id"
        dataSource={rows}
        loading={events.isLoading || events.isFetchingNextPage}
        pagination={false}
        tableLayout="fixed"
        scroll={{ x: 1260 }}
        columns={[
          { title: '时间', dataIndex: 'occurred_at', width: 168, render: (value: string) => <span className="audit-time">{formatTime(value)}</span> },
          { title: '操作账号', width: 138, render: (_, row) => <div><Typography.Text strong>{row.actor_username || '匿名'}</Typography.Text><div className="cell-caption">{row.actor_role ? roleLabel(row.actor_role) : '未认证'}</div></div> },
          { title: '事件', width: 210, render: (_, row) => <div><Typography.Text>{actionLabel(row.action)}</Typography.Text><div className="cell-caption">{categoryLabel(row.category)}</div></div> },
          { title: '操作对象', width: 300, render: (_, row) => <div className="table-stacked-cell"><Typography.Text ellipsis={{ tooltip: row.target_label || '-' }}>{row.target_label || '-'}</Typography.Text><Typography.Text type="secondary" className="cell-caption" ellipsis={{ tooltip: row.owner_username ? `所属：${row.owner_username}` : row.target_type || '-' }}>{row.owner_username ? `所属：${row.owner_username}` : row.target_type || '-'}</Typography.Text></div> },
          { title: '来源 IP', dataIndex: 'source_ip', width: 136, render: (value: string) => <span className="server-address">{value || '-'}</span> },
          { title: '请求 ID', dataIndex: 'request_id', width: 126, render: (value: string) => <Typography.Text code copyable={{ text: value }}>{value.slice(0, 8)}</Typography.Text> },
          { title: '结果', width: 92, render: (_, row) => <OutcomeTag event={row} /> },
          { title: '操作', width: 90, fixed: 'right', render: (_, row) => <Button type="text" icon={<EyeOutlined />} onClick={() => setSelected(row.id)}>详情</Button> },
        ]}
      />
      <LoadMoreFooter hasMore={Boolean(events.hasNextPage)} loading={events.isFetchingNextPage} onLoadMore={() => void events.fetchNextPage()} />
    </Card>

    <Drawer title="审计事件详情" width={620} open={Boolean(selected)} onClose={() => setSelected(null)}>
      {detail.data && <AuditDetail event={detail.data} onOpenOperation={(id) => navigate(`/operations?operation_id=${id}`)} />}
    </Drawer>
  </div>
}

function Metric({ label, value, icon, tone }: { label: string; value: number; icon: React.ReactNode; tone?: string }) {
  return <div className={`metric-item${tone ? ` audit-metric-${tone}` : ''}`}><div className="metric-label"><span className="metric-dot" />{label}</div><div className="metric-value">{value}</div><span className="metric-icon">{icon}</span></div>
}

function OutcomeTag({ event }: { event: AuditEvent }) {
  const values = { success: ['success', '成功'], failure: ['error', '失败'], denied: ['warning', '拒绝'] } as const
  const [color, label] = values[event.outcome]
  return <Space size={4}><Tag color={color}>{label}</Tag>{event.risk_level === 'high' && <TooltipTag />}</Space>
}

function TooltipTag() { return <Tag color="red">高风险</Tag> }

function AuditDetail({ event, onOpenOperation }: { event: AuditEvent; onOpenOperation: (id: string) => void }) {
  return <Space direction="vertical" size={20} style={{ width: '100%' }}>
    <Descriptions column={1} bordered size="small" items={[
      { key: 'time', label: '时间', children: formatTime(event.occurred_at) },
      { key: 'actor', label: '操作账号', children: `${event.actor_username || '匿名'}${event.actor_role ? ` · ${roleLabel(event.actor_role)}` : ''}` },
      { key: 'action', label: '事件', children: <Space><span>{actionLabel(event.action)}</span><OutcomeTag event={event} /></Space> },
      { key: 'target', label: '操作对象', children: event.target_label || '-' },
      { key: 'owner', label: '所属账号', children: event.owner_username || '-' },
      { key: 'ip', label: '来源 IP', children: event.source_ip || '-' },
      { key: 'request', label: '请求 ID', children: <Typography.Text code copyable>{event.request_id}</Typography.Text> },
      { key: 'operation', label: '操作 ID', children: event.operation_id ? <Space><Typography.Text code copyable>{event.operation_id}</Typography.Text><Button type="link" size="small" onClick={() => onOpenOperation(event.operation_id!)}>查看操作</Button></Space> : '-' },
      { key: 'error', label: '错误码', children: event.error_code || '-' },
      { key: 'agent', label: 'User-Agent', children: event.user_agent || '-' },
    ]} />
    <div><Typography.Text strong>脱敏变更摘要</Typography.Text><pre className="audit-changes">{JSON.stringify(event.changes ?? {}, null, 2)}</pre></div>
  </Space>
}

const categoryOptions = [
  { value: 'authentication', label: '认证安全' }, { value: 'account', label: '账号管理' },
  { value: 'package', label: '安装包' }, { value: 'environment', label: '环境' },
  { value: 'service', label: '服务' }, { value: 'communication', label: '用户通讯' }, { value: 'audit', label: '审计' },
]

const actions: Record<string, string> = {
  'auth.login': '账号登录', 'auth.logout': '退出登录', 'auth.password.change': '修改本人密码', 'auth.session.revoke': '撤销本人会话',
  'account.create': '新增账号', 'account.password.reset': '重置账号密码', 'account.enable': '启用账号', 'account.disable': '禁用账号', 'account.delete': '删除账号', 'account.session.revoke': '撤销指定会话', 'account.sessions.revoke': '强制全部下线',
  'package.upload': '上传安装包', 'package.replace': '替换安装包', 'package.note.update': '修改安装包备注', 'package.delete': '删除安装包',
  'package.version.activate': '切换安装包版本', 'package.version.delete': '删除安装包版本',
  'environment.create': '新增环境', 'environment.update': '修改环境', 'environment.delete': '删除环境', 'environment.validate': 'SSH 校验', 'environment.import': '导入环境', 'environment.export': '导出环境',
  'tag.create': '新增资源标签', 'tag.update': '修改资源标签', 'tag.delete': '删除资源标签',
  'service.config.update': '保存实例配置', 'service.config.rollback': '回滚实例配置', 'service.health_check': '手动健康检查', 'audit.detail.view': '查看审计详情', 'audit.export': '导出审计日志',
  'communication.create': '新建协作事项', 'communication.read': '读取协作事项', 'communication.message.send': '发送通讯消息', 'communication.message.admin.send': '管理员发送消息', 'communication.receipt.user.send': '用户发送回执', 'communication.close': '关闭协作事项', 'communication.reopen': '重新打开协作事项',
}

const actionOptions = [
  ...Object.entries(actions).map(([value, label]) => ({ value, label })),
  ...['install', 'start', 'stop', 'reset'].flatMap((verb) => ['requested', 'completed'].map((phase) => {
    const value = `service.${verb}.${phase}`
    return { value, label: actionLabel(value) }
  })),
]

function actionLabel(value: string) {
  if (actions[value]) return actions[value]
  const lifecycle = value.match(/^service\.(install|start|stop|reset)\.(requested|completed)$/)
  if (!lifecycle) return value
  const verbs: Record<string, string> = { install: '安装', start: '启动', stop: '停止', reset: '重置' }
  return `${verbs[lifecycle[1]]}服务 · ${lifecycle[2] === 'requested' ? '已发起' : '已完成'}`
}
function categoryLabel(value: string) { return categoryOptions.find((item) => item.value === value)?.label ?? value }
function roleLabel(value: string) { return value === 'admin' ? '管理员' : '普通账号' }
function formatTime(value: string) { return new Date(value).toLocaleString('zh-CN', { hour12: false }) }
function localDateTime(date: Date) {
  const offset = date.getTimezoneOffset() * 60000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}
function defaultDraft(days = 1): DraftFilter { const now = new Date(); return { from: localDateTime(new Date(now.getTime() - days * 24 * 3600 * 1000)), to: localDateTime(now) } }
function toFilter(draft: DraftFilter): AuditFilter { return { ...draft, from: new Date(draft.from).toISOString(), to: new Date(draft.to).toISOString() } }
