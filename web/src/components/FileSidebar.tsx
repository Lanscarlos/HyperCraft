import type { ReactNode, RefObject } from 'react'

import { formatBytes } from '../format'
import type { FileUsage } from '../types'
import { Glyph } from './Glyph'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'

/**
 * The column that answers "where am I".
 *
 * That is the whole of its job now. It used to answer "what is in here" as
 * well — the tree and the listing shared it, stacked, each collapsible — and
 * the two were fighting over one column's height: the deeper the tree the less
 * room the listing had, at exactly the moment the listing was longest. The
 * listing has moved into the main area, and what is left here is three ways of
 * saying where things are: the tree, a search, and what you had open recently.
 *
 * The three are panels rather than modes. Switching between them changes
 * nothing outside this column — not the main area, not the rail — and each
 * keeps its own scroll position, which is why all three stay mounted and are
 * hidden rather than unmounted. A search you scrolled through, went to look at
 * the tree, and came back to should still be where you left it.
 */
export type SidePanel = 'tree' | 'search' | 'recent'

const PANELS: { id: SidePanel; label: string }[] = [
  { id: 'tree', label: '文件' },
  { id: 'search', label: '搜索' },
  { id: 'recent', label: '最近' },
]

export interface FileSidebarProps {
  collapsed: boolean
  onToggle: () => void
  /** True while the column is floating over the main area instead of standing
   *  beside it. It shadows itself then, and picking a file closes it. */
  overlay: boolean
  panel: SidePanel
  onPanel: (next: SidePanel) => void
  /** 新建文件 / 新建文件夹, the same pair the toolbar's 新建 offers. */
  newItems: MenuItem[]
  onRefresh: () => void
  more: MenuItem[]
  pending: boolean
  /** What the instance weighs, and what the disk under it holds. Null until
   *  the first walk comes back. */
  usage: FileUsage | null
  tree: ReactNode
  search: ReactNode
  recent: ReactNode
  gripRef?: RefObject<HTMLDivElement>
}

export function FileSidebar({
  collapsed,
  onToggle,
  overlay,
  panel,
  onPanel,
  newItems,
  onRefresh,
  more,
  pending,
  usage,
  tree,
  search,
  recent,
}: FileSidebarProps) {
  if (collapsed) {
    // 40px, with one job: get back. Vertical text does not fit in it and a
    // label nobody can read is worse than none, so the button is all there is
    // — and it is the same glyph that folded the column, in the same corner.
    return (
      <div className="fside fside--shut">
        <button
          type="button"
          className="fside__open"
          onClick={onToggle}
          aria-label="展开侧边栏（⌘/Ctrl + B）"
          title="展开侧边栏（⌘/Ctrl + B）"
          aria-expanded={false}
          aria-controls="file-sidebar"
        >
          <Glyph name="sidebar" />
        </button>
      </div>
    )
  }

  return (
    <aside className="fside" id="file-sidebar" data-overlay={overlay || undefined}>
      <header className="fside__head">
        <h2 className="fside__title">资源管理器</h2>
        <div className="fside__acts">
          <Menu className="iconbtn" items={newItems} title="新建" ariaLabel="新建">
            <Glyph name="new-file" />
          </Menu>
          <button
            type="button"
            className="iconbtn"
            onClick={onRefresh}
            disabled={pending}
            title="刷新"
            aria-label="刷新"
          >
            <Glyph name="refresh" className={pending ? 'spin' : undefined} />
          </button>
          <Menu className="iconbtn" items={more} title="更多" ariaLabel="更多">
            <Glyph name="ellipsis" />
          </Menu>
          <button
            type="button"
            className="iconbtn"
            onClick={onToggle}
            aria-label="收起侧边栏（⌘/Ctrl + B）"
            title="收起侧边栏（⌘/Ctrl + B）"
            aria-expanded
            aria-controls="file-sidebar"
          >
            <Glyph name="sidebar" />
          </button>
        </div>
      </header>

      <div className="segmented fside__tabs" role="tablist" aria-label="侧边栏面板">
        {PANELS.map((one) => (
          <button
            key={one.id}
            type="button"
            role="tab"
            className={`segmented__option${panel === one.id ? ' segmented__option--active' : ''}`}
            aria-selected={panel === one.id}
            onClick={() => onPanel(one.id)}
          >
            {one.label}
          </button>
        ))}
      </div>

      {/* All three mounted, one shown. Unmounting the other two would throw
          away their scroll position and, for the search panel, what was typed
          into it — and coming back to a search you had scrolled through is
          most of why the panels are worth having. */}
      <div className="fside__body">
        <div className="fside__panel" hidden={panel !== 'tree'}>
          {tree}
        </div>
        <div className="fside__panel" hidden={panel !== 'search'}>
          {search}
        </div>
        <div className="fside__panel" hidden={panel !== 'recent'}>
          {recent}
        </div>
      </div>

      {usage !== null && <Quota usage={usage} />}
    </aside>
  )
}

/**
 * What this instance weighs, against the disk it is on.
 *
 * Not a quota: the panel has no per-instance limit to enforce and inventing
 * one here would be inventing a feature in a status line. It is the two
 * numbers an operator actually acts on — this server's share, and how much
 * room is left for it to grow into.
 */
function Quota({ usage }: { usage: FileUsage }) {
  const share = usage.diskTotal > 0 ? Math.min(1, usage.bytes / usage.diskTotal) : 0
  return (
    <div className="fside__quota">
      <div className="fside__quota-line">
        <span>实例占用</span>
        <b>
          {formatBytes(usage.bytes)}
          {usage.diskTotal > 0 && ` / ${formatBytes(usage.diskTotal)}`}
        </b>
      </div>
      <div
        className="meter fside__quota-bar"
        role="img"
        aria-label={`实例占用 ${formatBytes(usage.bytes)}，磁盘共 ${formatBytes(usage.diskTotal)}`}
      >
        <span className="meter__fill" style={{ width: `${Math.round(share * 100)}%` }} />
      </div>
    </div>
  )
}
