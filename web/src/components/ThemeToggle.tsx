import { useLayoutEffect, useState } from 'react'
import { Moon, Sun } from 'lucide-react'

type Theme = 'light' | 'dark'

const themeStorageKey = 'sing-box-webui:theme'

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(readInitialTheme)
  const nextTheme = theme === 'light' ? 'dark' : 'light'
  const label = nextTheme === 'dark' ? '切换到深色主题' : '切换到浅色主题'

  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme
    try {
      window.localStorage.setItem(themeStorageKey, theme)
    } catch {
      // Theme still applies when browser storage is unavailable.
    }
  }, [theme])

  return (
    <button
      type="button"
      className="icon-button theme-toggle"
      aria-label={label}
      aria-pressed={theme === 'dark'}
      title={label}
      onClick={() => setTheme(nextTheme)}
    >
      {theme === 'light' ? <Moon size={17} /> : <Sun size={17} />}
    </button>
  )
}

function readInitialTheme(): Theme {
  // The inline script in index.html has already applied the effective theme
  // before first paint, so prefer the value it resolved.
  if (document.documentElement.dataset.theme === 'dark' || document.documentElement.dataset.theme === 'light') {
    return document.documentElement.dataset.theme
  }
  try {
    const stored = window.localStorage.getItem(themeStorageKey)
    if (stored === 'dark' || stored === 'light') return stored
  } catch {
    // Fall through to the system preference.
  }
  return systemPrefersDark() ? 'dark' : 'light'
}

function systemPrefersDark(): boolean {
  try {
    return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches
  } catch {
    return false
  }
}
