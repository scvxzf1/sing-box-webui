import { useEffect, useState, type DragEvent, type KeyboardEvent } from 'react'
import { Eye, EyeOff, GripVertical, RotateCcw } from 'lucide-react'
import { navigationItems, type ViewName } from '../components/Navigation'
import { PageHeading } from '../components/PageHeading'
import { defaultFontScale, defaultSidebarWidth, defaultUIPreferences, defaultViewScale, fontScale, type ConfigurableViewName, type UIPreferences, viewScale } from '../uiPreferences'

interface SettingsViewProps {
  preferences: UIPreferences
  onChange: (preferences: UIPreferences) => void
}

const labels = new Map<ViewName, string>([
  ...navigationItems.map((item) => [item.id, item.label] as const),
  ['settings', '全局设置'],
])

export function SettingsView({ preferences, onChange }: SettingsViewProps) {
  const hidden = new Set(preferences.hiddenNavigation)
  const [sidebarWidthDraft, setSidebarWidthDraft] = useState(preferences.sidebarWidth)
  const [settingsScaleDraft, setSettingsScaleDraft] = useState(viewScale(preferences, 'settings'))
  const [settingsFontScaleDraft, setSettingsFontScaleDraft] = useState(fontScale(preferences, 'settings'))
  const [draggingView, setDraggingView] = useState<ConfigurableViewName | null>(null)
  const [dropIndicator, setDropIndicator] = useState<{ view: ConfigurableViewName; position: 'before' | 'after' } | null>(null)

  useEffect(() => setSidebarWidthDraft(preferences.sidebarWidth), [preferences.sidebarWidth])
  useEffect(() => setSettingsScaleDraft(viewScale(preferences, 'settings')), [preferences])
  useEffect(() => setSettingsFontScaleDraft(fontScale(preferences, 'settings')), [preferences])

  const moveNavigation = (view: ConfigurableViewName, direction: -1 | 1) => {
    const currentIndex = preferences.navigationOrder.indexOf(view)
    const nextIndex = currentIndex + direction
    if (currentIndex < 0 || nextIndex < 0 || nextIndex >= preferences.navigationOrder.length) return
    const navigationOrder = [...preferences.navigationOrder]
    ;[navigationOrder[currentIndex], navigationOrder[nextIndex]] = [navigationOrder[nextIndex], navigationOrder[currentIndex]]
    onChange({ ...preferences, navigationOrder })
  }

  const handleDragStart = (event: DragEvent<HTMLDivElement>, view: ConfigurableViewName) => {
    setDraggingView(view)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', view)
  }

  const handleDragOver = (event: DragEvent<HTMLDivElement>, view: ConfigurableViewName) => {
    event.preventDefault()
    if (!draggingView || draggingView === view) return
    const bounds = event.currentTarget.getBoundingClientRect()
    setDropIndicator({ view, position: event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after' })
    event.dataTransfer.dropEffect = 'move'
  }

  const handleDrop = (event: DragEvent<HTMLDivElement>, targetView: ConfigurableViewName) => {
    event.preventDefault()
    const sourceView = draggingView ?? event.dataTransfer.getData('text/plain') as ConfigurableViewName
    const indicator = dropIndicator
    setDraggingView(null)
    setDropIndicator(null)
    if (!sourceView || sourceView === targetView || !indicator || indicator.view !== targetView) return
    const navigationOrder = preferences.navigationOrder.filter((view) => view !== sourceView)
    const targetIndex = navigationOrder.indexOf(targetView)
    if (targetIndex < 0) return
    navigationOrder.splice(targetIndex + (indicator.position === 'after' ? 1 : 0), 0, sourceView)
    onChange({ ...preferences, navigationOrder })
  }

  const handleNavigationKeyDown = (event: KeyboardEvent<HTMLDivElement>, view: ConfigurableViewName) => {
    if (event.target !== event.currentTarget || (event.key !== 'ArrowUp' && event.key !== 'ArrowDown')) return
    event.preventDefault()
    moveNavigation(view, event.key === 'ArrowUp' ? -1 : 1)
  }

  const commitSidebarWidth = () => {
    if (sidebarWidthDraft !== preferences.sidebarWidth) onChange({ ...preferences, sidebarWidth: sidebarWidthDraft })
  }

  const commitSettingsScale = () => {
    if (settingsScaleDraft !== viewScale(preferences, 'settings')) {
      onChange({ ...preferences, viewScales: { ...preferences.viewScales, settings: settingsScaleDraft } })
    }
  }

  const commitSettingsFontScale = () => {
    if (settingsFontScaleDraft !== fontScale(preferences, 'settings')) {
      onChange({ ...preferences, fontScales: { ...preferences.fontScales, settings: settingsFontScaleDraft } })
    }
  }

  const toggleHidden = (view: ConfigurableViewName) => {
    const hiddenNavigation = hidden.has(view)
      ? preferences.hiddenNavigation.filter((item) => item !== view)
      : [...preferences.hiddenNavigation, view]
    onChange({ ...preferences, hiddenNavigation })
  }

  return (
    <>
      <PageHeading eyebrow="INTERFACE PREFERENCES" title="全局设置" />
      <div className="global-settings-layout">
        <section className="global-settings-section" aria-labelledby="sidebar-settings-title">
          <div className="global-settings-heading">
            <div><span>布局</span><h2 id="sidebar-settings-title">侧边栏</h2></div>
            <strong>{sidebarWidthDraft}px</strong>
          </div>
          <label className="global-slider-field">
            <span>侧边栏宽度</span>
            <input
              aria-label="侧边栏宽度"
              type="range"
              min="168"
              max="320"
              step="4"
              value={sidebarWidthDraft}
              onChange={(event) => setSidebarWidthDraft(Number(event.target.value))}
              onPointerUp={commitSidebarWidth}
              onKeyUp={commitSidebarWidth}
              onBlur={commitSidebarWidth}
            />
            <output>{sidebarWidthDraft}px</output>
          </label>
        </section>

        <section className="global-settings-section" aria-labelledby="navigation-settings-title">
          <div className="global-settings-heading">
            <div><span>导航</span><h2 id="navigation-settings-title">顺序与显示</h2></div>
            <strong>{navigationItems.length - preferences.hiddenNavigation.length}/{navigationItems.length} 可见</strong>
          </div>
          <div className="navigation-settings-list">
            {preferences.navigationOrder.map((view, index) => (
              <div
                className={`navigation-settings-row ${hidden.has(view) ? 'navigation-settings-row--hidden' : ''} ${draggingView === view ? 'navigation-settings-row--dragging' : ''} ${dropIndicator?.view === view ? `navigation-settings-row--drop-${dropIndicator.position}` : ''}`}
                key={view}
                draggable
                tabIndex={0}
                aria-label={`${labels.get(view)}，拖动排序`}
                aria-grabbed={draggingView === view}
                onDragStart={(event) => handleDragStart(event, view)}
                onDragOver={(event) => handleDragOver(event, view)}
                onDrop={(event) => handleDrop(event, view)}
                onDragEnd={() => { setDraggingView(null); setDropIndicator(null) }}
                onKeyDown={(event) => handleNavigationKeyDown(event, view)}
              >
                <GripVertical className="drag-handle" size={15} aria-hidden="true" />
                <span className="navigation-order-index">{String(index + 1).padStart(2, '0')}</span>
                <strong>{labels.get(view) ?? view}</strong>
                <button className="icon-button" type="button" title={hidden.has(view) ? '显示' : '隐藏'} aria-label={`${hidden.has(view) ? '显示' : '隐藏'} ${labels.get(view)}`} onClick={() => toggleHidden(view)}>
                  {hidden.has(view) ? <EyeOff size={15} /> : <Eye size={15} />}
                </button>
              </div>
            ))}
          </div>
        </section>

        <section className="global-settings-section global-settings-section--wide" aria-labelledby="scale-settings-title">
          <div className="global-settings-heading">
            <div><span>显示</span><h2 id="scale-settings-title">页面缩放/字体缩放</h2></div>
            <strong>页面与字体 75% - 130%</strong>
          </div>
          <div className="view-scale-grid">
            {[...preferences.navigationOrder, 'settings' as const].map((view) => {
              const scale = view === 'settings' ? settingsScaleDraft : viewScale(preferences, view)
              const textScale = view === 'settings' ? settingsFontScaleDraft : fontScale(preferences, view)
              return (
                <div className="view-scale-item" key={view}>
                  <strong>{labels.get(view) ?? view}</strong>
                  <label className="global-slider-field">
                    <span>页面</span>
                    <input
                      aria-label={`${labels.get(view)} 页面缩放`}
                      type="range"
                      min="75"
                      max="130"
                      step="5"
                      value={scale}
                      onChange={(event) => {
                        const value = Number(event.target.value)
                        if (view === 'settings') setSettingsScaleDraft(value)
                        else onChange({ ...preferences, viewScales: { ...preferences.viewScales, [view]: value } })
                      }}
                      onPointerUp={view === 'settings' ? commitSettingsScale : undefined}
                      onKeyUp={view === 'settings' ? commitSettingsScale : undefined}
                      onBlur={view === 'settings' ? commitSettingsScale : undefined}
                    />
                    <output>{scale}%</output>
                  </label>
                  <label className="global-slider-field global-slider-field--font">
                    <span>字体</span>
                    <input
                      aria-label={`${labels.get(view)} 字体缩放`}
                      type="range"
                      min="75"
                      max="130"
                      step="5"
                      value={textScale}
                      onChange={(event) => {
                        const value = Number(event.target.value)
                        if (view === 'settings') setSettingsFontScaleDraft(value)
                        else onChange({ ...preferences, fontScales: { ...preferences.fontScales, [view]: value } })
                      }}
                      onPointerUp={view === 'settings' ? commitSettingsFontScale : undefined}
                      onKeyUp={view === 'settings' ? commitSettingsFontScale : undefined}
                      onBlur={view === 'settings' ? commitSettingsFontScale : undefined}
                    />
                    <output>{textScale}%</output>
                  </label>
                </div>
              )
            })}
          </div>
        </section>

        <div className="global-settings-footer">
          <button
            className="button"
            type="button"
            onClick={() => onChange({
              ...defaultUIPreferences,
              sidebarWidth: defaultSidebarWidth,
              navigationOrder: [...defaultUIPreferences.navigationOrder],
              hiddenNavigation: [],
              viewScales: {},
              fontScales: {},
            })}
          >
            <RotateCcw size={16} aria-hidden="true" />恢复默认
          </button>
          <span>默认页面缩放 {defaultViewScale}% · 默认字体缩放 {defaultFontScale}%</span>
        </div>
      </div>
    </>
  )
}
