import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

export type EventStreamState = 'connecting' | 'connected' | 'error'

export function useEventStream(url: string): EventStreamState {
  const [state, setState] = useState<EventStreamState>('connecting')
  const queryClient = useQueryClient()

  useEffect(() => {
    const stream = new EventSource(url)
    setState('connecting')

    stream.onopen = () => setState('connected')
    stream.onerror = () => setState('error')

    const refreshSubscriptionState = () => {
      void queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
      void queryClient.invalidateQueries({ queryKey: ['subscription'] })
      void queryClient.invalidateQueries({ queryKey: ['pools'] })
    }
    const subscriptionEvents = ['subscription.updated', 'subscription.failed', 'subscription.activated', 'subscriptions.reordered', 'node.selected']
    for (const eventType of subscriptionEvents) stream.addEventListener(eventType, refreshSubscriptionState)

    return () => {
      for (const eventType of subscriptionEvents) stream.removeEventListener(eventType, refreshSubscriptionState)
      stream.close()
    }
  }, [queryClient, url])

  return state
}
