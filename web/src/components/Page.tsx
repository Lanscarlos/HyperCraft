import type { ReactNode } from 'react'

interface Props {
  title?: ReactNode
  /** The paragraph under the title: what this page is for. */
  lead?: ReactNode
  /** Facts or actions that belong beside the title — meta chips, a button row. */
  aside?: ReactNode
  /** Anything above the title. A back link, mostly. */
  above?: ReactNode
  /**
   * Tiles and cards rather than prose, so the body takes the width it is given
   * instead of a reading column. Paragraphs stay capped either way.
   */
  wide?: boolean
  /** Optional: a page that is still loading is a head and nothing else. */
  children?: ReactNode
}

/**
 * The frame every panel-wide page sits in.
 *
 * There used to be three of these — one for prose pages, one for the dashboard,
 * one for settings — with three different maximum widths and two different
 * ideas about who owns the scrollbar. A page that picked the wrong one looked
 * almost right, which is the worst way for a mistake to look. Now there is one
 * frame and one decision to make: prose, or tiles.
 */
export function Page({ title, lead, aside, above, wide, children }: Props) {
  const head = above ?? title ?? lead ?? aside

  return (
    <div className={wide ? 'page page--wide' : 'page'}>
      {head !== undefined && (
        <header className="page__head">
          <div>
            {above}
            {title !== undefined && <h1>{title}</h1>}
            {lead !== undefined && <p className="page__lead">{lead}</p>}
          </div>
          {aside}
        </header>
      )}
      {children}
    </div>
  )
}
