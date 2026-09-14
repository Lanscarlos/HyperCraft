import { useEffect, useRef, useState } from 'react'
import type { MutableRefObject, ReactNode } from 'react'

/** One thing the completion popup can offer: the text it would insert, and the
 *  line of Chinese that is most of why offering it is worth anything. */
export interface ArgSuggestion {
  sample: string
  note: string
}

/**
 * The parts of a card-per-argument editor that have nothing to do with which
 * arguments are being edited.
 *
 * Deliberately not a shared "vocabulary" abstraction over both argument
 * syntaxes. A JVM flag is -XX:key=value and carries a unit and a +/- boolean
 * form; a server argument is `--key value` and has neither. A model wide
 * enough for both would give the server-argument editor two fields it can
 * never use, which is the abstraction that fits neither. What genuinely
 * repeats is the shell — the grid, the remove button, and the completion
 * popup — and those talk in strings.
 */

/** The grid, and the button that adds to it. Empty state and the way out of it
 *  are one box: an editor with nothing in it used to carry a centred paragraph
 *  saying so on top of this, which is the same sentence twice and 250px of
 *  height. */
export function ArgGrid({
  children,
  onAdd,
  addLabel,
  emptyNote,
  isEmpty,
  footnote,
}: {
  children: ReactNode
  onAdd: () => void
  addLabel: string
  emptyNote: string
  isEmpty: boolean
  footnote: ReactNode
}) {
  return (
    <div className="jvmargs">
      <div className="jvmargs__grid">
        {children}
        <button
          className={`jvmcard__add${isEmpty ? ' jvmcard__add--empty' : ''}`}
          type="button"
          onClick={onAdd}
        >
          {addLabel}
          {isEmpty && <small>{emptyNote}</small>}
        </button>
      </div>
      <small className="muted">{footnote}</small>
    </div>
  )
}

export function ArgRemove({ label, onRemove }: { label: string; onRemove: () => void }) {
  return (
    <button
      className="jvmcard__del"
      type="button"
      aria-label={`删除 ${label || '这一行'}`}
      onClick={onRemove}
    >
      ✕
    </button>
  )
}

/**
 * The one field that takes a whole argument, and the completion under it.
 *
 * Its own popup rather than a <datalist>: a native one cannot be themed, so it
 * arrived in the panel's pixel face and left in the browser's, and it has
 * nowhere to put the note that is most of why the list is worth offering. The
 * interaction is the one a datalist gave — arrow keys, Enter, and anything at
 * all still typeable — so the list stays a shortcut and never becomes a gate.
 */
export function ArgComplete({
  id,
  raw,
  editing,
  focusing,
  placeholder,
  ariaLabel,
  suggest,
  onEdit,
  onCommit,
  trailing,
}: {
  id: string
  raw: string
  editing: boolean
  focusing: MutableRefObject<string | null>
  placeholder: string
  ariaLabel: string
  suggest: (draft: string) => ArgSuggestion[]
  onEdit: () => void
  onCommit: (text: string) => void
  /** The remove button, which sits on the same row but belongs to the card. */
  trailing: ReactNode
}) {
  const [draft, setDraft] = useState(raw)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing) setDraft(raw)
  }, [editing, raw])

  useEffect(() => {
    if (focusing.current === id) {
      focusing.current = null
      input.current?.focus()
    }
  })

  const hits = open && editing ? suggest(draft) : []

  const pick = (entry: ArgSuggestion) => {
    setOpen(false)
    setDraft(entry.sample)
    onCommit(entry.sample)
  }

  return (
    <div className="jvmpick">
      <div className="jvmcard__head">
        <input
          ref={input}
          className="input-slim jvmpick__input"
          value={editing ? draft : raw}
          spellCheck={false}
          placeholder={placeholder}
          aria-label={ariaLabel}
          autoComplete="off"
          role="combobox"
          aria-expanded={hits.length > 0}
          onFocus={() => {
            onEdit()
            setOpen(true)
            setActive(-1)
          }}
          onChange={(e) => {
            setDraft(e.target.value)
            setOpen(true)
            setActive(-1)
          }}
          onBlur={(e) => {
            setOpen(false)
            onCommit(e.target.value)
          }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
              if (hits.length === 0) return
              e.preventDefault()
              const step = e.key === 'ArrowDown' ? 1 : -1
              setActive((i) => (i + step + hits.length) % hits.length)
              return
            }
            if (e.key === 'Enter') {
              e.preventDefault()
              if (active >= 0 && hits[active]) pick(hits[active])
              else e.currentTarget.blur()
              return
            }
            if (e.key === 'Escape') {
              // One Escape closes the list; a second puts the card back the way
              // it was, which for one added by mistake means it disappears.
              if (open && hits.length > 0) {
                setOpen(false)
                return
              }
              setDraft(raw)
              onCommit(raw)
            }
          }}
        />
        {trailing}
      </div>

      {hits.length > 0 && (
        <div className="jvmpick__pop" role="listbox">
          {hits.map((entry, index) => (
            <button
              key={entry.sample}
              type="button"
              role="option"
              aria-selected={index === active}
              className={`jvmpick__item${index === active ? ' jvmpick__item--active' : ''}`}
              // The input must keep focus through the press, or its own blur
              // commits the half-typed text before the click ever lands.
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(entry)}
            >
              <b>{entry.sample}</b>
              <small>{entry.note}</small>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
