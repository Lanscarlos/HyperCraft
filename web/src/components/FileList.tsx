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
import type { MenuItem } from './Menu'

/**
 * What is in the directory the tree is standing in.
 *
 * Two densities rather than a table that quietly loses columns. The listing
 * used to drop 修改时间 the moment a file was opened beside it, which reads as
 * a rendering bug from the one seat that matters — somebody looking for the
 * file the server touched last, in a column that was there a second ago. A
 * column appears and disappears because a person asked it to, and the thing
 * they ask with is the 紧凑 / 详情 control in this panel's own head.
 */

export type Density = 'compact' | 'detail'
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
  density: Density
  onDensity: (next: Density) => void
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
  onClearSelection: () => void
  /** The file in front of the editor, so its row is marked. */
  activePath: string | null
  /** Open files with unsaved edits: the row carries the same dot the tab does,
   *  because a tab scrolled out of the strip is one you can walk away from. */
  dirtyPaths: Set<string>
  onOpen: (entry: FileEntry) => void
  onOpenBackground: (entry: FileEntry) => void
  onRename: (entry: FileEntry) => void
  onDelete: (entry: FileEntry) => void
  onMove: (entries: FileEntry[]) => void
  onDownload: (entries: FileEntry[]) => void
  onBulkDelete: (entries: FileEntry[]) => void
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
  density,
  onDensity,
  sort,
  onSort,
  selected,
  cursor,
  onSelect,
  onClearSelection,
  activePath,
  dirtyPaths,
  onOpen,
  onOpenBackground,
  onRename,
  onDelete,
  onMove,
  onDownload,
  onBulkDelete,
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

  const picked = entries.filter((entry) => selected.has(entry.path))
  const pickedBytes = picked.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0)

  const rowMenu = (entry: FileEntry): MenuItem[] => [
    {
      label: '重命名',
      disabled: busy || !entry.writable,
      onSelect: () => onRename(entry),
    },
    {
      label: '复制路径',
      onSelect: () => {
        void navigator.clipboard?.writeText(entry.path)
      },
    },
    {
      label: '移动到…',
      disabled: busy || !entry.writable,
      onSelect: () => onMove([entry]),
    },
    // Only for something the editor can actually hold. A jar opened "in a new
    // tab" would be a tab showing a download card nobody asked for.
    ...(entry.isDir || !entry.editable
      ? []
      : [
          {
            label: '在新标签打开',
            onSelect: () => onOpenBackground(entry),
          },
        ]),
    {
      label: '删除',
      danger: true,
      disabled: busy || !entry.writable,
      onSelect: () => onDelete(entry),
    },
  ]

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
      {/* The head is one row that says one of two things: what this panel is,
          or what is about to happen to the things ticked in it. They never
          coexist — a bulk bar stacked under a title is a second row of
          furniture above a list that has just lost half its height. */}
      {picked.length > 0 ? (
        <div className="flist__bulk">
          <span className="flist__bulk-count">
            已选 {picked.length} 项 · {formatBytes(pickedBytes)}
          </span>
          <Button size="small" disabled={busy} onClick={() => onDownload(picked)}>
            下载
          </Button>
          <Button size="small" disabled={busy || !writable} onClick={() => onMove(picked)}>
            移动到…
          </Button>
          <Button
            size="small"
            variant="danger"
            disabled={busy || !writable}
            onClick={() => onBulkDelete(picked)}
          >
            删除
          </Button>
          <button type="button" className="link flist__bulk-clear" onClick={onClearSelection}>
            取消选择
          </button>
        </div>
      ) : (
        <div className="flist__head">
          <span className="flist__where">当前目录</span>
          <div className="segmented segmented--inline" role="group" aria-label="列表密度">
            <button
              type="button"
              className={`segmented__option${
                density === 'compact' ? ' segmented__option--active' : ''
              }`}
              aria-pressed={density === 'compact'}
              onClick={() => onDensity('compact')}
            >
              紧凑
            </button>
            <button
              type="button"
              className={`segmented__option${
                density === 'detail' ? ' segmented__option--active' : ''
              }`}
              aria-pressed={density === 'detail'}
              onClick={() => onDensity('detail')}
            >
              详情
            </button>
          </div>
        </div>
      )}

      {/* The column heads stay in both densities, and they are the only way to
          change the ordering. Hiding them in 紧凑 would put the sort behind a
          density switch, which is the shape this rewrite is removing. */}
      <div className="flist__cols" data-density={density}>
        <span className="flist__col flist__col--mark" aria-hidden="true" />
        <SortHead label="名称" column="name" sort={sort} onSort={onSort} />
        <SortHead label="大小" column="size" sort={sort} onSort={onSort} end />
        {density === 'detail' && (
          <>
            <SortHead label="修改时间" column="modified" sort={sort} onSort={onSort} />
            <span className="flist__col flist__col--ops" aria-hidden="true" />
          </>
        )}
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
          // the tree beside it is still a way out.
          <div className="alert alert--error flist__error">
            <span>{error}</span>
            <Button size="small" onClick={onRetry}>
              重试
            </Button>
          </div>
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
              density={density}
              ticked={selected.has(entry.path)}
              current={entry.path === activePath}
              cursored={entry.path === cursor}
              dirty={dirtyPaths.has(entry.path)}
              busy={busy}
              onSelect={onSelect}
              onOpen={onOpen}
              menu={rowMenu(entry)}
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
  density,
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
  density: Density
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
      data-density={density}
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

      <span className="frow__name" title={entry.name}>
        {entry.name}
        {dirty && <span className="frow__dot" aria-label="有未保存的修改" />}
        {entry.symlink && <Badge>符号链接</Badge>}
      </span>

      <span className="frow__size num">{entry.isDir ? '—' : formatBytes(entry.size)}</span>

      {density === 'detail' && (
        <time className="frow__time" dateTime={entry.modified} title={stamp(entry.modified)}>
          {formatSince(entry.modified)}
        </time>
      )}

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
