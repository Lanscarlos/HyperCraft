import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { LibraryPlugin, PluginRelease, PluginVersion } from '../types'
import { hasPluginUpdate } from '../types'
import type { PluginController } from '../usePlugins'
import { loaderNote } from './PluginInstallDialog'
import { Page } from './Page'
import { PluginIcon } from './PluginIcon'

/**
 * Everything about one plugin: where it comes from, which versions the panel
 * holds, what upstream offers, and which servers are running it.
 *
 * Its own page rather than an expanding card, because this is where the work
 * happens — comparing versions, reading release notes, deciding what to roll
 * back to — and none of that fits in a tile that has to sit beside nineteen
 * others. The list page answers "what do I have and what needs attention"; this
 * one answers "what should I do about this one".
 *
 * The shape changed twice for the same reason: things that were one question
 * had been split, and things that were two had been merged.
 *
 *   - 概况 was six equal-weight facts, three of which are read-only state
 *     (downloaded, upstream, last checked) and three of which are settings the
 *     operator can change (target directory, asset pattern, prereleases). They
 *     look identical in a definition list and they are not the same kind of
 *     thing at all, so the settings moved into a section of their own and
 *     became editable where they are read — which is the whole of what the
 *     编辑来源 dialog was for.
 *   - The version list was two lists, 库里的版本 and 上游发布, and answering
 *     "which versions exist and which do I have" meant computing a difference
 *     between them by eye. It is one list now, each row saying which it is.
 */
export function PluginDetailPage({
  item,
  plugins,
  onBack,
}: {
  item: LibraryPlugin
  plugins: PluginController
  /** Where 从库中移除 lands, since this page stops existing. */
  onBack: () => void
}) {
  const [releases, setReleases] = useState<PluginRelease[] | null>(null)
  const [loadingReleases, setLoadingReleases] = useState(false)
  const [releaseError, setReleaseError] = useState<string | null>(null)
  const [fetchedAt, setFetchedAt] = useState<Date | null>(null)

  const { busy, downloading, job } = plugins
  const updatable = hasPluginUpdate(item)
  const totalSize = item.versions.reduce((sum, version) => sum + version.size, 0)

  // Fetching the release list is a network round trip per click, so it is not
  // done on mount. See the note beside the refresh button: the anonymous
  // GitHub API allows sixty calls an hour, and a page that spends one on every
  // visit is a page that stops working on the twentieth.
  const loadReleases = async () => {
    if (loadingReleases) return
    setLoadingReleases(true)
    setReleaseError(null)
    try {
      setReleases(await api.pluginReleases(item.id))
      setFetchedAt(new Date())
    } catch (err) {
      setReleaseError(err instanceof Error ? err.message : '获取版本列表失败')
    } finally {
      setLoadingReleases(false)
    }
  }

  // A finished download changes what this page shows, so re-read both halves.
  useEffect(() => {
    if (job?.state !== 'done' || job.pluginId !== item.id) return
    void plugins.refresh()
  }, [job?.state, job?.tag, job?.pluginId, item.id, plugins])

  /**
   * Removing the plugin from the library.
   *
   * Named for what it does — it removes a cache entry and a source record, not
   * the plugin off anybody's server — and the confirmation says both halves:
   * what goes, and what is untouched. That second sentence used to live as a
   * permanent line of explanation halfway down this page, where it was read
   * once and became furniture. It belongs on the one button it is about.
   */
  const remove = async () => {
    const kept =
      item.usedBy.length > 0
        ? `\n\n${item.usedBy.join('、')} 上已经装好的副本不受影响 —— 那是各自目录里的另一份文件。`
        : ''
    if (
      !window.confirm(
        `从插件库移除「${item.name}」，删掉本地 ${item.versions.length} 个版本共 ${formatBytes(totalSize)}？${kept}`,
      )
    ) {
      return
    }
    await plugins.remove(item.id)
    onBack()
  }

  return (
    <Page
      wide
      // No back link. The breadcrumb above already goes to 插件库, and two of
      // them meant the page opened with a stray ← floating over its title.
      title={
        <>
          {item.name}
          {updatable && <span className="badge badge--update">有更新</span>}
          {item.source.private && <span className="badge">私有</span>}
        </>
      }
      lead={
        <>
          <a href={`https://github.com/${item.source.repo}`} target="_blank" rel="noreferrer">
            {item.source.repo}
          </a>
          {item.note && ` · ${item.note}`}
        </>
      }
      aside={
        <div className="field__tools">
          <button
            className="btn"
            type="button"
            disabled={busy}
            onClick={() => void plugins.check(item.id)}
          >
            检查更新
          </button>
          <button className="btn btn--danger" type="button" disabled={busy} onClick={() => void remove()}>
            从库中移除
          </button>
        </div>
      }
    >
      {item.checkError && <div className="alert alert--warn">检查更新失败：{item.checkError}</div>}

      <section className="panel">
        <div className="plugin-detail__head">
          <PluginIcon className="plugin-detail__icon" src={item.iconUrl} name={item.name} />
          <dl className="asset__facts plugin-detail__facts">
            <div>
              <dt>已下载</dt>
              <dd>
                {item.versions.length} 个版本
                {totalSize > 0 && ` · ${formatBytes(totalSize)}`}
              </dd>
            </div>
            <div>
              <dt>上游最新</dt>
              <dd>{item.latest ? item.latest.version : '未知'}</dd>
            </div>
            <div>
              <dt>检查于</dt>
              {/* Go's zero time survives JSON as 0001-01-01, which is truthy
                  and formats as 1/01/01 —— a plugin nobody has ever checked
                  was claiming to have been checked in the year one. */}
              <dd>{checked(item.checkedAt) ?? '从未'}</dd>
            </div>
            <div>
              <dt>使用中</dt>
              <dd>
                {item.usedBy.length > 0 ? (
                  <span
                    className="plugin-detail__users"
                    title="各自持有一份副本，删掉库里的版本不会动到它们"
                  >
                    {item.usedBy.map((name) => (
                      <span className="badge" key={name}>
                        {name}
                      </span>
                    ))}
                  </span>
                ) : (
                  '没有实例在用'
                )}
              </dd>
            </div>
          </dl>
        </div>
      </section>

      <SourceSettings item={item} busy={busy} onSave={(input) => plugins.edit(item.id, input)} />

      <VersionList
        item={item}
        releases={releases}
        loading={loadingReleases}
        error={releaseError}
        fetchedAt={fetchedAt}
        busy={busy || downloading}
        onRefresh={() => void loadReleases()}
        onDownload={(tag) => void plugins.download(item.id, tag)}
        onDrop={(version) => {
          if (
            window.confirm(
              `从库里删掉 ${item.name} ${version.version}（${formatBytes(version.size)}）？已经装到实例里的副本不受影响。`,
            )
          ) {
            void plugins.removeVersion(item.id, version.tag)
          }
        }}
      />
    </Page>
  )
}

/**
 * The three fields that decide what gets downloaded, edited where they are
 * read.
 *
 * They used to sit among the read-only facts and be changed through a dialog
 * reached by a button three inches away, which is two indirections for
 * "prereleases: on". A dirty form here is explicit — the save button appears
 * only when something differs — so nothing is written by wandering through the
 * fields.
 */
function SourceSettings({
  item,
  busy,
  onSave,
}: {
  item: LibraryPlugin
  busy: boolean
  onSave: (input: {
    name: string
    repo: string
    assetPattern?: string
    prerelease?: boolean
    private?: boolean
    targetDir?: string
    note?: string
  }) => Promise<boolean>
}) {
  const [targetDir, setTargetDir] = useState(item.targetDir)
  const [assetPattern, setAssetPattern] = useState(item.source.assetPattern ?? '')
  const [prerelease, setPrerelease] = useState(item.source.prerelease ?? false)
  const [saving, setSaving] = useState(false)

  // Re-seeded when the plugin's record changes underneath — a finished save, a
  // refresh — so the form shows what is stored rather than what was typed.
  useEffect(() => {
    setTargetDir(item.targetDir)
    setAssetPattern(item.source.assetPattern ?? '')
    setPrerelease(item.source.prerelease ?? false)
  }, [item.targetDir, item.source.assetPattern, item.source.prerelease])

  const dirty =
    targetDir !== item.targetDir ||
    assetPattern !== (item.source.assetPattern ?? '') ||
    prerelease !== (item.source.prerelease ?? false)

  const save = async () => {
    setSaving(true)
    await onSave({
      name: item.name,
      repo: item.source.repo,
      assetPattern,
      prerelease,
      private: item.source.private,
      targetDir,
      note: item.note,
    })
    setSaving(false)
  }

  return (
    <section className="panel panel--form">
      <h2 className="panel__title">来源设置</h2>

      <div className="field-row">
        <label className="field">
          <span>安装目录</span>
          <input
            value={targetDir}
            placeholder="plugins"
            disabled={busy || saving}
            onChange={(event) => setTargetDir(event.target.value)}
          />
          <small>
            复制到实例里的哪个子目录。Bukkit 系是 <code>plugins</code>，Fabric 和 Forge 是{' '}
            <code>mods</code>。
          </small>
        </label>

        <label className="field">
          <span>文件名匹配</span>
          <input
            value={assetPattern}
            placeholder="自动挑选"
            disabled={busy || saving}
            onChange={(event) => setAssetPattern(event.target.value)}
          />
          <small>
            一次 Release 发好几个 jar 时用它挑，比如 <code>*-bukkit.jar</code>。留空就让面板自己选。
          </small>
        </label>
      </div>

      <label className="checkbox checkbox--stacked">
        <input
          type="checkbox"
          checked={prerelease}
          disabled={busy || saving}
          onChange={(event) => setPrerelease(event.target.checked)}
        />
        <span>包含预发布版本</span>
        <small>关掉的话，标了 prerelease 的 Release 不会出现在下面的版本列表里，也不算「有更新」。</small>
      </label>

      {dirty && (
        <div className="modal__actions">
          <button className="btn" disabled={saving} onClick={() => {
            setTargetDir(item.targetDir)
            setAssetPattern(item.source.assetPattern ?? '')
            setPrerelease(item.source.prerelease ?? false)
          }}>
            撤销
          </button>
          <button className="btn btn--primary" disabled={saving} onClick={() => void save()}>
            {saving ? '保存中…' : '保存来源设置'}
          </button>
        </div>
      )}
    </section>
  )
}

/** Formats a last-checked timestamp, or nothing when there has never been
 *  one. Go writes its zero time out rather than omitting it — `omitempty` does
 *  not reach inside a struct — so "has it been checked" is a question about
 *  the year, not about whether the field is there. */
function checked(at?: string): string | null {
  if (!at || at.startsWith('0001-')) return null
  return formatDate(at)
}

/** One row of the merged list: a version that exists, and what the panel has
 *  to say about it. */
interface VersionRow {
  tag: string
  version: string
  prerelease: boolean
  publishedAt: string
  /** The downloaded copy, when there is one. */
  held?: PluginVersion
  /** The upstream release, when the list has been fetched. */
  release?: PluginRelease
  /** Which server software this particular jar is built for. A plugin that
   *  supports several ships one build per platform under the same release
   *  number, and the only other sign of it is a suffix on the version. */
  loaders?: string[]
}

/**
 * Every version, in one list.
 *
 * It was two panels — what the library holds, and what upstream publishes —
 * and the question anyone comes here with ("which versions are there, and
 * which of them do I have") was the difference between them, computed by
 * looking back and forth. Merging is nearly free, because the two lists are
 * keyed by the same tag; what it buys is that "已下载" becomes a property of a
 * version rather than a list a version is in.
 *
 * Before the upstream list is fetched, this is just the library's own versions
 * — which is correct rather than partial, and says so.
 */
function VersionList({
  item,
  releases,
  loading,
  error,
  fetchedAt,
  busy,
  onRefresh,
  onDownload,
  onDrop,
}: {
  item: LibraryPlugin
  releases: PluginRelease[] | null
  loading: boolean
  error: string | null
  fetchedAt: Date | null
  busy: boolean
  onRefresh: () => void
  onDownload: (tag: string) => void
  onDrop: (version: PluginVersion) => void
}) {
  const [expanded, setExpanded] = useState<string | null>(null)

  const byTag = new Map<string, VersionRow>()
  for (const version of item.versions) {
    byTag.set(version.tag, {
      tag: version.tag,
      version: version.version,
      prerelease: version.prerelease,
      publishedAt: version.publishedAt,
      held: version,
      loaders: version.loaders,
    })
  }
  for (const release of releases ?? []) {
    const existing = byTag.get(release.tag)
    if (existing) {
      existing.release = release
      existing.loaders = existing.loaders ?? release.loaders
      continue
    }
    byTag.set(release.tag, {
      tag: release.tag,
      version: release.version,
      prerelease: release.prerelease,
      publishedAt: release.publishedAt,
      release,
      loaders: release.loaders,
    })
  }
  const rows = Array.from(byTag.values()).sort(
    (a, b) => new Date(b.publishedAt).getTime() - new Date(a.publishedAt).getTime(),
  )

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">版本</h2>
        <div className="chart-head__actions">
          <span className="muted">
            {releases
              ? `${rows.length} 个版本 · 库里有 ${item.versions.length} 个`
              : `库里的 ${item.versions.length} 个`}
          </span>
          <button
            className="btn btn--icon"
            type="button"
            disabled={busy || loading}
            title={
              fetchedAt
                ? `上次拉取 ${fetchedAt.toLocaleTimeString()}。列版本要问一次 GitHub —— 匿名调用一小时只有 60 次配额。`
                : '拉取上游还发布过哪些版本。要问一次 GitHub —— 匿名调用一小时只有 60 次配额，所以是点出来的而不是打开页面就拉。'
            }
            onClick={onRefresh}
          >
            {loading ? '…' : '⟳'}
          </button>
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      {!releases && !error && (
        <p className="chart-note">
          这里现在只是库里已经下载的。点右上角的 ⟳ 把上游还发布过哪些版本一起列出来 ——
          那要问一次 GitHub，而匿名调用一小时只有 60 次配额，所以它是点出来的，不是打开页面就拉。
        </p>
      )}

      {rows.length === 0 ? (
        <p className="muted">还没下载过任何版本，也还没列过上游有哪些。</p>
      ) : (
        <div className="plugin-versions">
          {rows.map((row) => {
            const notes = (row.release?.notes ?? row.held?.notes ?? '').trim()
            const open = expanded === row.tag
            const size = row.held?.size ?? row.release?.asset.size ?? 0

            return (
              <div className="plugin-version" key={row.tag}>
                <span
                  className="plugin-version__name"
                  title={row.held?.fileName ?? row.release?.asset.name}
                >
                  {row.version}
                  {row.prerelease && <span className="badge badge--warn">预发布</span>}
                  {row.held ? (
                    <span className="badge badge--ok">已下载</span>
                  ) : (
                    <span className="badge badge--muted">可下载</span>
                  )}
                  {loaderNote(row.loaders) && (
                    <span className="badge" title="这一份 jar 是给这些服务端核心用的">
                      {loaderNote(row.loaders)}
                    </span>
                  )}
                </span>

                <span className="plugin-version__meta">
                  {size > 0 && `${formatBytes(size)} · `}
                  {formatDate(row.publishedAt)}
                  {row.held && ` · 下载于 ${formatDate(row.held.addedAt)}`}
                </span>

                {/* Already in hand — the release list carries its own notes, so
                    expanding costs nothing beyond the fetch that was already
                    made. Collapsed by default because a changelog is three
                    paragraphs and the list is thirty versions long. */}
                {notes !== '' && (
                  <button
                    className="link plugin-version__notes-toggle"
                    onClick={() => setExpanded(open ? null : row.tag)}
                    aria-expanded={open}
                  >
                    {open ? '收起说明' : '发布说明'}
                  </button>
                )}

                {row.held ? (
                  <button className="link link--danger" disabled={busy} onClick={() => onDrop(row.held!)}>
                    删除
                  </button>
                ) : (
                  <button className="link" disabled={busy} onClick={() => onDownload(row.tag)}>
                    下载
                  </button>
                )}

                {open && notes !== '' && <pre className="plugin-version__notes">{notes}</pre>}
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}
