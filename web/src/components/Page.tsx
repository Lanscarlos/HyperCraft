import type { ReactNode } from 'react'

interface HeadProps {
  title?: ReactNode
  /** The paragraph under the title: what this page is for. */
  lead?: ReactNode
  /** Facts or actions that belong beside the title — meta chips, a button row. */
  aside?: ReactNode
  /** Anything above the title. A back link, mostly. */
  above?: ReactNode
}

interface Props extends HeadProps {
  /**
   * Tiles and cards rather than prose, so the body takes the width it is given
   * instead of a reading column. Paragraphs stay capped either way.
   */
  wide?: boolean
  /**
   * A full-bleed workspace: one canvas that takes every pixel it is given,
   * rather than a column of content to read. The file pane's edit mode is the
   * only caller today. Content pages must not use it — prose has
   * --content-max and tiles have --content-max-wide, and those two are still
   * the whole of the choice for anything you read rather than work in.
   */
  full?: boolean
  /** Optional: a page that is still loading is a head and nothing else. */
  children?: ReactNode
}

/**
 * The head alone: title on the left, the page's own facts or buttons on the
 * right, a lead under the title.
 *
 * Exported on its own for the instance sections, which are not pages — the
 * pane around them owns the scroll and the width — but have to *open* like
 * one. They used to open eight different ways: the console with the server's
 * name, 监控 with a row of filter chips, 文件 with a card, 插件 with a
 * sixteen-pixel heading and a button row, and so on. Read one after another,
 * as they are, they looked like eight panels by eight authors. One head, in
 * the same place every other page in the panel keeps it, is the fix.
 */
export function PageHead({ title, lead, aside, above }: HeadProps) {
  return (
    <header className="page__head">
      <div>
        {above}
        {title !== undefined && <h1>{title}</h1>}
        {lead !== undefined && <p className="page__lead">{lead}</p>}
      </div>
      {aside}
    </header>
  )
}

/**
 * The frame every panel-wide page sits in.
 *
 * There used to be three of these — one for prose pages, one for the dashboard,
 * one for the instance tabs — with three different maximum widths and two
 * different ideas about who owns the scrollbar. A page that picked the wrong
 * one looked almost right, which is the worst way for a mistake to look. Now
 * there is one frame and one decision to make: prose, or tiles.
 */
export function Page({ title, lead, aside, above, wide, full, children }: Props) {
  const head = above ?? title ?? lead ?? aside
  // full wins over wide: a caller asking for both means the workspace.
  const form = full ? 'page page--full' : wide ? 'page page--wide' : 'page'

  return (
    <div className={form}>
      {head !== undefined && <PageHead title={title} lead={lead} aside={aside} above={above} />}
      {children}
    </div>
  )
}
