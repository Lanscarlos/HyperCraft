import type { ReactNode } from 'react'

interface Props {
  /** The one line that says what is not here. */
  title: ReactNode
  /** What to do about it, in a sentence. Optional — some absences need no
   *  advice. */
  children?: ReactNode
  /** The call to action, when there is one. It is allowed to be the screen's
   *  filled button: an empty shelf has nothing else to claim it. */
  action?: ReactNode
  /** One quiet line inside a table or a list, in place of its rows, rather
   *  than a block of its own. */
  inline?: boolean
}

/**
 * "Nothing here yet", written once.
 *
 * Six shapes said this before — a dashed box, a `<p>` inside a table, a
 * `.muted` paragraph, a `.file-empty` with a glyph, and two more per-page ones —
 * and which one you got depended on the page, not on the situation. Two
 * situations exist: a section that is empty (a block, so the space reads as
 * meant to be filled rather than as a rendering failure), and a list that has
 * nothing to show under its header (a line, so the header stays where it is).
 *
 * The note is a <div> rather than a <p> because callers pass prose with links
 * and the odd paragraph of their own. As a <p> the parser closed it at the
 * caller's first block child, which then escaped the note's reading measure
 * and ran the full width of the card.
 */
export function EmptyState({ title, children, action, inline }: Props) {
  if (inline) {
    return (
      <div className="empty empty--inline">
        <p className="empty__title">{title}</p>
        {children !== undefined && <div className="empty__note">{children}</div>}
        {action !== undefined && <div className="empty__actions">{action}</div>}
      </div>
    )
  }
  return (
    <div className="empty">
      <p className="empty__title">{title}</p>
      {children !== undefined && <div className="empty__note">{children}</div>}
      {action !== undefined && <div className="empty__actions">{action}</div>}
    </div>
  )
}
