import { useEffect, useMemo, useRef, useState } from 'react'
import { CloudUploadOutlined, DeleteOutlined, FileSearchOutlined, PauseOutlined, PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App, Button, Card, Form, Input, Modal, Progress, Select, Space, Table, Tag, Typography, Upload } from 'antd'
import type { UploadFile } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { api } from '../../api/client'
import { useAuth } from '../../app/AuthContext'
import type { Model, ModelTask, OperationEvent } from '../../types'

type UploadValues = { name: string; environment_id: string; target_dir: string }
type PendingUpload = UploadValues & { upload_id: string; owner_id?: string; filename: string; size: number; chunk_bytes: number }
const pendingKey = 'dp:model-upload:v1'

const statusLabels: Record<Model['status'], { text: string; color: string }> = {
  uploading: { text: '上传中', color: 'processing' }, deploying: { text: '部署中', color: 'processing' },
  ready: { text: '可用', color: 'success' }, failed: { text: '失败', color: 'error' },
  deleting: { text: '删除中', color: 'warning' }, deleted: { text: '已删除', color: 'default' },
}

export function ModelsPage() {
  const { message } = App.useApp()
  const { ownerId, user, users } = useAuth()
  const queryClient = useQueryClient()
  const [form] = Form.useForm<UploadValues>()
  const [open, setOpen] = useState(false)
  const [fileList, setFileList] = useState<UploadFile[]>([])
  const [uploadOwner, setUploadOwner] = useState(user.id)
  const [uploading, setUploading] = useState(false)
  const [paused, setPaused] = useState(false)
  const pausedRef = useRef(false)
  const [progress, setProgress] = useState(0)
	const [keyword, setKeyword] = useState('')
  const [pending, setPending] = useState<PendingUpload | null>(() => {
    try { return JSON.parse(localStorage.getItem(pendingKey) ?? 'null') as PendingUpload | null } catch { return null }
  })
  const [deleting, setDeleting] = useState<Model | null>(null)
  const [confirmName, setConfirmName] = useState('')
  const [logTask, setLogTask] = useState<ModelTask | null>(null)
  const [logs, setLogs] = useState<OperationEvent[]>([])

  const modelsQuery = useQuery({
    queryKey: ['models', ownerId], queryFn: () => api.listModels(ownerId),
    refetchInterval: (query) => query.state.data?.some((item) => ['deploying', 'deleting'].includes(item.status)) ? 2000 : false,
  })
  const environmentsQuery = useQuery({
    queryKey: ['environments', 'model-upload', uploadOwner],
    queryFn: () => api.listEnvironments(user.role === 'admin' ? uploadOwner : undefined),
  })

  useEffect(() => {
    if (!logTask) return
    setLogs([])
    const source = new EventSource(`/api/v1/model-tasks/${logTask.id}/events`)
    const append = (event: MessageEvent) => setLogs((items) => [...items, JSON.parse(event.data) as OperationEvent])
    source.addEventListener('log', append); source.addEventListener('state', append)
    source.onerror = () => source.close()
    return () => source.close()
  }, [logTask?.id])

  const begin = async () => {
    const values = await form.validateFields()
    const file = fileList[0]?.originFileObj
    if (!file) { message.error('请选择 .tar.gz 模型文件'); return }
    if (!file.name.toLowerCase().endsWith('.tar.gz')) { message.error('模型文件必须是 .tar.gz'); return }
    let session = pending
    if (session && (session.filename !== file.name || session.size !== file.size)) {
      message.error('请选择与未完成会话相同的原始文件'); return
    }
    setUploading(true); setPaused(false); pausedRef.current = false
    try {
      if (!session) {
        const created = await api.createModelUpload({ ...values, original_filename: file.name, total_bytes: file.size }, user.role === 'admin' ? uploadOwner : undefined)
        session = { ...values, upload_id: created.upload_id, owner_id: user.role === 'admin' ? uploadOwner : undefined, filename: file.name, size: file.size, chunk_bytes: created.chunk_bytes }
        localStorage.setItem(pendingKey, JSON.stringify(session)); setPending(session)
      }
      let offset = await api.modelUploadOffset(session.upload_id)
      setProgress(Math.floor(offset * 100 / file.size))
      while (offset < file.size) {
        if (pausedRef.current) { message.info('上传已暂停，可稍后继续'); return }
        const nextEnd = Math.min(offset + session.chunk_bytes, file.size)
        offset = await api.uploadModelChunk(session.upload_id, offset, file.slice(offset, nextEnd))
        setProgress(Math.floor(offset * 100 / file.size))
      }
      const task = await api.completeModelUpload(session.upload_id)
      localStorage.removeItem(pendingKey); setPending(null); setOpen(false); setFileList([]); form.resetFields()
      message.success('文件上传完成，目标机正在校验并解压'); setLogTask(task)
      void queryClient.invalidateQueries({ queryKey: ['models'] })
    } catch (error) { message.error((error as Error).message) }
    finally { setUploading(false) }
  }

  const openUpload = () => {
    if (pending) {
      setUploadOwner(pending.owner_id ?? user.id)
      form.setFieldsValue({ name: pending.name, environment_id: pending.environment_id, target_dir: pending.target_dir })
    } else {
      setUploadOwner(user.role === 'admin' ? (ownerId ?? user.id) : user.id)
      form.setFieldsValue({ target_dir: '/opt/models/model-name' })
    }
    setOpen(true)
  }

  const cancelPending = async () => {
    if (!pending) return
    try { await api.cancelModelUpload(pending.upload_id); localStorage.removeItem(pendingKey); setPending(null); setOpen(false); message.success('上传会话已取消'); void queryClient.invalidateQueries({ queryKey: ['models'] }) }
    catch (error) { message.error((error as Error).message) }
  }

  const removeModel = async () => {
    if (!deleting) return
    try { const result = await api.deleteModel(deleting.id, confirmName); setDeleting(null); setConfirmName(''); message.success('删除请求已提交'); if ('id' in result) setLogTask(result); void queryClient.invalidateQueries({ queryKey: ['models'] }) }
    catch (error) { message.error((error as Error).message) }
  }

  const columns = useMemo<ColumnsType<Model>>(() => [
    { title: '模型', dataIndex: 'name', width: 180, render: (value, item) => <Space direction="vertical" size={0}><Typography.Text strong>{value}</Typography.Text><Typography.Text type="secondary" ellipsis style={{ maxWidth: 210 }}>{item.original_filename}</Typography.Text></Space> },
    ...(user.role === 'admin' ? [{ title: '所属账号', dataIndex: 'owner_username', width: 120 }] : []),
    { title: '目标环境', width: 180, render: (_, item) => <Space direction="vertical" size={0}><span>{item.environment_name}</span><Typography.Text type="secondary">{item.environment_ip}</Typography.Text></Space> },
    { title: '目标目录', dataIndex: 'target_dir', ellipsis: true, width: 260 },
    { title: '大小', width: 120, render: (_, item) => item.expanded_size_bytes ? `${bytes(item.expanded_size_bytes)} / 包 ${bytes(item.size_bytes)}` : bytes(item.size_bytes) },
    { title: '状态', width: 150, render: (_, item) => <Space direction="vertical" size={2}><Tag color={statusLabels[item.status].color}>{statusLabels[item.status].text}</Tag>{item.latest_task && ['deploying','deleting'].includes(item.status) && <Progress size="small" percent={item.latest_task.progress} showInfo={false} />}</Space> },
    { title: '操作', width: 210, fixed: 'right', render: (_, item) => <Space>
      {item.latest_task && <Button size="small" icon={<FileSearchOutlined />} onClick={() => setLogTask(item.latest_task!)}>日志</Button>}
      {item.status === 'failed' && item.latest_task?.action === 'deploy' && <Button size="small" icon={<ReloadOutlined />} onClick={() => void api.retryModel(item.id).then((task) => { setLogTask(task); void queryClient.invalidateQueries({queryKey:['models']}) }).catch((e: Error) => message.error(e.message))}>重试</Button>}
      {['ready','failed','uploading'].includes(item.status) && <Button danger size="small" icon={<DeleteOutlined />} onClick={() => { setDeleting(item); setConfirmName('') }}>删除</Button>}
    </Space> },
  ], [message, queryClient, user.role])
	const visibleModels = (modelsQuery.data ?? []).filter((item) => {
		const value = keyword.trim().toLowerCase()
		return !value || [item.name, item.environment_name, item.environment_ip, item.target_dir, item.original_filename].some((field) => field.toLowerCase().includes(value))
	})

  return <div className="page">
    <div className="page-heading"><div><Typography.Title level={2}>模型管理</Typography.Title><Typography.Paragraph type="secondary">将大模型包分片直传目标环境，DP 不保存完整模型文件。</Typography.Paragraph></div><Button type="primary" icon={<CloudUploadOutlined />} onClick={openUpload}>上传模型</Button></div>
    {pending && <Alert showIcon type="warning" message={`存在未完成上传：${pending.name}`} description="重新选择同一个本地文件后，将从目标机已经保存的位置继续。" action={<Button size="small" onClick={openUpload}>继续上传</Button>} />}
	<Card><Input.Search allowClear value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="搜索模型、环境、IP 或目标目录" style={{ maxWidth: 420, marginBottom: 16 }} /><Table<Model> rowKey="id" loading={modelsQuery.isLoading} dataSource={visibleModels} columns={columns} scroll={{ x: 1200 }} pagination={{ pageSize: 20, showSizeChanger: true }} /></Card>
    <Modal title={pending ? '继续上传模型' : '上传离线模型'} open={open} onCancel={() => !uploading && setOpen(false)} footer={<Space>{pending && <Button danger disabled={uploading} onClick={() => void cancelPending()}>取消会话</Button>}{uploading && <Button icon={paused ? <PlayCircleOutlined /> : <PauseOutlined />} onClick={() => { pausedRef.current = !pausedRef.current; setPaused(pausedRef.current) }}>{paused ? '将在当前分片后暂停' : '暂停'}</Button>}<Button type="primary" loading={uploading} onClick={() => void begin()}>{pending ? '继续上传' : '开始上传'}</Button></Space>} destroyOnClose={false}>
      <Alert type="info" showIcon message="文件直接写入目标机暂存目录" description="不占用 DP 数据盘存储完整模型包；目标机解压期间必须同时容纳压缩包、展开目录和安全余量。" style={{ marginBottom: 16 }} />
      <Form form={form} layout="vertical" disabled={uploading}>
        {user.role === 'admin' && <Form.Item label="所属账号"><Select value={uploadOwner} options={users.filter((item) => item.enabled).map((item) => ({value:item.id,label:item.username}))} onChange={(value) => { setUploadOwner(value); form.setFieldValue('environment_id', undefined) }} /></Form.Item>}
        <Form.Item name="name" label="模型名称" rules={[{required:true,max:128}]}><Input /></Form.Item>
		<Form.Item name="environment_id" label="目标环境" extra="仅展示已保存 SSH 凭据并完成主机指纹校验的环境" rules={[{required:true,message:'请选择目标环境'}]}><Select showSearch optionFilterProp="label" options={(environmentsQuery.data ?? []).filter((env) => env.has_password && env.last_validation_at).map((env) => ({value:env.id,label:`${env.name} · ${env.ip}`}))} /></Form.Item>
        <Form.Item name="target_dir" label="目标绝对目录" rules={[{required:true,pattern:/^\//,message:'请输入绝对目录'}]}><Input placeholder="/opt/models/Qwen3" /></Form.Item>
        <Form.Item label="模型压缩包" required><Upload beforeUpload={() => false} accept=".tar.gz,application/gzip" maxCount={1} fileList={fileList} onChange={({fileList}) => setFileList(fileList.slice(-1))}><Button>选择 .tar.gz 文件</Button></Upload></Form.Item>
      </Form>
      {(uploading || progress > 0) && <Progress percent={progress} status={paused ? 'normal' : 'active'} />}
    </Modal>
    <Modal title="删除模型" open={Boolean(deleting)} okButtonProps={{ danger:true, disabled: confirmName !== deleting?.name }} okText="确认删除" onOk={() => void removeModel()} onCancel={() => setDeleting(null)}><Alert type="error" showIcon message="请先停止所有引用该目录的推理服务" description="DP 无法判断模型是否正在被使用。删除时会校验远端目录中的 DP 归属标记。" /><Typography.Paragraph style={{marginTop:16}}>输入模型名称 <Typography.Text code>{deleting?.name}</Typography.Text>：</Typography.Paragraph><Input value={confirmName} onChange={(e) => setConfirmName(e.target.value)} /></Modal>
    <Modal title={`模型任务日志${logTask ? ` · ${logTask.action === 'deploy' ? '部署' : '删除'}` : ''}`} width={820} open={Boolean(logTask)} footer={null} onCancel={() => setLogTask(null)}><pre className="terminal">{logs.map((event) => `${new Date(event.time).toLocaleTimeString()} [${event.stream ?? event.stage ?? 'state'}] ${event.message ?? ''}`).join('\n') || '正在等待任务日志…'}</pre></Modal>
  </div>
}

function bytes(value: number) { if (!value) return '0 B'; const units=['B','KiB','MiB','GiB','TiB']; const index=Math.min(Math.floor(Math.log(value)/Math.log(1024)),units.length-1); return `${(value/1024**index).toFixed(index?1:0)} ${units[index]}` }
