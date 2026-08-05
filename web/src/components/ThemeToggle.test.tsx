import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { ThemeToggle } from './ThemeToggle'

describe('ThemeToggle', () => {
  beforeEach(() => {
    window.localStorage.clear()
    delete document.documentElement.dataset.theme
  })

  it('switches theme and persists the selection', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<ThemeToggle />)

    expect(document.documentElement.dataset.theme).toBe('light')
    await user.click(screen.getByRole('button', { name: '切换到深色主题' }))
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(window.localStorage.getItem('sing-box-webui:theme')).toBe('dark')

    unmount()
    render(<ThemeToggle />)
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(screen.getByRole('button', { name: '切换到浅色主题' })).toHaveAttribute('aria-pressed', 'true')
  })
})
