import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { BrowseVersion, InstallTarget, PluginBrowseDetail, PluginListing } from '../types'
import { isLive } from '../types'
import { useDismiss } from '../useDismiss'
import { CompatBadge } from './PluginCompat'
import { PluginIcon } from './PluginIcon'
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
 *
 * What it does is download, into the panel-wide library. It installs onto no
 * server: that decision belongs to whoever is looking at a server, and the
 * page for it is the plugin list or the server's own. The footer says where
 * the jar went and offers the way there, which is the whole handoff.
 */
export function PluginDrawer({
  listing,
  against,
  reference,
  onClose,
  onDownloaded,
  onOpenLibrary,
}: {
  listing: PluginListing
  /** Instance ids the versions are judged against; empty means no badges. */
  against: string[]
  reference: InstallTarget | null
  onClose: () => void
  onDownloaded: () => void
  onOpenLibrary: () => void
}) {
  const { leaving, close } = useDismiss(onClose)
  const [detail, setDetail] = useState<PluginBrowseDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [chosen, setChosen] = useState<string>('')
  const [downloading, setDownloading] = useState(false)
  const [progress, setProgress] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    setLoading(true)
    api
      .browsePlugin(listing.source, listing.id, against)
      .then((next) => {
        if (!live) return
        setDetail(next)
        // The newest compatible version, falling back to the newest at all —
        // an operator on 1.16.5 opening a plugin whose newest release is for
        // 1.21 wants the one that will run, preselected.
        // With nothing to judge against there is no "compatible" to prefer,
        // and the newest is the only sensible default.
        const fit = next.versions.find((version) => version.compat?.state === 'ok')
        setChosen((fit ?? next.versions[0])?.tag ?? '')
        setError(null)
      })
      .catch((err) => live && setError(err instanceof Error ? err.message : '读取插件详情失败'))
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
    // Joined rather than passed as an array: a new array literal every render
    // would refetch the drawer on every keystroke behind it.
  }, [listing.source, listing.id, against.join(',')])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || downloading) return
      event.preventDefault()
      close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [close, downloading])

  const version = detail?.versions.find((entry) => entry.tag === chosen) ?? null
  const incompatible = version?.compat?.state === 'bad'
  // Whether the way out is picking a different version or giving up on this
  // plugin. Two very different answers, and the footer says which.
  const anyFits = (detail?.versions ?? []).some((entry) => entry.compat?.state !== 'bad')

  /**
   * Registers the source and pulls the jar into the library.
   *
   * Two steps because the library tracks where a plugin comes from as well as
   * which of its versions are on disk — that record is what makes "check for
   * updates" and "roll it back" possible later, and it is the same record
   * whether the plugin came from a registry or from somebody's GitHub.
   */
  const download = async (install?: InstallTarget) => {
    if (!version) return
    setDownloading(true)
    setError(null)
    setDone(null)

    try {
      setProgress('正在登记到插件库…')
      const item = await api.trackPlugin({
        source: listing.source,
        id: listing.id,
        name: listing.name,
        iconUrl: listing.iconUrl,
      })

      if (version.held) {
        setDone(`插件库里已经有 ${version.version} 了。`)
      } else {
        setProgress(`正在下载 ${version.version}…`)
        await api.downloadPlugin(item.id, version.tag)
        await waitForDownload(item.id, version.tag, setProgress)
        setDone(`已下载 ${version.version} 到插件库。`)
      }

      if (install) {
        setProgress(`正在复制到 ${install.name}…`)
        await api.installInstancePlugin(install.id, item.id, version.tag)
        setDone(
          `已下载 ${version.version} 到插件库，并复制到 ${install.name}` +
            (isLive(install.state) ? ' —— 重启后生效。' : '。'),
        )
      }
      onDownloaded()
    } catch (err) {
      setError(err instanceof Error ? err.message : '下载失败')
    } finally {
      setDownloading(false)
      setProgress(null)
    }
  }

  return createPortal(
    <div className={`drawer${leaving ? ' drawer--leaving' : ''}`}>
      <div className="drawer__scrim" onClick={() => !downloading && close()} aria-hidden="true" />
      <aside className="drawer__panel" role="dialog" aria-label={listing.name}>
        <header className="drawer__head">
          <div className="drawer__title">
            <PluginIcon className="browse-row__icon" src={listing.iconUrl} name={listing.name} />
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
          <button className="link" onClick={() => !downloading && close()} aria-label="关闭">
            ✕
          </button>
        </header>

        <div className="drawer__body">
          {error && <div className="alert alert--error">{error}</div>}
          {done && (
            <div className="alert alert--ok">
              {done}
              <button className="link" onClick={onOpenLibrary}>
                去插件列表装到实例
              </button>
            </div>
          )}
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
                    必需的前置装不上，服务端启动时这个插件会直接加载失败 —— 记得一起下。
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
            {incompatible ? (
              // The button is greyed out, and a greyed-out button with no
              // sentence beside it is a dead end. The version list above is
              // where the way out is, so this says to look there.
              <span className="drawer__warn">
                {version?.compat?.detail ?? version?.compat?.label}
                {anyFits ? ' —— 上面挑一个绿色的版本' : ' —— 这个插件没有适配这台服的版本'}
              </span>
            ) : (
              <span className="muted">
                下载到面板插件库
                {reference ? `，之后再装到 ${reference.name} 或别的服` : '，之后再决定装到哪台服'}
              </span>
            )}
            {progress && <span className="drawer__progress">{progress}</span>}
          </div>

          {/* The two-step model is right for a fleet and it is one hop too many
              for the person running one server: download into the library,
              then go to another page to copy it across. With exactly one
              reference server ticked there is no ambiguity about where it
              would go, so that becomes the primary and the library-only path
              stays beside it — the cache still happens either way, this only
              collapses the trip. */}
          {reference && !incompatible && listing.downloadable && (
            <button
              className="btn"
              disabled={downloading || !version}
              onClick={() => void download()}
            >
              仅下载到库
            </button>
          )}
          <button
            className="btn btn--primary"
            disabled={downloading || !version || !listing.downloadable || incompatible}
            onClick={() => void download(reference ?? undefined)}
          >
            {downloading
              ? '处理中…'
              : incompatible
                ? '不兼容'
                : reference
                  ? `下载并装到 ${reference.name}`
                  : version?.held
                    ? '库里已有'
                    : `下载 ${version?.version ?? ''}`}
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
 * used it last is still readable: without the check, an operator downloading
 * onto a panel that fetched something else a minute ago would see "done"
 * immediately for a jar that is not there yet.
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
