import { useEffect, useState } from 'react'

import { applyPref, current, onThemeChange, readPref, type ThemePref } from '../theme'

/** Three states in a fixed cycle rather than a switch: "跟随系统" is a real
 *  choice and has to be reachable, and a menu for three options is more
 *  chrome than a footer row can afford. */
const ORDER: ThemePref[] = ['system', 'light', 'dark']

const LABELS: Record<ThemePref, { icon: string; title: string }> = {
  system: { icon: '◐', title: '主题：跟随系统' },
  light: { icon: '☀', title: '主题：浅色' },
  dark: { icon: '☾', title: '主题：深色' },
}

export function ThemeToggle() {
  const [pref, setPref] = useState<ThemePref>(readPref)
  // Only so the button re-renders when the system flips under 跟随系统; the
  // mode itself lives on <html>, not in React.
  const [, setTheme] = useState(current)

  useEffect(() => onThemeChange(setTheme), [])

  const next = ORDER[(ORDER.indexOf(pref) + 1) % ORDER.length]
  const label = LABELS[pref]

  return (
    <button
      className="theme-toggle"
      title={`${label.title}（点击切换为${LABELS[next].title.slice(3)}）`}
      aria-label={label.title}
      onClick={() => {
        setPref(next)
        applyPref(next)
      }}
    >
      {label.icon}
    </button>
  )
}
