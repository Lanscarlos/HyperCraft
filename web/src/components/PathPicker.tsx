import { useEffect, useState } from 'react'

import { api } from '../api'
import type { HostListing } from '../types'
import { Modal } from './Modal'

interface Props {
  /** Where to open. Empty starts at the panel's own servers directory. */
  initialPath: string
  onPick: (path: string) => void
  onCancel: () => void
}

/**
 * Browses directories on the machine the panel runs on.
 *
 * A server directory is an absolute path on the host, and the operator is the
 * only one who knows where their disks are mounted — so this is a picker, not a
 * jail. It lists names only; nothing here opens a file. A path that does not
 * exist yet is a normal choice, since creating an instance creates it.
 */
export function PathPicker({ initialPath, onPick, onCancel }: Props) {
  const [path, setPath] = useState(initialPath)
  const [listing, setListing] = useState<HostListing | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  // What the operator typed into the path box, which only becomes `path` when
  // they submit it — otherwise every keystroke would fire a listing.
  const [typed, setTyped] = useState(initialPath)

  useEffect(() => {
    let live = true
    setLoading(true)
    api
      .browseHost(path)
      .then((fetched) => {
        if (!live) return
        setListing(fetched)
        setTyped(fetched.path)
        setError(null)
      })
      .catch((err) => live && setError(err instanceof Error ? err.message : '读取目录失败'))
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
  }, [path])

  const current = listing?.path ?? path
  const directories = listing?.entries.filter((entry) => entry.isDir) ?? []
  const files = listing?.entries.filter((entry) => !entry.isDir) ?? []

  return (
    <Modal onClose={onCancel}>
      <div className="modal__card modal__card--wide">
        <h2 className="modal__title">选择目录</h2>

        <form
          className="picker__path"
          onSubmit={(event) => {
            event.preventDefault()
            setPath(typed.trim())
          }}
        >
          <input
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            spellCheck={false}
            aria-label="路径"
          />
          <button className="btn" type="submit">
            前往
          </button>
        </form>

        {listing && listing.shortcuts.length > 0 && (
          <div className="chart-filters">
            <span className="chart-filters__label">快捷位置</span>
            {listing.shortcuts.map((shortcut) => (
              <button
                key={shortcut.path}
                type="button"
                className={`chip${shortcut.path === current ? ' chip--active' : ''}`}
                onClick={() => setPath(shortcut.path)}
                title={shortcut.path}
              >
                {shortcut.label}
              </button>
            ))}
          </div>
        )}

        {error && <div className="alert alert--error">{error}</div>}
        {listing && !listing.exists && (
          <div className="alert alert--ok">
            这个目录还不存在，选它会在创建实例时一并建好。
          </div>
        )}
        {listing?.error && (
          <div className="alert alert--error">读不了这个目录：{listing.error}</div>
        )}

        <div className="picker__list">
          {listing?.parent && (
            <button className="picker__row" type="button" onClick={() => setPath(listing.parent)}>
              <span className="file-icon">↑</span>
              <span className="sidebar__name">上级目录</span>
            </button>
          )}
          {directories.map((entry) => (
            <button
              key={entry.path}
              className="picker__row"
              type="button"
              onClick={() => setPath(entry.path)}
            >
              <span className="file-icon">📁</span>
              <span className="sidebar__name">{entry.name}</span>
            </button>
          ))}
          {files.map((entry) => (
            <div key={entry.path} className="picker__row picker__row--plain">
              <span className="file-icon">📄</span>
              <span className="sidebar__name">{entry.name}</span>
            </div>
          ))}
          {!loading && listing?.exists && listing.entries.length === 0 && (
            <p className="muted">这个目录是空的。</p>
          )}
          {loading && <p className="muted">正在读取…</p>}
        </div>

        <p className="chart-note">
          {listing?.truncated && '目录里的文件太多，只显示了前一部分。'}
          {listing && listing.jars.length > 0 && ` 这里有 ${listing.jars.length} 个 jar 文件。`}
        </p>

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel}>
            取消
          </button>
          <button className="btn btn--primary" type="button" onClick={() => onPick(current)}>
            选择这个目录
          </button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * The directory field plus its 「浏览…」 button, used by both the create dialog
 * and the launch settings so they behave identically.
 */
export function DirectoryField({
  value,
  onChange,
  disabled,
  label = '服务器目录',
  hint,
  placeholder,
  className,
}: {
  value: string
  onChange: (path: string) => void
  disabled?: boolean
  label?: string
  hint?: React.ReactNode
  placeholder?: string
  /** Extra classes for the field, e.g. `field--full` inside a column layout —
   *  a path plus a browse button is the widest thing on any of these forms. */
  className?: string
}) {
  const [picking, setPicking] = useState(false)

  return (
    <div className={className ? `field ${className}` : 'field'}>
      <span>{label}</span>
      <div className="field__with-button">
        <input
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          spellCheck={false}
        />
        <button
          className="btn"
          type="button"
          onClick={() => setPicking(true)}
          disabled={disabled}
        >
          浏览…
        </button>
      </div>
      {hint && <small>{hint}</small>}

      {picking && (
        <PathPicker
          initialPath={value.trim()}
          onCancel={() => setPicking(false)}
          onPick={(picked) => {
            onChange(picked)
            setPicking(false)
          }}
        />
      )}
    </div>
  )
}
