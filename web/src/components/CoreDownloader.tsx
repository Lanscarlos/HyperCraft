import { useEffect, useRef, useState } from 'react'

import { ApiError, api } from '../api'
import { formatBytes } from '../format'
import type {
  CoreBuild,
  CoreDownloadJob,
  CoreProject,
  CoreVersion,
  InstanceStatus,
} from '../types'

interface Props {
  instance: InstanceStatus
  /** Called once a download lands, with the file name it wrote. */
  onDownloaded: (fileName: string, setAsJar: boolean) => void
}

const SUPPORT_LABELS: Record<string, string> = {
  SUPPORTED: '官方支持中',
  UNSUPPORTED: '已停止支持',
  DEPRECATED: '已弃用',
  UNKNOWN: '',
}

function supportLabel(version: CoreVersion): string {
  return SUPPORT_LABELS[version.support] ?? version.support
}

function versionLabel(version: CoreVersion): string {
  const parts = [version.id]
  if (version.javaMinimum > 0) parts.push(`Java ${version.javaMinimum}+`)
  const support = supportLabel(version)
  if (support) parts.push(support)
  return parts.join(' · ')
}

/** The version most people want: newest, still supported, not a pre-release. */
function pickDefault(versions: CoreVersion[]): string {
  const supported = versions.find((v) => v.stable && v.support === 'SUPPORTED')
  return (supported ?? versions.find((v) => v.stable) ?? versions[0])?.id ?? ''
}

/**
 * Fetches a server core straight onto the machine the panel runs on.
 *
 * The transfer belongs to the daemon, not to this component: it keeps going
 * with the tab closed, and reopening the page picks the progress back up.
 */
export function CoreDownloader({ instance, onDownloaded }: Props) {
  const [projects, setProjects] = useState<CoreProject[]>([])
  const [projectId, setProjectId] = useState('')
  const [versions, setVersions] = useState<CoreVersion[]>([])
  const [versionId, setVersionId] = useState('')
  const [build, setBuild] = useState<CoreBuild | null>(null)
  const [showUnstable, setShowUnstable] = useState(false)
  const [setAsJar, setSetAsJar] = useState(true)
  const [job, setJob] = useState<CoreDownloadJob | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  const project = projects.find((p) => p.id === projectId)
  const downloading = job?.state === 'downloading'

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
        setError(null)
      })
      .catch((err) => live && setError(err instanceof Error ? err.message : '获取版本列表失败'))
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

  useEffect(() => {
    let live = true
    api
      .coreDownload(instance.id)
      .then((current) => live && setJob(current))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [instance.id])

  useEffect(() => {
    if (!downloading) return
    const timer = window.setInterval(() => {
      api
        .coreDownload(instance.id)
        .then(setJob)
        // A blip in polling is not worth an error banner; the next tick retries.
        .catch(() => undefined)
    }, 800)
    return () => window.clearInterval(timer)
  }, [downloading, instance.id])

  // Report a finished download exactly once, even though polling keeps
  // returning the same completed job afterwards.
  const reported = useRef<string | null>(null)
  useEffect(() => {
    if (job?.state !== 'done') return
    const key = `${job.fileName}@${job.finishedAt ?? ''}`
    if (reported.current === key) return
    reported.current = key
    onDownloaded(job.fileName, job.setAsJar)
  }, [job, onDownloaded])

  const start = async (overwrite: boolean) => {
    if (!projectId || !versionId) return
    setBusy(true)
    setError(null)
    try {
      setJob(
        await api.startCoreDownload(instance.id, {
          project: projectId,
          version: versionId,
          setAsJar,
          overwrite,
        }),
      )
    } catch (err) {
      // 409 with the file already there is the "same build again" case, usually
      // a re-download after a broken file — worth offering, never worth doing
      // silently to a jar that may be the one currently running.
      if (err instanceof ApiError && err.status === 409 && !overwrite) {
        const name = build?.fileName ?? '该文件'
        if (window.confirm(`${name} 已经在实例目录里了，要重新下载并覆盖吗？`)) {
          setBusy(false)
          await start(true)
          return
        }
      } else {
        setError(err instanceof Error ? err.message : '下载失败')
      }
    } finally {
      setBusy(false)
    }
  }

  const cancel = async () => {
    setBusy(true)
    try {
      await api.cancelCoreDownload(instance.id)
      setJob(await api.coreDownload(instance.id))
    } catch (err) {
      setError(err instanceof Error ? err.message : '取消失败')
    } finally {
      setBusy(false)
    }
  }

  // The panel can be built without the downloader; an empty catalogue means
  // "not available here", and uploading a jar still works.
  if (loading || projects.length === 0) return null

  const visible = versions.filter((v) => showUnstable || v.stable)
  const selected = versions.find((v) => v.id === versionId)

  return (
    <section className="panel">
      <h3 className="panel__title">下载服务端核心</h3>
      <p className="chart-note">
        由面板直接下载到实例目录，走服务器自己的网络，不经过你的浏览器。
        下载过程归守护进程管，关掉网页也会继续。
      </p>

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

      <label className="checkbox">
        <input
          type="checkbox"
          checked={setAsJar}
          onChange={(e) => setSetAsJar(e.target.checked)}
          disabled={downloading}
        />
        <span>
          下载完成后设为启动 jar
          {project?.kind === 'proxy' && '（代理端不吃 --nogui，会一并清空服务端参数）'}
        </span>
      </label>

      {selected && selected.javaMinimum > 0 && (
        <p className="chart-note">
          该版本至少需要 Java {selected.javaMinimum}，机器上的 Java 太旧会在启动时直接报错。
        </p>
      )}
      {build && !isRecommended(build.channel) && (
        <div className="alert alert--error">
          这是 {build.channel} 频道的构建，PaperMC 不建议用在正式服上。
        </div>
      )}

      {job && <JobStatus job={job} />}
      {error && <div className="alert alert--error">{error}</div>}

      <div className="settings__actions">
        {downloading ? (
          <button className="btn btn--danger" type="button" onClick={() => void cancel()} disabled={busy}>
            取消下载
          </button>
        ) : (
          <button
            className="btn btn--primary"
            type="button"
            onClick={() => void start(false)}
            disabled={busy || !versionId}
          >
            {busy ? '准备中…' : '下载'}
          </button>
        )}
        {project?.kind === 'server' && (
          <span className="file-toolbar__hint">
            下载完记得去「服务器配置」同意 EULA，否则服务端启动后会立刻退出。
          </span>
        )}
      </div>
    </section>
  )
}

function isRecommended(channel: string): boolean {
  return ['STABLE', 'RECOMMENDED', 'DEFAULT'].includes(channel.toUpperCase())
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
    return (
      <div className="alert alert--ok">
        已下载 {job.fileName}
        {job.setAsJar && '，并设为启动 jar'}。
      </div>
    )
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
