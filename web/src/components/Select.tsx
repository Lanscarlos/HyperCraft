import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties, KeyboardEvent } from 'react'

import { useDismiss } from '../useDismiss'
import { useMediaQuery } from '../useMediaQuery'

export interface SelectOption {
  value: string
  label: string
  /** A second line, for what the label alone cannot say — a publish date, a
   *  path. Dropped into the label on the native fallback, which has no room
   *  for two lines. */
  note?: string
  disabled?: boolean
}

interface Props {
  value: string
  onChange: (value: string) => void
  options: SelectOption[]
  disabled?: boolean
  /** Extra classes on the trigger: `select--block` to fill its row,
   *  `input-slim` for the size a dense table row wants. */
  className?: string
  /**
   * The control's name. Not optional in practice: most of these sit inside a
   * `<label class="field">`, and a `<label>` wrapping a button labels nothing
   * — a button is not a labelable element — so the visible caption has to be
   * repeated here or the control has no accessible name at all.
   */
  ariaLabel?: string
  /** Shown when `value` matches no option. */
  placeholder?: string
  title?: string
}

/** Between the trigger and the sheet, and between the sheet and the viewport
 *  edge it would otherwise touch. */
const GAP = 6
const EDGE = 8
/** Roughly eight rows. Past that the list scrolls rather than growing: a
 *  hundred plugin versions must not become a hundred-row wall. */
const MAX_HEIGHT = 288
/** A sheet narrower than this is unreadable, whatever the trigger's width — a
 *  version picker in a table cell is 90px wide and its options are not. */
const MIN_WIDTH = 168
/** What Home/End do in one step, and what PageUp/PageDown do in eight. */
const PAGE = 8
/** How long a typed prefix stays a prefix. */
const TYPE_AHEAD = 700

interface Anchor {
  /** Whether the sheet opens above the trigger rather than below it. */
  up: boolean
  left: number
  width: number
  /** Distance from the viewport's top edge, or from its bottom when `up`. */
  offset: number
  maxHeight: number
}

/** Where the sheet goes, measured against the trigger and the viewport.
 *
 *  Below the trigger unless there is not room for a usable list down there and
 *  there is more room up — a version picker in the last row of a long table
 *  would otherwise open into two visible rows and a scrollbar. */
function place(anchor: HTMLElement): Anchor {
  const rect = anchor.getBoundingClientRect()
  const below = window.innerHeight - rect.bottom - GAP - EDGE
  const above = rect.top - GAP - EDGE
  const up = below < Math.min(MAX_HEIGHT, 180) && above > below

  const width = Math.min(Math.max(rect.width, MIN_WIDTH), window.innerWidth - EDGE * 2)
  const left = Math.min(Math.max(rect.left, EDGE), window.innerWidth - width - EDGE)

  return {
    up,
    left,
    width,
    offset: up ? window.innerHeight - rect.top + GAP : rect.bottom + GAP,
    maxHeight: Math.max(Math.min(MAX_HEIGHT, up ? above : below), 96),
  }
}

/**
 * The panel's dropdown.
 *
 * A `<select>` is two controls in one trench coat: a box the page draws, and a
 * popup the *platform* draws. The box has been ours for a while — `appearance:
 * none` and the rules in styles.css give it the same height, border and focus
 * ring as an input. The popup was never ours, which is why choosing a Java
 * runtime looked like a Windows 95 list dropping out of a control that had
 * been styled to the millimetre, and why it appeared and vanished between two
 * frames on a page where every other surface fades in.
 *
 * So this is the popup, rebuilt: a listbox that reads the same tokens as
 * everything else, rises out of the trigger, and leaves the way the menus and
 * dialogs leave (see useDismiss). What that costs is the behaviour a real
 * `<select>` gives away for free, so all of it is paid back here —
 *
 *   - the keyboard: arrows, Home/End, PageUp/PageDown, Enter, Escape, and
 *     type-ahead, which is the one people do not know they rely on until a
 *     hand-rolled listbox takes it away;
 *   - the reading: `combobox` + `listbox` with `aria-activedescendant`, so
 *     focus never leaves the trigger and a screen reader is told what is
 *     highlighted rather than being moved somewhere it cannot get back from;
 *   - the phone: on a coarse pointer this renders a real `<select>` and stops
 *     there. A thumb-sized native picker beats anything drawn in a page, and
 *     the trigger looks identical either way.
 *
 * The sheet is portalled to <body> and positioned against the trigger's
 * viewport rect, for the reason the modal is (see Modal.tsx): absolute
 * positioning puts it inside whichever scroll box or `overflow: hidden` card
 * the trigger happens to live in, and half these selects live in a dialog or a
 * scrolling table.
 */
export function Select({
  value,
  onChange,
  options,
  disabled,
  className,
  ariaLabel,
  placeholder,
  title,
}: Props) {
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const [anchor, setAnchor] = useState<Anchor | null>(null)

  const button = useRef<HTMLButtonElement | null>(null)
  const sheet = useRef<HTMLDivElement | null>(null)
  const typed = useRef({ text: '', at: 0 })

  const id = useId()
  const listId = `${id}-list`
  const optionId = (index: number) => `${id}-opt-${index}`

  // A phone gets the platform's picker. Checked here rather than in CSS
  // because the two branches are different elements, not two skins.
  const coarse = useMediaQuery('(pointer: coarse)')

  const hide = useCallback(() => setOpen(false), [])
  const { leaving, close } = useDismiss(hide)

  const selected = options.findIndex((option) => option.value === value)
  const current = selected >= 0 ? options[selected] : null

  /** The first option that can actually be landed on, walking `step` at a time
   *  from `from`. Disabled entries are passed over rather than stopped at, and
   *  the ends do not wrap — same as the control this replaces. */
  const reachable = useCallback(
    (from: number, step: number) => {
      for (let i = from; i >= 0 && i < options.length; i += step) {
        if (!options[i].disabled) return i
      }
      return -1
    },
    [options],
  )

  const show = useCallback(() => {
    if (disabled || options.length === 0) return
    const start = selected >= 0 && !options[selected].disabled ? selected : reachable(0, 1)
    setActive(Math.max(start, 0))
    setOpen(true)
  }, [disabled, options, selected, reachable])

  const commit = useCallback(
    (index: number) => {
      const option = options[index]
      if (!option || option.disabled) return
      // Closed first, then the change — the same order the menus use. A sheet
      // still on screen while the page reflows underneath it reads as a glitch.
      close()
      if (option.value !== value) onChange(option.value)
    },
    [options, close, onChange, value],
  )

  // Follow the trigger. The sheet is fixed to the viewport, so anything that
  // moves the trigger — a scrolled dialog body, a resized window, a table
  // scrolled sideways — moves the sheet too, or it is left hanging in space.
  useLayoutEffect(() => {
    if (!open || !button.current) return
    const anchorEl = button.current
    const follow = () => setAnchor(place(anchorEl))
    follow()
    // Capture, because the scroll that matters is usually some ancestor's and
    // scroll events do not bubble.
    window.addEventListener('scroll', follow, true)
    window.addEventListener('resize', follow)
    return () => {
      window.removeEventListener('scroll', follow, true)
      window.removeEventListener('resize', follow)
    }
  }, [open])

  // Keep the highlighted row on screen, including the one highlighted on open:
  // opening a hundred-version list scrolled to the top, with the version you
  // are actually on somewhere below the fold, is the same as not marking it.
  useEffect(() => {
    if (!open) return
    sheet.current
      ?.querySelector<HTMLElement>('[data-active="true"]')
      ?.scrollIntoView({ block: 'nearest' })
  }, [open, active, anchor])

  useEffect(() => {
    if (!open) return
    const onDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (button.current?.contains(target)) return
      if (sheet.current?.contains(target)) return
      close()
    }
    window.addEventListener('pointerdown', onDown)
    return () => window.removeEventListener('pointerdown', onDown)
  }, [open, close])

  /** Jump to the next option whose label starts with what has been typed.
   *  Within the timeout the letters accumulate, so `1.2` finds 1.21 rather
   *  than bouncing between everything starting with a 1. */
  const typeAhead = useCallback(
    (key: string) => {
      const now = performance.now()
      const text = (now - typed.current.at < TYPE_AHEAD ? typed.current.text : '') + key
      typed.current = { text, at: now }

      const prefix = text.toLowerCase()
      // Start after the current row so repeating one letter cycles.
      const from = text.length === 1 ? active + 1 : active
      for (let step = 0; step < options.length; step += 1) {
        const index = (from + step) % options.length
        const option = options[index]
        if (!option.disabled && option.label.toLowerCase().startsWith(prefix)) {
          setActive(index)
          return
        }
      }
    },
    [active, options],
  )

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    const { key } = event

    /**
     * This key was the list's, and nobody else gets a turn at it.
     *
     * Not a formality. 获取插件 drives its result list from arrow keys and
     * Enter on the surrounding div, and the rail's three dropdowns sit inside
     * it: without this, arrowing through 排序 walks the cursor down the search
     * results behind it and Enter opens whichever plugin it landed on. Escape
     * is the same story one level up — half of these open inside a dialog that
     * closes on Escape from a window listener, and one Escape should shut the
     * list and leave the form standing.
     */
    const take = () => {
      event.preventDefault()
      event.stopPropagation()
    }

    if (!open) {
      if (key === 'ArrowDown' || key === 'ArrowUp' || key === 'Enter' || key === ' ') {
        take()
        show()
      }
      return
    }

    const step = (delta: number) => {
      take()
      const next = reachable(
        Math.min(Math.max(active + delta, 0), options.length - 1),
        Math.sign(delta),
      )
      // Nothing reachable that way (a run of disabled entries at the end):
      // stay where we are rather than falling off the list.
      if (next >= 0) setActive(next)
    }

    switch (key) {
      case 'ArrowDown':
        return step(1)
      case 'ArrowUp':
        return step(-1)
      case 'PageDown':
        return step(PAGE)
      case 'PageUp':
        return step(-PAGE)
      case 'Home':
        take()
        return setActive(Math.max(reachable(0, 1), 0))
      case 'End':
        take()
        return setActive(Math.max(reachable(options.length - 1, -1), 0))
      case 'Enter':
      case ' ':
        take()
        return commit(active)
      case 'Escape':
        take()
        return close()
      case 'Tab':
        // Focus is leaving; there is no one left to watch an exit animation.
        // Not taken: Tab belongs to the page, and swallowing it would strand
        // the reader on a control they have already finished with.
        return setOpen(false)
      default:
        if (key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
          take()
          typeAhead(key)
        }
    }
  }

  const trigger = ['select', className].filter(Boolean).join(' ')

  if (coarse) {
    return (
      <select
        className={trigger}
        value={value}
        disabled={disabled}
        aria-label={ariaLabel}
        title={title}
        onChange={(event) => onChange(event.target.value)}
      >
        {!current && <option value={value}>{placeholder ?? value}</option>}
        {options.map((option) => (
          <option key={option.value} value={option.value} disabled={option.disabled}>
            {option.note ? `${option.label} · ${option.note}` : option.label}
          </option>
        ))}
      </select>
    )
  }

  return (
    <>
      <button
        ref={button}
        type="button"
        className={trigger}
        disabled={disabled || options.length === 0}
        title={title}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open ? optionId(active) : undefined}
        aria-label={ariaLabel}
        onClick={() => (open ? close() : show())}
        onKeyDown={onKeyDown}
      >
        <span className="select__value">{current?.label ?? placeholder ?? value}</span>
        <svg
          className="select__chevron"
          width="12"
          height="12"
          viewBox="0 0 12 12"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="m2.5 4.5 3.5 3.5 3.5-3.5" />
        </svg>
      </button>

      {open &&
        anchor &&
        createPortal(
          <div
            ref={sheet}
            id={listId}
            role="listbox"
            aria-label={ariaLabel}
            className="select-sheet"
            data-state={leaving ? 'out' : 'in'}
            data-dir={anchor.up ? 'up' : 'down'}
            style={{
              left: anchor.left,
              width: anchor.width,
              maxHeight: anchor.maxHeight,
              ...(anchor.up ? { bottom: anchor.offset } : { top: anchor.offset }),
            }}
            // Focus stays on the trigger — it is what carries
            // aria-activedescendant and the key handling, and a press on a
            // non-focusable row would otherwise dump focus on <body>.
            onMouseDown={(event) => event.preventDefault()}
          >
            {options.map((option, index) => (
              <div
                key={option.value}
                id={optionId(index)}
                role="option"
                className="select-sheet__option"
                aria-selected={index === selected}
                aria-disabled={option.disabled || undefined}
                data-active={index === active || undefined}
                // Capped so a long list still finishes arriving promptly: past
                // the first few rows the cascade has already read as a cascade.
                style={{ '--i': Math.min(index, 7) } as CSSProperties}
                onPointerEnter={() => !option.disabled && setActive(index)}
                onClick={() => commit(index)}
              >
                <span className="select-sheet__text">
                  <span className="select-sheet__label">{option.label}</span>
                  {option.note && <small className="select-sheet__note">{option.note}</small>}
                </span>
                <svg
                  className="select-sheet__tick"
                  width="12"
                  height="12"
                  viewBox="0 0 12 12"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.8"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="m2.2 6.3 2.5 2.5 5.1-5.4" />
                </svg>
              </div>
            ))}
          </div>,
          document.body,
        )}
    </>
  )
}
