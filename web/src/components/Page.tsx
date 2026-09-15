import type { ReactNode } from 'react'

interface HeadProps {
  title?: ReactNode
  /** A number beside the title — how many of the thing the page lists. */
  count?: ReactNode
  /** The paragraph under the title: what this page is for. */
  lead?: ReactNode
  /** The page's standing facts — how many, how big, where on disk — as a row
   *  of chips under the lead. They used to sit in the aside, where a long
   *  path wrapped them onto a second line under nothing in particular. */
  facts?: ReactNode
  /** The page's own actions, at the end of the head. At most two buttons and
   *  an overflow `Menu`; only one of them filled. Anything more belongs in
   *  the toolbar of the list it acts on. */
  actions?: ReactNode
  /** Anything above the title. A back link, mostly. */
  above?: ReactNode
  /**
   * The title is already on screen — the top bar's breadcrumb ends on this
   * page's name — so the heading is kept for the document outline and for a
   * screen reader, and gives its row back to the page. What is left of the
   * head is one line: the page's facts on the left, its actions on the right.
   *
   * Only for a page you reach through a breadcrumb that names it. Set it
   * anywhere else and the page has no visible title at all.
   */
  titleHidden?: boolean
  /** @deprecated The old free-form right half. Every caller is moving to
   *  `facts` + `actions`; this slot goes away when the last one has. */
  aside?: ReactNode
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
 * The head alone: title on the left, the page's own actions on the right, a
 * lead and the page's facts under the title.
 *
 * Exported on its own for the instance sections, which are not pages — the
 * pane around them owns the scroll and the width — but have to *open* like
 * one. They used to open eight different ways: the console with the server's
 * name, 监控 with a row of filter chips, 文件 with a card, 插件 with a
 * sixteen-pixel heading and a button row, and so on. Read one after another,
 * as they are, they looked like eight panels by eight authors. One head, in
 * the same place every other page in the panel keeps it, is the fix.
 */
export function PageHead({
  title,
  count,
  lead,
  facts,
  actions,
  above,
  aside,
  titleHidden,
}: HeadProps) {
  return (
    <header className={`page__head${titleHidden ? ' page__head--bare' : ''}`}>
      <div className="page__heading">
        {above}
        {title !== undefined && (
          <h1 className={titleHidden ? 'sr-only' : undefined}>
            {title}
            {count !== undefined && <span className="page__count">{count}</span>}
          </h1>
        )}
        {lead !== undefined && <p className="page__lead">{lead}</p>}
        {facts !== undefined && <p className="meta-chips page__facts">{facts}</p>}
      </div>
      {actions !== undefined && <div className="page__actions">{actions}</div>}
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
export function Page({
  title,
  count,
  lead,
  facts,
  actions,
  above,
  aside,
  titleHidden,
  wide,
  full,
  children,
}: Props) {
  const head = above ?? title ?? lead ?? facts ?? actions ?? aside
  // full wins over wide: a caller asking for both means the workspace.
  const form = full ? 'page page--full' : wide ? 'page page--wide' : 'page'

  return (
    <div className={form}>
      {head !== undefined && (
        <PageHead
          title={title}
          count={count}
          lead={lead}
          facts={facts}
          actions={actions}
          above={above}
          aside={aside}
          titleHidden={titleHidden}
        />
      )}
      {children}
    </div>
  )
}
