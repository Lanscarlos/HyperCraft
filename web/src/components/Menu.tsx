import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'

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

/**
 * A button and the short list of actions behind it.
 *
 * It exists for the actions that must stay reachable without being a target:
 * 强制结束 beside 启动 at the same size is a way to lose a world to one slip.
 * Everything a popover has to get right — dismiss on an outside press, dismiss
 * on Escape, close before running the action so the page never repaints under
 * an open sheet — is here once rather than in each caller.
 */
export function Menu({ items, children, className, title, ariaLabel }: Props) {
  const [open, setOpen] = useState(false)
  const wrap = useRef<HTMLDivElement | null>(null)

  // The sheet outlives the decision to close it by one exit animation, so
  // dismissing it looks like the reverse of opening it rather than like the
  // sheet failing to render. `open` is still the single source of truth for
  // whether the menu is *usable*: the leaving sheet is on its way out and
  // stops taking clicks the moment `leaving` is set.
  const hide = useCallback(() => setOpen(false), [])
  const { leaving, close } = useDismiss(hide)

  useEffect(() => {
    if (!open) return
    const onDown = (event: MouseEvent) => {
      if (!wrap.current?.contains(event.target as Node)) close()
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
    <div className="menu" ref={wrap} data-state={leaving ? 'out' : undefined}>
      <button
        className={className}
        onClick={() => (open ? close() : setOpen(true))}
        aria-haspopup="menu"
        aria-expanded={open}
        title={title}
        aria-label={ariaLabel}
      >
        {children}
      </button>

      {open && (
        <div className="menu__sheet" role="menu">
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
        </div>
      )}
    </div>
  )
}
