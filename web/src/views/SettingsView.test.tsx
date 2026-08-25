import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { defaultUIPreferences } from '../uiPreferences'
import { SettingsView } from './SettingsView'

describe('SettingsView', () => {
  it('changes sidebar width, navigation order, visibility and page scale', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    const preferences = {
      ...defaultUIPreferences,
      navigationOrder: [...defaultUIPreferences.navigationOrder],
      hiddenNavigation: [],
      viewScales: {},
      fontScales: {},
    }
    render(<SettingsView preferences={preferences} onChange={onChange} />)

    const sidebarWidth = screen.getByRole('slider', { name: '侧边栏宽度' })
    fireEvent.change(sidebarWidth, { target: { value: '276' } })
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.pointerUp(sidebarWidth)
    expect(onChange.mock.calls[0]?.[0].sidebarWidth).toBe(276)

    fireEvent.change(screen.getByRole('slider', { name: '节点 页面缩放' }), { target: { value: '125' } })
    expect(onChange.mock.calls[1]?.[0].viewScales).toEqual({ nodes: 125 })

    const settingsScale = screen.getByRole('slider', { name: '全局设置 页面缩放' })
    fireEvent.change(settingsScale, { target: { value: '115' } })
    fireEvent.pointerUp(settingsScale)
    expect(onChange.mock.calls[2]?.[0].viewScales).toEqual({ settings: 115 })

    fireEvent.change(screen.getByRole('slider', { name: '节点 字体缩放' }), { target: { value: '120' } })
    expect(onChange.mock.calls[3]?.[0].fontScales).toEqual({ nodes: 120 })

    const settingsFontScale = screen.getByRole('slider', { name: '全局设置 字体缩放' })
    fireEvent.change(settingsFontScale, { target: { value: '110' } })
    fireEvent.pointerUp(settingsFontScale)
    expect(onChange.mock.calls[4]?.[0].fontScales).toEqual({ settings: 110 })

    const transferred = new Map<string, string>()
    const dataTransfer = {
      effectAllowed: 'none',
      dropEffect: 'none',
      setData: (type: string, value: string) => transferred.set(type, value),
      getData: (type: string) => transferred.get(type) ?? '',
    }
    fireEvent.dragStart(screen.getByLabelText('连接，拖动排序'), { dataTransfer })
    fireEvent.dragOver(screen.getByLabelText('订阅，拖动排序'), { dataTransfer, clientY: 100 })
    fireEvent.drop(screen.getByLabelText('订阅，拖动排序'), { dataTransfer })
    expect(onChange.mock.calls[5]?.[0].navigationOrder.slice(0, 2)).toEqual(['subscriptions', 'connection'])

    await user.click(screen.getByRole('button', { name: '隐藏 核心' }))
    expect(onChange.mock.calls[6]?.[0].hiddenNavigation).toEqual(['core'])

    await user.click(screen.getByRole('button', { name: '恢复默认' }))
    expect(onChange.mock.calls[7]?.[0]).toMatchObject({ sidebarWidth: 220, hiddenNavigation: [], viewScales: {}, fontScales: {} })
  })
})
