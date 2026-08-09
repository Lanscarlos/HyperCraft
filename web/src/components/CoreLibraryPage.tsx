import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { CoreBuild, CoreDownloadJob, CoreProject, CoreVersion, ServerCore } from '../types'
import type { CoreController } from '../useCores'
import { Page } from './Page'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

const SUPPORT_LABELS: Record<string, string> = {
  SUPPORTED: '官方支持中',
  UNSUPPORTED: '已停止支持',
  DEPRECATED: '已弃用',
  UNKNOWN: '',
}

/** The version most people want: newest, still supported, not a pre-release. */
function pickDefault(versions: CoreVersion[]): string {
  const supported = versions.find((v) => v.stable && v.support === 'SUPPORTED')
  return (supported ?? versions.find((v) => v.stable) ?? versions[0])?.id ?? ''
}

function isRecommended(channel: string): boolean {
  return ['STABLE', 'RECOMMENDED', 'DEFAULT'].includes(channel.toUpperCase())
}

/**
 * Panel-wide server core management: what has been downloaded, and a one-click
 * fetch of a new build.
 *
 * It is its own page for the same reason the Java one is — a core is shared.
 * Download Paper 1.21.11 once and every instance you create afterwards can be
 * stamped out of it, offline and instantly, instead of pulling the same 60 MB
 * again per server. Instances are handed their own copy, so deleting a core
 * here never touches a server that is already running one.
 */
export function CoreLibraryPage({
  cores,
  onOpenJava,
}: {
  cores: CoreController
  onOpenJava: () => void
}) {
  const [projects, setProjects] = useState<CoreProject[]>([])
  const [projectId, setProjectId] = useState('')
  const [versions, setVersions] = useState<CoreVersion[]>([])
  const [versionId, setVersionId] = useState('')
  const [build, setBuild] = useState<CoreBuild | null>(null)
  const [showUnstable, setShowUnstable] = useState(false)
  const [filter, setFilter] = useState('')
  const [catalogueError, setCatalogueError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const { library, job, downloading, busy } = cores
  const project = projects.find((p) => p.id === projectId)

  useEffect(() => {
    let live = true
    api
      .listCoreProjects()
      .then((list) => {
        if (!live) return
        setProjects(list)
        setProjectId((current) => current || list[0]?.id || '')
      })
      .catch(() => setProjects([]))
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
  }, [])

  useEffect(() => {
    if (!projectId) return
    let live = true
    setVersions([])
    setVersionId('')
    setBuild(null)
    setFilter('')
    api
      .listCoreVersions(projectId)
      .then((list) => {
        if (!live) return
        setVersions(list)
        setVersionId(pickDefault(list))
        setCatalogueError(null)
      })
      .catch(
        (err) =>
          live && setCatalogueError(err instanceof Error ? err.message : '获取版本列表失败'),
      )
    return () => {
      live = false
    }
  }, [projectId])

  // Resolving the build up front means the operator sees the exact file, its
  // size and its channel before anything touches the disk.
  useEffect(() => {
    if (!projectId || !versionId) return
    let live = true
    setBuild(null)
    api
      .latestCoreBuild(projectId, versionId)
      .then((latest) => live && setBuild(latest))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [projectId, versionId])

  // A finished download adds a core; the library list has to catch up.
  useEffect(() => {
    if (job?.state !== 'done') return
    void cores.refresh()
  }, [job?.state, job?.coreId, cores])

  const remove = async (core: ServerCore) => {
    const inUse =
      core.usedBy.length > 0
        ? `实例「${core.usedBy.join('、')}」正在用同名的 jar 启动，不过它们各自有一份副本，删掉库里的这个不影响它们。`
        : ''
    if (
      !window.confirm(`确定要从核心库删除 ${core.fileName}（${formatBytes(core.size)}）吗？${inUse}`)
    ) {
      return
    }
    await cores.remove(core.id)
  }

  // Two filters over one list: the stability switch is a decision about what is
  // safe to run, the text box is only about finding a row in a list of 200.
  // Whatever is selected always survives both — the download button names that
  // version, so it has to stay on screen next to it.
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const matched = versions.filter(
      (v) => (showUnstable || v.stable) && (!needle || v.id.toLowerCase().includes(needle)),
    )
    if (versionId && !matched.some((v) => v.id === versionId)) {
      const picked = versions.find((v) => v.id === versionId)
      if (picked) return [picked, ...matched]
    }
    return matched
  }, [versions, showUnstable, filter, versionId])

  const selected = versions.find((v) => v.id === versionId)
  const stored = cores.cores
  const totalSize = stored.reduce((sum, core) => sum + core.size, 0)

  return (
    <Page
      wide
      title="服务端核心"
      lead="面板下载的服务端 jar 都在这里存一份。创建实例时直接从这里挑一个复制过去，同一个核心开十个服也只下载一次；下载走服务器自己的网络，不经过你的浏览器，关掉网页也会继续。"
      aside={
        <p className="meta-chips">
          <span>{stored.length > 0 ? `${stored.length} 个核心` : '核心库还是空的'}</span>
          {stored.length > 0 && <span>共 {formatBytes(totalSize)}</span>}
          {library?.root && <span title={library.root}>存放于 {library.root}</span>}
        </p>
      }
    >

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">核心库</h2>
          <p className="chart-head__meta">把自己的 jar 丢进核心库目录，也会出现在这里</p>
        </div>

        {stored.length === 0 ? (
          <div className="welcome__empty">
            <p>核心库还是空的。</p>
            <p className="muted">
              在下面下一个 Paper 或 Velocity，或者把自己的 jar（Forge、Fabric、模组整合包的服务端）
              直接放进核心库目录。
            </p>
          </div>
        ) : (
          <div className="asset-grid">
            {stored.map((core) => (
              <CoreCard
                key={core.id}
                core={core}
                busy={busy}
                onRemove={() => void remove(core)}
              />
            ))}
          </div>
        )}
      </section>

      {/* The list of downloadable projects comes from upstream, so this card
          is the one thing on the page that waits on the network — and it used
          to simply not be there until it was, which reads as the page having
          finished a card short. */}
      {loading && (
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

      {!loading && projects.length > 0 && (
        <section className="panel">
          <div className="chart-head">
            <h2 className="panel__title">下载新核心</h2>
            <p className="chart-head__meta">
              下载完成后，新建实例时选它，或在实例的「启动设置 → 从核心库安装」里装上
            </p>
          </div>

          {job && <JobStatus job={job} />}
          {cores.error && <div className="alert alert--error">{cores.error}</div>}
          {catalogueError && <div className="alert alert--error">{catalogueError}</div>}

          <div className="field">
            <span>核心</span>
            <div className="choice-grid choice-grid--wide">
              {projects.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className={`choice${item.id === projectId ? ' choice--active' : ''}`}
                  aria-pressed={item.id === projectId}
                  disabled={downloading}
                  onClick={() => setProjectId(item.id)}
                >
                  <span className="choice__label">
                    {item.name}
                    {item.kind === 'proxy' && <span className="badge">代理端</span>}
                  </span>
                  <span className="choice__note">{item.description}</span>
                </button>
              ))}
            </div>
          </div>

          <div className="field">
            <div className="field__head">
              <span>版本</span>
              <div className="field__tools">
                <input
                  className="input-slim"
                  type="search"
                  value={filter}
                  placeholder="筛选版本，如 1.21"
                  aria-label="筛选版本"
                  onChange={(e) => setFilter(e.target.value)}
                  disabled={downloading || versions.length === 0}
                />
                <label className="checkbox checkbox--inline">
                  <input
                    type="checkbox"
                    checked={showUnstable}
                    onChange={(e) => {
                      setShowUnstable(e.target.checked)
                      if (!e.target.checked && selected && !selected.stable) {
                        setVersionId(pickDefault(versions))
                      }
                    }}
                    disabled={downloading}
                  />
                  <span>显示预览版和快照</span>
                </label>
              </div>
            </div>

            {versions.length === 0 ? (
              <p className="muted">正在获取版本列表…</p>
            ) : visible.length === 0 ? (
              <p className="muted">没有匹配的版本。</p>
            ) : (
              <div className="version-list">
                {visible.map((version) => (
                  <button
                    key={version.id}
                    type="button"
                    className={`version${version.id === versionId ? ' version--active' : ''}${
                      version.stable ? '' : ' version--unstable'
                    }`}
                    aria-pressed={version.id === versionId}
                    disabled={downloading}
                    title={SUPPORT_LABELS[version.support] || undefined}
                    onClick={() => setVersionId(version.id)}
                  >
                    {version.id}
                  </button>
                ))}
              </div>
            )}

            {selected && (
              <small>
                {[
                  SUPPORT_LABELS[selected.support] ?? selected.support,
                  selected.javaMinimum > 0 ? `需要 Java ${selected.javaMinimum}+` : '',
                  selected.builds > 0 ? `${selected.builds} 个构建` : '',
                  selected.stable ? '' : '预览版',
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </small>
            )}
          </div>

          {build ? (
            <div className="build-summary">
              <div className="build-summary__file">
                <code>{build.fileName}</code>
                <span className="badge">构建 #{build.build}</span>
                {!isRecommended(build.channel) && (
                  <span className="badge badge--warn">{build.channel}</span>
                )}
              </div>
              <dl className="asset__facts">
                <div>
                  <dt>体积</dt>
                  <dd>{formatBytes(build.size)}</dd>
                </div>
                <div>
                  <dt>发布于</dt>
                  <dd>{formatDate(build.time)}</dd>
                </div>
                <div>
                  <dt>校验</dt>
                  <dd title={build.sha256}>SHA-256 已提供</dd>
                </div>
              </dl>
            </div>
          ) : (
            versionId && <p className="muted">正在确认最新构建…</p>
          )}

          {selected && selected.javaMinimum > 0 && (
            <p className="chart-note">
              该版本至少需要 Java {selected.javaMinimum}，机器上的 Java 太旧会在启动时直接报错 ——
              <button className="link" onClick={onOpenJava}>
                Java 环境
              </button>
              页面可以一键装一个。
            </p>
          )}
          {build && !isRecommended(build.channel) && (
            <div className="alert alert--warn">
              这是 {build.channel} 频道的构建，PaperMC 不建议用在正式服上。
            </div>
          )}

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

function CoreCard({
  core,
  busy,
  onRemove,
}: {
  core: ServerCore
  busy: boolean
  onRemove: () => void
}) {
  const title = core.imported ? core.fileName : `${core.projectName} ${core.version}`

  return (
    <article className="asset">
      <div className="asset__head">
        <span className="asset__tile asset__tile--accent">
          {(core.projectName || core.fileName).slice(0, 1).toUpperCase()}
        </span>
        <div className="asset__title">
          <strong title={title}>{title}</strong>
          <span className="asset__sub">
            {core.imported ? '自行放入的 jar' : core.projectName || '未知来源'}
          </span>
        </div>
        {core.kind === 'proxy' && <span className="badge">代理端</span>}
        {core.imported && <span className="badge">自行放入</span>}
        {!core.imported && !isRecommended(core.channel) && (
          <span className="badge badge--warn">{core.channel}</span>
        )}
      </div>

      <dl className="asset__facts">
        {!core.imported && (
          <div>
            <dt>构建</dt>
            <dd>#{core.build}</dd>
          </div>
        )}
        <div>
          <dt>体积</dt>
          <dd>{formatBytes(core.size)}</dd>
        </div>
        <div>
          <dt>加入于</dt>
          <dd>{formatDate(core.addedAt)}</dd>
        </div>
      </dl>

      <p className="asset__path" title={core.fileName}>
        <code>{core.fileName}</code>
      </p>

      <footer className="asset__actions">
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
