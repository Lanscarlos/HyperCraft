import { useCallback, useEffect, useRef, useState } from 'react'
import type { KeyboardEvent, ReactNode } from 'react'

export interface TabItem<T extends string> {
  id: T
  label: string
  /** A badge after the label — "已开启", an update count. */
  badge?: ReactNode
}

interface Props<T extends string> {
  items: TabItem<T>[]
  active: T
  onSelect: (id: T) => void
  /** What this set of tabs is for, read out before the tabs themselves. */
  label: string
  /** Ties each tab to its panel: `${idPrefix}-tab-x` and `${idPrefix}-panel-x`. */
  idPrefix: string
}

/** Which edge the strip is cut off at, so the fade can say so. */
type Edge = 'none' | 'start' | 'end' | 'both'

/**
 * A row of tabs, as a real tablist.
 *
 * Two things the hand-rolled rows did not do. Arrow keys move between tabs —
 * the pattern every screen reader announces and then expects to work — and a
 * strip too narrow for its tabs fades at whichever edge it is cut off at.
 * Before, the six instance tabs on a phone scrolled sideways with the
 * scrollbar hidden, which is indistinguishable from having only four tabs.
 */
export function Tabs<T extends string>({ items, active, onSelect, label, idPrefix }: Props<T>) {
  const strip = useRef<HTMLDivElement | null>(null)
  const [edge, setEdge] = useState<Edge>('none')

  const measure = useCallback(() => {
    const el = strip.current
    if (!el) return
    // A pixel of slack: fractional scroll positions would otherwise leave a
    // fade painted on a strip that is already at its end.
    const atStart = el.scrollLeft > 1
    const atEnd = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    setEdge(atStart && atEnd ? 'both' : atStart ? 'start' : atEnd ? 'end' : 'none')
  }, [])

  useEffect(() => {
    const el = strip.current
    if (!el) return
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    el.addEventListener('scroll', measure, { passive: true })
    return () => {
      observer.disconnect()
      el.removeEventListener('scroll', measure)
    }
  }, [measure, items.length])

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const index = items.findIndex((item) => item.id === active)
    const last = items.length - 1
    let target: number
    switch (event.key) {
      case 'ArrowRight':
        target = index >= last ? 0 : index + 1
        break
      case 'ArrowLeft':
        target = index <= 0 ? last : index - 1
        break
      case 'Home':
        target = 0
        break
      case 'End':
        target = last
        break
      default:
        return
    }
    event.preventDefault()
    onSelect(items[target].id)
    // Focus follows selection, which is what the roving tabindex below sets up;
    // it also scrolls the tab into view when the strip is cut off.
    document.getElementById(`${idPrefix}-tab-${items[target].id}`)?.focus()
  }

  return (
    <div
      className="tabs"
      role="tablist"
      aria-label={label}
      data-edge={edge}
      ref={strip}
      onKeyDown={onKeyDown}
    >
      {items.map((item) => {
        const selected = item.id === active
        return (
          <button
            key={item.id}
            id={`${idPrefix}-tab-${item.id}`}
            role="tab"
            aria-selected={selected}
            aria-controls={`${idPrefix}-panel-${item.id}`}
            // Only the selected tab is in the tab order; the arrow keys reach
            // the rest. Tabbing past a six-tab strip to get at the panel is
            // what this avoids.
            tabIndex={selected ? 0 : -1}
            className={`tabs__tab${selected ? ' tabs__tab--active' : ''}`}
            onClick={() => onSelect(item.id)}
          >
            {item.label}
            {item.badge}
          </button>
        )
      })}
    </div>
  )
}
