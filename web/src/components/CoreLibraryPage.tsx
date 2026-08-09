import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { CoreBuild, CoreDownloadJob, CoreProject, CoreVersion, ServerCore } from '../types'
import type { CoreController } from '../useCores'

const SUPPORT_LABELS: Record<string, string> = {
  SUPPORTED: '官方支持中',
  UNSUPPORTED: '已停止支持',
  DEPRECATED: '已弃用',
  UNKNOWN: '',
}

function versionLabel(version: CoreVersion): string {
  const parts = [version.id]
  if (version.javaMinimum > 0) parts.push(`Java ${version.javaMinimum}+`)
  const support = SUPPORT_LABELS[version.support] ?? version.support
  if (support) parts.push(support)
  return parts.join(' · ')
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
export function CoreLibraryPage({ cores }: { cores: CoreController }) {
  const [projects, setProjects] = useState<CoreProject[]>([])
  const [projectId, setProjectId] = useState('')
  const [versions, setVersions] = useState<CoreVersion[]>([])
  const [versionId, setVersionId] = useState('')
  const [build, setBuild] = useState<CoreBuild | null>(null)
  const [showUnstable, setShowUnstable] = useState(false)
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
    api
      .listCoreVersions(projectId)
      .then((list) => {
        if (!live) return
        setVersions(list)
        setVersionId(pickDefault(list))
        setCatalogueError(null)
      })
      .catch((err) =>
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
    if (!window.confirm(`确定要从核心库删除 ${core.fileName}（${formatBytes(core.size)}）吗？${inUse}`)) {
      return
    }
    await cores.remove(core.id)
  }

  const visible = versions.filter((v) => showUnstable || v.stable)
  const selected = versions.find((v) => v.id === versionId)
  const stored = cores.cores

  return (
    <div className="page">
      <h1>服务端核心</h1>
      <p className="page__lead">
        面板下载的服务端 jar 都在这里存一份。创建实例时直接从这里挑一个复制过去，
        同一个核心开十个服也只下载一次；下载走服务器自己的网络，不经过你的浏览器，
        关掉网页也会继续。
      </p>

      <section className="panel">
        <div className="chart-head">
          <h3 className="panel__title">核心库</h3>
          <p className="chart-head__meta">
            {stored.length > 0 ? `${stored.length} 个核心` : '还是空的'}
            {library?.root && ' · '}
            {library?.root && <code>{library.root}</code>}
          </p>
        </div>

        <div className="java-list">
          {stored.map((core) => (
            <div className="java-row" key={core.id}>
              <div className="java-row__main">
                <strong>{core.imported ? core.fileName : `${core.projectName} ${core.version}`}</strong>
                {!core.imported && <span className="badge">构建 #{core.build}</span>}
                {core.kind === 'proxy' && <span className="badge">代理端</span>}
                {core.imported && <span className="badge">自行放入</span>}
                {!core.imported && !isRecommended(core.channel) && (
                  <span className="badge">{core.channel}</span>
                )}
                <span className="java-row__spacer" />
                <button
                  className="link link--danger"
                  disabled={busy}
                  onClick={() => void remove(core)}
                >
                  删除
                </button>
              </div>
              <div className="java-row__meta">
                {formatBytes(core.size)} · 加入于 {new Date(core.addedAt).toLocaleString()}
                {core.usedBy.length > 0 && <> · 使用中：{core.usedBy.join('、')}</>}
              </div>
              <div className="java-row__meta">
                <code>{core.fileName}</code>
              </div>
            </div>
          ))}

          {stored.length === 0 && (
            <p className="muted">
              核心库还是空的。下面下一个，或者把自己的 jar（Forge、Fabric、模组整合包的服务端）
              直接丢进 <code>{library?.root ?? 'data/cores'}</code>，它也会出现在这里。
            </p>
          )}
        </div>
      </section>

      {!loading && projects.length > 0 && (
        <section className="panel">
          <h3 className="panel__title">下载新核心</h3>

          {job && <JobStatus job={job} />}
          {cores.error && <div className="alert alert--error">{cores.error}</div>}
          {catalogueError && <div className="alert alert--error">{catalogueError}</div>}

          <div className="field-row">
            <label className="field">
              <span>核心</span>
              <select
                value={projectId}
                onChange={(e) => setProjectId(e.target.value)}
                disabled={downloading}
              >
                {projects.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                    {item.kind === 'proxy' ? '（代理端）' : ''}
                  </option>
                ))}
              </select>
              {project && <small>{project.description}</small>}
            </label>

            <label className="field">
              <span>版本</span>
              <select
                value={versionId}
                onChange={(e) => setVersionId(e.target.value)}
                disabled={downloading || visible.length === 0}
              >
                {visible.map((version) => (
                  <option key={version.id} value={version.id}>
                    {versionLabel(version)}
                  </option>
                ))}
              </select>
              <small>
                {build
                  ? `将下载 ${build.fileName}（构建 #${build.build}，${formatBytes(build.size)}）`
                  : versions.length === 0
                    ? '正在获取版本列表…'
                    : '正在确认最新构建…'}
              </small>
            </label>
          </div>

          <label className="checkbox">
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
            <span>显示预览版和快照（-pre / -rc / SNAPSHOT）</span>
          </label>

          {selected && selected.javaMinimum > 0 && (
            <p className="chart-note">
              该版本至少需要 Java {selected.javaMinimum}，机器上的 Java 太旧会在启动时直接报错 ——
              「Java 运行时」页面可以一键装一个。
            </p>
          )}
          {build && !isRecommended(build.channel) && (
            <div className="alert alert--error">
              这是 {build.channel} 频道的构建，PaperMC 不建议用在正式服上。
            </div>
          )}

          <div className="settings__actions">
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
                下载到核心库
              </button>
            )}
            <span className="file-toolbar__hint">
              下载完成后，在实例的「启动设置」或新建实例时选它即可。
            </span>
          </div>
        </section>
      )}
    </div>
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
