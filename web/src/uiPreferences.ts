import type { ViewName } from './components/Navigation'

export type ConfigurableViewName = Exclude<ViewName, 'settings'>
export type ScalableViewName = ViewName

export const uiPreferencesStorageKey = 'sing-box-webui:ui-preferences-v1'
export const defaultSidebarWidth = 220
export const defaultViewScale = 100
export const defaultFontScale = 100

export const configurableViewNames: ConfigurableViewName[] = [
  'connection',
  'subscriptions',
  'links',
  'nodes',
  'pools',
  'chains',
  'channels',
  'rules',
  'traffic',
  'dns',
  'core',
  'overview',
]

export const scalableViewNames: ScalableViewName[] = [...configurableViewNames, 'settings']

export interface UIPreferences {
  sidebarWidth: number
  navigationOrder: ConfigurableViewName[]
  hiddenNavigation: ConfigurableViewName[]
  viewScales: Partial<Record<ScalableViewName, number>>
  fontScales: Partial<Record<ScalableViewName, number>>
}

export const defaultUIPreferences: UIPreferences = {
  sidebarWidth: defaultSidebarWidth,
  navigationOrder: [...configurableViewNames],
  hiddenNavigation: [],
  viewScales: {},
  fontScales: {},
}

export function readUIPreferences(): UIPreferences {
  try {
    const raw = window.localStorage.getItem(uiPreferencesStorageKey)
    if (!raw) return cloneDefaultPreferences()
    const parsed = JSON.parse(raw) as Partial<UIPreferences>
    const known = new Set(configurableViewNames)
    const storedOrder = Array.isArray(parsed.navigationOrder)
      ? parsed.navigationOrder.filter((view): view is ConfigurableViewName => typeof view === 'string' && known.has(view as ConfigurableViewName))
      : []
    const navigationOrder = [...new Set([...storedOrder, ...configurableViewNames])]
    const hiddenNavigation = Array.isArray(parsed.hiddenNavigation)
      ? [...new Set(parsed.hiddenNavigation.filter((view): view is ConfigurableViewName => typeof view === 'string' && known.has(view as ConfigurableViewName)))]
      : []
    const viewScales: Partial<Record<ScalableViewName, number>> = {}
    const fontScales: Partial<Record<ScalableViewName, number>> = {}
    if (parsed.viewScales && typeof parsed.viewScales === 'object') {
      for (const view of scalableViewNames) {
        const value = parsed.viewScales[view]
        if (typeof value === 'number' && Number.isFinite(value)) viewScales[view] = clampScale(value)
      }
    }
    if (parsed.fontScales && typeof parsed.fontScales === 'object') {
      for (const view of scalableViewNames) {
        const value = parsed.fontScales[view]
        if (typeof value === 'number' && Number.isFinite(value)) fontScales[view] = clampScale(value)
      }
    }
    return {
      sidebarWidth: clampSidebarWidth(parsed.sidebarWidth),
      navigationOrder,
      hiddenNavigation,
      viewScales,
      fontScales,
    }
  } catch {
    return cloneDefaultPreferences()
  }
}

export function saveUIPreferences(preferences: UIPreferences) {
  window.localStorage.setItem(uiPreferencesStorageKey, JSON.stringify(preferences))
}

export function viewScale(preferences: UIPreferences, view: ScalableViewName) {
  return preferences.viewScales[view] ?? defaultViewScale
}

export function fontScale(preferences: UIPreferences, view: ScalableViewName) {
  return preferences.fontScales[view] ?? defaultFontScale
}

function cloneDefaultPreferences(): UIPreferences {
  return {
    ...defaultUIPreferences,
    navigationOrder: [...defaultUIPreferences.navigationOrder],
    hiddenNavigation: [],
    viewScales: {},
    fontScales: {},
  }
}

function clampSidebarWidth(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? Math.min(320, Math.max(168, Math.round(value))) : defaultSidebarWidth
}

function clampScale(value: number) {
  return Math.min(130, Math.max(75, Math.round(value / 5) * 5))
}
