import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'

afterEach(() => vi.unstubAllGlobals())

describe('changePassword', () => {
  it('does not send the client-only password confirmation field', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { password_changed: true } }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await api.changePassword({
      current_password: 'temporary-password',
      new_password: 'new-password',
      confirm_password: 'new-password',
    } as Parameters<typeof api.changePassword>[0] & { confirm_password: string })

    expect(fetchMock).toHaveBeenCalledOnce()
    const [, init] = fetchMock.mock.calls[0]
    expect(JSON.parse(init.body as string)).toEqual({
      current_password: 'temporary-password',
      new_password: 'new-password',
    })
  })
})

describe('audit events', () => {
  it('normalizes missing and null actor roles to an empty array', async () => {
    const events = [
      { id: 'event-1', actor_roles: undefined },
      { id: 'event-2', actor_roles: null },
    ]
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { items: events, next_cursor: '' } }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: events[1] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
    vi.stubGlobal('fetch', fetchMock)

    const page = await api.listAuditEvents({ from: '2026-09-01T00:00:00Z', to: '2026-09-02T00:00:00Z' })
    const detail = await api.getAuditEvent('event-2')

    expect(page.items.map((item) => item.actor_roles)).toEqual([[], []])
    expect(detail.actor_roles).toEqual([])
  })
})
