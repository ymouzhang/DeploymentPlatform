import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { CloseOutlined, CloudUploadOutlined, MinusOutlined, StopOutlined } from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { App, Button, Card, Progress, Space, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'

export type PackageUploadInput = {
  serviceType: string
  file: File
  note?: string
  ownerId?: string
}

export type PackageUploadTask = {
  id: string
  serviceType: string
  ownerId?: string
  filename: string
  status: 'uploading' | 'validating' | 'completed' | 'failed' | 'cancelled'
  progress: number
  error?: string
}

type PackageUploadContextValue = {
  tasks: PackageUploadTask[]
  start: (input: PackageUploadInput) => void
  cancel: (id: string) => void
  dismiss: (id: string) => void
}

const PackageUploadContext = createContext<PackageUploadContextValue | null>(null)

export function PackageUploadProvider({ children }: { children: ReactNode }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [tasks, setTasks] = useState<PackageUploadTask[]>([])
  const controllers = useRef(new Map<string, AbortController>())
  const activeKeys = useRef(new Set<string>())
  const active = tasks.some((task) => task.status === 'uploading' || task.status === 'validating')

  useEffect(() => {
    if (!active) return
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [active])

  useEffect(() => () => {
    controllers.current.forEach((controller) => controller.abort())
  }, [])

  const patchTask = (id: string, patch: Partial<PackageUploadTask>) => {
    setTasks((items) => items.map((item) => item.id === id ? { ...item, ...patch } : item))
  }

  const dismiss = (id: string) => {
    setTasks((items) => items.filter((item) => item.id !== id))
  }

  const cancel = (id: string) => {
    controllers.current.get(id)?.abort()
  }

  const start = (input: PackageUploadInput) => {
    const taskKey = `${input.ownerId ?? ''}\u0000${input.serviceType}`
    if (activeKeys.current.has(taskKey)) throw new Error('该账号的同一服务类型已有上传任务正在进行')

    const id = crypto.randomUUID()
    const controller = new AbortController()
    activeKeys.current.add(taskKey)
    controllers.current.set(id, controller)
    setTasks((items) => [...items, {
      id,
      serviceType: input.serviceType,
      ownerId: input.ownerId,
      filename: input.file.name,
      status: 'uploading',
      progress: 0,
    }])
    void api.uploadPackageWithProgress({
      ...input,
      signal: controller.signal,
      onProgress: (loaded, total) => patchTask(id, { progress: total > 0 ? Math.min(100, Math.floor(loaded * 100 / total)) : 0 }),
      onUploaded: () => patchTask(id, { status: 'validating', progress: 100 }),
    }).then(() => {
      patchTask(id, { status: 'completed', progress: 100 })
      message.success(`${input.serviceType} 安装包已上传并通过校验`)
      void queryClient.invalidateQueries({ queryKey: ['packages'] })
      void queryClient.invalidateQueries({ queryKey: ['service-types'] })
    }).catch((error: Error) => {
      if (error.name === 'AbortError') {
        patchTask(id, { status: 'cancelled', error: '上传已取消' })
        return
      }
      patchTask(id, { status: 'failed', error: error.message })
      message.error(`${input.serviceType} 上传失败：${error.message}`)
    }).finally(() => {
      controllers.current.delete(id)
      activeKeys.current.delete(taskKey)
    })
  }

  return <PackageUploadContext.Provider value={{ tasks, start, cancel, dismiss }}>
    {children}
    {tasks.length > 0 && <PackageUploadIndicator tasks={tasks} cancel={cancel} dismiss={dismiss} />}
  </PackageUploadContext.Provider>
}

function PackageUploadIndicator({ tasks, cancel, dismiss }: {
  tasks: PackageUploadTask[]
  cancel: (id: string) => void
  dismiss: (id: string) => void
}) {
  const navigate = useNavigate()
  const [expanded, setExpanded] = useState(false)
  const active = tasks.filter((task) => task.status === 'uploading' || task.status === 'validating').length
  const activeTasks = tasks.filter((task) => task.status === 'uploading' || task.status === 'validating')
  const overallProgress = activeTasks.length > 0
    ? Math.round(activeTasks.reduce((sum, task) => sum + task.progress, 0) / activeTasks.length)
    : 100
  if (!expanded) {
    return <Button className="upload-task-fab package-upload-fab" icon={<CloudUploadOutlined />} onClick={() => setExpanded(true)}>
      安装包任务 · {active > 0 ? `${active} 个 · ${overallProgress}%` : `${tasks.length} 项`}
    </Button>
  }
  return <Card className="upload-task-panel package-upload-panel" size="small" title={<Space><CloudUploadOutlined /><span>安装包任务</span></Space>} extra={<Space size={2}><Button type="link" size="small" onClick={() => navigate('/packages')}>查看</Button><Button type="text" size="small" aria-label="收起安装包任务" icon={<MinusOutlined />} onClick={() => setExpanded(false)} /></Space>}>
    <Space orientation="vertical" size={12} style={{ width: '100%' }}>
      {active > 0 && <Typography.Text type="warning" strong>{active} 个任务进行中，请勿刷新或关闭浏览器</Typography.Text>}
      {tasks.map((task) => {
        const running = task.status === 'uploading' || task.status === 'validating'
        const label = task.status === 'uploading' ? `上传中 ${task.progress}%` : task.status === 'validating' ? '已上传，服务端校验中' : task.status === 'completed' ? '上传并校验完成' : task.status === 'cancelled' ? '已取消' : `失败：${task.error ?? '未知错误'}`
        return <div key={task.id}>
          <Space style={{ width: '100%', justifyContent: 'space-between' }}>
            <Typography.Text strong ellipsis style={{ maxWidth: 250 }}>{task.serviceType} · {task.filename}</Typography.Text>
            {running ? <Button type="text" danger size="small" aria-label={`取消 ${task.serviceType}`} icon={<StopOutlined />} onClick={() => cancel(task.id)} /> : <Button type="text" size="small" aria-label={`清除 ${task.serviceType}`} icon={<CloseOutlined />} onClick={() => dismiss(task.id)} />}
          </Space>
          <Typography.Text type={task.status === 'failed' ? 'danger' : 'secondary'}>{label}</Typography.Text>
          <Progress percent={task.progress} showInfo={false} status={task.status === 'failed' ? 'exception' : task.status === 'completed' ? 'success' : running ? 'active' : 'normal'} size="small" />
        </div>
      })}
    </Space>
  </Card>
}

export function usePackageUpload() {
  const value = useContext(PackageUploadContext)
  if (!value) throw new Error('PackageUploadContext is unavailable')
  return value
}
