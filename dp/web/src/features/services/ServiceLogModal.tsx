import { memo, useEffect, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import Anser from 'anser'
import { Button, Modal, Space, Switch, Tag, Typography } from 'antd'
import type { Service } from '../../types'

interface Props {
  service: Service | null
  open: boolean
  onClose: () => void
}

interface LogEvent {
  seq: number
  time: string
  stream: string
  message: string
}

type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

const statusMeta: Record<ConnectionStatus, { label: string; color: string }> = {
  connecting: { label: '连接中', color: 'processing' },
  connected: { label: '已连接', color: 'success' },
  disconnected: { label: '已断开', color: 'default' },
  error: { label: '连接失败', color: 'error' },
}

const maxLines = 5_000

export function ServiceLogModal({ service, open, onClose }: Props) {
  const [logs, setLogs] = useState<LogEvent[]>([])
  const [status, setStatus] = useState<ConnectionStatus>('connecting')
  const [error, setError] = useState('')
  const [follow, setFollow] = useState(true)
  const [connectionKey, setConnectionKey] = useState(0)
  const terminalRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open || !service) return
    setLogs([])
    setFollow(true)
    setStatus('connecting')
    setError('')
    const source = new EventSource(
      `/api/v1/services/${service.service_instance.id}/logs/stream?tail=300`,
    )
    source.onopen = () => setStatus('connected')
    source.addEventListener('log', (raw) => {
      const event = JSON.parse((raw as MessageEvent<string>).data) as LogEvent
      setLogs((current) => [...current, event].slice(-maxLines))
    })
    source.addEventListener('stream-error', (raw) => {
      const payload = JSON.parse((raw as MessageEvent<string>).data) as { message: string }
      setError(payload.message)
      setStatus('error')
      source.close()
    })
    source.addEventListener('end', () => {
      setStatus('disconnected')
      source.close()
    })
    source.onerror = () => {
      setStatus('disconnected')
      source.close()
    }
    return () => source.close()
  }, [connectionKey, open, service])

  useEffect(() => {
    if (follow && terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight
    }
  }, [follow, logs])

  const meta = statusMeta[status]
  const title = service ? `实时日志 - ${service.service_instance.name}` : '实时日志'

  return (
    <Modal
      title={<Space><span>{title}</span><Tag color={meta.color}>{meta.label}</Tag></Space>}
      width={1000}
      open={open}
      onCancel={onClose}
      destroyOnHidden
      footer={
        <Space>
          <Typography.Text type="secondary">自动滚动</Typography.Text>
          <Switch size="small" checked={follow} onChange={setFollow} />
          <Button onClick={() => setLogs([])}>清空屏幕</Button>
          <Button
            disabled={status === 'connecting' || status === 'connected'}
            onClick={() => {
              setLogs([])
              setConnectionKey((value) => value + 1)
            }}
          >
            重新连接
          </Button>
          <Button type="primary" onClick={onClose}>关闭</Button>
        </Space>
      }
    >
      <div className="operation-meta">
        <Typography.Text type="secondary">{service?.service_instance.host.ip}</Typography.Text>
        <Typography.Text type="secondary" copyable>
          {service?.service_instance.install_dir}
        </Typography.Text>
      </div>
      <div
        ref={terminalRef}
        className="terminal service-log-terminal"
        onScroll={(event) => {
          const element = event.currentTarget
          setFollow(element.scrollHeight - element.scrollTop - element.clientHeight < 24)
        }}
      >
        {logs.length === 0 && (
          <div className="terminal-placeholder">
            {status === 'connecting' ? '正在连接 Docker Compose 日志…' : '暂无日志'}
          </div>
        )}
        {logs.map((event) => (
          <div className="service-log-line" key={event.seq}>
            <AnsiText value={event.message} />
          </div>
        ))}
      </div>
      {error && <Typography.Paragraph type="danger" className="operation-error">{error}</Typography.Paragraph>}
    </Modal>
  )
}

const AnsiText = memo(function AnsiText({ value }: { value: string }) {
  return Anser.ansiToJson(value, { remove_empty: true }).map((part, index) => {
    const decorations = new Set(part.decorations)
    const foreground = ansiColor(part.fg_truecolor || part.fg)
    const background = ansiColor(part.bg_truecolor || part.bg)
    const reverse = decorations.has('reverse')
    const style: CSSProperties = {
      color: reverse ? background : foreground,
      backgroundColor: reverse ? foreground : background,
      fontWeight: decorations.has('bold') ? 700 : undefined,
      fontStyle: decorations.has('italic') ? 'italic' : undefined,
      opacity: decorations.has('dim') ? 0.65 : undefined,
      visibility: decorations.has('hidden') ? 'hidden' : undefined,
      textDecoration: [
        decorations.has('underline') ? 'underline' : '',
        decorations.has('strikethrough') ? 'line-through' : '',
      ].filter(Boolean).join(' ') || undefined,
    }
    return <span style={style} key={index}>{part.content}</span>
  })
})

function ansiColor(value?: string) {
  return value && /^\d{1,3}(?:,\s*\d{1,3}){2}$/.test(value) ? `rgb(${value})` : undefined
}
