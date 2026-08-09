import type { CSSProperties, ReactNode } from 'react'

/**
 * Placeholders that hold a view's shape while its first fetch is in flight.
 *
 * The panel used to answer "not loaded yet" with a one-line `加载中…` box. That
 * box is two lines tall and the thing that replaces it is twenty, so every tab
 * you opened collapsed the pane and then shoved it back out — the single
 * biggest source of the panel feeling jumpy, and it fired on every trip to 文件
 * or 资源, not just the first one.
 *
 * A placeholder fixes it by being the wrong content at the *right size*: same
 * cards, same rows, same heights, so the real data lands in the space already
 * cut for it and nothing moves. The bars are deliberately uneven in width —
 * a column of identical ones reads as a table that failed to load rather than
 * as text on its way.
 *
 * Nothing here appears immediately. `.skeleton-screen` holds itself invisible
 * for ~140ms first (see styles.css), which is longer than a local API call
 * takes, so the common case — a panel that answers off the daemon's own memory
 * — never flashes grey on the way to being ready. A placeholder that blinks is
 * worse than no placeholder at all.
 */

interface BarProps {
  /** Any CSS length. Vary it down a column so the block reads as prose. */
  w?: string
  /** Bar height in px; the default is one line of body text. */
  h?: number
  /** For anything round in the real layout — a chip, a toggle, a dot. */
  pill?: boolean
}

export function Skeleton({ w = '100%', h = 13, pill }: BarProps) {
  const style: CSSProperties = { width: w, height: `${h}px` }
  return <span className={pill ? 'skeleton skeleton--pill' : 'skeleton'} style={style} />
}

/**
 * The wrapper every placeholder goes in.
 *
 * Carries the appear-delay, and says "正在加载" once to a screen reader instead
 * of letting it walk thirty empty bars — which is why everything inside is
 * hidden from the accessibility tree.
 */
export function SkeletonScreen({
  label = '正在加载…',
  inPage,
  children,
}: {
  label?: string
  /**
   * Set when the placeholder is a child of `<Page>`, which already supplies
   * the column, the gap and the padding. Left off it stands in for a `.stack`
   * — the instance tabs' own frame — and brings that padding itself. Getting
   * this backwards is the one way a placeholder can *cause* the shift it
   * exists to prevent, so it is a prop rather than something to eyeball.
   */
  inPage?: boolean
  children: ReactNode
}) {
  return (
    <div className={inPage ? 'skeleton-screen' : 'skeleton-screen skeleton-screen--stack'}>
      <span className="sr-only" role="status">
        {label}
      </span>
      <div aria-hidden="true" className="skeleton-screen__body">
        {children}
      </div>
    </div>
  )
}

/** A card-shaped placeholder: the `.panel` chrome with a title and some lines. */
export function SkeletonPanel({
  title = true,
  lines = 3,
  children,
}: {
  title?: boolean
  lines?: number
  children?: ReactNode
}) {
  return (
    <section className="panel skeleton-panel">
      {title && <Skeleton w="34%" h={15} />}
      {children ??
        Array.from({ length: lines }, (_, index) => (
          // Widths walk down rather than repeat, the way a paragraph's last
          // line is short.
          <Skeleton key={index} w={`${92 - index * 13}%`} />
        ))}
    </section>
  )
}

/** A list of rows — file listings, plugin lists, anything with a left icon,
 *  a name, and something right-aligned. */
export function SkeletonRows({ rows = 6 }: { rows?: number }) {
  return (
    <div className="skeleton-rows">
      {Array.from({ length: rows }, (_, index) => (
        <div className="skeleton-rows__row" key={index}>
          <Skeleton w="16px" h={16} />
          {/* A deterministic spread of widths: random ones would reshuffle on
              every render while the fetch is still running. */}
          <Skeleton w={`${34 + ((index * 37) % 44)}%`} />
          <Skeleton w="52px" />
        </div>
      ))}
    </div>
  )
}
