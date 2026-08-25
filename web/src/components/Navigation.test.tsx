import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Navigation } from './Navigation'

describe('Navigation', () => {
  it('applies custom order and hidden items while keeping settings available', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<Navigation active="nodes" onChange={onChange} order={['nodes', 'connection', 'core']} hidden={['connection']} />)

    const buttons = screen.getAllByRole('button')
    expect(buttons.map((button) => button.textContent?.trim())).toEqual(['节点', '核心', '全局设置'])
    await user.click(screen.getByRole('button', { name: '全局设置' }))
    expect(onChange).toHaveBeenCalledWith('settings')
  })
})
