import { useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { CoreBuild, CoreProject, CoreVersion } from '../types'

export const SUPPORT_LABELS: Record<string, string> = {
  SUPPORTED: '官方支持中',
  UNSUPPORTED: '已停止支持',
  DEPRECATED: '已弃用',
  UNKNOWN: '',
}

export function isRecommended(channel: string): boolean {
  return ['STABLE', 'RECOMMENDED', 'DEFAULT'].includes(channel.toUpperCase())
}

/** The version most people want: newest, still supported, not a pre-release. */
function pickDefault(versions: CoreVersion[]): string {
  const supported = versions.find((v) => v.stable && v.support === 'SUPPORTED')
  return (supported ?? versions.find((v) => v.stable) ?? versions[0])?.id ?? ''
}

export interface CatalogueState {
  projects: CoreProject[]
  projectId: string
  setProjectId: (id: string) => void
  project: CoreProject | undefined
  versions: CoreVersion[]
  versionId: string
  setVersionId: (id: string) => void
  /** The version row behind `versionId`, for its support and Java facts. */
  selected: CoreVersion | undefined
  /** The versions the two filters leave standing, selection always included. */
  visible: CoreVersion[]
  filter: string
  setFilter: (value: string) => void
  showUnstable: boolean
  setShowUnstable: (value: boolean) => void
  /** The exact file a download would write, resolved before anyone clicks. */
  build: CoreBuild | null
  loading: boolean
  error: string | null
}

/**
 * The upstream catalogue: projects, their versions, and the build a download
 * would fetch.
 *
 * A hook rather than a component because both callers have to keep it alive
 * across renders where it is not on screen — the library page steps between
 * its shelf and its download form, the wizard between its five steps — and
 * three chained network round trips must not be thrown away and asked for
 * again on the way back.
 */
export function useCoreCatalogue(enabled: boolean): CatalogueState {
  const [projects, setProjects] = useState<CoreProject[]>([])
  const [projectId, setProjectId] = useState('')
  const [versions, setVersions] = useState<CoreVersion[]>([])
  const [versionId, setVersionId] = useState('')
  const [build, setBuild] = useState<CoreBuild | null>(null)
  const [showUnstable, setShowUnstable] = useState(false)
  const [filter, setFilter] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!enabled) return
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
  }, [enabled])

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
        setError(null)
      })
      .catch(
        (err) => live && setError(err instanceof Error ? err.message : '获取版本列表失败'),
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

  return {
    projects,
    projectId,
    setProjectId,
    project: projects.find((p) => p.id === projectId),
    versions,
    versionId,
    setVersionId,
    selected: versions.find((v) => v.id === versionId),
    visible,
    filter,
    setFilter,
    showUnstable,
    setShowUnstable,
    build,
    loading,
    error,
  }
}

/**
 * Picking a build out of the catalogue: which core, which version, and what
 * exactly lands on disk.
 *
 * The state lives in the caller (see useCoreCatalogue) and only the controls
 * are here, so the two places that download a core — the library page and the
 * creation wizard — cannot end up disagreeing about what a channel warning
 * says or which version is picked by default.
 *
 * The button that starts the download is deliberately *not* part of this: on
 * the library page it is the point of the page, in the wizard it is one step
 * of five, and the two say different things.
 */
export function CoreCatalogue({
  catalogue,
  disabled,
  /** Where "go install a Java" points, when the caller has somewhere to send
   *  them. The wizard has not — its own next step is that install. */
  onOpenJava,
  javaNote,
}: {
  catalogue: CatalogueState
  disabled: boolean
  onOpenJava?: () => void
  javaNote?: (major: number) => React.ReactNode
}) {
  const { projects, projectId, versions, versionId, visible, selected, build } = catalogue

  return (
    <>
      <div className="field">
        <span>核心</span>
        <div className="choice-grid choice-grid--wide">
          {projects.map((item) => (
            <button
              key={item.id}
              type="button"
              className={`choice${item.id === projectId ? ' choice--active' : ''}`}
              aria-pressed={item.id === projectId}
              disabled={disabled}
              onClick={() => catalogue.setProjectId(item.id)}
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
              value={catalogue.filter}
              placeholder="筛选版本，如 1.21"
              aria-label="筛选版本"
              onChange={(e) => catalogue.setFilter(e.target.value)}
              disabled={disabled || versions.length === 0}
            />
            <label className="checkbox checkbox--inline">
              <input
                type="checkbox"
                checked={catalogue.showUnstable}
                onChange={(e) => {
                  catalogue.setShowUnstable(e.target.checked)
                  if (!e.target.checked && selected && !selected.stable) {
                    catalogue.setVersionId(pickDefault(versions))
                  }
                }}
                disabled={disabled}
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
                disabled={disabled}
                title={SUPPORT_LABELS[version.support] || undefined}
                onClick={() => catalogue.setVersionId(version.id)}
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
          {javaNote ? (
            javaNote(selected.javaMinimum)
          ) : (
            <>
              该版本至少需要 Java {selected.javaMinimum}，机器上的 Java 太旧会在启动时直接报错 ——
              {onOpenJava && (
                <>
                  <button className="link" onClick={onOpenJava}>
                    Java 环境
                  </button>
                  页面可以一键装一个。
                </>
              )}
            </>
          )}
        </p>
      )}
      {build && !isRecommended(build.channel) && (
        <div className="alert alert--warn">
          这是 {build.channel} 频道的构建，PaperMC 不建议用在正式服上。
        </div>
      )}
    </>
  )
}
