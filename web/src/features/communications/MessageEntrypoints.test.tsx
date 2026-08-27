// @vitest-environment jsdom

import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { HeaderMessageEntry, SidebarMessageIcon, SidebarMessageLabel } from './MessageEntrypoints'

describe('message entrypoints', () => {
  it('emphasizes unread messages and keeps the header entry actionable', () => {
    const onClick = vi.fn()
    render(<HeaderMessageEntry unread={3} onClick={onClick} />)

    const button = screen.getByRole('button', { name: '消息中心，3 条未读消息' })
    expect(button.className).toContain('is-unread')
    expect(screen.getByText('3')).toBeTruthy()
    fireEvent.click(button)
    expect(onClick).toHaveBeenCalledOnce()
  })

  it('shows a capped unread count in the sidebar label', () => {
    render(<SidebarMessageLabel unread={120} />)
    expect(screen.getByText('消息中心')).toBeTruthy()
    expect(screen.getByText('99+')).toBeTruthy()
  })

  it('keeps an unread dot on the collapsed sidebar icon', () => {
    const { container } = render(<SidebarMessageIcon unread={1} className="ant-menu-item-icon" />)
    expect(container.querySelector('.ant-badge-dot')).toBeTruthy()
    expect(container.querySelector('.nav-message-icon')?.className).toContain('ant-menu-item-icon')
  })

  it('uses the neutral header style when all messages are read', () => {
    render(<HeaderMessageEntry unread={0} />)
    const button = screen.getByRole('button', { name: '消息中心' })
    expect(button.className).not.toContain('is-unread')
  })
})
