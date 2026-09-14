import { useRef, useState } from 'react'
import type { RefObject } from 'react'

import { downloadURL } from '../api'
import { formatBytes, formatSince } from '../format'
import type { FileEntry } from '../types'
import { Badge } from './Badge'
import { Button } from './Button'
import { EmptyState } from './EmptyState'
import { FileIcon } from './FileIcon'
import { Glyph } from './Glyph'
import { Menu } from './Menu'
import { Note } from './Note'
import type { MenuItem } from './Menu'

/**
 * What is in the directory you are standing in — the main area's other form.
 *
 * It had two densities, 紧凑 and 详情, and the reason was that it lived in a
 * 264-380px rail where four columns did not fit. The choice was really "which
 * columns do I give up", and it had to be made by hand because the pane could
 * not know. In the main area there is room for all four at every width worth
 * having, so there is one form and 修改时间 is simply there — which is what
 * somebody looking for the file the server touched last was after.
 *
 * Narrow windows still shed a column, but as a breakpoint rather than a
 * switch, and they shed the one whose answer is also in the row's tooltip.
 */

export type SortKey = 'name' | 'size' | 'modified'

export interface Sort {
  key: SortKey
  asc: boolean
}

/** How a click on a row changes the selection. */
export type SelectMode = 'replace' | 'toggle' | 'range'

export interface FileListProps {
  instanceId: string
  dir: string
  /** Already filtered and ordered — the pane owns both, because the bar's
   *  search box and this panel's column heads feed the same list. */
  entries: FileEntry[]
  /** How many the directory holds before filtering, so the foot can say what
   *  the filter is hiding. */
  total: number
  sort: Sort
  onSort: (key: SortKey) => void
  /** Ticked rows — what the bulk bar acts on. A plain click is not in here:
   *  see `cursor`. */
  selected: Set<string>
  /** The row the keyboard is on, and the last one plainly clicked. Distinct
   *  from `selected` because a click both picks a row and opens it, and
   *  turning the head into a bulk bar every time somebody opens a file is a
   *  bar nobody asked for standing where the density switch was. */
  cursor: string | null
  onSelect: (path: string, mode: SelectMode) => void
  /** The file in front of the editor, so its row is marked. */
  activePath: string | null
  /** Open files with unsaved edits: the row carries the same dot the tab does,
   *  because a tab scrolled out of the strip is one you can walk away from. */
  dirtyPaths: Set<string>
  onOpen: (entry: FileEntry) => void
  /** The row's ⋯ menu, built by the pane. It is the pane's rather than this
   *  component's because the tree's right-click menu has to be the same items
   *  in the same order, and the only way to be sure of that is for there to be
   *  one list. */
  menuFor: (entry: FileEntry) => MenuItem[]
  onUpload: () => void
  onDropFiles: (files: File[]) => void
  onRetry: () => void
  onClearQuery: () => void
  query: string
  error: string | null
  pending: boolean
  busy: boolean
  writable: boolean
  bodyRef: RefObject<HTMLDivElement>
}

/** What a directory rule refuses, on the control rather than as a banner. */
const NOT_YOURS = '这一项不在你的角色允许的范围内'

export function FileList({
  instanceId,
  dir,
  entries,
  total,
  sort,
  onSort,
  selected,
  cursor,
  onSelect,
  activePath,
  dirtyPaths,
  onOpen,
  menuFor,
  onUpload,
  onDropFiles,
  onRetry,
  onClearQuery,
  query,
  error,
  pending,
  busy,
  writable,
  bodyRef,
}: FileListProps) {
  const [dropping, setDropping] = useState(false)
  // Drag events fire on every element the pointer crosses, so one drag across
  // the panel is a stream of enter/leave pairs. Counting them is what keeps
  // the drop hint from flickering all the way down the list.
  const depth = useRef(0)

  return (
    <section
      className="flist"
      data-dropping={dropping || undefined}
      onDragEnter={(event) => {
        if (!hasFiles(event.dataTransfer)) return
        depth.current += 1
        setDropping(true)
      }}
      onDragOver={(event) => {
        if (!hasFiles(event.dataTransfer)) return
        event.preventDefault()
      }}
      onDragLeave={() => {
        depth.current = Math.max(0, depth.current - 1)
        if (depth.current === 0) setDropping(false)
      }}
      onDrop={(event) => {
        if (!hasFiles(event.dataTransfer)) return
        event.preventDefault()
        depth.current = 0
        setDropping(false)
        onDropFiles(Array.from(event.dataTransfer.files))
      }}
    >
      {/* The column heads are the only way to change the ordering, so they are
          always here. They share one grid with the rows below rather than
          declaring their own tracks: a header whose columns are stated
          separately from its rows is a header that goes out of line the first
          time a filename is long. */}
      <div className="flist__cols">
        <span className="flist__col flist__col--mark" aria-hidden="true" />
        <SortHead label="名称" column="name" sort={sort} onSort={onSort} />
        <SortHead label="大小" column="size" sort={sort} onSort={onSort} end />
        <SortHead label="修改时间" column="modified" sort={sort} onSort={onSort} />
        <span className="flist__col flist__col--ops" aria-hidden="true" />
      </div>

      <div
        className="flist__body"
        ref={bodyRef}
        data-pending={pending || undefined}
        role="listbox"
        aria-label="文件列表"
        aria-multiselectable
        tabIndex={-1}
      >
        {error ? (
          // Inline, not a screen of its own: what failed is one directory, and
          // the tree beside it is still a way out. A Note rather than .alert —
          // it is a condition this panel is in, and .alert is the one slot
          // under a page head for that page's own load failure.
          <Note tone="error" className="flist__error">
            <span>{error}</span>
            <Button size="small" onClick={onRetry}>
              重试
            </Button>
          </Note>
        ) : entries.length === 0 ? (
          query ? (
            <EmptyState
              title={`没有匹配「${query}」的文件`}
              action={<Button onClick={onClearQuery}>清除筛选</Button>}
            >
              搜索只看当前目录。要找的东西可能在下一层。
            </EmptyState>
          ) : (
            <EmptyState
              title="这个目录还是空的"
              action={
                writable ? (
                  <Button variant="primary" onClick={onUpload}>
                    上传文件
                  </Button>
                ) : undefined
              }
            >
              {writable ? '把文件拖进来，或者' : NOT_YOURS}
            </EmptyState>
          )
        ) : (
          entries.map((entry) => (
            <Row
              key={entry.path}
              entry={entry}
              instanceId={instanceId}
              ticked={selected.has(entry.path)}
              current={entry.path === activePath}
              cursored={entry.path === cursor}
              dirty={dirtyPaths.has(entry.path)}
              busy={busy}
              onSelect={onSelect}
              onOpen={onOpen}
              menu={menuFor(entry)}
            />
          ))
        )}
      </div>

      <div className="flist__foot">
        <span>
          {total} 项
          {query && entries.length !== total && ` · 筛选出 ${entries.length} 项`}
        </span>
      </div>

      {dropping && (
        <div className="flist__drop" aria-hidden="true">
          <Glyph name="upload" className="flist__drop-glyph" />
          松手上传到 <b>{dir === '' ? '实例根目录' : dir}</b>
        </div>
      )}
    </section>
  )
}

function Row({
  entry,
  instanceId,
  ticked,
  current,
  cursored,
  dirty,
  busy,
  onSelect,
  onOpen,
  menu,
}: {
  entry: FileEntry
  instanceId: string
  ticked: boolean
  current: boolean
  cursored: boolean
  dirty: boolean
  busy: boolean
  onSelect: (path: string, mode: SelectMode) => void
  onOpen: (entry: FileEntry) => void
  menu: MenuItem[]
}) {
  return (
    <div
      className="frow"
      data-ticked={ticked || undefined}
      data-on={current || undefined}
      data-cursor={cursored || undefined}
      role="option"
      aria-selected={ticked}
      tabIndex={-1}
      onClick={(event) => {
        // Shift is a range, ⌘/Ctrl adds one, and a plain click is "this one" —
        // which also opens it, because in a file manager picking a thing and
        // opening it are the same gesture. A plain click ticks nothing, which
        // is what keeps the bulk bar out of the head of a list somebody is
        // merely reading. Double click is deliberately not a second meaning:
        // one click, one outcome.
        if (event.shiftKey) {
          onSelect(entry.path, 'range')
          return
        }
        if (event.metaKey || event.ctrlKey) {
          onSelect(entry.path, 'toggle')
          return
        }
        onSelect(entry.path, 'replace')
        onOpen(entry)
      }}
    >
      {/* The icon and the tick share one box and trade places. Two boxes would
          mean the row gets wider under the pointer, and a row that moves as
          you reach for it is a row you miss. */}
      <span className="frow__mark">
        <FileIcon name={entry.name} dir={entry.isDir} />
        <input
          type="checkbox"
          className="tick frow__tick"
          checked={ticked}
          aria-label={`选择 ${entry.name}`}
          onClick={(event) => event.stopPropagation()}
          onChange={() => onSelect(entry.path, 'toggle')}
        />
      </span>

      {/* The name is in a child of its own, not a bare text node in the flex
          row: a flex container cannot truncate what it does not directly hold,
          so the ellipsis has to live with the text. Same trap .ftree__label
          is written the way it is to avoid. */}
      <span className="frow__name" title={entry.name}>
        <span className="frow__text">{entry.name}</span>
        {dirty && <span className="frow__dot" aria-label="有未保存的修改" />}
        {entry.symlink && <Badge>符号链接</Badge>}
      </span>

      <span className="frow__size num">{entry.isDir ? '—' : formatBytes(entry.size)}</span>

      <time className="frow__time" dateTime={entry.modified} title={stamp(entry.modified)}>
        {formatSince(entry.modified)}
      </time>

      {/* Off until the pointer or the keyboard is on this row. Twenty rows of
          three standing icons is sixty icons competing with the filenames, and
          one of the three deletes a world folder without leaving the row. */}
      <span className="frow__ops">
        {/* Files only: the daemon has no endpoint that packs a directory, and
            downloading a world folder one file at a time from the browser is
            not the same offer. */}
        {!entry.isDir && (
          <a
            className="iconbtn"
            href={downloadURL(instanceId, entry.path)}
            download
            title="下载"
            aria-label={`下载 ${entry.name}`}
            onClick={(event) => event.stopPropagation()}
          >
            <Glyph name="download" />
          </a>
        )}
        <span onClick={(event) => event.stopPropagation()}>
          <Menu
            className="iconbtn"
            items={menu}
            title={busy ? undefined : `${entry.name} 的操作`}
            ariaLabel={`${entry.name} 的操作`}
          >
            <Glyph name="ellipsis" />
          </Menu>
        </span>
      </span>
    </div>
  )
}

function SortHead({
  label,
  column,
  sort,
  onSort,
  end,
}: {
  label: string
  column: SortKey
  sort: Sort
  onSort: (key: SortKey) => void
  end?: boolean
}) {
  const active = sort.key === column
  return (
    <span
      className={`flist__col${end ? ' flist__col--end' : ''}`}
      // Which column this is, so the narrow-screen rules can shed it by name
      // rather than by counting children — the tracks and the heads are one
      // grid, and a :nth-child that drifts out of step with them is a header
      // sitting over the wrong column.
      data-col={column}
      aria-sort={active ? (sort.asc ? 'ascending' : 'descending') : 'none'}
    >
      <button type="button" className="flist__sort" onClick={() => onSort(column)}>
        {label}
        <span className="flist__mark" aria-hidden="true" data-off={!active || undefined}>
          {active && !sort.asc ? '▼' : '▲'}
        </span>
      </button>
    </span>
  )
}

/** The full stamp for a row's tooltip. formatDate drops seconds because a
 *  column does not need them; a tooltip is where the exact answer goes. */
function stamp(iso: string): string {
  const at = new Date(iso)
  return Number.isNaN(at.getTime()) ? '' : at.toLocaleString('zh-CN', { hour12: false })
}

function hasFiles(transfer: DataTransfer | null): boolean {
  // Dragging selected text across the page is not an upload.
  return transfer != null && Array.from(transfer.types).includes('Files')
}
