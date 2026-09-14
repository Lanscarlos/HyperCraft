import { useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

import { api } from '../api'
import type { FileSearchHit } from '../types'
import { EmptyState } from './EmptyState'
import { FileIcon } from './FileIcon'
import { Note } from './Note'
import { SkeletonRows } from './Skeleton'
import { ToolbarSearch } from './Toolbar'

/**
 * Searching one instance's files by name and by content.
 *
 * The matching is the daemon's — see internal/serverfiles/index.go — for the
 * same reason ⌘P's is: an instance holds tens of thousands of paths and this
 * panel shows forty. What arrives is the forty, with the line numbers and the
 * offset of the hit inside each line, so clicking a result can land the caret
 * on the word rather than merely on the file.
 *
 * Results group by file and open collapsed past the first few: a config with
 * twenty matches is one row until it is asked about, or the panel is one file.
 */
const DEBOUNCE_MS = 180

/** Files shown expanded without being asked. Past this the list is a wall of
 *  code lines rather than a list of files. */
const OPEN_BY_DEFAULT = 3

export function FileSearchPanel({
  instanceId,
  onOpen,
  boxRef,
}: {
  instanceId: string
  /** Opens a file, optionally putting the caret on a line. */
  onOpen: (path: string, line?: number) => void
  boxRef: RefObject<HTMLInputElement>
}) {
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<FileSearchHit[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [shut, setShut] = useState<Set<string>>(new Set())

  // One request per pause, and the answer to a query that is no longer the one
  // in the box is thrown away. Without the second half, a slow search for "a"
  // lands after a fast one for "abc" and the panel shows the wrong results with
  // the right query above them.
  const latest = useRef(0)
  useEffect(() => {
    const needle = query.trim()
    if (needle === '') {
      setHits(null)
      setBusy(false)
      setFailed(null)
      return
    }
    const mine = ++latest.current
    setBusy(true)
    const timer = window.setTimeout(() => {
      api
        .searchFiles(instanceId, needle)
        .then((found) => {
          if (latest.current !== mine) return
          setHits(found)
          setFailed(null)
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

  return (
    <div className="fsearch">
      <ToolbarSearch
        ref={boxRef}
        className="fsearch__box"
        value={query}
        placeholder="搜索文件名与内容"
        aria-label="搜索文件名与内容"
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape') setQuery('')
        }}
      />

      <div className="fsearch__out">
        {failed !== null ? (
          <Note tone="error">{failed}</Note>
        ) : busy && hits === null ? (
          <SkeletonRows rows={5} />
        ) : hits === null ? (
          <EmptyState inline title="搜索这个实例的文件">
            文件名和文本内容都会搜。存档、libraries 这类目录不在范围里。
          </EmptyState>
        ) : hits.length === 0 ? (
          <EmptyState inline title="没有匹配的文件">
            换个词试试，或者用 ⌘/Ctrl + P 按路径找。
          </EmptyState>
        ) : (
          hits.map((hit, index) => {
            const open = !shut.has(hit.path) && index < OPEN_BY_DEFAULT
            return (
              <div className="fsearch__file" key={hit.path}>
                <button
                  type="button"
                  className="fsearch__head"
                  onClick={() => {
                    if (hit.lines.length === 0) {
                      onOpen(hit.path)
                      return
                    }
                    setShut((current) => {
                      const next = new Set(current)
                      if (!next.delete(hit.path)) next.add(hit.path)
                      return next
                    })
                  }}
                  title={hit.path}
                >
                  <FileIcon name={hit.name} />
                  <span className="fsearch__name">{hit.name}</span>
                  {hit.lines.length > 0 && (
                    <span className="fsearch__count">{hit.lines.length}</span>
                  )}
                </button>
                <span className="fsearch__dir" title={hit.path}>
                  {dirOf(hit.path)}
                </span>
                {open &&
                  hit.lines.map((line) => (
                    <button
                      key={line.n}
                      type="button"
                      className="fsearch__line"
                      onClick={() => onOpen(hit.path, line.n)}
                      title={`第 ${line.n} 行`}
                    >
                      <span className="fsearch__no num">{line.n}</span>
                      <span className="fsearch__text">
                        {line.text.slice(0, line.col)}
                        <mark>{line.text.slice(line.col, line.col + line.len)}</mark>
                        {line.text.slice(line.col + line.len)}
                      </span>
                    </button>
                  ))}
              </div>
            )
          })
        )}
      </div>
    </div>
  )
}

function dirOf(path: string): string {
  const at = path.lastIndexOf('/')
  return at < 0 ? '实例根目录' : path.slice(0, at)
}
