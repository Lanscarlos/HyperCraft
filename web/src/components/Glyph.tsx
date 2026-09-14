import type { ReactElement } from 'react'

/**
 * The line icons the file pane draws with.
 *
 * Separate from components/Icon because these are about file types rather than
 * navigation, and drawn on the same 24px grid with the same stroke so a row of
 * them lines up with the rest of the panel.
 *
 * Lifted out of FileManager so the tree can use the same set: in edit mode the
 * tree shows files, and a file drawn with one icon in the listing and another
 * in the tree is two answers to the same question.
 */

export type GlyphName =
  | 'cube'
  | 'up'
  | 'left'
  | 'clock'
  | 'split'
  | 'ellipsis'
  | 'chevron'
  | 'home'
  | 'upload'
  | 'download'
  | 'refresh'
  | 'search'
  | 'rename'
  | 'trash'
  | 'new-folder'
  | 'new-file'
  | 'folder'
  | 'folder-open'
  | 'doc'
  | 'config'
  | 'image'
  | 'archive'
  | 'jar'
  | 'script'
  | 'data'

const GLYPHS: Record<GlyphName, ReactElement> = {
  up: <path d="M12 20V5m0 0-6 6m6-6 6 6" />,
  home: <path d="M4 10.5 12 4l8 6.5V19a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 19v-8.5Z" />,
  upload: (
    <>
      <path d="M12 16V4m0 0-4.5 4.5M12 4l4.5 4.5" />
      <path d="M4.5 15v3.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V15" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v12m0 0-4.5-4.5M12 16l4.5-4.5" />
      <path d="M4.5 16v2.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V16" />
    </>
  ),
  refresh: (
    <>
      <path d="M20 12a8 8 0 1 1-2.6-5.9" />
      <path d="M20 4v4.5h-4.5" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6" />
      <path d="m15 15 4.5 4.5" />
    </>
  ),
  // A pencil: renaming is writing on the thing, not moving it.
  rename: (
    <>
      <path d="M4.5 19.5h4L20 8a2.1 2.1 0 0 0-3-3L5.5 16.5l-1 3Z" />
      <path d="m14.5 6.5 3 3" />
    </>
  ),
  trash: (
    <>
      <path d="M4.5 6.5h15M9.5 6.5V5a1.5 1.5 0 0 1 1.5-1.5h2A1.5 1.5 0 0 1 14.5 5v1.5" />
      <path d="M6.5 6.5 7.5 20a1.5 1.5 0 0 0 1.5 1.4h6a1.5 1.5 0 0 0 1.5-1.4l1-13.5" />
      <path d="M10.5 10.5v7M13.5 10.5v7" />
    </>
  ),
  'new-folder': (
    <>
      <path d="M4 6.5a2 2 0 0 1 2-2h3.4l1.8 2.2H18a2 2 0 0 1 2 2v3" />
      <path d="M4 6.5v11a2 2 0 0 0 2 2h7" />
      <path d="M17.5 15v6M14.5 18h6" />
    </>
  ),
  'new-file': (
    <>
      <path d="M13.5 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h4" />
      <path d="M13.5 3.5 19 9v3" />
      <path d="M18 15v6M15 18h6" />
    </>
  ),
  folder: <path d="M4 6.5A2 2 0 0 1 6 4.5h3.4l1.8 2.2H18a2 2 0 0 1 2 2v8.8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6.5Z" />,
  'folder-open': (
    <>
      <path d="M4 18.5V6.5a2 2 0 0 1 2-2h3.4l1.8 2.2H18a2 2 0 0 1 2 2v1.8" />
      <path d="M4 18.5 6.4 11h15.1l-2.4 7.5H4Z" />
    </>
  ),
  doc: (
    <>
      <path d="M13.5 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9l-5.5-5.5Z" />
      <path d="M13.5 3.5V9H19" />
      <path d="M8.5 13.5h7M8.5 17h4.5" />
    </>
  ),
  // Sliders, the same shape the settings page uses: a file of knobs.
  config: (
    <>
      <path d="M3.5 8h8M16 8h4.5M3.5 16h4.5M12.5 16h8" />
      <circle cx="13.75" cy="8" r="2.25" />
      <circle cx="10.25" cy="16" r="2.25" />
    </>
  ),
  image: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <circle cx="9" cy="9.75" r="1.6" />
      <path d="m4.5 17.5 4.75-4.75 3.25 3.25 2.75-2.5 4.75 4" />
    </>
  ),
  archive: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M3.5 9.5h17M10.25 13h3.5" />
    </>
  ),
  // The cube the core library uses: a jar is a packaged build.
  jar: (
    <>
      <path d="M12 3 20 7.5v9L12 21l-8-4.5v-9L12 3Z" />
      <path d="m4 7.5 8 4.5 8-4.5M12 12v9" />
    </>
  ),
  script: (
    <>
      <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
      <path d="m7.5 10 2.5 2.5-2.5 2.5M13 15h3.5" />
    </>
  ),
  data: (
    <>
      <ellipse cx="12" cy="6.5" rx="7.5" ry="3" />
      <path d="M4.5 6.5v11c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-11" />
      <path d="M4.5 12c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3" />
    </>
  ),
  // An isometric block, the same projection the preview draws in.
  cube: (
    <>
      <path d="M12 3.2 20.5 8v8L12 20.8 3.5 16V8Z" />
      <path d="M3.5 8 12 12.6 20.5 8" />
      <path d="M12 12.6v8.2" />
    </>
  ),
  left: <path d="M20 12H5m0 0 6-6m-6 6 6 6" />,
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.4V12l3.2 1.9" />
    </>
  ),
  split: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M12 4.5v15" />
    </>
  ),
  // The one glyph in the set that is filled rather than stroked: three dots
  // drawn as rings at this size read as three tiny doughnuts. Filling them is
  // a property of the shape, so it is set here and not by a caller.
  ellipsis: (
    <>
      <circle cx="5.5" cy="12" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="18.5" cy="12" r="1.5" fill="currentColor" stroke="none" />
    </>
  ),
  chevron: <path d="m6 9.5 6 6 6-6" />,
}

/**
 * The file manager's own icon set.
 *
 * Separate from components/Icon because these are about file types rather than
 * navigation, and drawn on the same 24px grid with the same stroke so a row of
 * them lines up with the rest of the panel.
 */
export function Glyph({ name, className }: { name: GlyphName; className?: string }) {
  return (
    <svg
      className={className ? `icon ${className}` : 'icon'}
      viewBox="0 0 24 24"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {GLYPHS[name]}
    </svg>
  )
}
