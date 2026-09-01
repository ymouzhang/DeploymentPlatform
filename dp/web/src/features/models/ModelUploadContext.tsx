import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { CloudUploadOutlined, PauseOutlined } from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { App, Button, Card, Progress, Space, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'

export type ModelUploadValues = { name: string; environment_id: string; target_dir: string }
export type PendingModelUpload = ModelUploadValues & {
  upload_id: string
  owner_id?: string
  filename: string
  size: number
  chunk_bytes: number
}

type UploadStatus = 'waiting' | 'uploading' | 'pausing' | 'paused' | 'failed'
type UploadActivity = { status: UploadStatus; progress: number; error?: string }

type ModelUploadContextValue = {
  pending: PendingModelUpload | null
  activity: UploadActivity | null
  start: (values: ModelUploadValues, file: File, ownerId?: string) => Promise<void>
  resume: (file: File) => Promise<void>
  pause: () => void
  cancel: () => Promise<void>
}

const ModelUploadContext = createContext<ModelUploadContextValue | null>(null)

function loadPending(storageKey: string): PendingModelUpload | null {
  try {
    const value = localStorage.getItem(storageKey)
    return value ? JSON.parse(value) as PendingModelUpload : null
  } catch {
    return null
  }
}

export function ModelUploadProvider({ userId, children }: { userId: string; children: ReactNode }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const storageKey = `dp:model-upload:v2:${userId}`
  const initialPending = useRef(loadPending(storageKey)).current
  const [pending, setPending] = useState<PendingModelUpload | null>(initialPending)
  const [activity, setActivity] = useState<UploadActivity | null>(
    initialPending ? { status: 'waiting', progress: 0 } : null,
  )
  const runID = useRef(0)
  const pauseRequested = useRef(false)
  const browserRequired = activity?.status === 'uploading' || activity?.status === 'pausing'

  useEffect(() => {
    if (!browserRequired) return
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warnBeforeUnload)
    return () => window.removeEventListener('beforeunload', warnBeforeUnload)
  }, [browserRequired])

  const savePending = (session: PendingModelUpload | null) => {
    setPending(session)
    if (session) localStorage.setItem(storageKey, JSON.stringify(session))
    else localStorage.removeItem(storageKey)
  }

  const transfer = async (session: PendingModelUpload, file: File, currentRunID: number) => {
    try {
      let offset = await api.modelUploadOffset(session.upload_id)
      setActivity({ status: 'uploading', progress: Math.floor(offset * 100 / file.size) })
      while (offset < file.size) {
        if (currentRunID !== runID.current) return
        if (pauseRequested.current) {
          setActivity({ status: 'paused', progress: Math.floor(offset * 100 / file.size) })
          message.info('模型上传已暂停，可在模型管理页面继续')
          return
        }
        const nextEnd = Math.min(offset + session.chunk_bytes, file.size)
        offset = await api.uploadModelChunk(session.upload_id, offset, file.slice(offset, nextEnd))
        if (currentRunID !== runID.current) return
        setActivity({
          status: pauseRequested.current ? 'pausing' : 'uploading',
          progress: Math.floor(offset * 100 / file.size),
        })
      }
      if (currentRunID !== runID.current) return
      await api.completeModelUpload(session.upload_id)
      savePending(null)
      setActivity(null)
      message.success('模型文件上传完成，目标机正在后台校验并解压，现在可以关闭浏览器')
      void queryClient.invalidateQueries({ queryKey: ['models'] })
    } catch (error) {
      if (currentRunID !== runID.current) return
      const text = (error as Error).message
      setActivity((current) => ({ status: 'failed', progress: current?.progress ?? 0, error: text }))
      message.error(`模型后台上传失败：${text}`)
      void queryClient.invalidateQueries({ queryKey: ['models'] })
    }
  }

  const run = (session: PendingModelUpload, file: File) => {
    pauseRequested.current = false
    const currentRunID = ++runID.current
    setActivity((current) => ({ status: 'uploading', progress: current?.progress ?? 0 }))
    void transfer(session, file, currentRunID)
  }

  const start = async (values: ModelUploadValues, file: File, ownerId?: string) => {
    if (pending) throw new Error('已有未完成的模型上传，请先继续或取消')
    const created = await api.createModelUpload(
      { ...values, original_filename: file.name, total_bytes: file.size },
      ownerId,
    )
    const session: PendingModelUpload = {
      ...values,
      upload_id: created.upload_id,
      owner_id: ownerId,
      filename: file.name,
      size: file.size,
      chunk_bytes: created.chunk_bytes,
    }
    savePending(session)
    void queryClient.invalidateQueries({ queryKey: ['models'] })
    run(session, file)
  }

  const resume = async (file: File) => {
    if (!pending) throw new Error('没有可继续的模型上传')
    if (pending.filename !== file.name || pending.size !== file.size) {
      throw new Error('请选择与未完成上传相同的原始文件')
    }
    run(pending, file)
  }

  const pause = () => {
    if (activity?.status !== 'uploading') return
    pauseRequested.current = true
    setActivity((current) => current ? { ...current, status: 'pausing' } : current)
  }

  const cancel = async () => {
    if (!pending) return
    if (activity?.status === 'uploading' || activity?.status === 'pausing') {
      throw new Error('请等待当前分片暂停后再取消上传')
    }
    ++runID.current
    await api.cancelModelUpload(pending.upload_id)
    savePending(null)
    setActivity(null)
    void queryClient.invalidateQueries({ queryKey: ['models'] })
  }

  return (
    <ModelUploadContext.Provider value={{ pending, activity, start, resume, pause, cancel }}>
      {children}
      {pending && activity && <BackgroundUploadIndicator pending={pending} activity={activity} pause={pause} />}
    </ModelUploadContext.Provider>
  )
}

function BackgroundUploadIndicator({ pending, activity, pause }: {
  pending: PendingModelUpload
  activity: UploadActivity
  pause: () => void
}) {
  const navigate = useNavigate()
  const running = activity.status === 'uploading' || activity.status === 'pausing'
  const label = activity.status === 'failed' ? '上传失败' : activity.status === 'paused' || activity.status === 'waiting' ? '等待继续' : activity.status === 'pausing' ? '正在暂停' : '后台上传中'
  return (
    <Card size="small" style={{ position: 'fixed', right: 24, bottom: 24, zIndex: 1100, width: 330, boxShadow: '0 12px 36px rgba(24,32,56,.18)' }}>
      <Space orientation="vertical" size={8} style={{ width: '100%' }}>
        <Space><CloudUploadOutlined /><Typography.Text strong ellipsis style={{ maxWidth: 250 }}>{pending.name}</Typography.Text></Space>
        <Typography.Text type={activity.status === 'failed' ? 'danger' : 'secondary'}>{label}{activity.error ? `：${activity.error}` : ''}</Typography.Text>
        {running && <Typography.Text type="warning" strong>上传依赖当前浏览器，请勿刷新或关闭浏览器</Typography.Text>}
        <Progress percent={activity.progress} status={activity.status === 'failed' ? 'exception' : running ? 'active' : 'normal'} size="small" />
        <Space>
          {activity.status === 'uploading' && <Button size="small" icon={<PauseOutlined />} onClick={pause}>暂停</Button>}
          <Button size="small" type="link" onClick={() => navigate('/models')}>查看模型管理</Button>
        </Space>
      </Space>
    </Card>
  )
}

export function useModelUpload() {
  const value = useContext(ModelUploadContext)
  if (!value) throw new Error('ModelUploadContext is unavailable')
  return value
}
