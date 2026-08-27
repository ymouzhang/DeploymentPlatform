import { EyeOutlined, ReloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { Button, Card, DatePicker, Input, Select, Space, Table, Tag, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../../api/client'
import { useAuth } from '../../app/AuthContext'
import type { Operation, OperationFilter } from '../../types'
import { OperationModal } from './OperationModal'
import { TagFilter, TagList } from '../tags/TagControls'

const actionLabels = { install: '安装', start: '启动', stop: '停止', reset: '重置' }
const statusLabels: Record<string, string> = { queued: '排队中', running: '执行中', succeeded: '成功', failed: '失败', timed_out: '超时', interrupted: '中断' }

export function OperationsPage() {
  const { users } = useAuth(); const navigate = useNavigate(); const [search] = useSearchParams(); const linkedOperation = search.get('operation_id') || undefined; const initial: OperationFilter = { keyword: linkedOperation, status: search.get('status') || undefined, owner_id: search.get('owner_id') || undefined, tag_id: search.getAll('tag_id'), limit: 50 }; const [filter, setFilter] = useState<OperationFilter>(initial); const [draft, setDraft] = useState<OperationFilter>(initial); const [selected, setSelected] = useState<string | undefined>(linkedOperation)
  const query = useQuery({ queryKey: ['operations', filter], queryFn: () => api.listOperations(filter), refetchInterval: 5000 })
  const tagsQuery = useQuery({ queryKey: ['tags', 'all'], queryFn: () => api.listTags() })
  const columns = useMemo<ColumnsType<Operation>>(() => [
    { title: '操作时间', dataIndex: 'created_at', width: 170, render: formatTime },
    { title: '操作账号', dataIndex: 'actor_username', width: 130, render: (v) => v || '-' },
    { title: '所属账号', dataIndex: 'owner_username', width: 130, render: (v) => <Tag>{v || '-'}</Tag> },
    { title: '目标服务', render: (_, item) => <Space direction="vertical" size={0}><Typography.Text strong>{item.environment_name || item.environment_id}</Typography.Text><Typography.Text type="secondary" className="cell-caption">{item.environment_ip} · {item.service_type}</Typography.Text></Space> },
    { title: '标签快照', width: 220, render: (_, item) => <TagList tags={item.tags} /> },
    { title: '动作', dataIndex: 'action', width: 90, render: (v: keyof typeof actionLabels) => actionLabels[v] },
    { title: '状态', dataIndex: 'status', width: 100, render: (v: string) => <Tag color={v === 'succeeded' ? 'success' : v === 'running' || v === 'queued' ? 'processing' : 'error'}>{statusLabels[v] ?? v}</Tag> },
    { title: '阶段 / 失败摘要', width: 250, render: (_, item) => item.error_message ? <Space direction="vertical" size={0}><Typography.Text type="danger" ellipsis={{ tooltip: item.error_message }}>{item.error_message}</Typography.Text><Typography.Text type="secondary" className="cell-caption">建议：{failureAdvice(item)}</Typography.Text></Space> : item.stage },
    { title: '耗时', width: 100, render: (_, item) => duration(item) },
    { title: '请求 ID', dataIndex: 'request_id', width: 110, render: (value?: string) => value ? <Typography.Text code copyable={{ text: value }}>{value.slice(0, 8)}</Typography.Text> : '-' },
    { title: '操作', width: 240, render: (_, item) => <Space><Button icon={<EyeOutlined />} onClick={() => setSelected(item.id)}>日志</Button><Button onClick={() => navigate(`/audit?operation_id=${item.id}`)}>审计</Button>{item.status !== 'succeeded' && <Button onClick={() => navigate(remediationLink(item))}>去处理</Button>}</Space> },
  ], [navigate])
  return <div className="page"><div className="page-heading"><div><div className="page-eyebrow">Operations</div><Typography.Title level={2}>操作中心</Typography.Title><Typography.Paragraph type="secondary">跨账号追踪生命周期操作，集中定位失败阶段并回看完整日志。</Typography.Paragraph></div><Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>刷新</Button></div>
    <Card className="content-card audit-filter-card"><div className="audit-filter-grid"><label><span>关键字</span><Input allowClear value={draft.keyword} placeholder="环境、IP、服务类型、操作 ID" onChange={(e) => setDraft({ ...draft, keyword: e.target.value })} /></label><label><span>操作账号</span><Select allowClear value={draft.actor_id} options={users.map((u) => ({ value: u.id, label: u.username }))} onChange={(v) => setDraft({ ...draft, actor_id: v })} /></label><label><span>所属账号</span><Select allowClear value={draft.owner_id} options={users.map((u) => ({ value: u.id, label: u.username }))} onChange={(v) => setDraft({ ...draft, owner_id: v, tag_id: [] })} /></label><label><span>资源标签</span><TagFilter width={260} tags={(tagsQuery.data ?? []).filter((tag) => !draft.owner_id || tag.owner_id === draft.owner_id)} value={draft.tag_id ?? []} onChange={(value) => setDraft({ ...draft, tag_id: value })} /></label><label><span>状态</span><Select allowClear value={draft.status} options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))} onChange={(v) => setDraft({ ...draft, status: v })} /></label><label><span>动作</span><Select allowClear value={draft.action} options={Object.entries(actionLabels).map(([value, label]) => ({ value, label }))} onChange={(v) => setDraft({ ...draft, action: v })} /></label><label><span>时间范围</span><DatePicker.RangePicker showTime onChange={(values) => setDraft({ ...draft, from: values?.[0]?.toISOString(), to: values?.[1]?.toISOString() })} /></label></div><div className="audit-filter-actions"><Button onClick={() => { setDraft({ limit: 50 }); setFilter({ limit: 50 }) }}>重置</Button><Button type="primary" onClick={() => setFilter({ ...draft, cursor: undefined })}>查询</Button></div></Card>
    <Card className="content-card table-card" styles={{ body: { padding: 0 } }}><Table rowKey="id" columns={columns} dataSource={query.data?.items ?? []} loading={query.isLoading} pagination={false} /><div className="audit-load-more"><Button disabled={!query.data?.next_cursor} onClick={() => setFilter({ ...filter, cursor: query.data?.next_cursor })}>下一页</Button></div></Card>
    <OperationModal operationId={selected ?? null} open={Boolean(selected)} onClose={() => setSelected(undefined)} onComplete={() => void query.refetch()} />
  </div>
}
function formatTime(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-' }
function duration(item: Operation) { const end = item.finished_at ? new Date(item.finished_at).getTime() : Date.now(); const start = new Date(item.started_at ?? item.created_at).getTime(); const seconds = Math.max(0, Math.floor((end - start) / 1000)); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s` }
function failureAdvice(item: Operation) { if (item.error_code?.includes('PACKAGE')) return '检查对应账号的安装包'; if (item.error_code?.includes('TIMEOUT')) return '检查 SSH 连通性和远端脚本'; if (item.error_code?.includes('PASSWORD') || item.error_message?.includes('SSH')) return '重新校验 SSH 凭据'; if (item.action === 'install') return '检查日志后重置残留状态'; return '查看日志并检查目标环境' }
export function remediationLink(item: Operation) { if (item.error_code?.includes('PACKAGE')) return `/packages?owner_id=${item.owner_id ?? ''}`; if (item.action === 'install' && !item.error_code?.includes('SSH')) return `/services?owner_id=${item.owner_id ?? ''}&environment_id=${item.environment_id}`; return `/environments?owner_id=${item.owner_id ?? ''}` }
