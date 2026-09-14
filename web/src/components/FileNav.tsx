import { useRef } from 'react'
import type { ReactNode } from 'react'

import { Icon } from './Icon'

/**
 * The navigation column: the tree above, the listing below, one column wide.
 *
 * They used to be two columns, and in a deep plugin path that arrangement
 * spent 264px on a listing holding one file while the tree beside it — which
 * wanted 780px of height and had 740 — was already scrolling. The two are
 * anti-correlated: the deeper the tree, the fewer files at the leaf. Stacked,
 * they share one column and the editor keeps the width the second one was
 * costing it.
 *
 * The price of stacking is height, and the answer to it is the headers. A
 * section collapses to its own 28px bar, which hands its whole share to the
 * other one: collapse 目录 while editing a file you have already found,
 * collapse 文件 while walking to somewhere else. That is why the headers are
 * here rather than the divider alone — a divider can only split the height
 * between them, and what is actually wanted most of the time is all of it for
 * whichever one is being used.
 *
 * A collapsed section keeps its bar on screen. Folding something away with no
 * visible way back is the one shape this pane has been through before and is
 * not going through again.
 */
export interface FileNavProps {
  /** Where the divider sits, as the tree's share of the space the two open
   *  sections have between them. 0.55 by default; only meaningful when both
   *  are open. */
  split: number
  onSplit: (next: number) => void
  treeOpen: boolean
  listOpen: boolean
  onToggleTree: () => void
  onToggleList: () => void
  tree: ReactNode
  list: ReactNode
  /** Goes on the 文件 head, at the far end. The listing's density switch lives
   *  there rather than in a head of its own: stacked, the two would be
   *  「文件」 above 「当前目录」 — 28px and 36px of furniture saying the same
   *  thing twice above a list that has just lost half its height. */
  listTools?: ReactNode
}

/** Neither section may be dragged smaller than this. Below it a tree shows
 *  two rows and a listing shows one, which is not a section, it is a hint
 *  that something is broken. */
const MIN_SHARE = 96

export function FileNav({
  split,
  onSplit,
  treeOpen,
  listOpen,
  onToggleTree,
  onToggleList,
  tree,
  list,
  listTools,
}: FileNavProps) {
  const frame = useRef<HTMLDivElement | null>(null)

  /**
   * Drags the divider.
   *
   * The ratio is what is stored rather than a pixel height: the pane's height
   * changes with the window, and a stored 368px is a different split on every
   * screen. The clamp is in pixels because MIN_SHARE is about how many rows
   * fit, which a ratio cannot say.
   */
  const drag = (event: React.PointerEvent<HTMLDivElement>) => {
    const box = frame.current
    if (!box) return
    event.preventDefault()

    const move = (at: PointerEvent) => {
      const rect = box.getBoundingClientRect()
      // The two heads and the divider are not the sections' to share.
      const room = rect.height - HEAD * 2 - GRIP
      if (room <= MIN_SHARE * 2) return
      const top = at.clientY - rect.top - HEAD
      const floor = MIN_SHARE / room
      onSplit(Math.min(1 - floor, Math.max(floor, top / room)))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
  }

  const both = treeOpen && listOpen

  return (
    <div className="fm__nav" ref={frame}>
      <Section label="目录" open={treeOpen} onToggle={onToggleTree} grow={both ? split : 1}>
        {tree}
      </Section>

      {/* Only when there are two things to divide. A handle between a section
          and a collapsed bar would move a boundary that is not there. */}
      {both && (
        <div
          className="fm__grip fm__grip--row"
          role="separator"
          aria-orientation="horizontal"
          aria-label="调整目录树与文件列表的高度"
          onPointerDown={drag}
          onDoubleClick={() => onSplit(0.55)}
        />
      )}

      <Section
        label="文件"
        open={listOpen}
        onToggle={onToggleList}
        grow={both ? 1 - split : 1}
        aside={listOpen ? listTools : undefined}
      >
        {list}
      </Section>
    </div>
  )
}

/** The head's height, and the divider's. Shared with the drag maths above, so
 *  they are constants rather than numbers repeated in a stylesheet and a
 *  handler that would drift apart. Kept in step with .fm__sec-head and
 *  .fm__grip--row in styles.css. */
const HEAD = 28
const GRIP = 14

function Section({
  label,
  open,
  onToggle,
  grow,
  aside,
  children,
}: {
  label: string
  open: boolean
  onToggle: () => void
  /** This section's share of the room, as a flex grow factor. */
  grow: number
  /** Controls belonging to this section, at the far end of its head. */
  aside?: ReactNode
  children: ReactNode
}) {
  return (
    <section
      className="fm__sec"
      data-open={open || undefined}
      // Only a growing section needs the number; a collapsed one is its head.
      style={open ? { flexGrow: grow } : undefined}
    >
      {/* The head is a row, not a button: the controls in `aside` are
          interactive and nesting them inside a button is markup a browser is
          entitled to rearrange. The twisty and the label are the button. */}
      <div className="fm__sec-head">
        <button
          type="button"
          className="fm__sec-toggle"
          onClick={onToggle}
          aria-expanded={open}
          title={open ? `收起${label}` : `展开${label}`}
        >
          <Icon name="expand" className="fm__sec-twist" />
          {label}
        </button>
        {aside !== undefined && <div className="fm__sec-tools">{aside}</div>}
      </div>
      {open && <div className="fm__sec-body">{children}</div>}
    </section>
  )
}
