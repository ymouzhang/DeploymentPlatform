import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { communicationKeys } from '../features/communications/queryKeys'

export interface RealtimeEvent {
  id: string
  type: 'communication.changed'
  resource_id: string
  change: 'created' | 'message' | 'read' | 'closed' | 'reopened'
  occurred_at: string
}

export async function applyRealtimeEvent(client: QueryClient, event: RealtimeEvent) {
  await Promise.all([
    client.invalidateQueries({ queryKey: communicationKeys.summary }),
    client.invalidateQueries({ queryKey: communicationKeys.lists }),
    client.invalidateQueries({ queryKey: communicationKeys.detail(event.resource_id), exact: true }),
    client.invalidateQueries({ queryKey: ['admin-dashboard'] }),
  ])
}

export async function synchronizeCommunicationQueries(client: QueryClient) {
  await Promise.all([
    client.invalidateQueries({ queryKey: communicationKeys.summary }),
    client.invalidateQueries({ queryKey: communicationKeys.lists }),
    client.invalidateQueries({ queryKey: communicationKeys.details }),
    client.invalidateQueries({ queryKey: ['admin-dashboard'] }),
  ])
}

export function RealtimeEvents() {
  const client = useQueryClient()

  useEffect(() => {
    const source = new EventSource('/api/v1/events')
    const synchronize = () => { void synchronizeCommunicationQueries(client) }
    const changed = (raw: Event) => {
      try {
        const event = JSON.parse((raw as MessageEvent<string>).data) as RealtimeEvent
        if (event.type === 'communication.changed' && event.resource_id) {
          void applyRealtimeEvent(client, event)
        }
      } catch {
        synchronize()
      }
    }
    const visible = () => {
      if (document.visibilityState === 'visible') synchronize()
    }
    source.addEventListener('open', synchronize)
    source.addEventListener('sync', synchronize)
    source.addEventListener('communication.changed', changed)
    document.addEventListener('visibilitychange', visible)
    return () => {
      document.removeEventListener('visibilitychange', visible)
      source.close()
    }
  }, [client])

  return null
}
