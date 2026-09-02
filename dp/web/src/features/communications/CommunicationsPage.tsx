import { CheckCircleOutlined, CloseCircleOutlined, LinkOutlined, MessageOutlined, PlusOutlined, ReloadOutlined, SendOutlined } from '@ant-design/icons'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Badge, Button, Card, Drawer, Empty, Form, Input, List, Modal, Select, Space, Tag, Timeline, Typography } from 'antd'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../../api/client'
import { useAuth } from '../../app/AuthContext'
import type { CommunicationFilter, CommunicationMessage, CommunicationResourceInput } from '../../types'
import { communicationKeys } from './queryKeys'
import { LoadMoreFooter } from '../../components/ListPagination'

export function CommunicationsPage() {
  const { message, modal } = App.useApp()
  const auth = useAuth()
	const coordinator = auth.hasAll('communication.read')
	const canCreate = auth.can('communication.create')
	const canManage = auth.can('communication.manage')
  const client = useQueryClient()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [filter, setFilter] = useState<CommunicationFilter>(() => ({
    limit: 30,
    unread: searchParams.get('unread') === 'true' ? true : undefined,
  }))
  const [selectedID, setSelectedID] = useState<string | undefined>(() => searchParams.get('thread_id') || undefined)
  const [createOpen, setCreateOpen] = useState(false)
  const [createForm] = Form.useForm()
  const [replyForm] = Form.useForm()
  const targetUserID = Form.useWatch('target_user_id', createForm)
  const listQuery = useInfiniteQuery({
    queryKey: communicationKeys.list(filter),
    initialPageParam: '',
    queryFn: ({ pageParam }) => api.listCommunications({ ...filter, cursor: pageParam || undefined }),
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchInterval: 30_000,
  })
  const detailQuery = useQuery({ queryKey: communicationKeys.detail(selectedID), queryFn: () => api.getCommunication(selectedID!), enabled: Boolean(selectedID), refetchInterval: 30_000 })
  const packagesQuery = useQuery({ queryKey: ['communication-packages', targetUserID], queryFn: () => api.listPackages(targetUserID), enabled: canCreate && Boolean(targetUserID) })
  const hostsQuery = useQuery({ queryKey: ['communication-hosts', targetUserID], queryFn: () => api.listHosts(targetUserID), enabled: canCreate && Boolean(targetUserID) })
  const servicesQuery = useQuery({ queryKey: ['communication-services', targetUserID], queryFn: () => api.listServices(targetUserID), enabled: canCreate && Boolean(targetUserID) })

  const refresh = () => {
    void client.invalidateQueries({ queryKey: communicationKeys.lists })
    void client.invalidateQueries({ queryKey: communicationKeys.summary })
    if (selectedID) void client.invalidateQueries({ queryKey: communicationKeys.detail(selectedID), exact: true })
  }
  const read = useMutation({ mutationFn: api.markCommunicationRead, onSuccess: (item) => { client.setQueryData(communicationKeys.detail(item.id), item); refresh() } })
  useEffect(() => {
    if (selectedID && detailQuery.data?.unread_count) read.mutate(selectedID)
  }, [detailQuery.data?.unread_count, selectedID])

  const create = useMutation({
    mutationFn: api.createCommunication,
    onSuccess: (item) => { message.success('协作事项已发送'); setCreateOpen(false); createForm.resetFields(); setSelectedID(item.id); refresh() },
    onError: (error: Error) => message.error(error.message),
  })
  const send = useMutation({
    mutationFn: ({ id, content }: { id: string; content: string }) => api.sendCommunicationMessage(id, content),
    onSuccess: () => { message.success(coordinator ? '消息已发送' : '回执已发送'); replyForm.resetFields(); refresh() },
    onError: (error: Error) => message.error(error.message),
  })
  const close = useMutation({ mutationFn: ({ id, content }: { id: string; content: string }) => api.closeCommunication(id, content), onSuccess: () => { message.success('事项已关闭，用户已收到通知'); refresh() }, onError: (error: Error) => message.error(error.message) })
  const reopen = useMutation({ mutationFn: ({ id, content }: { id: string; content: string }) => api.reopenCommunication(id, content), onSuccess: () => { message.success('事项已重新打开，用户可以继续回复'); refresh() }, onError: (error: Error) => message.error(error.message) })

  const resourceOptions = useMemo(() => [
    { label: '安装包', options: (packagesQuery.data ?? []).map((item) => ({ value: `package:${item.service_type}`, label: item.service_type })) },
    { label: '主机', options: (hostsQuery.data ?? []).map((item) => ({ value: `host:${item.id}`, label: `${item.name} · ${item.ip}` })) },
    { label: '服务', options: (servicesQuery.data ?? []).map(({ service_instance: item }) => ({ value: `service:${item.id}`, label: `${item.service_type} · ${item.name}` })) },
  ], [hostsQuery.data, servicesQuery.data, packagesQuery.data])
  const targetUsers = auth.users.filter((user) => user.enabled && user.permissions['communication.read'] === 'own')
  const detail = detailQuery.data
  const rows = listQuery.data?.pages.flatMap((page) => page.items) ?? []

  return <div className="page communication-page">
    <div className="page-heading"><div><div className="page-eyebrow">Collaboration</div><Typography.Title level={2}>消息中心</Typography.Title><Typography.Paragraph type="secondary">{coordinator ? '向用户发送资源相关事项，并跟踪已读、回执和处理状态。' : '查看协作事项，并在事项开启期间提交回执。'}</Typography.Paragraph></div>{canCreate && <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>新建事项</Button>}</div>
    <Card className="content-card communication-filter-card"><Space wrap><Input.Search allowClear placeholder="搜索标题或目标账号" style={{ width: 260 }} onSearch={(keyword) => setFilter((value) => ({ ...value, keyword, cursor: undefined }))} /><Select allowClear placeholder="全部状态" style={{ width: 150 }} options={[{ value: 'open', label: '处理中' }, { value: 'closed', label: '已关闭' }]} onChange={(status) => setFilter((value) => ({ ...value, status, cursor: undefined }))} /><Select allowClear value={filter.unread} placeholder="全部消息" style={{ width: 150 }} options={[{ value: true, label: '有未读消息' }, { value: false, label: '已读' }]} onChange={(unread) => setFilter((value) => ({ ...value, unread, cursor: undefined }))} />{coordinator && <Select allowClear showSearch optionFilterProp="label" placeholder="全部用户" style={{ width: 180 }} options={targetUsers.map((user) => ({ value: user.id, label: user.username }))} onChange={(target_user_id) => setFilter((value) => ({ ...value, target_user_id, cursor: undefined }))} />}</Space></Card>
    <Card className="content-card communication-list-card" styles={{ body: { padding: 0 } }}><List loading={listQuery.isLoading} locale={{ emptyText: <Empty description="暂无通讯事项" /> }} dataSource={rows} renderItem={(item) => <List.Item className={`communication-row${item.unread_count ? ' is-unread' : ''}`} onClick={() => setSelectedID(item.id)} extra={<Space direction="vertical" align="end"><Tag color={item.status === 'open' ? 'green' : 'default'}>{item.status === 'open' ? '处理中' : '已关闭'}</Tag>{item.reopen_count > 0 && <Tag color="blue" icon={<ReloadOutlined />}>重新打开过 {item.reopen_count} 次</Tag>}</Space>}><List.Item.Meta avatar={<Badge count={item.unread_count} overflowCount={99}><div className="communication-avatar"><MessageOutlined /></div></Badge>} title={<Space><Typography.Text strong>{item.title}</Typography.Text>{coordinator && <Tag>{item.target_username}</Tag>}</Space>} description={<div className="communication-row-copy"><span>{item.last_message || '暂无消息'}</span><small>{item.resources.length ? `关联 ${item.resources.length} 个资源 · ` : ''}{new Date(item.updated_at).toLocaleString('zh-CN')}</small></div>} /></List.Item>} /><LoadMoreFooter hasMore={Boolean(listQuery.hasNextPage)} loading={listQuery.isFetchingNextPage} onLoadMore={() => void listQuery.fetchNextPage()} /></Card>

    <Drawer width={820} title={detail?.title ?? '事项详情'} open={Boolean(selectedID)} onClose={() => setSelectedID(undefined)} loading={detailQuery.isLoading} extra={detail && canManage && (detail.status === 'open' ? <Button danger icon={<CloseCircleOutlined />} onClick={() => stateConfirm(modal, '关闭事项', '关闭后用户将无法继续回复，并会收到关闭通知。', (content) => close.mutate({ id: detail.id, content }))}>关闭事项</Button> : <Button type="primary" icon={<ReloadOutlined />} onClick={() => stateConfirm(modal, '重新打开事项', '重新打开会被明确记录，并通知用户可以继续回复。', (content) => reopen.mutate({ id: detail.id, content }))}>重新打开</Button>)}>
      {detail && <div className="communication-detail"><div className="communication-detail-summary"><Space wrap><Tag color={detail.status === 'open' ? 'green' : 'default'}>{detail.status === 'open' ? '处理中' : '已关闭'}</Tag>{detail.reopen_count > 0 && <Tag color="blue">已重新打开 {detail.reopen_count} 次</Tag>}<Typography.Text type="secondary">目标账号：{detail.target_username}</Typography.Text><Typography.Text type="secondary">创建人：{detail.created_by_username}</Typography.Text></Space>{detail.status === 'closed' && <div className="communication-closed-note"><CloseCircleOutlined /> 事项已关闭，无法继续回复</div>}</div>
        <div className="communication-resources"><Typography.Title level={5}>关联资源</Typography.Title>{detail.resources.length ? <Space wrap>{detail.resources.map((resource) => <Button key={resource.id} size="small" icon={<LinkOutlined />} disabled={!resource.available} onClick={() => resource.link && navigate(resource.link)}>{resource.resource_label}{!resource.available ? '（已失效）' : ''}</Button>)}</Space> : <Typography.Text type="secondary">未关联资源</Typography.Text>}</div>
        <Typography.Title level={5}>沟通记录</Typography.Title><Timeline className="communication-timeline" items={(detail.messages ?? []).map((item) => ({ color: timelineColor(item), children: <MessageItem message={item} admin={coordinator} targetUserID={detail.target_user_id} /> }))} />
        {detail.status === 'open' && <Form form={replyForm} layout="vertical" onFinish={({ content }) => send.mutate({ id: detail.id, content })}><Form.Item name="content" label={coordinator ? '继续发送消息' : '发送回执'} rules={[{ required: true, whitespace: true, message: '请输入消息内容' }, { max: 5000 }]}><Input.TextArea rows={4} maxLength={5000} showCount placeholder={coordinator ? '补充说明或回复用户…' : '向管理员反馈处理情况…'} /></Form.Item><Button type="primary" htmlType="submit" icon={<SendOutlined />} loading={send.isPending}>{coordinator ? '发送消息' : '发送回执'}</Button></Form>}
      </div>}
    </Drawer>

    <Modal width={680} title="新建协作事项" open={createOpen} onCancel={() => { setCreateOpen(false); createForm.resetFields() }} onOk={() => createForm.validateFields().then((values) => create.mutate({ target_user_id: values.target_user_id, title: values.title, content: values.content, resources: decodeResources(values.resources ?? []) }))} confirmLoading={create.isPending} okText="发送给用户"><Form form={createForm} layout="vertical"><Form.Item name="target_user_id" label="目标用户" rules={[{ required: true, message: '请选择目标用户' }]}><Select showSearch optionFilterProp="label" options={targetUsers.map((user) => ({ value: user.id, label: user.username }))} onChange={() => createForm.setFieldValue('resources', [])} /></Form.Item><Form.Item name="title" label="事项标题" rules={[{ required: true, whitespace: true }, { max: 100 }]}><Input maxLength={100} showCount /></Form.Item><Form.Item name="content" label="消息内容" rules={[{ required: true, whitespace: true }, { max: 5000 }]}><Input.TextArea rows={5} maxLength={5000} showCount /></Form.Item><Form.Item name="resources" label="关联资源" extra="仅展示目标用户所属的资源；关联不会自动执行任何运维操作。"><Select mode="multiple" allowClear maxCount={50} disabled={!targetUserID} loading={packagesQuery.isLoading || hostsQuery.isLoading || servicesQuery.isLoading} options={resourceOptions} placeholder={targetUserID ? '选择安装包、主机或服务（可多选）' : '请先选择目标用户'} /></Form.Item></Form></Modal>
  </div>
}

function MessageItem({ message, admin, targetUserID }: { message: CommunicationMessage; admin: boolean; targetUserID?: string }) {
  const stateMessage = message.type === 'system_closed' || message.type === 'system_reopened'
  const userRecipient = message.recipients.find((item) => item.user_id === targetUserID)
  return <div className={`communication-message${stateMessage ? ' is-system' : ''}`}><div className="communication-message-head"><Space><Typography.Text strong>{stateMessage ? (message.type === 'system_closed' ? '事项已关闭' : '事项已重新打开') : message.sender_username}</Typography.Text>{!stateMessage && <Tag>{message.type === 'admin_message' ? '协调消息' : '用户回执'}</Tag>}</Space><Typography.Text type="secondary">{new Date(message.created_at).toLocaleString('zh-CN')}</Typography.Text></div><div className="communication-message-content">{message.content}</div>{admin && userRecipient && <div className="communication-read-state">{userRecipient.read_at ? <span className="is-read"><CheckCircleOutlined /> 用户已读于 {new Date(userRecipient.read_at).toLocaleString('zh-CN')}</span> : <span>用户未读</span>}</div>}</div>
}

function timelineColor(message: CommunicationMessage) {
  if (message.type === 'system_closed') return 'gray'
  if (message.type === 'system_reopened') return 'blue'
  return message.type === 'admin_message' ? 'green' : 'orange'
}

function decodeResources(values: string[]): CommunicationResourceInput[] {
  return values.map((value) => { const [resource_type, identifier] = value.split(':', 2) as [CommunicationResourceInput['resource_type'], string]; return resource_type === 'package' ? { resource_type, resource_key: identifier } : { resource_type, resource_id: identifier } })
}

function stateConfirm(modal: ReturnType<typeof App.useApp>['modal'], title: string, description: string, submit: (content: string) => void) {
  let content = ''
  modal.confirm({ title, content: <div><Typography.Paragraph>{description}</Typography.Paragraph><Input.TextArea rows={3} maxLength={5000} placeholder="可选：补充说明" onChange={(event) => { content = event.target.value }} /></div>, onOk: () => submit(content) })
}
