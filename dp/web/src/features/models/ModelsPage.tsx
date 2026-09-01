import { useEffect, useMemo, useState } from 'react'
import { CloudUploadOutlined, DeleteOutlined, FileSearchOutlined, ReloadOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, App, Button, Card, Form, Input, Modal, Progress, Select, Space, Table, Tag, Typography, Upload } from 'antd'
import type { UploadFile } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { api } from '../../api/client'
import { useAuth } from '../../app/AuthContext'
import type { Model, ModelTask, OperationEvent } from '../../types'
import { useModelUpload, type ModelUploadValues } from './ModelUploadContext'

const statusLabels: Record<Model['status'], { text: string; color: string }> = {
  uploading: { text: '上传中', color: 'processing' }, deploying: { text: '部署中', color: 'processing' },
  ready: { text: '可用', color: 'success' }, failed: { text: '失败', color: 'error' },
  deleting: { text: '删除中', color: 'warning' }, deleted: { text: '已删除', color: 'default' },
}

export function ModelsPage() {
  const { message } = App.useApp()
  const { ownerId, user, users } = useAuth()
  const queryClient = useQueryClient()
  const { pending, activity, start, resume, cancel } = useModelUpload()
  const [form] = Form.useForm<ModelUploadValues>()
  const [open, setOpen] = useState(false)
  const [fileList, setFileList] = useState<UploadFile[]>([])
  const [uploadOwner, setUploadOwner] = useState(user.id)
	const [keyword, setKeyword] = useState('')
  const [deleting, setDeleting] = useState<Model | null>(null)
  const [confirmName, setConfirmName] = useState('')
  const [logTask, setLogTask] = useState<ModelTask | null>(null)
  const [logs, setLogs] = useState<OperationEvent[]>([])

  const modelsQuery = useQuery({
    queryKey: ['models', ownerId], queryFn: () => api.listModels(ownerId),
    refetchInterval: (query) => query.state.data?.some((item) => ['uploading', 'deploying', 'deleting'].includes(item.status)) ? 2000 : false,
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
    if (pending && (pending.filename !== file.name || pending.size !== file.size)) {
      message.error('请选择与未完成会话相同的原始文件'); return
    }
    try {
      if (pending) await resume(file)
      else await start(values, file, user.role === 'admin' ? uploadOwner : undefined)
      setOpen(false); setFileList([]); form.resetFields()
      message.warning('模型上传已转入后台，可以切换页面，但请勿刷新或关闭浏览器')
    } catch (error) { message.error((error as Error).message) }
  }

  const openUpload = () => {
    if (activity?.status === 'uploading' || activity?.status === 'pausing') {
      message.info('模型正在后台上传，可通过右下角进度卡片查看或暂停')
      return
    }
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
    try { await cancel(); setOpen(false); message.success('上传会话已取消') }
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
    {pending && <Alert showIcon type={activity?.status === 'failed' ? 'error' : 'warning'} message={`${activity?.status === 'failed' ? '后台上传失败' : '存在未完成上传'}：${pending.name}`} description={activity?.error ?? (['uploading','pausing'].includes(activity?.status ?? '') ? '上传依赖当前浏览器。可以关闭弹窗并切换 DP 页面，但请勿刷新、关闭标签页或退出浏览器。' : '刷新或关闭浏览器后，需要重新选择同一个本地文件，上传会从目标机已经保存的位置继续。')} action={!['uploading','pausing'].includes(activity?.status ?? '') ? <Button size="small" onClick={openUpload}>继续上传</Button> : undefined} />}
	<Card><Input.Search allowClear value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="搜索模型、环境、IP 或目标目录" style={{ maxWidth: 420, marginBottom: 16 }} /><Table<Model> rowKey="id" loading={modelsQuery.isLoading} dataSource={visibleModels} columns={columns} scroll={{ x: 1200 }} pagination={{ pageSize: 20, showSizeChanger: true }} /></Card>
    <Modal title={pending ? '继续上传模型' : '上传离线模型'} open={open} onCancel={() => setOpen(false)} footer={<Space>{pending && <Button danger onClick={() => void cancelPending()}>取消会话</Button>}<Button type="primary" onClick={() => void begin()}>{pending ? '继续后台上传' : '开始后台上传'}</Button></Space>} destroyOnClose={false}>
      <Alert type="info" showIcon message="文件直接写入目标机暂存目录" description="不占用 DP 数据盘存储完整模型包；目标机解压期间必须同时容纳压缩包、展开目录和安全余量。" style={{ marginBottom: 16 }} />
      <Alert type="warning" showIcon message="后台上传期间不能关闭浏览器" description="开始后可以关闭本弹窗并切换 DP 内的其他页面，但刷新页面、关闭标签页、退出登录或关闭浏览器都会中断上传。中断后可重新选择同一文件断点续传。" style={{ marginBottom: 16 }} />
      <Form form={form} layout="vertical">
        {user.role === 'admin' && <Form.Item label="所属账号"><Select value={uploadOwner} options={users.filter((item) => item.enabled).map((item) => ({value:item.id,label:item.username}))} onChange={(value) => { setUploadOwner(value); form.setFieldValue('environment_id', undefined) }} /></Form.Item>}
        <Form.Item name="name" label="模型名称" rules={[{required:true,max:128}]}><Input /></Form.Item>
		<Form.Item name="environment_id" label="目标环境" extra="仅展示已保存 SSH 凭据并完成主机指纹校验的环境" rules={[{required:true,message:'请选择目标环境'}]}><Select showSearch optionFilterProp="label" options={(environmentsQuery.data ?? []).filter((env) => env.has_password && env.last_validation_at).map((env) => ({value:env.id,label:`${env.name} · ${env.ip}`}))} /></Form.Item>
        <Form.Item name="target_dir" label="目标绝对目录" rules={[{required:true,pattern:/^\//,message:'请输入绝对目录'}]}><Input placeholder="/opt/models/Qwen3" /></Form.Item>
        <Form.Item label="模型压缩包" required><Upload beforeUpload={() => false} accept=".tar.gz,application/gzip" maxCount={1} fileList={fileList} onChange={({fileList}) => setFileList(fileList.slice(-1))}><Button>选择 .tar.gz 文件</Button></Upload></Form.Item>
      </Form>
    </Modal>
    <Modal title="删除模型" open={Boolean(deleting)} okButtonProps={{ danger:true, disabled: confirmName !== deleting?.name }} okText="确认删除" onOk={() => void removeModel()} onCancel={() => setDeleting(null)}><Alert type="error" showIcon message="请先停止所有引用该目录的推理服务" description="DP 无法判断模型是否正在被使用。删除时会校验远端目录中的 DP 归属标记。" /><Typography.Paragraph style={{marginTop:16}}>输入模型名称 <Typography.Text code>{deleting?.name}</Typography.Text>：</Typography.Paragraph><Input value={confirmName} onChange={(e) => setConfirmName(e.target.value)} /></Modal>
    <Modal title={`模型任务日志${logTask ? ` · ${logTask.action === 'deploy' ? '部署' : '删除'}` : ''}`} width={820} open={Boolean(logTask)} footer={null} onCancel={() => setLogTask(null)}><pre className="terminal">{logs.map((event) => `${new Date(event.time).toLocaleTimeString()} [${event.stream ?? event.stage ?? 'state'}] ${event.message ?? ''}`).join('\n') || '正在等待任务日志…'}</pre></Modal>
  </div>
}

function bytes(value: number) { if (!value) return '0 B'; const units=['B','KiB','MiB','GiB','TiB']; const index=Math.min(Math.floor(Math.log(value)/Math.log(1024)),units.length-1); return `${(value/1024**index).toFixed(index?1:0)} ${units[index]}` }
