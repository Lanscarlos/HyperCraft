import { useEffect } from 'react'

import { ask } from '../confirm'
import { formatBytes, formatDate } from '../format'
import type { LibraryView } from '../routes'
import type { CoreDownloadJob, ServerCore } from '../types'
import type { CoreController } from '../useCores'
import { CoreCatalogue, isRecommended, useCoreCatalogue } from './CoreCatalogue'
import { Page } from './Page'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/**
 * Panel-wide server core management: what has been downloaded, and a one-click
 * fetch of a new build.
 *
 * It is its own page for the same reason the Java one is — a core is shared.
 * Download Paper 1.21.11 once and every instance you create afterwards can be
 * stamped out of it, offline and instantly, instead of pulling the same 60 MB
 * again per server. Instances are handed their own copy, so deleting a core
 * here never touches a server that is already running one.
 *
 * The shelf and the catalogue are two pages now — see LIBRARY_VIEWS — but one
 * component, because the catalogue is three chained fetches (projects, then
 * versions, then the build) and stepping over to look at the shelf should not
 * throw the answer away and ask again on the way back. The catalogue itself
 * lives in useCoreCatalogue/CoreCatalogue, shared with the creation wizard,
 * which downloads a core from the same three requests.
 */
export function CoreLibraryPage({
  cores,
  view,
  onOpenView,
  onOpenJava,
}: {
  cores: CoreController
  view: LibraryView
  onOpenView: (view: LibraryView) => void
  onOpenJava: () => void
}) {
  // Kept alive across a step over to the shelf: the catalogue is three chained
  // fetches and coming back should not re-run them.
  const catalogue = useCoreCatalogue(true)
  const { projects, projectId, versionId, project, loading } = catalogue

  const { library, job, downloading, busy } = cores

  // A finished download adds a core; the library list has to catch up.
  useEffect(() => {
    if (job?.state !== 'done') return
    void cores.refresh()
  }, [job?.state, job?.coreId, cores])

  const remove = async (core: ServerCore) => {
    const ok = await ask({
      title: '从核心库删除这个 jar？',
      lead: `${core.fileName}（${formatBytes(core.size)}）`,
      detail:
        core.usedBy.length > 0
          ? `实例「${core.usedBy.join('、')}」正在用同名的 jar 启动，不过它们各自有一份副本，删掉库里的这个不影响它们。`
          : '没有实例在用它。之后还要的话可以从下载页再取一次。',
      confirmLabel: '删除',
      danger: true,
    })
    if (!ok) return
    await cores.remove(core.id)
  }

  const stored = cores.cores
  const totalSize = stored.reduce((sum, core) => sum + core.size, 0)

  const downloadView = view === 'download'

  return (
    <Page
      wide
      title={downloadView ? '下载核心' : '服务端核心'}
      lead={
        downloadView
          ? '从上游直接拉一个构建下来。下载走服务器自己的网络，不经过你的浏览器，关掉网页也会继续。'
          : '面板下载的服务端 jar 都在这里存一份。创建实例时直接从这里挑一个复制过去，同一个核心开十个服也只下载一次。'
      }
      aside={
        <p className="meta-chips">
          <span>{stored.length > 0 ? `${stored.length} 个核心` : '核心库还是空的'}</span>
          {stored.length > 0 && <span>共 {formatBytes(totalSize)}</span>}
          {library?.root && <span title={library.root}>存放于 {library.root}</span>}
        </p>
      }
    >
      {/* A download keeps going after you navigate away, so its progress
          follows you to the shelf rather than only living on the page that
          started it. */}
      {!downloadView && job && job.state === 'downloading' && <JobStatus job={job} />}

      {!downloadView && (
      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">核心库</h2>
          <p className="chart-head__meta">把自己的 jar 丢进核心库目录，也会出现在这里</p>
        </div>

        {stored.length === 0 ? (
          <div className="welcome__empty">
            <p>核心库还是空的。</p>
            <p className="muted">
              <button className="link" type="button" onClick={() => onOpenView('download')}>
                下一个 Paper 或 Velocity
              </button>
              ，或者把自己的 jar（Forge、Fabric、模组整合包的服务端）直接放进核心库目录。
            </p>
          </div>
        ) : (
          <div className="asset-list">
            {stored.map((core) => (
              <CoreRow key={core.id} core={core} busy={busy} onRemove={() => void remove(core)} />
            ))}
          </div>
        )}
      </section>
      )}

      {/* The list of downloadable projects comes from upstream, so this card
          is the one thing on the page that waits on the network — and it used
          to simply not be there until it was, which reads as the page having
          finished a card short. */}
      {downloadView && loading && (
        <SkeletonScreen inPage label="正在读取可下载的核心…">
          <SkeletonPanel title={false}>
            <div className="chart-head">
              <Skeleton w="88px" h={15} />
              <Skeleton w="260px" h={12} />
            </div>
            <Skeleton w="100%" h={34} />
            <Skeleton w="72%" h={34} />
          </SkeletonPanel>
        </SkeletonScreen>
      )}

      {downloadView && !loading && projects.length === 0 && (
        <div className="alert alert--error">
          没能取到可下载的核心列表 —— 通常是这台机器连不上外网。已经下载过的核心不受影响，
          在「核心库」里照常可用。
        </div>
      )}

      {downloadView && !loading && projects.length > 0 && (
        <section className="panel">
          <p className="chart-note">
            下载完成后，新建实例时选它，或在实例的「实例设置 → 从核心库安装」里装上。
          </p>

          {job && <JobStatus job={job} />}
          {cores.error && <div className="alert alert--error">{cores.error}</div>}
          {catalogue.error && <div className="alert alert--error">{catalogue.error}</div>}

          <CoreCatalogue catalogue={catalogue} disabled={downloading} onOpenJava={onOpenJava} />

          <div className="actions">
            {downloading ? (
              <button
                className="btn btn--danger"
                type="button"
                onClick={() => void cores.cancel()}
                disabled={busy}
              >
                取消下载
              </button>
            ) : (
              <button
                className="btn btn--primary"
                type="button"
                onClick={() => projectId && versionId && void cores.download(projectId, versionId)}
                disabled={busy || !versionId}
              >
                下载 {project?.name ?? ''} {versionId}
              </button>
            )}
          </div>
        </section>
      )}
    </Page>
  )
}

function CoreRow({
  core,
  busy,
  onRemove,
}: {
  core: ServerCore
  busy: boolean
  onRemove: () => void
}) {
  // An imported jar has no project and no version, so its file name is the only
  // name it has — which is also why it is the one row that does not repeat the
  // file name underneath.
  const title = core.imported ? core.fileName : `${core.projectName} ${core.version}`

  return (
    <article className="asset">
      <div className="asset__head">
        <span className="asset__tile asset__tile--accent">
          {(core.projectName || core.fileName).slice(0, 1).toUpperCase()}
        </span>
        <div className="asset__title">
          <span className="asset__label">
            <strong title={title}>{title}</strong>
            {core.kind === 'proxy' && <span className="badge">代理端</span>}
            {core.imported && <span className="badge">自行放入</span>}
            {!core.imported && !isRecommended(core.channel) && (
              <span className="badge badge--warn">{core.channel}</span>
            )}
          </span>
          <span className="asset__sub">
            <span>{core.imported ? '自行放入的 jar' : core.projectName || '未知来源'}</span>
            {!core.imported && <code title={core.fileName}>{core.fileName}</code>}
          </span>
        </div>
      </div>

      <dl className="asset__facts asset__facts--split">
        <div>
          <dt>构建</dt>
          {/* A dash rather than a missing pair: the column has to stay a column
              even on the row that has nothing to put in it. */}
          <dd>{core.imported ? '—' : `#${core.build}`}</dd>
        </div>
        <div>
          <dt>体积</dt>
          <dd>{formatBytes(core.size)}</dd>
        </div>
        <div>
          <dt>加入于</dt>
          <dd>{formatDate(core.addedAt)}</dd>
        </div>
      </dl>

      <footer className="asset__actions asset__actions--split">
        {core.usedBy.length > 0 ? (
          <span className="asset__users">
            使用中：
            {core.usedBy.map((name) => (
              <span className="badge" key={name}>
                {name}
              </span>
            ))}
          </span>
        ) : (
          <span className="muted">暂时没有实例用它</span>
        )}
        <button className="link link--danger" disabled={busy} onClick={onRemove}>
          删除
        </button>
      </footer>
    </article>
  )
}

function JobStatus({ job }: { job: CoreDownloadJob }) {
  if (job.state === 'downloading') {
    const fraction = job.total > 0 ? job.downloaded / job.total : 0
    return (
      <div className="download-status">
        <div className="progress">
          <div className="progress__bar" style={{ width: `${Math.round(fraction * 100)}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${Math.round(fraction * 100)}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : formatBytes(job.downloaded)}
          </span>
        </div>
        <p className="chart-note">
          正在下载 {job.fileName}（{job.projectName} {job.version} 构建 #{job.build}）
        </p>
      </div>
    )
  }

  if (job.state === 'done') {
    return <div className="alert alert--ok">已下载 {job.fileName}，现在可以复制到任意实例。</div>
  }

  if (job.state === 'cancelled') {
    return <div className="alert alert--ok">已取消下载 {job.fileName}，未写入任何文件。</div>
  }

  return (
    <div className="alert alert--error">
      下载失败：{job.error ?? '未知错误'}
      {job.fileName && `（${job.fileName}）`}
    </div>
  )
}
