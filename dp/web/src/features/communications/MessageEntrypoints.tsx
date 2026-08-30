import { MessageFilled, MessageOutlined } from '@ant-design/icons'
import { Badge, Button, Tooltip } from 'antd'

interface MessageEntryProps {
  unread: number
  onClick?: () => void
  className?: string
}

function displayedUnread(unread: number) {
  return unread > 99 ? '99+' : String(unread)
}

export function SidebarMessageIcon({ unread, className }: MessageEntryProps) {
  return (
    <Badge dot={unread > 0} offset={[3, 0]} className={`nav-message-icon${className ? ` ${className}` : ''}`}>
      {unread > 0 ? <MessageFilled /> : <MessageOutlined />}
    </Badge>
  )
}

export function SidebarMessageLabel({ unread }: MessageEntryProps) {
  return (
    <span className="nav-message-label">
      <span>消息中心</span>
      {unread > 0 && <span className="nav-unread-count">{displayedUnread(unread)}</span>}
    </span>
  )
}

export function HeaderMessageEntry({ unread, onClick }: MessageEntryProps) {
  const hasUnread = unread > 0
  const label = hasUnread ? `消息中心，${unread} 条未读消息` : '消息中心'
  return (
    <Tooltip title={hasUnread ? `${unread} 条未读消息` : '消息中心'}>
      <Badge count={unread} overflowCount={99} className={`header-message-badge${hasUnread ? ' has-unread' : ''}`}>
        <Button
          type="text"
          className={`header-message-button${hasUnread ? ' is-unread' : ''}`}
          icon={hasUnread ? <MessageFilled /> : <MessageOutlined />}
          aria-label={label}
          onClick={onClick}
        />
      </Badge>
    </Tooltip>
  )
}
