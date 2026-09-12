import { useEffect, useId, useRef, useState } from 'react'
import type { MutableRefObject } from 'react'

import {
  KNOWN_FLAGS,
  formatFlags,
  knownFor,
  parseFlag,
  parseFlags,
  reflow,
  wantsUnit,
  type Flag,
} from '../jvmFlags'
import { Select } from './Select'

/**
 * The JVM arguments, one per row, each with the control its syntax calls for.
 *
 * The text is still the truth. This component parses the same one-per-line
 * string the textarea holds and writes the same string back, so presets,
 * 从启动脚本读, the Aikar heap warning and saving all go on working against
 * one value and know nothing about rows. Nothing here can express an argument
 * you could not have typed — the property jvmPresets.ts cares about.
 *
 * Rows carry the line they came from verbatim, and only a row an operator
 * actually edited is rebuilt from its parts. Without that, opening this view
 * on someone else's args and touching one of them would rewrite the spacing of
 * the other twenty, and the diff in 配置历史 would be a wall instead of a line.
 */
export function JVMArgsEditor({
  value,
  onChange,
}: {
  value: string
  onChange: (text: string) => void
}) {
  const [rows, setRows] = useState<Flag[]>(() => parseFlags(value))
  /** The text this component last produced. Anything else arriving in `value`
   *  came from outside — a preset, a script import, a different instance —
   *  and replaces the rows wholesale. */
  const emitted = useRef(value)
  /** The row being typed into as free text: a new one, or one whose name the
   *  operator clicked to rewrite. At most one at a time. */
  const [editing, setEditing] = useState<string | null>(null)
  /** Set when a row should take focus on the next render, so 添加参数 lands
   *  the cursor in the row it just created. */
  const focusing = useRef<string | null>(null)
  const listId = useId()

  useEffect(() => {
    if (value === emitted.current) return
    emitted.current = value
    setRows(parseFlags(value))
    setEditing(null)
  }, [value])

  const push = (next: Flag[]) => {
    const text = formatFlags(next)
    emitted.current = text
    setRows(next)
    onChange(text)
  }

  const patch = (id: string, changes: Partial<Flag>) =>
    push(rows.map((row) => (row.id === id ? reflow(row, changes) : row)))

  const remove = (id: string) => push(rows.filter((row) => row.id !== id))

  const add = () => {
    const row = parseFlag('')
    focusing.current = row.id
    setEditing(row.id)
    // Not through push(): an empty row is not an argument yet, and emitting it
    // would put a blank line into the value every 添加参数 press.
    setRows([...rows, row])
  }

  /** Re-reads a free-text row once typing stops. An emptied row is dropped
   *  rather than kept as a blank line — the same thing saving would do to it. */
  const commit = (id: string, text: string) => {
    setEditing(null)
    const next = rows
      .map((row) => (row.id === id ? { ...parseFlag(text), id: row.id } : row))
      .filter((row) => row.raw !== '')
    push(next)
  }

  return (
    <div className="jvmargs">
      {rows.length === 0 && (
        <p className="jvmargs__empty">
          还没有 JVM 参数。上面的预设可以一次填好一套，也可以自己加。
        </p>
      )}

      <ul className="jvmargs__list">
        {rows.map((flag) => (
          <ArgRow
            key={flag.id}
            flag={flag}
            listId={listId}
            editing={editing === flag.id}
            focusing={focusing}
            onEdit={() => setEditing(flag.id)}
            onCommit={(text) => commit(flag.id, text)}
            onPatch={(changes) => patch(flag.id, changes)}
            onRemove={() => remove(flag.id)}
          />
        ))}
      </ul>

      <div className="jvmargs__foot">
        <button className="btn btn--row" type="button" onClick={add}>
          + 添加参数
        </button>
        <small className="muted">
          面板不认识的参数照样能加，会原样保存。参数会放在 <code>-jar</code> 之前。
        </small>
      </div>

      {/* Native rather than the console's own popup: it completes and still
          lets anything be typed, which is the whole point — the list is a
          shortcut, never a gate. */}
      <datalist id={listId}>
        {KNOWN_FLAGS.map((entry) => (
          <option key={entry.sample} value={entry.sample} label={entry.note} />
        ))}
      </datalist>
    </div>
  )
}

const UNITS = [
  { value: '', label: '无单位' },
  { value: 'K', label: 'K' },
  { value: 'M', label: 'M' },
  { value: 'G', label: 'G' },
]

function ArgRow({
  flag,
  listId,
  editing,
  focusing,
  onEdit,
  onCommit,
  onPatch,
  onRemove,
}: {
  flag: Flag
  listId: string
  editing: boolean
  focusing: MutableRefObject<string | null>
  onEdit: () => void
  onCommit: (text: string) => void
  onPatch: (changes: Partial<Flag>) => void
  onRemove: () => void
}) {
  const known = knownFor(flag)
  const [draft, setDraft] = useState(flag.raw)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (editing) setDraft(flag.raw)
  }, [editing, flag.raw])

  useEffect(() => {
    if (focusing.current === flag.id) {
      focusing.current = null
      input.current?.focus()
    }
  })

  // Free text: a row being retyped, and every row whose syntax this panel
  // cannot classify. The second case is not a failure state — -Xss512k and
  // -javaagent:… live here permanently and are none the worse for it.
  if (editing || flag.kind === 'raw') {
    return (
      <li className="jvmarg jvmarg--raw">
        <input
          ref={input}
          className="input-slim jvmarg__free"
          value={editing ? draft : flag.raw}
          list={listId}
          spellCheck={false}
          placeholder="-XX:+UseG1GC"
          aria-label="JVM 参数"
          onFocus={onEdit}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={(e) => onCommit(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              e.currentTarget.blur()
            }
            // Escape puts the row back the way it was, which for a row added
            // by mistake means it disappears — committing '' drops it.
            if (e.key === 'Escape') {
              setDraft(flag.raw)
              onCommit(flag.raw)
            }
          }}
        />
        <RemoveButton flag={flag} onRemove={onRemove} />
      </li>
    )
  }

  return (
    <li className="jvmarg">
      <div className="jvmarg__body">
        <button
          type="button"
          className="jvmarg__name"
          onClick={onEdit}
          title="改这一行的原文"
        >
          <span className="jvmarg__prefix">{flag.prefix}</span>
          {flag.name}
        </button>

        <div className="jvmarg__control">
          {flag.kind === 'boolean' && (
            <div className="jvmarg__bool" role="group" aria-label={`${flag.prefix}${flag.name}`}>
              <button
                type="button"
                className={`chip${flag.on ? ' chip--active' : ''}`}
                aria-pressed={flag.on}
                onClick={() => onPatch({ on: true })}
              >
                开
              </button>
              <button
                type="button"
                className={`chip${flag.on ? '' : ' chip--active'}`}
                aria-pressed={!flag.on}
                onClick={() => onPatch({ on: false })}
              >
                关
              </button>
            </div>
          )}

          {flag.kind === 'number' && (
            <>
              <input
                type="number"
                className="input-slim jvmarg__num"
                value={flag.value}
                min={known?.min}
                max={known?.max}
                aria-label={`${flag.prefix}${flag.name} 的值`}
                onChange={(e) => onPatch({ value: e.target.value })}
              />
              {wantsUnit(flag) && (
                <Select
                  className="input-slim jvmarg__unit"
                  /* Displayed upper-case because the options are; a lower-case
                     suffix already on the line is only rewritten if this is
                     actually used, which keeps an untouched row untouched. */
                  value={flag.unit.toUpperCase()}
                  options={UNITS}
                  ariaLabel={`${flag.prefix}${flag.name} 的单位`}
                  onChange={(unit) => onPatch({ unit })}
                />
              )}
            </>
          )}

          {flag.kind === 'text' && (
            <input
              type="text"
              className="input-slim jvmarg__text"
              value={flag.value}
              spellCheck={false}
              aria-label={`${flag.prefix}${flag.name} 的值`}
              onChange={(e) => onPatch({ value: e.target.value })}
            />
          )}
        </div>
      </div>

      <RemoveButton flag={flag} onRemove={onRemove} />

      {known && <small className="jvmarg__note">{known.note}</small>}
    </li>
  )
}

function RemoveButton({ flag, onRemove }: { flag: Flag; onRemove: () => void }) {
  return (
    <button
      className="btn btn--icon btn--row jvmarg__del"
      type="button"
      aria-label={`删除 ${flag.raw || '这一行'}`}
      onClick={onRemove}
    >
      ✕
    </button>
  )
}
