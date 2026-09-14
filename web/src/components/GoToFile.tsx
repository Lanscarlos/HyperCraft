import { useEffect, useMemo, useRef, useState } from 'react'

import { api } from '../api'
import { segments, shift } from '../fuzzy'
import type { FileHit } from '../types'
import { FileIcon } from './FileIcon'
import { Modal } from './Modal'
import { Note } from './Note'
import { SkeletonRows } from './Skeleton'

/**
 * 转到文件 — the answer to "which file was that" that is not "expand the tree".
 *
 * world/region holds several hundred .mca and every plugin brings a dozen
 * files, so walking down to one by opening folders is the slowest path there
 * is. Matching happens on the daemon (see internal/serverfiles/index.go) over
 * the whole instance; what arrives is fifty rows and the offsets to highlight.
 *
 * With nothing typed it shows what you had open recently, because the file you
 * were just in is nearly always the file you are looking for.
 */
const DEBOUNCE_MS = 140

/** How many recents the empty state shows. Ten is a screenful without
 *  scrolling, and past that it is not "recent" any more. */
const RECENT_SHOWN = 10

export function GoToFile({
  instanceId,
  recent,
  onOpen,
  onClose,
}: {
  instanceId: string
  recent: string[]
  /** `split` is ⌘Enter: open it in the other group. */
  onOpen: (path: string, split: boolean) => void
  onClose: () => void
}) {
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<FileHit[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [at, setAt] = useState(0)
  const box = useRef<HTMLInputElement | null>(null)
  const listing = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    box.current?.focus()
  }, [])

  // One request per pause, and an answer to a query that is no longer the one
  // in the box is dropped: a slow search for "a" landing after a fast one for
  // "abc" would show the wrong rows under the right query.
  const latest = useRef(0)
  useEffect(() => {
    const needle = query.trim()
    if (needle === '') {
      setHits(null)
      setBusy(false)
      return
    }
    const mine = ++latest.current
    setBusy(true)
    const timer = window.setTimeout(() => {
      api
        .findFiles(instanceId, needle)
        .then((found) => {
          if (latest.current !== mine) return
          setHits(found)
          setFailed(null)
          setAt(0)
        })
        .catch((err: unknown) => {
          if (latest.current !== mine) return
          setFailed(err instanceof Error ? err.message : '搜索失败')
        })
        .finally(() => {
          if (latest.current === mine) setBusy(false)
        })
    }, DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [instanceId, query])

  /**
   * Recently opened first, then the server's score.
   *
   * The daemon ranks by how well the path matches, which is the right answer
   * for a path it has never seen you open. It cannot know which files you were
   * in an hour ago, and that is the stronger signal of the two — so it is
   * applied here, where the list is.
   */
  const rows = useMemo<Row[]>(() => {
    if (hits === null) {
      return recent.slice(0, RECENT_SHOWN).map((path) => ({ path, name: baseName(path), match: [] }))
    }
    const rank = new Map(recent.map((path, index) => [path, index]))
    return hits
      .slice()
      .sort((a, b) => {
        const ra = rank.get(a.path) ?? Infinity
        const rb = rank.get(b.path) ?? Infinity
        return ra === rb ? b.score - a.score : ra - rb
      })
      .map((hit) => ({ path: hit.path, name: hit.name, match: hit.match }))
  }, [hits, recent])

  // The highlighted row has to stay on screen while the arrows walk past the
  // fold, the same way the editor's tab strip scrolls its front tab back.
  useEffect(() => {
    listing.current?.querySelector('.gotofile__row--on')?.scrollIntoView({ block: 'nearest' })
  }, [at])

  const choose = (index: number, split: boolean) => {
    const row = rows[index]
    if (row !== undefined) onOpen(row.path, split)
  }

  return (
    <Modal onClose={onClose} label="转到文件">
      <div className="gotofile">
        <input
          ref={box}
          className="gotofile__box"
          value={query}
          spellCheck={false}
          autoComplete="off"
          placeholder="输入文件名的一部分，例如 vulp con"
          aria-label="转到文件"
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'ArrowDown') {
              event.preventDefault()
              setAt((current) => Math.min(rows.length - 1, current + 1))
              return
            }
            if (event.key === 'ArrowUp') {
              event.preventDefault()
              setAt((current) => Math.max(0, current - 1))
              return
            }
            if (event.key === 'Enter') {
              event.preventDefault()
              choose(at, event.metaKey || event.ctrlKey)
            }
          }}
        />

        <div className="gotofile__out" ref={listing}>
          {failed !== null ? (
            <Note tone="error">{failed}</Note>
          ) : busy && hits === null ? (
            <SkeletonRows rows={6} />
          ) : rows.length === 0 ? (
            <p className="gotofile__empty">
              {query.trim() === '' ? '还没有打开过文件。' : '没有匹配的路径。'}
            </p>
          ) : (
            rows.map((row, index) => (
              <button
                key={row.path}
                type="button"
                className={`gotofile__row${index === at ? ' gotofile__row--on' : ''}`}
                onPointerEnter={() => setAt(index)}
                onClick={(event) => choose(index, event.metaKey || event.ctrlKey)}
                title={row.path}
              >
                <FileIcon name={row.name} />
                <span className="gotofile__text">
                  <span className="gotofile__name">
                    {/* The offsets are into the whole path, so they are moved
                        onto the basename rather than re-derived from it — a
                        second matcher would highlight different characters
                        from the ones that produced the ranking. */}
                    {segments(row.name, shift(row.match, row.path.length - row.name.length, row.name.length)).map(
                      (run, key) =>
                        run.hit ? <mark key={key}>{run.text}</mark> : <span key={key}>{run.text}</span>,
                    )}
                  </span>
                  <span className="gotofile__dir">{dirOf(row.path)}</span>
                </span>
              </button>
            ))
          )}
        </div>

        <p className="gotofile__hint">
          <kbd>↑</kbd> <kbd>↓</kbd> 选择 · <kbd>Enter</kbd> 打开 · <kbd>⌘/Ctrl + Enter</kbd> 在分屏打开
        </p>
      </div>
    </Modal>
  )
}

interface Row {
  path: string
  name: string
  match: number[]
}

function baseName(path: string): string {
  const at = path.lastIndexOf('/')
  return at < 0 ? path : path.slice(at + 1)
}

function dirOf(path: string): string {
  const at = path.lastIndexOf('/')
  return at < 0 ? '实例根目录' : path.slice(0, at)
}
