import { useEffect, useMemo, useRef, useState } from 'react'
import {
  CheckCircleFilled,
  CloseCircleFilled,
  ClockCircleOutlined,
  LoadingOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { Button, Modal, Space, Tag, Typography } from 'antd'
import { api } from '../../api/client'
import type { OperationEvent, OperationStatus } from '../../types'

interface Props {
  operationId: string | null
  open: boolean
  onClose: () => void
  onComplete: () => void
}

const terminalStatuses: OperationStatus[] = ['succeeded', 'failed', 'timed_out', 'interrupted']

const statusMeta: Record<OperationStatus, { label: string; color: string; icon: React.ReactNode }> = {
  queued: { label: '等待执行', color: 'default', icon: <ClockCircleOutlined /> },
  running: { label: '执行中', color: 'processing', icon: <LoadingOutlined /> },
  succeeded: { label: '执行成功', color: 'success', icon: <CheckCircleFilled /> },
  failed: { label: '执行失败', color: 'error', icon: <CloseCircleFilled /> },
  timed_out: { label: '执行超时', color: 'error', icon: <ClockCircleOutlined /> },
  interrupted: { label: '执行中断', color: 'warning', icon: <CloseCircleFilled /> },
}

export function OperationModal({ operationId, open, onClose, onComplete }: Props) {
  const [events, setEvents] = useState<OperationEvent[]>([])
  const [streamStatus, setStreamStatus] = useState<OperationStatus>('queued')
  const terminalRef = useRef<HTMLDivElement>(null)
  const completedRef = useRef(false)

  const operationQuery = useQuery({
    queryKey: ['operation', operationId],
    queryFn: () => api.getOperation(operationId!),
    enabled: open && Boolean(operationId),
    refetchInterval: ({ state }) => {
      const status = state.data?.status
      return status && terminalStatuses.includes(status) ? false : 1_000
    },
  })

  useEffect(() => {
    if (!open || !operationId) return
    setEvents([])
    setStreamStatus('queued')
    completedRef.current = false
    const source = new EventSource(`/api/v1/operations/${operationId}/events`)
    const receive = (raw: Event) => {
      const messageEvent = raw as MessageEvent<string>
      const event = JSON.parse(messageEvent.data) as OperationEvent
      setEvents((current) => {
        if (current.some((item) => item.seq === event.seq)) return current
        return [...current, event].sort((a, b) => a.seq - b.seq)
      })
      if (event.status) {
        setStreamStatus(event.status)
        if (terminalStatuses.includes(event.status)) {
          source.close()
          if (!completedRef.current) {
            completedRef.current = true
            onComplete()
          }
        }
      }
    }
    source.addEventListener('log', receive)
    source.addEventListener('state', receive)
    return () => source.close()
  }, [open, operationId, onComplete])

  useEffect(() => {
    terminalRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [events])

  const status = operationQuery.data?.status ?? streamStatus
  const meta = statusMeta[status]
  const logs = useMemo(
    () => events.filter((event) => event.type === 'log' || event.message),
    [events],
  )

  return (
    <Modal
      title={
        <Space>
          <span>执行结果</span>
          <Tag color={meta.color} icon={meta.icon}>{meta.label}</Tag>
        </Space>
      }
      width={900}
      open={open}
      onCancel={onClose}
      mask={{ closable: terminalStatuses.includes(status) }}
      footer={
        <Space>
          {operationQuery.data?.exit_code !== undefined && (
            <Typography.Text type="secondary">
              退出码：{operationQuery.data.exit_code}
            </Typography.Text>
          )}
          <Button onClick={onClose}>
            {terminalStatuses.includes(status) ? '关闭' : '后台继续执行'}
          </Button>
        </Space>
      }
    >
      <div className="operation-meta">
        <Typography.Text type="secondary">
          操作 ID：<Typography.Text code copyable>{operationId}</Typography.Text>
        </Typography.Text>
        <Typography.Text type="secondary">
          当前阶段：{operationQuery.data?.stage ?? 'queued'}
        </Typography.Text>
        {operationQuery.data?.request_id && <Typography.Text type="secondary">请求 ID：<Typography.Text code copyable>{operationQuery.data.request_id}</Typography.Text></Typography.Text>}
      </div>
      <div className="terminal">
        {logs.length === 0 && (
          <div className="terminal-placeholder">正在等待服务端输出…</div>
        )}
        {logs.map((event) => (
          <div className={`terminal-line terminal-${event.stream ?? 'system'}`} key={event.seq}>
            <span className="terminal-time">{formatLogTime(event.time)}</span>
            <span className="terminal-stream">{event.stream ?? event.type}</span>
            <span>{event.message}</span>
          </div>
        ))}
        <div ref={terminalRef} />
      </div>
      {operationQuery.data?.error_message && (
        <Typography.Paragraph type="danger" className="operation-error">
          {operationQuery.data.error_message}
        </Typography.Paragraph>
      )}
    </Modal>
  )
}

function formatLogTime(value: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value))
}
