import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { Icon } from './Icon'

/**
 * The directory tree beside the listing.
 *
 * Directories only. The listing in the middle is the answer to "what is in
 * here"; this is the answer to "where is here", and mixing files into it would
 * make it a second, worse copy of the listing rather than a map of the tree
 * around it.
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
}

interface Node {
  name: string
  path: string
}

export function FileTree({ instanceId, path, onOpen, reloadKey }: Props) {
  const [children, setChildren] = useState<Record<string, Node[]>>({})
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState<Set<string>>(new Set())

  const read = useCallback(
    async (dir: string) => {
      setLoading((current) => new Set(current).add(dir))
      try {
        const listing = await api.listFiles(instanceId, dir)
        setChildren((current) => ({
          ...current,
          [dir]: listing.entries
            .filter((entry) => entry.isDir)
            .map((entry) => ({ name: entry.name, path: entry.path })),
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

  return (
    <nav className="ftree" aria-label="目录树">
      <Row
        node={{ name: '实例根目录', path: '' }}
        depth={0}
        open={open.has('')}
        loading={loading.has('')}
        current={path === ''}
        hasChildren={(children[''] ?? []).length > 0}
        onToggle={() => toggle('')}
        onOpen={() => onOpen('')}
      />
      {open.has('') && (
        <Branch
          dirs={children[''] ?? []}
          depth={1}
          path={path}
          open={open}
          loading={loading}
          children_={children}
          onToggle={toggle}
          onOpen={onOpen}
        />
      )}
    </nav>
  )
}

function Branch({
  dirs,
  depth,
  path,
  open,
  loading,
  children_,
  onToggle,
  onOpen,
}: {
  dirs: Node[]
  depth: number
  path: string
  open: Set<string>
  loading: Set<string>
  children_: Record<string, Node[]>
  onToggle: (dir: string) => void
  onOpen: (dir: string) => void
}) {
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
            // nobody has opened is drawn as openable.
            hasChildren={children_[node.path] === undefined || children_[node.path].length > 0}
            onToggle={() => onToggle(node.path)}
            onOpen={() => onOpen(node.path)}
          />
          {open.has(node.path) && (
            <Branch
              dirs={children_[node.path] ?? []}
              depth={depth + 1}
              path={path}
              open={open}
              loading={loading}
              children_={children_}
              onToggle={onToggle}
              onOpen={onOpen}
            />
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
  node: Node
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
          a second glyph to the shared set for one caller is not worth it. */}
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
        {node.name}
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
