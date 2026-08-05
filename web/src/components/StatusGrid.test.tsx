import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { StatusGrid } from './StatusGrid'
import type { StatusResponse } from '../api/types'

const status: StatusResponse = {
  service: 'sing-box-webui',
  version: 'test',
  timestamp: '2026-08-05T00:00:00Z',
  components: {
    web: { state: 'healthy' },
    core: { state: 'unavailable', detail: 'Core is not configured' },
    singBox: { state: 'unavailable', detail: 'sing-box is not attached' },
  },
}

describe('StatusGrid', () => {
  it('renders backend and event stream state', () => {
    render(<StatusGrid status={status} eventStream="connected" />)

    expect(screen.getByText('Web API')).toBeInTheDocument()
    expect(screen.getByText('版本 test')).toBeInTheDocument()
    expect(screen.getByText('已连接')).toBeInTheDocument()
    expect(screen.getByText('未配置')).toBeInTheDocument()
  })
})
