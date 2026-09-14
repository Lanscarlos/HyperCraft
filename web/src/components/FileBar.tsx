import { useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

import { formatBytes } from '../format'
import { Button } from './Button'
import { Glyph } from './Glyph'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'
import { ToolbarSearch } from './Toolbar'

/**
 * The one bar across the top of the file pane.
 *
 * It replaces a page title and a sentence of prose ("服务器目录里的东西：jar、
 * 存档、配置和日志…"), which is a hundred pixels of vertical room spent saying
 * something true exactly once. The path is what a file manager's head is for.
 *
 * The same buttons, in the same order, in the same shape, whatever else is on
 * screen. The two toolbars this replaces had different counts, different
 * orders and different amounts of text depending on a mode, which is the
 * reliable way to break the muscle memory a daily tool runs on.
 */
export interface FileBarProps {
  dir: string
  /** What is in the directory, for the chip. Null while it is being read. */
  stats: { count: number; bytes: number } | null
  query: string
  onQuery: (next: string) => void
  searchRef: RefObject<HTMLInputElement>
  /** Walks to a directory, answering whether it was there. False leaves the
   *  bar in its editing state with the message beside what was typed. */
  onNavigate: (next: string) => Promise<boolean>
  onUpload: () => void
  onNewFile: () => void
  onNewFolder: () => void
  onRefresh: () => void
  /** The overflow menu, owned by the pane: it holds things about the page
   *  rather than about the directory. */
  more: MenuItem[]
  busy: boolean
  pending: boolean
  /** False in a folder a confined role may walk through but not write in. The
   *  buttons stay put and say why rather than disappearing — a toolbar that
   *  changes shape per directory is the thing this bar exists to stop. */
  writable: boolean
}

/** What a directory rule refuses, said on the button rather than as a banner:
 *  the question people ask is "why is this greyed out", with the pointer
 *  already on it. */
const READ_ONLY_HERE = '这个目录不在你的角色允许的范围内'

export function FileBar({
  dir,
  stats,
  query,
  onQuery,
  searchRef,
  onNavigate,
  onUpload,
  onNewFile,
  onNewFolder,
  onRefresh,
  more,
  busy,
  pending,
  writable,
}: FileBarProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(dir)
  const [bad, setBad] = useState<string | null>(null)
  const field = useRef<HTMLInputElement | null>(null)

  // Whatever the crumbs are showing is what the box opens with, including
  // after a jump somebody else triggered. Without this, pressing the blank
  // twice in two different directories offers the first one's path.
  useEffect(() => {
    setDraft(dir)
    setBad(null)
  }, [dir])

  useEffect(() => {
    if (!editing) return
    field.current?.focus()
    field.current?.select()
  }, [editing])

  const leave = () => {
    setEditing(false)
    setDraft(dir)
    setBad(null)
  }

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const target = draft.trim().replace(/^\/+|\/+$/g, '')
    if (await onNavigate(target)) {
      setEditing(false)
      setBad(null)
      return
    }
    // Not a navigation that failed — a path that is not there. Said here,
    // beside what was typed, because that is where the eye already is, and
    // because a toast for this would take the typed path off screen with it.
    setBad('这个目录不存在')
  }

  return (
    <header className="fbar">
      <Button
        icon
        className="fbar__up"
        aria-label="返回上一级"
        title="返回上一级（Backspace）"
        disabled={dir === '' || pending}
        onClick={() => void onNavigate(parentOf(dir))}
      >
        <Glyph name="left" />
      </Button>

      {editing ? (
        <form className="fbar__path" onSubmit={submit}>
          <input
            ref={field}
            className="fbar__input"
            value={draft}
            spellCheck={false}
            autoComplete="off"
            aria-label="目录路径"
            aria-invalid={bad !== null}
            placeholder="plugins/Vulpecula"
            onChange={(event) => {
              setDraft(event.target.value)
              setBad(null)
            }}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.stopPropagation()
                leave()
              }
            }}
            onBlur={leave}
          />
          {bad && (
            <small className="fbar__bad" role="alert">
              {bad}
            </small>
          )}
        </form>
      ) : (
        <nav className="fbar__crumbs" aria-label="目录路径">
          <Crumbs dir={dir} onNavigate={(next) => void onNavigate(next)} />
          {/* The click target that turns the trail into a box. A path copied
              out of a log is the common way anybody arrives at a deep
              directory, and before this the only way to use one was to walk
              it a folder at a time. */}
          <button
            type="button"
            className="fbar__blank"
            aria-label="编辑路径"
            title="点击可粘贴完整路径"
            onClick={() => setEditing(true)}
          >
            <span className="fbar__hint">点这里可粘贴路径</span>
          </button>
        </nav>
      )}

      {stats && (
        <span className="fbar__stats">
          {stats.count} 项 · {formatBytes(stats.bytes)}
        </span>
      )}

      <div className="fbar__tools">
        <ToolbarSearch
          ref={searchRef}
          className="fbar__search"
          value={query}
          placeholder="搜索当前目录"
          aria-label="搜索当前目录"
          onChange={(event) => onQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Escape') onQuery('')
          }}
        />
        <Button
          onClick={onUpload}
          disabled={busy || !writable}
          title={writable ? '上传到当前目录' : READ_ONLY_HERE}
        >
          <Glyph name="upload" />
          上传
        </Button>
        <Menu
          className="btn fbar__new"
          title={writable ? '新建' : READ_ONLY_HERE}
          ariaLabel="新建"
          items={[
            { label: '新建文件', disabled: busy || !writable, onSelect: onNewFile },
            { label: '新建文件夹', disabled: busy || !writable, onSelect: onNewFolder },
          ]}
        >
          <Glyph name="new-file" />
          新建
          <Glyph name="chevron" className="fbar__caret" />
        </Menu>
        <Button
          icon
          onClick={onRefresh}
          disabled={busy || pending}
          title="刷新"
          aria-label="刷新"
        >
          <Glyph name="refresh" className={pending ? 'spin' : undefined} />
        </Button>
        <Menu className="btn btn--icon" items={more} title="更多" ariaLabel="更多">
          <Glyph name="ellipsis" />
        </Menu>
      </div>
    </header>
  )
}

/**
 * The trail itself.
 *
 * Past three levels the middle is dropped rather than wrapped: the bar is one
 * row tall by design, and a trail that wraps takes the toolbar down with it.
 * The first crumb and the last two survive, which are the two ends anybody
 * navigates by — where the tree starts, and where you are.
 */
function Crumbs({ dir, onNavigate }: { dir: string; onNavigate: (next: string) => void }) {
  const parts = dir === '' ? [] : dir.split('/')
  const folded = parts.length > 3

  const crumb = (label: string, target: string, here: boolean) => (
    <button
      key={target || 'root'}
      type="button"
      className={`fbar__crumb${here ? ' fbar__crumb--here' : ''}`}
      onClick={() => onNavigate(target)}
      aria-current={here ? 'page' : undefined}
      title={target || '实例根目录'}
    >
      {label}
    </button>
  )

  const shown = folded ? parts.slice(parts.length - 2) : parts
  const offset = folded ? parts.length - 2 : 0

  return (
    <>
      {crumb('实例根目录', '', parts.length === 0)}
      {folded && (
        <>
          <span className="fbar__sep" aria-hidden="true">
            /
          </span>
          <span className="fbar__fold" title={parts.slice(0, parts.length - 2).join('/')}>
            …
          </span>
        </>
      )}
      {shown.map((part, index) => {
        const at = offset + index
        return (
          <span className="fbar__step" key={`${part}-${at}`}>
            <span className="fbar__sep" aria-hidden="true">
              /
            </span>
            {crumb(part, parts.slice(0, at + 1).join('/'), at === parts.length - 1)}
          </span>
        )
      })}
    </>
  )
}

function parentOf(dir: string): string {
  const index = dir.lastIndexOf('/')
  return index < 0 ? '' : dir.slice(0, index)
}
