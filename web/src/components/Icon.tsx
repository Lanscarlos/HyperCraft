/**
 * The panel's icon set, inline rather than a font or a sprite.
 *
 * There are nine of them and they are only ever painted in `currentColor`, so a
 * dependency — or a second network request — would buy nothing. They exist for
 * the collapsed sidebar: a 56px rail has no room for a label, and a row of
 * identical dots is not navigation.
 */
import type { ReactElement } from 'react'

export type IconName =
  | 'dashboard'
  | 'java'
  | 'cores'
  | 'plugins'
  | 'terminal'
  | 'settings'
  | 'menu'
  | 'collapse'
  | 'expand'

const PATHS: Record<IconName, ReactElement> = {
  dashboard: (
    <>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.6" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.6" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.6" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.6" />
    </>
  ),
  // A cup, for the runtime you install rather than the language you write.
  java: (
    <>
      <path d="M4.5 8.5h11v6a4.5 4.5 0 0 1-4.5 4.5H9a4.5 4.5 0 0 1-4.5-4.5v-6Z" />
      <path d="M15.5 10h2a2.5 2.5 0 0 1 0 5h-2" />
      <path d="M8 2.5v2.5M12 2.5v2.5" />
    </>
  ),
  cores: (
    <>
      <path d="M12 3 20 7.5v9L12 21l-8-4.5v-9L12 3Z" />
      <path d="m4 7.5 8 4.5 8-4.5M12 12v9" />
    </>
  ),
  // A block with a socket bitten out of its edge: a thing that plugs in.
  plugins: (
    <>
      <rect x="4" y="4" width="16" height="16" rx="3" />
      <path d="M4 9.5h2a2.5 2.5 0 0 1 0 5H4" />
    </>
  ),
  terminal: (
    <>
      <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
      <path d="m7.5 10 2.5 2.5-2.5 2.5M13 15h3.5" />
    </>
  ),
  // Sliders rather than a gear: the page behind it is four switches.
  settings: (
    <>
      <path d="M3.5 8h9M17 8h3.5M3.5 16h3.5M11.5 16h9" />
      <circle cx="14.75" cy="8" r="2.25" />
      <circle cx="9.25" cy="16" r="2.25" />
    </>
  ),
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  collapse: <path d="m14 6-6 6 6 6" />,
  expand: <path d="m10 6 6 6-6 6" />,
}

export function Icon({ name, className }: { name: IconName; className?: string }) {
  return (
    <svg
      className={className ? `icon ${className}` : 'icon'}
      viewBox="0 0 24 24"
      width="18"
      height="18"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {PATHS[name]}
    </svg>
  )
}
