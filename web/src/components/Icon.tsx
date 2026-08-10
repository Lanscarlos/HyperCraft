/**
 * The panel's icon set, inline rather than a font or a sprite.
 *
 * There are a couple of dozen and they are only ever painted in `currentColor`,
 * so a dependency — or a second network request — would buy nothing. They exist
 * for the collapsed sidebar: a 60px rail has no room for a label, and a row of
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
  | 'instances'
  | 'host'
  | 'files'
  | 'properties'
  | 'chart'
  | 'disk'
  | 'lock'
  | 'search'
  | 'back'
  | 'devices'
  | 'update'
  | 'github'

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
  // Stacked rack units: several servers, as opposed to the one you are in.
  instances: (
    <>
      <rect x="3.5" y="4" width="17" height="6" rx="2" />
      <rect x="3.5" y="14" width="17" height="6" rx="2" />
      <path d="M7 7h.01M7 17h.01" />
    </>
  ),
  // One box with a pedestal: the machine everything else runs on.
  host: (
    <>
      <rect x="3.5" y="3.5" width="17" height="12" rx="2" />
      <path d="M8 19.5h8M12 15.5v4" />
    </>
  ),
  files: (
    <>
      <path d="M4 6.5A2 2 0 0 1 6 4.5h3.4l1.8 2.2H18a2 2 0 0 1 2 2v8.8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6.5Z" />
    </>
  ),
  // A form: the key/value editor behind server.properties.
  properties: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M7.5 9h4M7.5 13h4M15 9h1.5M15 13h1.5M7.5 17h9" />
    </>
  ),
  chart: (
    <>
      <path d="M4 19.5V4.5M4 19.5h16" />
      <path d="m7 15 3.5-4.5 3 3L20 7" />
    </>
  ),
  disk: (
    <>
      <ellipse cx="12" cy="6.5" rx="7.5" ry="3" />
      <path d="M4.5 6.5v11c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-11" />
      <path d="M4.5 12c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3" />
    </>
  ),
  lock: (
    <>
      <rect x="4.5" y="10.5" width="15" height="9.5" rx="2" />
      <path d="M8 10.5V7.75a4 4 0 0 1 8 0v2.75" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6" />
      <path d="m15 15 4.5 4.5" />
    </>
  ),
  back: <path d="M20 12H4.5m0 0L10 6.5M4.5 12 10 17.5" />,
  // A laptop with a phone beside it: the things that have been paired, which
  // is what that page is a list of.
  devices: (
    <>
      <path d="M3 5.5h11v8H3z" />
      <path d="M1.5 17h14" />
      <rect x="17" y="8" width="5.5" height="9" rx="1.4" />
    </>
  ),
  // An arrow coming down into the panel itself, rather than a circular refresh:
  // this replaces the binary on disk, it does not reload anything.
  update: (
    <>
      <path d="M12 3.5v11m0 0 4-4m-4 4-4-4" />
      <path d="M4.5 17.5v1a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-1" />
    </>
  ),
  // A branch off a trunk, rather than the Octocat: the mark is a filled shape
  // and everything else here is one stroke weight in currentColor, so a silhouette
  // would read as a smudge at 18px beside the others.
  github: (
    <>
      <circle cx="6.5" cy="6" r="2.5" />
      <circle cx="6.5" cy="18" r="2.5" />
      <circle cx="17.5" cy="12" r="2.5" />
      <path d="M6.5 8.5v7" />
      <path d="M15 12h-2.5a6 6 0 0 1-6-6" />
    </>
  ),
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
