import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { BrowseVersion, InstallTarget, PluginBrowseDetail, PluginListing } from '../types'
import { useDismiss } from '../useDismiss'
import { CompatBadge } from './PluginCompat'
import { formatDownloads, sourceLabel } from './PluginBrowse'

/**
 * One plugin's detail, slid in from the right over the results.
 *
 * Not a page. The operator is comparing four candidates, and a page would take
 * the search text, the filters, the result list and the scroll position with
 * it — so opening the second candidate means retyping everything that led to
 * the first. Everything a comparison needs is here: the description, the
 * version list with a version actually selectable, what it depends on, and a
 * link out to the source for the things a panel should not try to reproduce.
 */
export function PluginDrawer({
  listing,
  instanceId,
  targets,
  onClose,
  onInstalled,
  onOpenInstance,
}: {
  listing: PluginListing
  instanceId: string
  /** Where 安装 would put it. Several is normal from the panel-wide entry. */
  targets: InstallTarget[]
  onClose: () => void
  onInstalled: () => void
  onOpenInstance?: (id: string) => void
}) {
  const { leaving, close } = useDismiss(onClose)
  const [detail, setDetail] = useState<PluginBrowseDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [chosen, setChosen] = useState<string>('')
  const [installing, setInstalling] = useState(false)
  const [progress, setProgress] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    setLoading(true)
    api
      .browsePlugin(listing.source, listing.id, instanceId || undefined)
      .then((next) => {
        if (!live) return
        setDetail(next)
        // The newest compatible version, falling back to the newest at all —
        // an operator on 1.16.5 opening a plugin whose newest release is for
        // 1.21 wants the one that will run, preselected.
        const fit = next.versions.find((version) => version.compat.state === 'ok')
        setChosen((fit ?? next.versions[0])?.tag ?? '')
        setError(null)
      })
      .catch((err) => live && setError(err instanceof Error ? err.message : '读取插件详情失败'))
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
  }, [listing.source, listing.id, instanceId])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || installing) return
      event.preventDefault()
      close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [close, installing])

  const version = detail?.versions.find((entry) => entry.tag === chosen) ?? null

  /**
   * Download once into the library, then copy into each server.
   *
   * The library step is not ceremony: it is what makes the same jar on five
   * servers one file with one checksum, and what makes rolling back possible
   * afterwards. Progress is reported per phase because the download is the
   * slow part and the copies are instant.
   */
  const install = async () => {
    if (!version || targets.length === 0) return
    setInstalling(true)
    setError(null)
    setDone(null)

    try {
      setProgress('正在登记到插件库…')
      const item = await api.trackPlugin({
        source: listing.source,
        id: listing.id,
        name: listing.name,
        targetDir: targets[0]?.pluginDir,
      })

      if (!version.held) {
        setProgress(`正在下载 ${version.version}…`)
        await api.downloadPlugin(item.id, version.tag)
        await waitForDownload(item.id, version.tag, setProgress)
      }

      const failures: string[] = []
      for (const target of targets) {
        setProgress(`正在装入 ${target.name}…`)
        try {
          await api.installInstancePlugin(target.id, item.id, version.tag)
        } catch (err) {
          failures.push(`${target.name}：${err instanceof Error ? err.message : '安装失败'}`)
        }
      }

      if (failures.length > 0) {
        setError(failures.join('；'))
        return
      }
      setDone(
        `已装入 ${targets.map((target) => target.name).join('、')}` +
          `${targets.some((target) => target.state === 'running') ? '，重启后生效' : ''}`,
      )
      onInstalled()
    } catch (err) {
      setError(err instanceof Error ? err.message : '安装失败')
    } finally {
      setInstalling(false)
      setProgress(null)
    }
  }

  const blocked = targets.length === 0
  const incompatible = version?.compat.state === 'bad'
  // Whether the way out is picking a different version or giving up on this
  // plugin. Two very different answers, and the footer says which.
  const anyFits = (detail?.versions ?? []).some((entry) => entry.compat.state !== 'bad')

  return createPortal(
    <div className={`drawer${leaving ? ' drawer--leaving' : ''}`}>
      <div className="drawer__scrim" onClick={() => !installing && close()} aria-hidden="true" />
      <aside className="drawer__panel" role="dialog" aria-label={listing.name}>
        <header className="drawer__head">
          <div className="drawer__title">
            {listing.iconUrl ? (
              <img className="browse-row__icon" src={listing.iconUrl} alt="" />
            ) : (
              <span className="browse-row__icon browse-row__icon--blank">
                {listing.name.slice(0, 1).toUpperCase()}
              </span>
            )}
            <div>
              <h2>{listing.name}</h2>
              <p className="drawer__sub">
                {listing.author && <span>{listing.author}</span>}
                <span>{sourceLabel(listing.source)}</span>
                <span>{formatDownloads(listing.downloads)} 次下载</span>
                {listing.updated && <span>更新于 {formatDate(listing.updated)}</span>}
              </p>
            </div>
          </div>
          <button className="link" onClick={() => !installing && close()} aria-label="关闭">
            ✕
          </button>
        </header>

        <div className="drawer__body">
          {error && <div className="alert alert--error">{error}</div>}
          {done && <div className="alert alert--ok">{done}</div>}
          {loading && <p className="muted">正在读取…</p>}

          {detail && (
            <>
              <section className="drawer__section">
                <CompatBadge compat={detail.listing.compat} />
                {detail.tracked && (
                  <p className="chart-note">
                    插件库里已经有它了
                    {detail.tracked.usedBy.length > 0 &&
                      ` · 正在被 ${detail.tracked.usedBy.join('、')} 使用`}
                  </p>
                )}
              </section>

              <section className="drawer__section">
                <h3>版本</h3>
                {detail.versions.length === 0 ? (
                  <p className="muted">这个来源没有列出可下载的版本。</p>
                ) : (
                  <div className="drawer__versions">
                    {detail.versions.slice(0, 12).map((entry) => (
                      <VersionRow
                        key={entry.tag}
                        version={entry}
                        chosen={entry.tag === chosen}
                        onChoose={() => setChosen(entry.tag)}
                      />
                    ))}
                  </div>
                )}
              </section>

              {version?.dependencies && version.dependencies.length > 0 && (
                <section className="drawer__section">
                  <h3>依赖</h3>
                  <ul className="drawer__deps">
                    {version.dependencies.map((dep) => (
                      <li key={dep.name}>
                        <span>{dep.name}</span>
                        <span className={`badge ${dep.required ? 'badge--warn' : ''}`}>
                          {dep.required ? '必需' : '可选'}
                        </span>
                      </li>
                    ))}
                  </ul>
                  <p className="chart-note">
                    必需的前置装不上，服务端启动时这个插件会直接加载失败 —— 记得一起装。
                  </p>
                </section>
              )}

              {version?.notes && (
                <section className="drawer__section">
                  <h3>更新日志</h3>
                  <pre className="drawer__notes">{version.notes.slice(0, 4000)}</pre>
                </section>
              )}

              <section className="drawer__section">
                <h3>简介</h3>
                <pre className="drawer__notes">{detail.body?.slice(0, 4000) || '（没有简介）'}</pre>
              </section>

              {listing.pageUrl && (
                <a className="link" href={listing.pageUrl} target="_blank" rel="noreferrer">
                  在 {sourceLabel(listing.source)} 上查看 ↗
                </a>
              )}
            </>
          )}
        </div>

        <footer className="drawer__foot">
          <div className="drawer__foot-info">
            {blocked ? (
              <span className="muted">先在左边选一台服务器</span>
            ) : incompatible ? (
              // The button is greyed out, and a greyed-out button with no
              // sentence beside it is a dead end. The version list above is
              // where the way out is, so this says to look there.
              <span className="drawer__warn">
                {version?.compat.detail ?? version?.compat.label}
                {anyFits ? ' —— 上面挑一个绿色的版本' : ' —— 这个插件没有适配这台服的版本'}
              </span>
            ) : (
              <span>
                装到 {targets.map((target) => target.name).join('、')}
                {onOpenInstance && targets.length === 1 && (
                  <button className="link" onClick={() => onOpenInstance(targets[0].id)}>
                    打开
                  </button>
                )}
              </span>
            )}
            {progress && <span className="drawer__progress">{progress}</span>}
          </div>
          <button
            className="btn btn--primary"
            disabled={blocked || installing || !version || !listing.downloadable || incompatible}
            onClick={() => void install()}
          >
            {installing
              ? '安装中…'
              : incompatible
                ? '不兼容'
                : version?.held
                  ? `安装 ${version.version}`
                  : `下载并安装 ${version?.version ?? ''}`}
          </button>
        </footer>
      </aside>
    </div>,
    document.body,
  )
}

function VersionRow({
  version,
  chosen,
  onChoose,
}: {
  version: BrowseVersion
  chosen: boolean
  onChoose: () => void
}) {
  return (
    <button
      className={`drawer__version${chosen ? ' drawer__version--chosen' : ''}`}
      onClick={onChoose}
      aria-pressed={chosen}
    >
      <span className="drawer__version-head">
        <strong>{version.version}</strong>
        {version.prerelease && <span className="badge badge--warn">预发布</span>}
        <CompatBadge compat={version.compat} />
        {version.unverified && (
          <span className="badge badge--muted" title="来源只公布了插件当前的兼容版本，不是这个版本的">
            兼容性未逐版本标注
          </span>
        )}
      </span>
      <span className="drawer__version-meta">
        {formatDate(version.publishedAt)}
        {version.asset.size > 0 && ` · ${formatBytes(version.asset.size)}`}
        {version.gameVersions?.length ? ` · ${version.gameVersions.slice(0, 4).join('、')}` : ''}
        {version.held && ' · 库里已有'}
      </span>
    </button>
  )
}

/**
 * Waits for the library download to finish.
 *
 * The download belongs to the daemon rather than to this request — closing the
 * tab does not stop it — so the only way to know it landed is to ask. Polled at
 * the same cadence the library page uses, so the progress reads the same in
 * both places.
 *
 * The job is matched against the plugin and tag that were asked for. There is
 * one download slot panel-wide, and the finished job left behind by whatever
 * used it last is still readable: without the check, an operator installing
 * onto a panel that downloaded something else a minute ago would see "done"
 * immediately and go on to install a jar that is not there yet.
 */
async function waitForDownload(
  pluginId: string,
  tag: string,
  report: (text: string) => void,
): Promise<void> {
  const deadline = Date.now() + 10 * 60 * 1000
  while (Date.now() < deadline) {
    await new Promise((resolve) => window.setTimeout(resolve, 700))
    const library = await api.pluginLibrary()
    const job = library.job
    if (!job || job.pluginId !== pluginId || job.tag !== tag) continue
    if (job.state === 'downloading') {
      const percent = job.total > 0 ? Math.round((job.downloaded / job.total) * 100) : 0
      report(`正在下载 ${job.version}… ${job.total > 0 ? `${percent}%` : formatBytes(job.downloaded)}`)
      continue
    }
    if (job.state === 'done') return
    throw new Error(job.error || '下载失败')
  }
  throw new Error('下载超时')
}
