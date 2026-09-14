import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { readPref, writePref } from '../localPrefs'
import { FileIcon } from './FileIcon'
import { Icon } from './Icon'

/**
 * The directory tree beside the listing.
 *
 * Folders, and only ever folders. The listing in the middle answers "what is
 * in here"; this answers "where is here". They used to trade jobs — the tree
 * grew files whenever the listing stepped aside — and that cost twice: the
 * same control meant two different things depending on a mode, and at this
 * rail's width the filenames it gained arrived pre-truncated
 * (`banned-players...`, `version_histor...`), which is a worse listing than
 * the one it was standing in for.
 *
 * Children are fetched when a node is first opened and then kept: walking back
 * up and down a tree is the single most common thing anyone does on this page,
 * and re-reading plugins/ every time it is expanded makes the tree feel slower
 * than the ".." it replaced. 刷新 on the bar drops the cache.
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
   *  borrows this component rather than growing a second tree. */
  compact?: boolean
}

/** One row of the tree. Exported because the file pane builds nodes of its own
 *  to hand around. */
export interface TreeNode {
  name: string
  path: string
  isDir: boolean
}

export function FileTree({ instanceId, path, onOpen, reloadKey, compact }: Props) {
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
          // Folders only, filtered here rather than at render: nothing in this
          // component has a use for the files, and carrying them would be
          // carrying a whole second listing per directory to throw away.
          [dir]: listing.entries
            .filter((entry) => entry.isDir)
            .map((entry) => ({ name: entry.name, path: entry.path, isDir: true }))
            .sort((a, b) => a.name.localeCompare(b.name, 'zh')),
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

  const rowProps = {
    path,
    open,
    loading,
    visible,
    loaded,
    onToggle: toggle,
    onOpen,
  }

  return (
    <nav className={compact ? 'ftree ftree--compact' : 'ftree'} aria-label="目录树">
      <Row
        node={{ name: '实例根目录', path: '', isDir: true }}
        depth={0}
        open={open.has('')}
        loading={loading.has('')}
        current={path === ''}
        hasChildren={!loaded('') || visible('').length > 0}
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
  open: Set<string>
  loading: Set<string>
  visible: (dir: string) => TreeNode[]
  loaded: (dir: string) => boolean
  onToggle: (dir: string) => void
  onOpen: (dir: string) => void
}

function Branch({ dirs, depth, ...rest }: BranchProps) {
  const { path, open, loading, visible, loaded, onToggle, onOpen } = rest

  return (
    <>
      {dirs.map((node) => (
        <div key={node.path}>
          <Row
            node={node}
            depth={depth}
            open={open.has(node.path)}
            loading={loading.has(node.path)}
            current={path === node.path}
            // Unknown until it has been read once, and an arrow that appears
            // after the fact is better than one that never does: a directory
            // nobody has opened yet is drawn as openable.
            hasChildren={!loaded(node.path) || visible(node.path).length > 0}
            onToggle={() => onToggle(node.path)}
            onOpen={() => onOpen(node.path)}
          />
          {open.has(node.path) && (
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
  hasChildren,
  onToggle,
  onOpen,
}: {
  node: TreeNode
  depth: number
  open: boolean
  loading: boolean
  current: boolean
  hasChildren: boolean
  onToggle: () => void
  onOpen: () => void
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
        <FileIcon name={node.name} dir />
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
