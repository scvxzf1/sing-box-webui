import { useLayoutEffect, useState } from 'react'
import { Moon, Sun } from 'lucide-react'

type Theme = 'light' | 'dark'

const themeStorageKey = 'sing-box-webui:theme'

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(readStoredTheme)
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

function readStoredTheme(): Theme {
  try {
    return window.localStorage.getItem(themeStorageKey) === 'dark' ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}
