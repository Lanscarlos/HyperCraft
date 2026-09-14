import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'

import { api } from '../api'
import { readPref, writePref } from '../localPrefs'
import type { FileEntry } from '../types'
import { FileIcon } from './FileIcon'
import { Icon } from './Icon'
import type { MenuItem } from './Menu'

/**
 * The tree in the sidebar: folders and the files inside them.
 *
 * It held folders only for a long time, and the reason was width. At 216px the
 * filenames it gained arrived pre-truncated — `banned-players...`,
 * `version_histor...` — which is a worse listing than the one it was standing
 * in for. The sidebar is 300px now and the listing has moved into the main
 * area, so both halves of that trade have changed: the truncation is mostly
 * gone, and the tree is no longer standing in for anything. What it gained
 * instead is the question the listing can no longer answer from over there —
 * which files do I have open, and where are they — so open files are marked
 * here and the current one is marked differently again.
 *
 * Children are fetched when a node is first opened and then kept: walking back
 * up and down a tree is the single most common thing anyone does on this page,
 * and re-reading plugins/ every time it is expanded makes the tree feel slower
 * than the ".." it replaced. 刷新 on the toolbar drops the cache.
 *
 * Which folders are open is remembered per instance. A tree that is collapsed
 * every time you come back is a tree you re-walk every time you come back.
 */

interface Props {
  instanceId: string
  /** The directory the listing is showing, so the tree can mark it. */
  path: string
  onOpen: (path: string) => void
  /** Bumped by 刷新, to drop what was cached. */
  reloadKey?: number
  /** Picking a folder here is the whole point of the 移动到… dialog, which
   *  borrows this component rather than growing a second tree. Folders only in
   *  that form: a dialog asking "into which folder" must not offer a file. */
  compact?: boolean
  /** Opening a file, as opposed to walking into a folder. Absent in the
   *  picker, which is also what turns the files off. */
  onOpenFile?: (entry: FileEntry) => void
  /** Every path with a tab open, and the one in front of them. */
  openPaths?: Set<string>
  activePath?: string | null
  /** The right-click menu for a row. The same function builds the ⋯ menu in the
   *  main listing, which is what keeps the two identical — the design note
   *  asks for the same items in the same order, and the only way to be sure of
   *  that is for there to be one list. */
  menuFor?: (entry: FileEntry) => MenuItem[]
}

/** One row of the tree. Exported because the file pane builds nodes of its own
 *  to hand around. */
export interface TreeNode {
  name: string
  path: string
  isDir: boolean
  /** The listing row this came from, for the menu and for opening. Absent on
   *  the synthetic root. */
  entry?: FileEntry
}

export function FileTree({
  instanceId,
  path,
  onOpen,
  reloadKey,
  compact,
  onOpenFile,
  openPaths,
  activePath,
  menuFor,
}: Props) {
  // Folders only in the picker. Elsewhere the files are the point.
  const files = onOpenFile !== undefined
  const [context, setContext] = useState<{ x: number; y: number; entry: FileEntry } | null>(null)
  const [children, setChildren] = useState<Record<string, TreeNode[]>>({})
  const [loading, setLoading] = useState<Set<string>>(new Set())

  // The root is always open — a tree whose only row is a collapsed root is a
  // tree with nothing in it — so it is in the fallback rather than a special
  // case further down.
  const prefKey = `hc.files.tree.${instanceId}`
  const [open, setOpen] = useState<Set<string>>(() => new Set(readPref<string[]>(prefKey, [''])))

  useEffect(() => {
    writePref(prefKey, [...open])
  }, [prefKey, open])

  const read = useCallback(
    async (dir: string) => {
      setLoading((current) => new Set(current).add(dir))
      try {
        const listing = await api.listFiles(instanceId, dir)
        setChildren((current) => ({
          ...current,
          [dir]: listing.entries
            .filter((entry) => files || entry.isDir)
            .map((entry) => ({
              name: entry.name,
              path: entry.path,
              isDir: entry.isDir,
              entry,
            }))
            // Folders first, each group by name. The same order the listing
            // uses, because a reader moving between the two should not have to
            // re-learn where things are.
            .sort((a, b) =>
              a.isDir === b.isDir
                ? a.name.localeCompare(b.name, 'zh-CN')
                : a.isDir
                  ? -1
                  : 1,
            ),
        }))
      } catch {
        // A directory that cannot be read collapses to a leaf rather than an
        // error: the listing in the middle is where a real failure is
        // reported, and two copies of the same message is one too many.
        setChildren((current) => ({ ...current, [dir]: [] }))
      } finally {
        setLoading((current) => {
          const next = new Set(current)
          next.delete(dir)
          return next
        })
      }
    },
    [instanceId, files],
  )

  // A new instance, or 刷新: drop the cache and read the root plus whatever is
  // on the way to where the listing is standing. Arriving at plugins/Vulpecula
  // from 配置历史 should show that path standing open, not a collapsed root the
  // reader has to re-walk.
  //
  // `open` is deliberately not reset here. It is the remembered set, and
  // overwriting it with the ancestors of the current path would be the
  // persistence quietly not working.
  useEffect(() => {
    setChildren({})
    void read('')
    for (const dir of ancestors(path)) void read(dir)
    setOpen((current) => {
      const next = new Set(current)
      for (const dir of ancestors(path)) next.add(dir)
      return next
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [instanceId, reloadKey, read])

  // Walking into a directory opens the path to it. Separate from the effect
  // above because that one also throws the cache away, and stepping into a
  // folder must not re-read the whole tree.
  useEffect(() => {
    for (const dir of ancestors(path)) {
      if (!(dir in children)) void read(dir)
    }
    setOpen((current) => {
      const next = new Set(current)
      for (const dir of ancestors(path)) next.add(dir)
      return next
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const toggle = (dir: string) => {
    setOpen((current) => {
      const next = new Set(current)
      if (next.has(dir)) next.delete(dir)
      else {
        next.add(dir)
        if (!(dir in children)) void read(dir)
      }
      return next
    })
  }

  const visible = useCallback((dir: string): TreeNode[] => children[dir] ?? [], [children])

  /** Whether a directory has been read at all. Distinct from "has no
   *  children": one is a fact about the disk, the other about this cache. */
  const loaded = useCallback((dir: string) => dir in children, [children])

  const openRow = (node: TreeNode) => {
    if (node.isDir || node.entry === undefined || onOpenFile === undefined) onOpen(node.path)
    else onOpenFile(node.entry)
  }

  const rowProps = {
    path,
    open,
    loading,
    visible,
    loaded,
    openPaths,
    activePath,
    onToggle: toggle,
    onOpenNode: openRow,
    onContext:
      menuFor === undefined
        ? undefined
        : (event: React.MouseEvent, node: TreeNode) => {
            if (node.entry === undefined) return
            event.preventDefault()
            setContext({ x: event.clientX, y: event.clientY, entry: node.entry })
          },
  }

  return (
    <nav className={compact ? 'ftree ftree--compact' : 'ftree'} aria-label="目录树">
      <Row
        node={{ name: '实例根目录', path: '', isDir: true }}
        depth={0}
        open={open.has('')}
        loading={loading.has('')}
        current={path === ''}
        held={false}
        hasChildren={!loaded('') || visible('').length > 0}
        onToggle={() => toggle('')}
        onOpen={() => onOpen('')}
      />
      {open.has('') && <Branch dirs={visible('')} depth={1} {...rowProps} />}

      {/* A context menu has to open where the pointer is, which is the one
          thing Menu cannot do — it anchors to its own trigger. The sheet's
          classes are shared with it so the two look like one thing. Same
          arrangement as the editor's tab strip. */}
      {context !== null &&
        menuFor !== undefined &&
        createPortal(
          <div
            className="menu__sheet"
            role="menu"
            data-state="in"
            data-dir="down"
            style={{ left: context.x, top: context.y }}
          >
            {menuFor(context.entry).map((item) => (
              <button
                key={item.label}
                type="button"
                role="menuitem"
                className={item.danger ? 'menu__item menu__item--danger' : 'menu__item'}
                disabled={item.disabled}
                onClick={() => {
                  setContext(null)
                  item.onSelect()
                }}
              >
                {item.label}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </nav>
  )
}

interface BranchProps {
  dirs: TreeNode[]
  depth: number
  path: string
  open: Set<string>
  loading: Set<string>
  visible: (dir: string) => TreeNode[]
  loaded: (dir: string) => boolean
  openPaths?: Set<string>
  activePath?: string | null
  onToggle: (dir: string) => void
  onOpenNode: (node: TreeNode) => void
  onContext?: (event: React.MouseEvent, node: TreeNode) => void
}

function Branch({ dirs, depth, ...rest }: BranchProps) {
  const { path, open, loading, visible, loaded, openPaths, activePath, onToggle, onOpenNode } =
    rest

  return (
    <>
      {dirs.map((node) => (
        <div key={node.path}>
          <Row
            node={node}
            depth={depth}
            open={open.has(node.path)}
            loading={loading.has(node.path)}
            current={node.isDir ? path === node.path : activePath === node.path}
            // Open in some tab, but not the one being read. It is a third
            // state and it needs to be: without it the tree can say where you
            // are and what you are reading, but not what else you have going.
            held={!node.isDir && openPaths?.has(node.path) === true}
            // Unknown until it has been read once, and an arrow that appears
            // after the fact is better than one that never does: a directory
            // nobody has opened yet is drawn as openable. A file never has one.
            hasChildren={node.isDir && (!loaded(node.path) || visible(node.path).length > 0)}
            onToggle={() => onToggle(node.path)}
            onOpen={() => onOpenNode(node)}
            onContext={rest.onContext && ((event) => rest.onContext?.(event, node))}
          />
          {node.isDir && open.has(node.path) && (
            <Branch dirs={visible(node.path)} depth={depth + 1} {...rest} />
          )}
        </div>
      ))}
    </>
  )
}

function Row({
  node,
  depth,
  open,
  loading,
  current,
  held,
  hasChildren,
  onToggle,
  onOpen,
  onContext,
}: {
  node: TreeNode
  depth: number
  open: boolean
  loading: boolean
  current: boolean
  held: boolean
  hasChildren: boolean
  onToggle: () => void
  onOpen: () => void
  onContext?: (event: React.MouseEvent) => void
}) {
  return (
    <div
      className={`ftree__row${current ? ' ftree__row--on' : ''}${held ? ' ftree__row--held' : ''}`}
      // Indent as padding on the row rather than a margin, so the hover and
      // the current-row tint still run the full width of the column.
      style={{ paddingLeft: `${6 + depth * 14}px` }}
      onContextMenu={onContext}
    >
      {/* One icon, turned. `expand` is the right-pointing chevron the sidebar
          uses; a tree twist pointing down is the same mark rotated, and adding
          a second glyph to the shared set for one caller is not worth it. A
          leaf keeps the empty button rather than losing it: without the
          placeholder its name would sit twelve pixels left of its siblings. */}
      <button
        type="button"
        className={`ftree__twist${open ? ' ftree__twist--open' : ''}`}
        onClick={onToggle}
        aria-label={open ? `收起 ${node.name}` : `展开 ${node.name}`}
        aria-expanded={open}
        disabled={!hasChildren}
      >
        {hasChildren && <Icon name="expand" />}
      </button>
      <button type="button" className="ftree__name" onClick={onOpen} title={node.path || '/'}>
        <FileIcon name={node.name} dir={node.isDir} />
        <span className="ftree__label">{node.name}</span>
      </button>
      {loading && <span className="ftree__wait" aria-hidden="true" />}
    </div>
  )
}

/** Every directory on the way to `path`, root first, `path` included. */
function ancestors(path: string): string[] {
  if (path === '') return ['']
  const parts = path.split('/').filter(Boolean)
  const out = ['']
  let seen = ''
  for (const part of parts) {
    seen = seen ? `${seen}/${part}` : part
    out.push(seen)
  }
  return out
}
