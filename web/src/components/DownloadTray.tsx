import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties } from 'react'

import { formatBytes } from '../format'
import type { DownloadJob } from '../types'
import { isDownloadActive } from '../types'
import { EDGE, placeVertically, useAnchor } from '../useAnchor'
import type { DownloadController } from '../useDownloads'
import { useDismiss } from '../useDismiss'
import { Badge } from './Badge'
import { Icon } from './Icon'

/** Tall enough for five rows and the footer. Past that the list scrolls. */
const MAX_HEIGHT = 380
/** Narrower than this and a file name has nowhere to go. */
const WIDTH = 360
/** More than this in the tray is a list, and a list belongs on the page. */
const MAX_ROWS = 5

/**
 * What the panel is downloading, from wherever you are.
 *
 * In the top bar rather than the sidebar because a download belongs to no
 * scope, and the sidebar is replaced wholesale between them — see Scope in
 * routes.ts. Before this there was nowhere to ask the question at all: each
 * shelf showed its own downloads on its own page, so "is anything still coming
 * down" meant visiting four of them.
 *
 * A tray *as well as* a page because the two answer different questions. The
 * 新建实例向导 is a page rather than a dialog precisely because two of its steps
 * start a download (see routes.ts), and "how much longer" is exactly what you
 * want to know while you are still in it — making that a trip to another page
 * would mean leaving the wizard half-finished. The page is for the other half:
 * failures, history, and why something did not work, which do not fit here.
 *
 * The button is always present, idle or not. One that appeared only during a
 * download would shift the whole top bar every time one started or ended, and
 * would leave no way in to the history.
 */
export function DownloadTray({
  downloads,
  onOpenPage,
}: {
  downloads: DownloadController
  onOpenPage: () => void
}) {
  const [open, setOpen] = useState(false)
  const button = useRef<HTMLButtonElement | null>(null)
  const sheet = useRef<HTMLDivElement | null>(null)

  const hide = useCallback(() => setOpen(false), [setOpen])
  const { leaving, close } = useDismiss(hide)

  const anchor = useAnchor(open, button, (rect) => ({
    ...placeVertically(rect, MAX_HEIGHT),
    // Right-aligned with the trigger, which sits in the top bar's right-hand
    // cluster: a sheet growing rightwards from there grows off the page. On a
    // narrow screen it takes the width it can get instead.
    box: {
      right: Math.max(EDGE, window.innerWidth - rect.right),
      width: Math.min(WIDTH, window.innerWidth - EDGE * 2),
    } as CSSProperties,
  }))

  useEffect(() => {
    if (!open) return
    const onDown = (event: PointerEvent) => {
      const target = event.target as Node
      if (button.current?.contains(target)) return
      // The sheet is portalled out of the trigger's subtree, so it has to be
      // asked about separately or every click inside it dismisses it.
      if (sheet.current?.contains(target)) return
      close()
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        close()
        // Focus goes back where it came from. Without this, dismissing the tray
        // leaves the caret at the top of the document.
        button.current?.focus()
      }
    }
    window.addEventListener('pointerdown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('pointerdown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open, close])

  const live = downloads.jobs.filter((job) => isDownloadActive(job.state))
  const active = downloads.active

  return (
    <div className="tray">
      <button
        ref={button}
        className="topbar__toggle tray__button"
        onClick={() => (open ? close() : setOpen(true))}
        aria-haspopup="dialog"
        aria-expanded={open}
        title={active > 0 ? `下载（${active} 个进行中）` : '下载'}
        aria-label={active > 0 ? `下载，${active} 个进行中` : '下载'}
      >
        <Icon name="queue" />
        {active > 0 && <Badge tone="update">{active}</Badge>}
      </button>

      {open &&
        anchor &&
        createPortal(
          <div
            ref={sheet}
            className="tray__sheet"
            role="dialog"
            aria-label="下载"
            data-state={leaving ? 'out' : 'in'}
            data-dir={anchor.up ? 'up' : 'down'}
            style={{
              ...anchor.box,
              maxHeight: anchor.maxHeight,
              ...(anchor.up ? { bottom: anchor.offset } : { top: anchor.offset }),
            }}
          >
            <div className="tray__rows">
              {live.length === 0 ? (
                <p className="tray__empty muted">当前没有下载。</p>
              ) : (
                live
                  .slice(-MAX_ROWS)
                  .reverse()
                  .map((job) => (
                    <TrayRow
                      key={job.id}
                      job={job}
                      busy={downloads.busy}
                      onCancel={() => void downloads.cancel(job.id)}
                    />
                  ))
              )}
            </div>
            <button
              className="tray__all"
              onClick={() => {
                close()
                onOpenPage()
              }}
            >
              查看全部
              <Icon name="chart" />
            </button>
          </div>,
          document.body,
        )}
    </div>
  )
}

/**
 * One live download, cut down to what fits.
 *
 * No failure text and no history: those are sentences, and a sentence in a
 * 360px sheet is either truncated or turns the tray into the page. The tray
 * says what is happening; the page says what happened.
 */
function TrayRow({
  job,
  busy,
  onCancel,
}: {
  job: DownloadJob
  busy: boolean
  onCancel: () => void
}) {
  const percent = job.total > 0 ? Math.round((job.downloaded / job.total) * 100) : 0

  return (
    <div className="tray__row">
      <div className="tray__head">
        <span className="tray__what">{job.title}</span>
        <button className="link link--danger" onClick={onCancel} disabled={busy}>
          取消
        </button>
      </div>
      {job.state === 'queued' ? (
        <p className="tray__meta muted">排队中</p>
      ) : (
        <div className="progress progress--slim">
          <div className="progress__bar" style={{ width: `${percent}%` }} />
          <span className="progress__label">
            {job.state === 'extracting'
              ? '解压中'
              : job.total > 0
                ? `${percent}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
                : formatBytes(job.downloaded)}
          </span>
        </div>
      )}
    </div>
  )
}
