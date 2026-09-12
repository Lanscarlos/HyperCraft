import type { ReactNode } from 'react'

/**
 * A shelf of `.asset` rows with its columns named once at the top.
 *
 * The rows are unchanged — same grid, same tracks — and they still carry a
 * `<dt>` beside every value. That is not redundancy left behind: below 1240px
 * the tracks collapse and the numbers travel as a group, so the header stops
 * lining up with anything and hides itself, and the per-row labels are what is
 * left to say which number is the size and which is the date. Above it the
 * labels hide instead and this row is the only place they appear, which is
 * what gets a shelf of eight runtimes down from eight screens to one.
 *
 * `head` is one entry per track of `.asset`, in order, empty string for the
 * ones with nothing to name (the action column, and the hole an engine row
 * leaves where a Java row puts its full version).
 */
export function Shelf({ head, children }: { head: string[]; children: ReactNode }) {
  return (
    <div className="asset-list asset-list--titled">
      {/* Hidden from assistive tech on purpose: the value's own <dt> is what
          names it there, in both layouts, and a header a screen reader cannot
          associate with a cell would only read every label a second time. */}
      <div className="asset-list__head" aria-hidden="true">
        {head.map((label, index) => (
          <span key={index}>{label}</span>
        ))}
      </div>
      {children}
    </div>
  )
}
