import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { FileIcon } from './FileIcon'
import { Icon } from './Icon'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'

/**
 * The directory tree beside the listing.
 *
 * Directories only, normally. The listing in the middle is the answer to "what
 * is in here"; this is the answer to "where is here", and mixing files into it
 * would make it a second, worse copy of the listing rather than a map of the
 * tree around it.
 *
 * Edit mode is the exception (`showFiles`): the listing is off screen there,
 * so the division of labour it was half of has nothing left to divide. The
 * cache keeps every entry either way and the filtering happens at render, so
 * switching modes does not re-read a single directory.
 *
 * Children are fetched when a node is first opened and then kept: walking back
 * up and down a tree is the single most common thing anyone does on this page,
 * and re-reading plugins/ every time it is expanded makes the tree feel slower
 * than the ".." it replaced. 刷新 on the toolbar drops the cache.
 */

interface Props {
  instanceId: string
  /** The directory the listing is showing, so the tree can mark it. */
  path: string
  onOpen: (path: string) => void
  /** Bumped by the toolbar's refresh, to drop what was cached. */
  reloadKey?: number
  /** Edit mode: files show up alongside the directories. */
  showFiles?: boolean
  /** The file in front of the editor, so the tree can mark that one too. */
  openPath?: string | null
  /** Open files with unsaved changes, marked the way the tabs mark them. */
  dirtyPaths?: Set<string>
  /** `pin` is a double click: the file pane opens a single click as a preview
   *  tab that the next single click reuses. */
  onOpenFile?: (path: string, pin?: boolean) => void
  /**
   * Filters names — but only among what has already been read. Walking every
   * unopened directory to answer a keystroke is a request storm, not a search,
   * and the filter box says so in as many words.
   */
  filter?: string
  /** The per-row actions, as a menu. Absent outside edit mode: the listing has
   *  its own 操作 column and two of them would be two places to look. */
  menuFor?: (node: TreeNode) => MenuItem[]
}

/** One row of the tree. Exported because the row actions live in FileManager,
 *  which owns every request the panel makes about files. */
export interface TreeNode {
  name: string
  path: string
  isDir: boolean
}

export function FileTree({
  instanceId,
  path,
  onOpen,
  reloadKey,
  showFiles,
  openPath,
  dirtyPaths,
  onOpenFile,
  filter,
  menuFor,
}: Props) {
  const [children, setChildren] = useState<Record<string, TreeNode[]>>({})
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState<Set<string>>(new Set())

  const read = useCallback(
    async (dir: string) => {
      setLoading((current) => new Set(current).add(dir))
      try {
        const listing = await api.listFiles(instanceId, dir)
        setChildren((current) => ({
          ...current,
          // Everything, not just the directories: which of the two modes is on
          // is a rendering question, and filtering here would mean re-reading
          // every directory on the way into edit mode and out of it again.
          [dir]: listing.entries
            .map((entry) => ({ name: entry.name, path: entry.path, isDir: entry.isDir }))
            .sort(byKindThenName),
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
    [instanceId],
  )

  // The root is always read, and the ancestors of wherever the listing is are
  // opened with it — arriving at plugins/Vulpecula from 配置历史 should show
  // that path standing open, not a collapsed root the reader has to re-walk.
  useEffect(() => {
    setChildren({})
    setOpen(new Set(ancestors(path)))
    void read('')
    for (const dir of ancestors(path)) void read(dir)
  }, [instanceId, reloadKey, read])

  useEffect(() => {
    for (const dir of ancestors(path)) {
      if (!(dir in children)) void read(dir)
    }
    setOpen((current) => {
      const next = new Set(current)
      for (const dir of ancestors(path)) next.add(dir)
      return next
    })
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

  const visible = useCallback(
    (dir: string): TreeNode[] => {
      const all = children[dir] ?? []
      const kept = showFiles ? all : all.filter((node) => node.isDir)
      const needle = (filter ?? '').trim().toLowerCase()
      if (needle === '') return kept
      // Directories always survive the filter: hiding a folder hides the path
      // to the file that did match, and the tree would read as empty for a
      // name it is in fact showing one level down.
      return kept.filter((node) => node.isDir || node.name.toLowerCase().includes(needle))
    },
    [children, showFiles, filter],
  )

  /** Whether a directory has been read at all. Distinct from "has no visible
   *  children": one is a fact about the disk, the other about the filter. */
  const loaded = useCallback((dir: string) => dir in children, [children])

  const rowProps = {
    path,
    openPath,
    dirtyPaths,
    open,
    loading,
    visible,
    loaded,
    onToggle: toggle,
    onOpen,
    onOpenFile,
    menuFor,
  }

  return (
    <nav className="ftree" aria-label="目录树">
      <Row
        node={{ name: '实例根目录', path: '', isDir: true }}
        depth={0}
        open={open.has('')}
        loading={loading.has('')}
        current={path === ''}
        hasChildren={visible('').length > 0}
        onToggle={() => toggle('')}
        onOpen={() => onOpen('')}
      />
      {open.has('') && <Branch dirs={visible('')} depth={1} {...rowProps} />}
    </nav>
  )
}

interface BranchProps {
  dirs: TreeNode[]
  depth: number
  path: string
  openPath?: string | null
  dirtyPaths?: Set<string>
  open: Set<string>
  loading: Set<string>
  visible: (dir: string) => TreeNode[]
  loaded: (dir: string) => boolean
  onToggle: (dir: string) => void
  onOpen: (dir: string) => void
  onOpenFile?: (path: string, pin?: boolean) => void
  menuFor?: (node: TreeNode) => MenuItem[]
}

function Branch({ dirs, depth, ...rest }: BranchProps) {
  const { path, openPath, dirtyPaths, open, loading, visible, loaded } = rest
  const { onToggle, onOpen, onOpenFile, menuFor } = rest

  return (
    <>
      {dirs.map((node) => (
        <div key={node.path}>
          <Row
            node={node}
            depth={depth}
            open={open.has(node.path)}
            loading={loading.has(node.path)}
            // A directory is marked when the listing is in it; a file, when it
            // is the one in front of the editor. Two different questions, and
            // in edit mode both are on screen at once.
            current={node.isDir ? path === node.path : openPath === node.path}
            dirty={!node.isDir && dirtyPaths?.has(node.path)}
            // Unknown until it has been read once, and an arrow that appears
            // after the fact is better than one that never does: a directory
            // nobody has opened is drawn as openable.
            hasChildren={node.isDir && (!loaded(node.path) || visible(node.path).length > 0)}
            menu={menuFor?.(node)}
            label={node.name}
            onToggle={() => onToggle(node.path)}
            onOpen={() => (node.isDir ? onOpen(node.path) : onOpenFile?.(node.path))}
            // Directories have nothing to pin: the second click of a double
            // one lands on a listing that the first click already moved.
            onPin={node.isDir ? undefined : () => onOpenFile?.(node.path, true)}
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
  dirty,
  hasChildren,
  menu,
  label,
  onToggle,
  onOpen,
  onPin,
}: {
  node: TreeNode
  depth: number
  open: boolean
  loading: boolean
  current: boolean
  dirty?: boolean
  hasChildren: boolean
  /** Absent outside edit mode, and on the root row, which is not a thing that
   *  can be renamed or deleted. */
  menu?: MenuItem[]
  label?: string
  onToggle: () => void
  onOpen: () => void
  /** Double click, where that means something: see onOpenFile. */
  onPin?: () => void
}) {
  return (
    <div
      className={`ftree__row${current ? ' ftree__row--on' : ''}`}
      // Indent as padding on the row rather than a margin, so the hover and
      // the current-row tint still run the full width of the rail.
      style={{ paddingLeft: `${6 + depth * 12}px` }}
    >
      {/* One icon, turned. `expand` is the right-pointing chevron the sidebar
          uses; a tree twist pointing down is the same mark rotated, and adding
          a second glyph to the shared set for one caller is not worth it.
          A file keeps the empty button rather than losing it: without the
          placeholder its name would sit twelve pixels left of its siblings. */}
      <button
        type="button"
        className={`ftree__twist${open ? ' ftree__twist--open' : ''}`}
        onClick={onToggle}
        aria-label={open ? `收起 ${node.name}` : `展开 ${node.name}`}
        aria-expanded={node.isDir ? open : undefined}
        disabled={!hasChildren}
      >
        {hasChildren && <Icon name="expand" />}
      </button>
      <button
        type="button"
        className="ftree__name"
        onClick={onOpen}
        onDoubleClick={onPin}
        title={node.path || '/'}
      >
        <FileIcon name={node.name} dir={node.isDir} />
        <span className="ftree__label">{node.name}</span>
        {dirty && <span className="ftree__dot" aria-label="有未保存的修改" />}
      </button>
      {loading && <span className="ftree__wait" aria-hidden="true" />}
      {/* A button rather than a hijacked right-click: taking over the context
          menu costs the reader "open in a new tab" and every habit like it,
          and leaves a keyboard with no way in at all. */}
      {menu && (
        <Menu
          className="ftree__more"
          items={menu}
          title={`${label ?? node.name} 的操作`}
          ariaLabel={`${label ?? node.name} 的操作`}
        >
          ⋯
        </Menu>
      )}
    </div>
  )
}

/** Directories first, then names. Not the order the listing arrives in: that
 *  order is the listing's business, and a tree with files scattered between
 *  folders is a tree nobody can scan. */
function byKindThenName(a: TreeNode, b: TreeNode): number {
  if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
  return a.name.localeCompare(b.name, 'zh')
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
