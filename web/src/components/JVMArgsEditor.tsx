import { useEffect, useRef, useState } from 'react'
import type { MutableRefObject } from 'react'

import {
  formatFlags,
  knownFor,
  parseFlag,
  parseFlags,
  reflow,
  suggestFlags,
  wantsUnit,
  type Flag,
} from '../jvmFlags'
import { ArgComplete, ArgGrid, ArgRemove } from './ArgShell'
import { Card } from './Card'
import { Select } from './Select'

/**
 * The JVM arguments, one small card each, with the control the argument's own
 * syntax asks for.
 *
 * The text is still the truth. This parses the same one-per-line string the
 * textarea holds and writes the same string back, so presets, 从启动脚本读,
 * the Aikar heap warning and saving all go on working against one value and
 * know nothing about cards. Nothing here can express an argument you could not
 * have typed — the property jvmPresets.ts cares about.
 *
 * Cards and not rows, after the rows shipped and were wrong: a row put the
 * name hard left and its control hard right, half the panel apart, so no value
 * read as belonging to the flag above it and no two rows lined up. A card
 * stacks name, note and control in that order and the grid puts two of them on
 * a line, which is both tidier and shorter — twenty of Aikar's flags are ten
 * rows of cards rather than twenty rows.
 *
 * Cards carry the line they came from verbatim, and only one an operator
 * actually edited is rebuilt from its parts. Without that, opening this on
 * someone else's arguments and touching one would rewrite the spacing of the
 * other twenty, and the diff in 配置历史 would be a wall instead of a line.
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
   *  and replaces the cards wholesale. */
  const emitted = useRef(value)
  /** The card being typed into as free text: a new one, or one whose name was
   *  clicked to rewrite it. At most one at a time. */
  const [editing, setEditing] = useState<string | null>(null)
  /** Set when a card should take focus on the next render, so 添加参数 lands
   *  the cursor in the card it just created. */
  const focusing = useRef<string | null>(null)

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

  /** Turning a card into its own text is only useful if the cursor goes with
   *  it — otherwise clicking the name produces a field you then have to click
   *  again. Both ways in go through here for that reason. */
  const edit = (id: string) => {
    focusing.current = id
    setEditing(id)
  }

  const add = () => {
    const row = parseFlag('')
    edit(row.id)
    // Not through push(): an empty card is not an argument yet, and emitting it
    // would put a blank line into the value on every 添加参数 press.
    setRows([...rows, row])
  }

  /** Re-reads a free-text card once typing stops. An emptied one is dropped
   *  rather than kept as a blank line — the same thing saving would do to it. */
  const commit = (id: string, text: string) => {
    setEditing(null)
    push(
      rows
        .map((row) => (row.id === id ? { ...parseFlag(text), id: row.id } : row))
        .filter((row) => row.raw !== ''),
    )
  }

  return (
    <ArgGrid
      onAdd={add}
      addLabel="+ 添加参数"
      emptyNote="还没有 JVM 参数；上面的预设可以一次填好一套"
      isEmpty={rows.length === 0}
      footnote={
        <>
          面板不认识的参数照样能加，会原样保存。参数会放在 <code>-jar</code> 之前。
        </>
      }
    >
      {rows.map((flag) => (
          <ArgCard
            key={flag.id}
            flag={flag}
            editing={editing === flag.id}
            focusing={focusing}
            onEdit={() => edit(flag.id)}
            onCommit={(text) => commit(flag.id, text)}
            onPatch={(changes) => patch(flag.id, changes)}
          onRemove={() => remove(flag.id)}
        />
      ))}
    </ArgGrid>
  )
}

const UNITS = [
  { value: '', label: '无单位' },
  { value: 'K', label: 'K' },
  { value: 'M', label: 'M' },
  { value: 'G', label: 'G' },
]

function ArgCard({
  flag,
  editing,
  focusing,
  onEdit,
  onCommit,
  onPatch,
  onRemove,
}: {
  flag: Flag
  editing: boolean
  focusing: MutableRefObject<string | null>
  onEdit: () => void
  onCommit: (text: string) => void
  onPatch: (changes: Partial<Flag>) => void
  onRemove: () => void
}) {
  const known = knownFor(flag)

  // Free text: a card being retyped, and every argument whose syntax this panel
  // cannot classify. The second is not a failure state — -Xss512k and
  // -javaagent:… live here permanently and are none the worse for it. Both take
  // the full width of the grid, because what goes in them is the long form.
  if (editing || flag.kind === 'raw') {
    return (
      <Card pad="tight" tone="sunken" className="jvmcard jvmcard--full">
        <ArgComplete
          id={flag.id}
          raw={flag.raw}
          editing={editing}
          focusing={focusing}
          placeholder="-XX:+UseG1GC"
          ariaLabel="JVM 参数"
          suggest={suggestFlags}
          onEdit={onEdit}
          onCommit={onCommit}
          trailing={<RemoveButton flag={flag} onRemove={onRemove} />}
        />
        {!editing && (
          <div className="jvmcard__note">面板不认识这个参数的写法，按原文保存。</div>
        )}
      </Card>
    )
  }

  return (
    <Card pad="tight" tone="sunken" className="jvmcard">
      <div className="jvmcard__head">
        <button type="button" className="jvmcard__name" onClick={onEdit} title="改这一行的原文">
          <span className="jvmcard__prefix">{flag.prefix}</span>
          {flag.name}
        </button>
        <RemoveButton flag={flag} onRemove={onRemove} />
      </div>

      {known && <div className="jvmcard__note">{known.note}</div>}

      <div className="jvmcard__control">
        {flag.kind === 'boolean' && (
          <>
            <input
              type="checkbox"
              className="switch"
              role="switch"
              checked={flag.on}
              aria-label={`${flag.prefix}${flag.name}`}
              onChange={(e) => onPatch({ on: e.target.checked })}
            />
            {/* Off is not gone: the argument stays on the command line as
                -XX:-Name, explicitly disabling something a default or an
                earlier argument may have turned on. The ✕ is what removes it,
                and saying so here is what keeps the two apart. */}
            <span className="switch__label">
              {flag.on ? '已启用' : '已禁用（写成 -XX:-…）'}
            </span>
          </>
        )}

        {flag.kind === 'number' && (
          <>
            <input
              type="number"
              className="input-slim jvmcard__num"
              value={flag.value}
              min={known?.min}
              max={known?.max}
              aria-label={`${flag.prefix}${flag.name} 的值`}
              onChange={(e) => onPatch({ value: e.target.value })}
            />
            {wantsUnit(flag) && (
              <Select
                className="input-slim jvmcard__unit"
                /* Displayed upper-case because the options are; a lower-case
                   suffix already on the line is only rewritten if this is
                   actually used, which keeps an untouched card untouched. */
                value={flag.unit.toUpperCase()}
                options={UNITS}
                ariaLabel={`${flag.prefix}${flag.name} 的单位`}
                onChange={(unit) => onPatch({ unit })}
              />
            )}
            {known?.suffix && <span className="switch__label">{known.suffix}</span>}
          </>
        )}

        {flag.kind === 'text' && (
          <input
            type="text"
            className="input-slim"
            value={flag.value}
            spellCheck={false}
            aria-label={`${flag.prefix}${flag.name} 的值`}
            onChange={(e) => onPatch({ value: e.target.value })}
          />
        )}
      </div>
    </Card>
  )
}

function RemoveButton({ flag, onRemove }: { flag: Flag; onRemove: () => void }) {
  return <ArgRemove label={flag.raw} onRemove={onRemove} />
}
