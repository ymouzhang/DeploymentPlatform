import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { CloudUploadOutlined, PauseOutlined } from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { App, Button, Card, Progress, Space, Typography } from 'antd'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'

export type ModelUploadValues = { name: string; host_id: string; target_dir: string }
export type PendingModelUpload = ModelUploadValues & {
  upload_id: string
  owner_id?: string
  filename: string
  size: number
  chunk_bytes: number
}

type UploadStatus = 'waiting' | 'uploading' | 'pausing' | 'paused' | 'failed'
export type ModelUploadActivity = { status: UploadStatus; progress: number; error?: string }

type ModelUploadContextValue = {
  pending: PendingModelUpload[]
  activities: Record<string, ModelUploadActivity>
  start: (values: ModelUploadValues, file: File, ownerId?: string) => Promise<void>
  resume: (uploadId: string, file: File) => Promise<void>
  pause: (uploadId: string) => void
  cancel: (uploadId: string) => Promise<void>
}

const ModelUploadContext = createContext<ModelUploadContextValue | null>(null)

function loadPending(storageKey: string, legacyKey: string): PendingModelUpload[] {
  try {
    const value = localStorage.getItem(storageKey)
    if (value) {
      const parsed = JSON.parse(value) as PendingModelUpload[]
      return Array.isArray(parsed) ? parsed : []
    }
    const legacyValue = localStorage.getItem(legacyKey)
    if (!legacyValue) return []
    const legacy = JSON.parse(legacyValue) as PendingModelUpload
    localStorage.removeItem(legacyKey)
    localStorage.setItem(storageKey, JSON.stringify([legacy]))
    return [legacy]
  } catch {
    return []
  }
}

export function ModelUploadProvider({ userId, children }: { userId: string; children: ReactNode }) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const storageKey = `dp:model-upload:v3:${userId}`
  const initialPending = useRef(loadPending(storageKey, `dp:model-upload:v2:${userId}`)).current
  const [pending, setPending] = useState<PendingModelUpload[]>(initialPending)
  const [activities, setActivities] = useState<Record<string, ModelUploadActivity>>(() =>
    Object.fromEntries(initialPending.map((item) => [item.upload_id, { status: 'waiting', progress: 0 }])),
  )
  const runs = useRef(new Map<string, number>())
  const pauseRequested = useRef(new Set<string>())
  const nextRunID = useRef(0)
  const browserRequired = Object.values(activities).some((item) => item.status === 'uploading' || item.status === 'pausing')

  useEffect(() => {
    if (!browserRequired) return
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warnBeforeUnload)
    return () => window.removeEventListener('beforeunload', warnBeforeUnload)
  }, [browserRequired])

  const savePending = (updater: (items: PendingModelUpload[]) => PendingModelUpload[]) => {
    setPending((items) => {
      const next = updater(items)
      if (next.length > 0) localStorage.setItem(storageKey, JSON.stringify(next))
      else localStorage.removeItem(storageKey)
      return next
    })
  }

  const patchActivity = (id: string, patch: Partial<ModelUploadActivity>) => {
    setActivities((items) => ({
      ...items,
      [id]: { ...(items[id] ?? { status: 'waiting', progress: 0 }), ...patch },
    }))
  }

  const removeActivity = (id: string) => {
    setActivities((items) => {
      const next = { ...items }
      delete next[id]
      return next
    })
  }

  const transfer = async (session: PendingModelUpload, file: File, runID: number) => {
    const id = session.upload_id
    try {
      let offset = await api.modelUploadOffset(id)
      patchActivity(id, { status: 'uploading', progress: Math.floor(offset * 100 / file.size), error: undefined })
      while (offset < file.size) {
        if (runs.current.get(id) !== runID) return
        if (pauseRequested.current.has(id)) {
          patchActivity(id, { status: 'paused', progress: Math.floor(offset * 100 / file.size) })
          message.info(`${session.name} 上传已暂停`)
          return
        }
        const nextEnd = Math.min(offset + session.chunk_bytes, file.size)
        offset = await api.uploadModelChunk(id, offset, file.slice(offset, nextEnd))
        if (runs.current.get(id) !== runID) return
        patchActivity(id, {
          status: pauseRequested.current.has(id) ? 'pausing' : 'uploading',
          progress: Math.floor(offset * 100 / file.size),
        })
      }
      if (runs.current.get(id) !== runID) return
      await api.completeModelUpload(id)
      savePending((items) => items.filter((item) => item.upload_id !== id))
      removeActivity(id)
      runs.current.delete(id)
      message.success(`${session.name} 文件上传完成，目标机正在后台校验并解压`)
      void queryClient.invalidateQueries({ queryKey: ['models'] })
    } catch (error) {
      if (runs.current.get(id) !== runID) return
      const text = (error as Error).message
      patchActivity(id, { status: 'failed', error: text })
      message.error(`${session.name} 后台上传失败：${text}`)
      void queryClient.invalidateQueries({ queryKey: ['models'] })
    }
  }

  const run = (session: PendingModelUpload, file: File) => {
    pauseRequested.current.delete(session.upload_id)
    const runID = ++nextRunID.current
    runs.current.set(session.upload_id, runID)
    patchActivity(session.upload_id, { status: 'uploading', error: undefined })
    void transfer(session, file, runID)
  }

  const start = async (values: ModelUploadValues, file: File, ownerId?: string) => {
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
    savePending((items) => [...items, session])
    void queryClient.invalidateQueries({ queryKey: ['models'] })
    run(session, file)
  }

  const resume = async (uploadId: string, file: File) => {
    const session = pending.find((item) => item.upload_id === uploadId)
    if (!session) throw new Error('没有可继续的模型上传')
    if (session.filename !== file.name || session.size !== file.size) {
      throw new Error('请选择与未完成上传相同的原始文件')
    }
    run(session, file)
  }

  const pause = (uploadId: string) => {
    if (activities[uploadId]?.status !== 'uploading') return
    pauseRequested.current.add(uploadId)
    patchActivity(uploadId, { status: 'pausing' })
  }

  const cancel = async (uploadId: string) => {
    const session = pending.find((item) => item.upload_id === uploadId)
    if (!session) return
    const activity = activities[uploadId]
    if (activity?.status === 'uploading' || activity?.status === 'pausing') {
      throw new Error('请等待当前分片暂停后再取消上传')
    }
    runs.current.delete(uploadId)
    await api.cancelModelUpload(uploadId)
    savePending((items) => items.filter((item) => item.upload_id !== uploadId))
    removeActivity(uploadId)
    void queryClient.invalidateQueries({ queryKey: ['models'] })
  }

  return <ModelUploadContext.Provider value={{ pending, activities, start, resume, pause, cancel }}>
    {children}
    {pending.length > 0 && <BackgroundUploadIndicator pending={pending} activities={activities} pause={pause} />}
  </ModelUploadContext.Provider>
}

function BackgroundUploadIndicator({ pending, activities, pause }: {
  pending: PendingModelUpload[]
  activities: Record<string, ModelUploadActivity>
  pause: (uploadId: string) => void
}) {
  const navigate = useNavigate()
  const runningCount = pending.filter((item) => ['uploading', 'pausing'].includes(activities[item.upload_id]?.status ?? '')).length
  return <Card className="model-upload-indicator" size="small" title={<Space><CloudUploadOutlined /><span>模型上传任务</span></Space>} extra={<Button type="link" size="small" onClick={() => navigate('/models')}>查看</Button>} style={{ position: 'fixed', right: 428, bottom: 24, zIndex: 1090, width: 380, maxHeight: 460, overflow: 'auto', boxShadow: '0 12px 36px rgba(24,32,56,.18)' }}>
    <Space orientation="vertical" size={12} style={{ width: '100%' }}>
      {runningCount > 0 && <Typography.Text type="warning" strong>{runningCount} 个任务上传中，请勿刷新或关闭浏览器</Typography.Text>}
      {pending.map((session) => {
        const activity = activities[session.upload_id] ?? { status: 'waiting', progress: 0 }
        const running = activity.status === 'uploading' || activity.status === 'pausing'
        const label = activity.status === 'failed' ? `失败：${activity.error ?? '未知错误'}` : activity.status === 'paused' || activity.status === 'waiting' ? '等待继续' : activity.status === 'pausing' ? '正在暂停当前分片' : `上传中 ${activity.progress}%`
        return <div key={session.upload_id}>
          <Space style={{ width: '100%', justifyContent: 'space-between' }}>
            <Typography.Text strong ellipsis style={{ maxWidth: 280 }}>{session.name} · {session.filename}</Typography.Text>
            {activity.status === 'uploading' && <Button type="text" size="small" aria-label={`暂停 ${session.name}`} icon={<PauseOutlined />} onClick={() => pause(session.upload_id)} />}
          </Space>
          <Typography.Text type={activity.status === 'failed' ? 'danger' : 'secondary'}>{label}</Typography.Text>
          <Progress percent={activity.progress} showInfo={false} status={activity.status === 'failed' ? 'exception' : running ? 'active' : 'normal'} size="small" />
        </div>
      })}
    </Space>
  </Card>
}

export function useModelUpload() {
  const value = useContext(ModelUploadContext)
  if (!value) throw new Error('ModelUploadContext is unavailable')
  return value
}
