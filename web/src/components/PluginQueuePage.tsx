import { useMemo } from 'react'

import { formatBytes, formatDate } from '../format'
import { toast } from '../toast'
import type { PluginDownloadJob } from '../types'
import { isJobActive } from '../types'
import { Page } from './Page'
import type { PluginController } from '../usePlugins'

/**
 * 下载队列 — every jar the panel is fetching, has fetched, or failed to.
 *
 * This page exists because downloads stopped being one at a time. A single
 * download was a status line above the library: one bar, one sentence, gone
 * when it finished. Five of them — two running, two waiting, one that failed
 * an hour ago — is a list, and a list that appeared and disappeared inside
 * 插件列表 would have shoved that table down the screen every time somebody
 * pressed 更新入库.
 *
 * Two sections, in the order the questions get asked. 进行中 is what the panel
 * is doing right now and is the reason anybody opens this page; 历史 is what it
 * did, and it is here because the old single-slot design lost a failure the
 * moment the next download started — the one record an operator actually needs
 * was the one guaranteed to be overwritten.
 *
 * Both sections are rows of the same shape, because a queued job, a running
 * one and a failed one are the same download at different moments, and a page
 * that redrew them in three different layouts would make that hard to see.
 */
export function PluginQueuePage({ plugins }: { plugins: PluginController }) {
  const { jobs } = plugins

  const [live, history] = useMemo(() => {
    const active: PluginDownloadJob[] = []
    const done: PluginDownloadJob[] = []
    for (const job of jobs) {
      ;(isJobActive(job.state) ? active : done).push(job)
    }
    // Oldest first while they are live: that is the order they will run in, and
    // a queue that reorders itself as jobs start is a queue nobody can read.
    // History keeps the newest-first order it arrives in.
    return [active.reverse(), done]
  }, [jobs])

  const failed = history.filter((job) => job.state === 'failed').length

  return (
    <Page
      wide
      title="下载队列"
      lead={`插件库最多同时下 ${MAX_CONCURRENT} 个 jar，多出来的排队等。下载归守护进程管，关掉标签页也会下完。这里下到的都是面板插件库，装到哪台服是「插件列表」和实例自己的「插件」页上的事。`}
      aside={
        <div className="page__actions">
          <button
            className="btn"
            disabled={plugins.busy || history.length === 0}
            onClick={() => void plugins.clearFinished().then(() => toast('已清空下载记录'))}
          >
            清空记录
          </button>
          <button
            className="btn btn--danger"
            disabled={plugins.busy || live.length === 0}
            title="停掉正在下和排队中的全部任务"
            onClick={() =>
              void plugins.cancel().then(() => toast(`已取消 ${live.length} 个下载`))
            }
          >
            全部取消
          </button>
        </div>
      }
    >
      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}

      <section className="dlqueue">
        <h2 className="dlqueue__title">
          进行中
          {live.length > 0 && <span className="dlqueue__count">{live.length}</span>}
        </h2>
        {live.length === 0 ? (
          <p className="dlqueue__empty muted">
            现在没有下载。在「插件列表」按更新入库，或者在「插件市场」里挑一个，任务就会出现在这里。
          </p>
        ) : (
          <div className="dlqueue__rows">
            {live.map((job) => (
              <JobRow
                key={job.id}
                job={job}
                busy={plugins.busy}
                onCancel={() => void plugins.cancel(job.id)}
              />
            ))}
          </div>
        )}
      </section>

      {history.length > 0 && (
        <section className="dlqueue">
          <h2 className="dlqueue__title">
            历史
            {failed > 0 && <span className="dlqueue__count dlqueue__count--bad">{failed} 个失败</span>}
          </h2>
          <div className="dlqueue__rows">
            {history.map((job) => (
              <JobRow key={job.id} job={job} busy={plugins.busy} />
            ))}
          </div>
        </section>
      )}
    </Page>
  )
}

/** Kept in step with maxConcurrent in internal/plugin/downloader.go. Only ever
 *  said out loud in the lead — nothing on this page is computed from it. */
const MAX_CONCURRENT = 3

/**
 * One download.
 *
 * The progress bar is only drawn for a job that has one. A queued job has no
 * progress and a bar sitting at 0% would read as a stalled download rather
 * than as a waiting one; a finished job's bar is a fact about the past that
 * nobody needs a graphic for. What every state does carry is the same first
 * line — what is being fetched — because that is what the operator is scanning
 * the column for.
 */
function JobRow({
  job,
  busy,
  onCancel,
}: {
  job: PluginDownloadJob
  busy: boolean
  onCancel?: () => void
}) {
  const fraction = job.total > 0 ? job.downloaded / job.total : 0
  const percent = Math.round(fraction * 100)

  return (
    <div className={`dlrow dlrow--${job.state}`}>
      <div className="dlrow__main">
        <span className="dlrow__what">
          <strong>{job.pluginName}</strong>
          {job.version && <span className="dlrow__ver">{job.version}</span>}
        </span>
        <JobBadge job={job} />
        {onCancel && (
          <button className="link link--danger dlrow__cancel" onClick={onCancel} disabled={busy}>
            取消
          </button>
        )}
      </div>

      {job.state === 'downloading' && (
        <div className="progress">
          <div className="progress__bar" style={{ width: `${percent}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${percent}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : formatBytes(job.downloaded)}
          </span>
        </div>
      )}

      {/* The failure reason goes on a line of its own rather than into the
          badge's tooltip: it is the whole content of a failed row, and it is
          regularly a sentence — "仓库里没有名为 X 的文件". */}
      {job.error && job.state === 'failed' && <p className="dlrow__error">{job.error}</p>}

      <p className="dlrow__meta">
        {job.fileName || '还没确定是哪个 jar'}
        {job.mirror && ` · 来自 ${mirrorLabel(job.mirror)}`}
        {job.state === 'queued' && ' · 排队中'}
        {job.finishedAt && ` · ${formatDate(job.finishedAt)}`}
      </p>
    </div>
  )
}

function JobBadge({ job }: { job: PluginDownloadJob }) {
  switch (job.state) {
    case 'queued':
      return <span className="badge badge--muted">排队</span>
    case 'downloading':
      return <span className="badge">下载中</span>
    case 'done':
      return <span className="badge badge--ok">已入库</span>
    case 'cancelled':
      return <span className="badge badge--muted">已取消</span>
    default:
      return <span className="badge badge--warn">失败</span>
  }
}

/**
 * Names a mirror id for the job line. Unknown ids are custom prefixes, which
 * are already their own name.
 */
export function mirrorLabel(id: string): string {
  return id === 'direct' ? '源站直连' : id
}
