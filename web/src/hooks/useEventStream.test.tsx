import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useEventStream } from './useEventStream'

class FakeEventSource {
  static current: FakeEventSource | null = null
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  private listeners = new Map<string, Set<EventListener>>()

  constructor(_url: string | URL) {
    FakeEventSource.current = this
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const callback = typeof listener === 'function' ? listener : listener.handleEvent.bind(listener)
    const listeners = this.listeners.get(type) ?? new Set<EventListener>()
    listeners.add(callback)
    this.listeners.set(type, listeners)
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    if (typeof listener === 'function') this.listeners.get(type)?.delete(listener)
  }

  close() {
    this.closed = true
  }

  emit(type: string) {
    for (const listener of this.listeners.get(type) ?? []) listener(new Event(type))
  }
}

function EventStreamHarness() {
  useEventStream('/api/v1/events')
  return null
}

describe('useEventStream', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    FakeEventSource.current = null
  })

  it('invalidates domain caches and uses snapshots as a full recovery point', async () => {
    vi.stubGlobal('EventSource', FakeEventSource)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue(undefined)
    const rendered = render(<QueryClientProvider client={client}><EventStreamHarness /></QueryClientProvider>)

    await waitFor(() => expect(FakeEventSource.current).not.toBeNull())
    FakeEventSource.current?.emit('rule.updated')
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['rules'] })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['rule-pools'] })

    FakeEventSource.current?.emit('dns-profile.updated')
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['dns-profile'] })

    FakeEventSource.current?.emit('snapshot')
    expect(invalidate).toHaveBeenCalledWith()

    const stream = FakeEventSource.current
    rendered.unmount()
    expect(stream?.closed).toBe(true)
  })
})
