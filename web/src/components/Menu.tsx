import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties, ReactNode } from 'react'

import { EDGE, placeVertically, useAnchor } from '../useAnchor'
import { useDismiss } from '../useDismiss'

export interface MenuItem {
  label: string
  onSelect: () => void
  /** Painted as a destructive action, and never the first thing under the cursor. */
  danger?: boolean
  disabled?: boolean
}

interface Props {
  items: MenuItem[]
  /** What goes inside the button that opens it. */
  children: ReactNode
  /** Class for that button, so a menu can look like whatever opens it. */
  className?: string
  title?: string
  ariaLabel?: string
}

/** Roughly eight items. Past that the sheet scrolls rather than growing. */
const MAX_HEIGHT = 320
/** A sheet narrower than this is unreadable, and a trigger with less room than
 *  this to its left gets a left-aligned sheet instead of a squeezed one. */
const MIN_WIDTH = 150

/**
 * A button and the short list of actions behind it.
 *
 * It exists for the actions that must stay reachable without being a target:
 * 强制结束 beside 启动 at the same size is a way to lose a world to one slip.
 * Everything a popover has to get right — dismiss on an outside press, dismiss
 * on Escape, close before running the action so the page never repaints under
 * an open sheet — is here once rather than in each caller.
 *
 * The sheet is portalled to <body> and positioned against the trigger's
 * viewport rect rather than being absolutely positioned inside the menu. Two
 * things were wrong with the absolute version and they are the same thing: a
 * sheet lives inside its trigger's box, so a table with `overflow: hidden` for
 * its rounded corners cropped the last row's menu to nothing, and a menu near
 * the bottom of the window opened downwards into the fold. Portalled and
 * placed, it escapes the card and flips up when down is where the room is not.
 */
export function Menu({ items, children, className, title, ariaLabel }: Props) {
  const [open, setOpen] = useState(false)
  const button = useRef<HTMLButtonElement | null>(null)
  const sheet = useRef<HTMLDivElement | null>(null)

  // The sheet outlives the decision to close it by one exit animation, so
  // dismissing it looks like the reverse of opening it rather than like the
  // sheet failing to render. `open` is still the single source of truth for
  // whether the menu is *usable*: the leaving sheet is on its way out and
  // stops taking clicks the moment `leaving` is set.
  const hide = useCallback(() => setOpen(false), [])
  const { leaving, close } = useDismiss(hide)

  const anchor = useAnchor(open, button, (rect) => ({
    ...placeVertically(rect, MAX_HEIGHT),
    // Right-aligned with the trigger — the ⋯ sits at the end of its row and a
    // sheet that grew rightwards from it would grow off the page. Unless the
    // trigger is near the left edge, where there is no room to unfold into and
    // the sheet starts from the margin instead. Width is left to the content:
    // these are four short labels, not a list of versions.
    box: (rect.right - EDGE >= MIN_WIDTH
      ? { right: window.innerWidth - rect.right, maxWidth: rect.right - EDGE }
      : { left: EDGE, maxWidth: window.innerWidth - EDGE * 2 }) as CSSProperties,
  }))

  useEffect(() => {
    if (!open) return
    const onDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (button.current?.contains(target)) return
      // The sheet is no longer inside the trigger's subtree, so it has to be
      // asked about separately or every click inside it dismisses it.
      if (sheet.current?.contains(target)) return
      close()
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close()
    }
    // Pointerdown rather than click: a menu that survives until mouseup looks
    // stuck when you click straight through to something behind it.
    window.addEventListener('pointerdown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('pointerdown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open, close])

  return (
    <div className="menu">
      <button
        ref={button}
        className={className}
        onClick={() => (open ? close() : setOpen(true))}
        aria-haspopup="menu"
        aria-expanded={open}
        title={title}
        aria-label={ariaLabel}
      >
        {children}
      </button>

      {open &&
        anchor &&
        createPortal(
          <div
            ref={sheet}
            className="menu__sheet"
            role="menu"
            data-state={leaving ? 'out' : 'in'}
            data-dir={anchor.up ? 'up' : 'down'}
            style={{
              ...anchor.box,
              maxHeight: anchor.maxHeight,
              ...(anchor.up ? { bottom: anchor.offset } : { top: anchor.offset }),
            }}
          >
            {items.map((item) => (
              <button
                key={item.label}
                role="menuitem"
                className={`menu__item${item.danger ? ' menu__item--danger' : ''}`}
                disabled={item.disabled}
                onClick={() => {
                  // The sheet closes on its own time; the action does not wait
                  // for it. 强制结束 should not sit behind a fading menu.
                  close()
                  item.onSelect()
                }}
              >
                {item.label}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </div>
  )
}
