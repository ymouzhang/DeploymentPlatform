// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { communicationKeys } from '../features/communications/queryKeys'
import { RealtimeEvents } from './RealtimeEvents'

afterEach(() => {
  vi.unstubAllGlobals()
  FakeEventSource.instances = []
})

describe('RealtimeEvents connection', () => {
  it('invalidates the changed thread and closes the account stream on unmount', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const client = new QueryClient()
    const detailKey = communicationKeys.detail('thread-1')
    client.setQueryData(detailKey, { id: 'thread-1' })
    const view = render(<QueryClientProvider client={client}><RealtimeEvents /></QueryClientProvider>)
    const source = FakeEventSource.instances[0]
    expect(source.url).toBe('/api/v1/events')

    source.emit('communication.changed', {
      id: 'event-1', type: 'communication.changed', resource_id: 'thread-1', change: 'message', occurred_at: new Date().toISOString(),
    })

    await waitFor(() => expect(client.getQueryState(detailKey)?.isInvalidated).toBe(true))
    view.unmount()
    expect(source.closed).toBe(true)
  })
})

class FakeEventSource {
  static instances: FakeEventSource[] = []
  readonly url: string
  closed = false
  private listeners = new Map<string, Set<(event: Event) => void>>()

  constructor(url: string | URL) {
    this.url = String(url)
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject | null) {
    if (!listener) return
    const callback = typeof listener === 'function' ? listener : (event: Event) => listener.handleEvent(event)
    const listeners = this.listeners.get(type) ?? new Set<(event: Event) => void>()
    listeners.add(callback)
    this.listeners.set(type, listeners)
  }

  emit(type: string, data: object) {
    const event = new MessageEvent(type, { data: JSON.stringify(data) })
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }

  close() { this.closed = true }
}
