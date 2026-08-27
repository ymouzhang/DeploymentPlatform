import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { communicationKeys } from '../features/communications/queryKeys'
import { applyRealtimeEvent, synchronizeCommunicationQueries } from './RealtimeEvents'

describe('realtime communication cache invalidation', () => {
  it('invalidates the summary, lists, and only the changed detail', async () => {
    const client = new QueryClient()
    const changed = communicationKeys.detail('changed')
    const untouched = communicationKeys.detail('untouched')
    client.setQueryData(communicationKeys.summary, { unread: 0 })
    client.setQueryData(communicationKeys.list({ limit: 30 }), { items: [] })
    client.setQueryData(changed, { id: 'changed' })
    client.setQueryData(untouched, { id: 'untouched' })
    client.setQueryData(['admin-dashboard', []], { communications: [] })

    await applyRealtimeEvent(client, {
      id: 'event-1', type: 'communication.changed', resource_id: 'changed', change: 'message', occurred_at: new Date().toISOString(),
    })

    expect(client.getQueryState(communicationKeys.summary)?.isInvalidated).toBe(true)
    expect(client.getQueryState(communicationKeys.list({ limit: 30 }))?.isInvalidated).toBe(true)
    expect(client.getQueryState(changed)?.isInvalidated).toBe(true)
    expect(client.getQueryState(untouched)?.isInvalidated).toBe(false)
    expect(client.getQueryState(['admin-dashboard', []])?.isInvalidated).toBe(true)
  })

  it('invalidates all cached communication details during reconnect sync', async () => {
    const client = new QueryClient()
    client.setQueryData(communicationKeys.detail('first'), { id: 'first' })
    client.setQueryData(communicationKeys.detail('second'), { id: 'second' })
    client.setQueryData(['admin-dashboard', ['tag-1']], { communications: [] })

    await synchronizeCommunicationQueries(client)

    expect(client.getQueryState(communicationKeys.detail('first'))?.isInvalidated).toBe(true)
    expect(client.getQueryState(communicationKeys.detail('second'))?.isInvalidated).toBe(true)
    expect(client.getQueryState(['admin-dashboard', ['tag-1']])?.isInvalidated).toBe(true)
  })
})
