import { useEffect, useState } from 'react'

import { api } from '../api'
import { toastError } from '../toast'
import type { HostListing, HostShortcut } from '../types'
import { Button } from './Button'
import { Modal } from './Modal'
import { Note } from './Note'
import { Toolbar } from './Toolbar'

interface Props {
  /** Where to open. Empty starts at the panel's own servers directory. In file
   *  mode this may be a file, and the picker opens in the directory holding
   *  it — see parentDirectoryOf. */
  initialPath: string
  onPick: (path: string) => void
  onCancel: () => void
  /** What comes back: the directory being browsed, or a file clicked inside
   *  it. Both modes browse directories the same way — only the answer
   *  differs, and only file mode makes a file row clickable. */
  mode?: 'dir' | 'file'
  title?: string
  lead?: React.ReactNode
  confirmLabel?: string
}

/**
 * Browses directories on the machine the panel runs on.
 *
 * A server directory is an absolute path on the host, and the operator is the
 * only one who knows where their disks are mounted — so this is a picker, not a
 * jail. It reports names only; nothing here reads a file's contents. A path
 * that does not exist yet is a normal choice in directory mode, since creating
 * an instance creates it.
 */
export function PathPicker({
  initialPath,
  onPick,
  onCancel,
  mode = 'dir',
  title = mode === 'file' ? '选择文件' : '选择目录',
  lead,
  confirmLabel = mode === 'file' ? '选择这个文件' : '选择这个目录',
}: Props) {
  // In file mode the caller's value is a file, and listing a file is an error
  // — so open in the directory holding it and preselect it.
  const [path, setPath] = useState(() =>
    mode === 'file' ? parentDirectoryOf(initialPath) : initialPath,
  )
  const [picked, setPicked] = useState(() => (mode === 'file' ? initialPath.trim() : ''))
  const [listing, setListing] = useState<HostListing | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  // What the operator typed into the path box, which only becomes `path` when
  // they submit it — otherwise every keystroke would fire a listing.
  const [typed, setTyped] = useState(initialPath)
  // The shortcut row is kept apart from the listing that delivered it: saving
  // one returns the new list, and re-listing the directory just to redraw a
  // chip would flash the whole picker.
  const [shortcuts, setShortcuts] = useState<HostShortcut[]>([])
  // Whether the row is in edit mode. Off by default: renaming is something you
  // do once, and a column of text boxes is not what you opened this to read.
  const [managing, setManaging] = useState(false)
  const [pinning, setPinning] = useState(false)
  // Bumped to remount the rename boxes, which hold their own value so that a
  // list refresh cannot jump the caret to the end mid-word. Rejecting an edit
  // therefore needs the box rebuilt to show the stored label again.
  const [renameNonce, setRenameNonce] = useState(0)

  useEffect(() => {
    let live = true
    setLoading(true)
    api
      .browseHost(path)
      .then((fetched) => {
        if (!live) return
        setListing(fetched)
        setShortcuts(fetched.shortcuts)
        setTyped(fetched.path)
        setError(null)
        // A file picked in one directory is not an answer about another.
        setPicked((chosen) =>
          chosen !== '' && parentDirectoryOf(chosen) === fetched.path ? chosen : '',
        )
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
  const custom = shortcuts.filter((entry) => entry.custom)
  const pinned = custom.find((entry) => entry.path === current)

  const pin = async () => {
    setPinning(true)
    try {
      setShortcuts((await api.addHostShortcut(current)).shortcuts)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '收藏失败')
    } finally {
      setPinning(false)
    }
  }

  const unpin = async (id: string) => {
    try {
      setShortcuts((await api.removeHostShortcut(id)).shortcuts)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '删除失败')
    }
  }

  // Committed on blur and on Enter rather than per keystroke: every letter
  // typed would otherwise be one write to panel.json.
  const rename = async (entry: HostShortcut, typedLabel: string) => {
    const label = typedLabel.trim()
    if (label === '' || label === entry.label) {
      // Put the box back to the stored label — remounting the input is what
      // does it, since it holds its own value. See the key below.
      setRenameNonce((n) => n + 1)
      return
    }
    try {
      setShortcuts((await api.renameHostShortcut(entry.id!, label)).shortcuts)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '改名失败')
      setRenameNonce((n) => n + 1)
    }
  }

  return (
    <Modal onClose={onCancel}>
      <div className="modal__card modal__card--wide">
        <h2 className="modal__title">{title}</h2>
        {lead && <p className="modal__lead">{lead}</p>}

        <form
          className="picker__path"
          onSubmit={(event) => {
            event.preventDefault()
            // The picker is opened from inside another dialog's form, and a
            // portal does not stop a React event from reaching the component
            // that rendered it. Without this, pressing 前往 to list a directory
            // also submits the dialog behind — which is 创建实例.
            event.stopPropagation()
            setPath(typed.trim())
          }}
        >
          <input
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            spellCheck={false}
            aria-label="路径"
          />
          <Button type="submit">
            前往
          </Button>
        </form>

        {shortcuts.length > 0 && (
          /* --flow because this row wraps as a matter of course: the chips are
             the operator's own saved directories. */
          <Toolbar className="toolbar--flow">
            <span className="toolbar__label">快捷位置</span>
            <div className="toolbar__chips">
              {shortcuts.map((shortcut) => (
                <button
                  key={shortcut.path}
                  type="button"
                  className={`chip${shortcut.path === current ? ' chip--on' : ''}`}
                  onClick={() => setPath(shortcut.path)}
                  title={shortcut.path}
                >
                  {shortcut.label}
                </button>
              ))}
            </div>
            {/* Where servers actually live on a given machine is something only
                its owner knows, and the built-in chips cover the panel's own
                directories and not much else. Without this every instance
                added costs the same walk down the same tree. */}
            <div className="toolbar__tools">
              {pinned ? (
                <button className="link" type="button" onClick={() => void unpin(pinned.id!)}>
                  取消收藏
                </button>
              ) : (
                <button
                  className="link"
                  type="button"
                  disabled={pinning}
                  onClick={() => void pin()}
                >
                  收藏这个目录
                </button>
              )}
              {custom.length > 0 && (
                <button className="link" type="button" onClick={() => setManaging((on) => !on)}>
                  {managing ? '完成' : '改名'}
                </button>
              )}
            </div>
          </Toolbar>
        )}

        {managing && custom.length > 0 && (
          <div className="picker__favs">
            {custom.map((entry) => (
              <div className="picker__fav" key={`${entry.id}-${renameNonce}`}>
                <input
                  className="input-slim"
                  defaultValue={entry.label}
                  maxLength={24}
                  spellCheck={false}
                  aria-label={`${entry.path} 的名字`}
                  onBlur={(event) => void rename(entry, event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      event.preventDefault()
                      event.currentTarget.blur()
                    }
                  }}
                />
                <span className="picker__favpath" title={entry.path}>
                  {entry.path}
                </span>
                <Button type="button" onClick={() => void unpin(entry.id!)}>
                  删除
                </Button>
              </div>
            ))}
          </div>
        )}

        {error && <div className="alert">{error}</div>}
        {listing && !listing.exists && (
          <Note tone={mode === 'file' ? 'warn' : 'ok'}>
            {mode === 'file'
              ? '这个目录不存在。'
              : '这个目录还不存在，选它会在创建实例时一并建好。'}
          </Note>
        )}
        {listing?.error && (
          <Note tone="error">读不了这个目录：{listing.error}</Note>
        )}

        <div className="picker__list">
          {listing?.parent && (
            <button className="picker__row" type="button" onClick={() => setPath(listing.parent)}>
              <span className="file-icon">↑</span>
              <span className="picker__name">上级目录</span>
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
              <span className="picker__name">{entry.name}</span>
            </button>
          ))}
          {files.map((entry) =>
            mode === 'file' ? (
              <button
                key={entry.path}
                className={`picker__row${entry.path === picked ? ' picker__row--on' : ''}`}
                type="button"
                aria-pressed={entry.path === picked}
                onClick={() => setPicked(entry.path)}
                onDoubleClick={() => onPick(entry.path)}
              >
                <span className="file-icon">📄</span>
                <span className="picker__name">{entry.name}</span>
              </button>
            ) : (
              /* Listed but inert: a directory that already holds a server
                 should not look empty, and nothing here picks a file. */
              <div key={entry.path} className="picker__row picker__row--plain">
                <span className="file-icon">📄</span>
                <span className="picker__name">{entry.name}</span>
              </div>
            ),
          )}
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
          <Button type="button" onClick={onCancel}>
            取消
          </Button>
          <Button
            variant="primary"
            type="button"
            disabled={mode === 'file' && picked === ''}
            onClick={() => onPick(mode === 'file' ? picked : current)}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/** The directory holding a path, guessed from whichever separator it uses.
 *
 *  Deliberately cheap: it runs before the first listing has answered, so the
 *  host's real separator is not known yet, and it only has to be good enough to
 *  open the picker near the right place — the operator navigates from wherever
 *  it lands. */
function parentDirectoryOf(path: string): string {
  const trimmed = path.trim()
  const cut = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  if (cut > 0) return trimmed.slice(0, cut)
  // A separator at index 0 means the file sits at the filesystem root, whose
  // own name is that separator — slicing it off would leave "" and start the
  // picker at the panel's servers directory instead.
  if (cut === 0) return trimmed.slice(0, 1)
  return trimmed
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
  /** Extra classes for the field. A path plus a browse button is the widest
   *  thing on any of these forms, so callers rarely need to narrow it. */
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
        <Button
          type="button"
          onClick={() => setPicking(true)}
          disabled={disabled}
        >
          浏览…
        </Button>
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
