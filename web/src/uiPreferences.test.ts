import { beforeEach, describe, expect, it } from 'vitest'
import { configurableViewNames, defaultUIPreferences, readUIPreferences, saveUIPreferences, uiPreferencesStorageKey } from './uiPreferences'

describe('UI preferences', () => {
  beforeEach(() => window.localStorage.clear())

  it('persists sidebar, navigation and per-view scale preferences', () => {
    saveUIPreferences({
      ...defaultUIPreferences,
      sidebarWidth: 284,
      navigationOrder: ['nodes', ...configurableViewNames.filter((view) => view !== 'nodes')],
      hiddenNavigation: ['core'],
      viewScales: { nodes: 125, connection: 85, settings: 115 },
      fontScales: { nodes: 120, settings: 110 },
    })

    const restored = readUIPreferences()
    expect(restored).toMatchObject({
      sidebarWidth: 284,
      hiddenNavigation: ['core'],
      viewScales: { nodes: 125, connection: 85, settings: 115 },
      fontScales: { nodes: 120, settings: 110 },
    })
    expect(restored.navigationOrder[0]).toBe('nodes')
  })

  it('repairs malformed and incomplete stored preferences', () => {
    window.localStorage.setItem(uiPreferencesStorageKey, JSON.stringify({
      sidebarWidth: 999,
      navigationOrder: ['nodes', 'nodes', 'unknown'],
      hiddenNavigation: ['unknown', 'core', 'core'],
      viewScales: { nodes: 51, core: 142, settings: 128 },
      fontScales: { nodes: 72, core: 144, settings: 117 },
    }))

    const preferences = readUIPreferences()
    expect(preferences.sidebarWidth).toBe(320)
    expect(preferences.navigationOrder[0]).toBe('nodes')
    expect(preferences.navigationOrder).toHaveLength(configurableViewNames.length)
    expect(preferences.hiddenNavigation).toEqual(['core'])
    expect(preferences.viewScales).toMatchObject({ nodes: 75, core: 130, settings: 130 })
    expect(preferences.fontScales).toMatchObject({ nodes: 75, core: 130, settings: 115 })
  })
})
