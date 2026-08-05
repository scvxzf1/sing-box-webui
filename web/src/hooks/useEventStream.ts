import { useEffect, useState } from 'react'

export type EventStreamState = 'connecting' | 'connected' | 'error'

export function useEventStream(url: string): EventStreamState {
  const [state, setState] = useState<EventStreamState>('connecting')

  useEffect(() => {
    const stream = new EventSource(url)
    setState('connecting')

    stream.onopen = () => setState('connected')
    stream.onerror = () => setState('error')

    return () => stream.close()
  }, [url])

  return state
}
