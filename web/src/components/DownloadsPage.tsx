import { useMemo } from 'react'

import { formatBytes, formatDate } from '../format'
import { toast } from '../toast'
import type { DownloadJob, DownloadKind } from '../types'
import { isDownloadActive } from '../types'
import type { DownloadController } from '../useDownloads'
import { Button } from './Button'
import { Icon } from './Icon'
import type { IconName } from './Icon'
import { Page } from './Page'

/**
 * 下载 — everything the panel is fetching, has fetched, or failed to fetch.
 *
 * This page was 插件库's 下载队列 and is otherwise unchanged, because what it
 * was already right about had nothing to do with plugins. A single download is
 * a status line: one bar, one sentence, gone when it finishes. Five of them —
 * two running, two waiting, one that failed an hour ago — is a list, and a list
 * that appeared and disappeared inside another page would shove that page's
 * layout around every time somebody pressed a button.
 *
 * What changed is who it covers. Downloads were four separate queues behind
 * four separate pages, three of which held a single slot and kept no history at
 * all, so a core download that failed at 3am was overwritten by the next one.
 * They are one queue now (internal/download), and this is where you read it.
 *
 * Two sections, in the order the questions get asked. 进行中 is what the panel
 * is doing right now and is the reason anybody opens this page; 历史 is what it
 * did, and it is here because the single-slot design lost a failure the moment
 * the next download started — the one record an operator actually needs was the
 * one guaranteed to be overwritten.
 *
 * Both sections are rows of the same shape, because a queued job, a running one
 * and a failed one are the same download at different moments, and a page that
 * redrew them in three different layouts would make that hard to see.
 */
export function DownloadsPage({
  downloads,
  only,
  onFilter,
}: {
  downloads: DownloadController
  /** Which shelf the list is narrowed to, from the query string. */
  only?: DownloadKind
  onFilter: (kind: DownloadKind | undefined) => void
}) {
  const { jobs } = downloads

  const shown = useMemo(
    () => (only ? jobs.filter((job) => job.kind === only) : jobs),
    [jobs, only],
  )

  const [live, history] = useMemo(() => {
    const active: DownloadJob[] = []
    const done: DownloadJob[] = []
    for (const job of shown) {
      ;(isDownloadActive(job.state) ? active : done).push(job)
    }
    // Oldest first while they are live: that is the order they will run in, and
    // a queue that reorders itself as jobs start is a queue nobody can read.
    // History keeps the newest-first order it arrives in.
    return [active.reverse(), done]
  }, [shown])

  const failed = history.filter((job) => job.state === 'failed').length
  // Counted over everything rather than over the filter, because the chips have
  // to say what is behind them.
  const counts = useMemo(() => {
    const out = new Map<DownloadKind, number>()
    for (const job of jobs) {
      if (isDownloadActive(job.state)) out.set(job.kind, (out.get(job.kind) ?? 0) + 1)
    }
    return out
  }, [jobs])

  return (
    <Page
      wide
      title="下载"
      lead="服务端核心、Java 环境、数据库引擎和插件都在这里排队。下载归守护进程管，关掉标签页、退出登录都不会中断它。装到哪台服是各自的库页和实例自己的事。"
      aside={
        <div className="page__actions">
          <Button
            disabled={downloads.busy || history.length === 0}
            onClick={() => void downloads.clearFinished().then(() => toast('已清空下载记录'))}
          >
            清空记录
          </Button>
        </div>
      }
    >
      {downloads.error && <div className="alert alert--error">{downloads.error}</div>}

      <div className="dlfilter" role="group" aria-label="按类型筛选">
        <FilterChip active={!only} label="全部" count={downloads.active} onClick={() => onFilter(undefined)} />
        {KINDS.map((kind) => (
          <FilterChip
            key={kind.id}
            active={only === kind.id}
            label={kind.label}
            icon={kind.icon}
            count={counts.get(kind.id) ?? 0}
            onClick={() => onFilter(kind.id)}
          />
        ))}
      </div>

      <section className="panel dlqueue">
        <h2 className="dlqueue__title">
          进行中
          {live.length > 0 && <span className="dlqueue__count">{live.length}</span>}
        </h2>
        {live.length === 0 ? (
          <p className="dlqueue__empty muted">
            现在没有下载。去「服务端核心」「Java 环境」「数据库环境」或「插件市场」开始一个，任务就会出现在这里。
          </p>
        ) : (
          <div className="dlqueue__rows">
            {live.map((job) => (
              <JobRow
                key={job.id}
                job={job}
                busy={downloads.busy}
                onCancel={() => void downloads.cancel(job.id)}
              />
            ))}
          </div>
        )}
      </section>

      {history.length > 0 && (
        <section className="panel dlqueue">
          <h2 className="dlqueue__title">
            历史
            {failed > 0 && (
              <span className="dlqueue__count dlqueue__count--bad">{failed} 个失败</span>
            )}
          </h2>
          <div className="dlqueue__rows">
            {history.map((job) => (
              <JobRow key={job.id} job={job} busy={downloads.busy} />
            ))}
          </div>
        </section>
      )}
    </Page>
  )
}

/** The shelves, in the order the library sidebar lists them. */
const KINDS: { id: DownloadKind; label: string; icon: IconName }[] = [
  { id: 'java', label: 'Java', icon: 'java' },
  { id: 'core', label: '核心', icon: 'cores' },
  { id: 'database', label: '数据库', icon: 'database' },
  { id: 'plugin', label: '插件', icon: 'plugins' },
]

function kindOf(kind: DownloadKind): { label: string; icon: IconName } {
  return KINDS.find((entry) => entry.id === kind) ?? { label: kind, icon: 'queue' }
}

function FilterChip({
  active,
  label,
  icon,
  count,
  onClick,
}: {
  active: boolean
  label: string
  icon?: IconName
  count: number
  onClick: () => void
}) {
  return (
    <button
      className={`dlfilter__chip${active ? ' dlfilter__chip--on' : ''}`}
      aria-pressed={active}
      onClick={onClick}
    >
      {icon && <Icon name={icon} />}
      {label}
      {count > 0 && <span className="dlfilter__n">{count}</span>}
    </button>
  )
}

/**
 * One download.
 *
 * The progress bar is only drawn for a job that has one. A queued job has no
 * progress and a bar sitting at 0% would read as a stalled download rather than
 * as a waiting one; a finished job's bar is a fact about the past that nobody
 * needs a graphic for. What every state does carry is the same first line —
 * what is being fetched — because that is what the operator is scanning the
 * column for.
 *
 * The shelf is named with an icon *and* a word. Colour never carries a meaning
 * on its own here, and four shelves is more than a palette can distinguish
 * anyway; see docs/design-system.md.
 */
function JobRow({
  job,
  busy,
  onCancel,
}: {
  job: DownloadJob
  busy: boolean
  onCancel?: () => void
}) {
  const fraction = job.total > 0 ? job.downloaded / job.total : 0
  const percent = Math.round(fraction * 100)
  const kind = kindOf(job.kind)
  const moving = job.state === 'downloading' || job.state === 'extracting'

  return (
    <div className={`dlrow dlrow--${job.state}`}>
      <div className="dlrow__main">
        <span className="dlrow__kind" title={kind.label}>
          <Icon name={kind.icon} />
          {kind.label}
        </span>
        <span className="dlrow__what">
          <strong>{job.title}</strong>
          {job.subtitle && <span className="dlrow__ver">{job.subtitle}</span>}
        </span>
        <JobBadge job={job} />
        {onCancel && (
          <button className="link link--danger dlrow__cancel" onClick={onCancel} disabled={busy}>
            取消
          </button>
        )}
      </div>

      {moving && (
        <div className="progress">
          <div className="progress__bar" style={{ width: `${percent}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${percent}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : job.state === 'extracting'
                ? '解压中'
                : formatBytes(job.downloaded)}
          </span>
        </div>
      )}

      {/* The failure reason goes on a line of its own rather than into the
          badge's tooltip: it is the whole content of a failed row, and it is
          regularly a sentence — "仓库里没有名为 X 的文件". */}
      {job.error && job.state === 'failed' && <p className="dlrow__error">{job.error}</p>}

      <p className="dlrow__meta">
        {job.fileName || '还没确定是哪个文件'}
        {job.route && ` · 来自 ${routeLabel(job.route)}`}
        {job.state === 'queued' && ' · 排队中'}
        {job.finishedAt && ` · ${formatDate(job.finishedAt)}`}
      </p>
    </div>
  )
}

function JobBadge({ job }: { job: DownloadJob }) {
  switch (job.state) {
    case 'queued':
      return <span className="badge badge--muted">排队</span>
    case 'downloading':
      return <span className="badge">下载中</span>
    case 'extracting':
      return <span className="badge">解压中</span>
    case 'done':
      return <span className="badge badge--ok">完成</span>
    case 'cancelled':
      return <span className="badge badge--muted">已取消</span>
    default:
      return <span className="badge badge--warn">失败</span>
  }
}

/**
 * Names a route id for the job line. Unknown ids are custom prefixes, which are
 * already their own name.
 */
export function routeLabel(id: string): string {
  switch (id) {
    case 'direct':
      return '源站直连'
    case 'official':
      return '官方'
    default:
      return id
  }
}
