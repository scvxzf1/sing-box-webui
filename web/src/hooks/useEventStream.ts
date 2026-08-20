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
    const refreshRules = () => {
      void queryClient.invalidateQueries({ queryKey: ['rules'] })
      void queryClient.invalidateQueries({ queryKey: ['rule-pools'] })
    }
    const refreshDNS = () => void queryClient.invalidateQueries({ queryKey: ['dns-profile'] })
    const refreshRuntime = () => {
      void queryClient.invalidateQueries({ queryKey: ['runtime'] })
      void queryClient.invalidateQueries({ queryKey: ['status'] })
      void queryClient.invalidateQueries({ queryKey: ['links'] })
    }
    const refreshAll = () => void queryClient.invalidateQueries()
    const subscriptionEvents = ['subscription.updated', 'subscription.failed', 'subscription.deleted', 'subscription.activated', 'subscriptions.reordered', 'node.selected']
    const ruleEvents = ['rule.created', 'rule.updated', 'rule.deleted', 'rules.reordered', 'rule-pool.created', 'rule-pool.updated', 'rule-pool.deleted', 'rule-pools.reordered', 'subscription.rules.synced', 'subscription.rules.reconciled']
    const runtimeEvents = ['runtime.applied', 'runtime.stopped', 'runtime.failed']
    for (const eventType of subscriptionEvents) stream.addEventListener(eventType, refreshSubscriptionState)
    for (const eventType of ruleEvents) stream.addEventListener(eventType, refreshRules)
    for (const eventType of runtimeEvents) stream.addEventListener(eventType, refreshRuntime)
    stream.addEventListener('dns-profile.updated', refreshDNS)
    stream.addEventListener('snapshot', refreshAll)

    return () => {
      for (const eventType of subscriptionEvents) stream.removeEventListener(eventType, refreshSubscriptionState)
      for (const eventType of ruleEvents) stream.removeEventListener(eventType, refreshRules)
      for (const eventType of runtimeEvents) stream.removeEventListener(eventType, refreshRuntime)
      stream.removeEventListener('dns-profile.updated', refreshDNS)
      stream.removeEventListener('snapshot', refreshAll)
      stream.close()
    }
  }, [queryClient, url])

  return state
}
