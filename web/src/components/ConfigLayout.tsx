import { useRef } from 'react'
import type { ReactNode } from 'react'

import { reducedMotion } from '../motion'
import { Select } from './Select'

/**
 * The shape 服务器配置 gives every file it edits.
 *
 * There are five of them — server.properties plus whatever the core writes —
 * and they arrive from two different endpoints in two different shapes. They
 * were also, for a while, two different pages: server.properties got a rail, a
 * per-row diff and a save bar, and bukkit.yml got a stack of panels and a
 * button at the bottom. Same job, same page, two answers, and the one you got
 * depended on which chip you had pressed.
 *
 * So the layout lives here and both callers feed it. What each caller keeps is
 * what is genuinely its own: where the values come from, and what saving them
 * means.
 */

/** One setting, in the form both endpoints can be read into. */
export interface ConfigSettingView {
  key: string
  label: string
  type: 'text' | 'number' | 'boolean' | 'select'
  options?: string[]
  hint?: string
  default: string
  /** An in-game command that applies this without a restart, for the few that
   *  have one. */
  live?: string
  /** What goes wrong if this is set carelessly. Only on the few that can open
   *  a server up or cannot be undone. */
  risk?: string
}

export interface ConfigGroupView {
  id: string
  label: string
  hint?: string
}

/* ------------------------------------------------------------------ shell */

export function ConfigLayout({
  groups,
  counts,
  changed,
  onlyChanged,
  onToggleOnlyChanged,
  note,
  path,
  children,
}: {
  groups: ConfigGroupView[]
  /** How many settings each group holds, by group id. */
  counts: Record<string, number>
  /** How many rows no longer read the way the file does. */
  changed: number
  onlyChanged: boolean
  onToggleOnlyChanged: () => void
  note: ReactNode
  path: string
  /** The sections, each carrying `id` equal to its group's id. */
  children: ReactNode
}) {
  const body = useRef<HTMLDivElement>(null)

  const jumpTo = (id: string) => {
    // Scoped to this form's own body: two files' sections can share a group id
    // ("basic" is in three of them), and a document-wide lookup would jump to
    // whichever rendered first.
    const target = body.current?.querySelector(`[data-group="${id}"]`)
    target?.scrollIntoView({ block: 'start', behavior: reducedMotion() ? 'auto' : 'smooth' })
  }

  return (
    <div className="cfg">
      {/* Anchors rather than tabs: the sections are one document, and scrolling
          between them is how anyone notices the setting next to the one they
          came for. */}
      <aside className="cfg__rail">
        <button
          type="button"
          className={`cfg__filter${onlyChanged ? ' cfg__filter--on' : ''}`}
          onClick={onToggleOnlyChanged}
          disabled={changed === 0}
          aria-pressed={onlyChanged}
        >
          仅看已修改
          <b>{changed}</b>
        </button>

        <nav className="cfg__anchors" aria-label="配置分组">
          {groups.map((group) => (
            <button
              type="button"
              className="cfg__anchor"
              key={group.id}
              onClick={() => jumpTo(group.id)}
            >
              <span>{group.label}</span>
              <b>{counts[group.id] ?? 0}</b>
            </button>
          ))}
        </nav>

        <p className="cfg__note">{note}</p>
        <p className="cfg__path" title={path}>
          {path}
        </p>
      </aside>

      <div className="cfg__body" ref={body}>
        {children}
      </div>
    </div>
  )
}

/**
 * Rises from the foot while anything is unsaved.
 *
 * The restart is stated once, here, rather than as a badge on every row: these
 * files are read at startup, so it is true of every line in all of them, and a
 * badge that is always on stops being read.
 */
export function ConfigSaveBar({
  changed,
  busy,
  onDiscard,
}: {
  changed: number
  busy: boolean
  onDiscard: () => void
}) {
  if (changed === 0) return null
  return (
    <div className="cfg__savebar" role="status">
      <span className="cfg__savecount">
        <b>{changed}</b> 项更改待保存
      </span>
      <span className="cfg__savenote">重启服务器后生效</span>
      <div className="cfg__saveactions">
        <button className="btn" type="button" onClick={onDiscard} disabled={busy}>
          放弃更改
        </button>
        <button className="btn btn--primary" type="submit" disabled={busy}>
          保存
        </button>
      </div>
    </div>
  )
}

/* -------------------------------------------------------------------- row */

export function ConfigRow({
  setting,
  value,
  unset,
  changed,
  original,
  onChange,
}: {
  setting: ConfigSettingView
  value: string
  /** The file has no such line; the value shown is the server's own default. */
  unset: boolean
  /** This row no longer reads the way the file does. */
  changed: boolean
  /** What the file says, or undefined when it has no such line. */
  original: string | undefined
  onChange: (value: string) => void
}) {
  const hint = [setting.hint, unset ? '当前使用默认值，未写入文件' : null]
    .filter(Boolean)
    .join(' · ')

  // Under the control rather than beside it: the original is read after the new
  // value, as the answer to "what was it before", and putting it in the label
  // turns every changed row into two columns of small text.
  const footnotes = (
    <>
      {changed && (
        <small className="cfg__was">
          原值 <s>{original ?? `（未写入，默认 ${setting.default || '空'}）`}</s>
        </small>
      )}
      {setting.risk && (
        <small className="cfg__risk">
          <span aria-hidden="true">⚠</span> {setting.risk}
        </small>
      )}
      {changed && setting.live && (
        <small className="cfg__live">
          也可以直接在控制台敲 <code>{setting.live}</code> 立即生效，不用等重启。
        </small>
      )}
    </>
  )

  const label = (
    <span className="cfg__name">
      {setting.label}
      {/* The real key beside the label that explains it. People who have edited
          the file by hand know the key and not the label, and people who have
          not need the key the moment they search the wiki for it. */}
      <code className="cfg__key">{setting.key}</code>
      {changed && <span className="badge badge--changed">已修改</span>}
    </span>
  )

  if (setting.type === 'boolean') {
    return (
      <div className={`cfg__row${changed ? ' cfg__row--changed' : ''}`}>
        <label className="checkbox">
          <input
            type="checkbox"
            checked={value === 'true'}
            onChange={(event) => onChange(event.target.checked ? 'true' : 'false')}
          />
          {label}
        </label>
        {hint && <small>{hint}</small>}
        {footnotes}
      </div>
    )
  }

  return (
    <div className={`cfg__row${changed ? ' cfg__row--changed' : ''}`}>
      <label className="field">
        {label}
        {setting.type === 'select' ? (
          <Select
            ariaLabel={setting.label}
            value={value}
            options={[
              // An unset key must not silently become the first option.
              ...(setting.options?.includes(value) ? [] : [{ value, label: value || '(未设置)' }]),
              ...(setting.options ?? []).map((option) => ({ value: option, label: option })),
            ]}
            onChange={onChange}
          />
        ) : (
          <input
            type={setting.type === 'number' ? 'number' : 'text'}
            value={value}
            onChange={(event) => onChange(event.target.value)}
            spellCheck={false}
          />
        )}
        {hint && <small>{hint}</small>}
      </label>
      {footnotes}
    </div>
  )
}

/** Which keys no longer read the way the file does. */
export function changedKeys(
  values: Record<string, string>,
  original: Record<string, string>,
): Set<string> {
  const set = new Set<string>()
  for (const [key, value] of Object.entries(values)) {
    if (value !== original[key]) set.add(key)
  }
  return set
}
