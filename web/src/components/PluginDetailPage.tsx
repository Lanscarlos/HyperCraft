import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes, formatDate } from '../format'
import type { LibraryPlugin, PluginRelease } from '../types'
import { hasPluginUpdate } from '../types'
import type { PluginController } from '../usePlugins'
import { Page } from './Page'
import { PluginDialog } from './PluginDialog'

/**
 * Everything about one plugin: where it comes from, which versions the panel
 * holds, what upstream offers, and which servers are running it.
 *
 * Its own page rather than an expanding card, because this is where the work
 * happens — comparing versions, reading release notes, deciding what to roll
 * back to — and none of that fits in a tile that has to sit beside nineteen
 * others. The list page answers "what do I have and what needs attention"; this
 * one answers "what should I do about this one".
 */
export function PluginDetailPage({
  item,
  plugins,
  onBack,
}: {
  item: LibraryPlugin
  plugins: PluginController
  onBack: () => void
}) {
  const [releases, setReleases] = useState<PluginRelease[] | null>(null)
  const [loadingReleases, setLoadingReleases] = useState(false)
  const [releaseError, setReleaseError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)

  const { busy, downloading, job } = plugins
  const updatable = hasPluginUpdate(item)
  const held = new Set(item.versions.map((version) => version.tag))
  const totalSize = item.versions.reduce((sum, version) => sum + version.size, 0)

  // Fetching the release list is a network round trip per click, so it is not
  // done on mount — except when there is an update to act on, which is the one
  // case where the operator came here to pick a version.
  const loadReleases = async (force = false) => {
    if (loadingReleases || (releases && !force)) return
    setLoadingReleases(true)
    setReleaseError(null)
    try {
      setReleases(await api.pluginReleases(item.id))
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

  const remove = async () => {
    const inUse =
      item.usedBy.length > 0
        ? `实例「${item.usedBy.join('、')}」正在用它，不过它们各自有一份副本，删掉库里的不影响已经装上的。`
        : ''
    if (!window.confirm(`确定要把「${item.name}」和它的 ${item.versions.length} 个版本从插件库删除吗？${inUse}`)) {
      return
    }
    await plugins.remove(item.id)
    onBack()
  }

  return (
    <Page
      wide
      above={
        <button className="link" onClick={onBack}>
          ← 插件库
        </button>
      }
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
          <button className="btn" type="button" disabled={busy} onClick={() => void plugins.check(item.id)}>
            检查更新
          </button>
          <button className="btn" type="button" disabled={busy} onClick={() => setEditing(true)}>
            编辑来源
          </button>
          <button className="btn btn--danger" type="button" disabled={busy} onClick={() => void remove()}>
            删除
          </button>
        </div>
      }
    >

      {item.checkError && <div className="alert alert--warn">检查更新失败：{item.checkError}</div>}

      <section className="panel">
        <h2 className="panel__title">概况</h2>
        <dl className="asset__facts">
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
            <dd>{item.checkedAt ? formatDate(item.checkedAt) : '从未'}</dd>
          </div>
          <div>
            <dt>安装目录</dt>
            <dd>{item.targetDir}/</dd>
          </div>
          <div>
            <dt>文件名匹配</dt>
            <dd>{item.source.assetPattern || '自动挑选'}</dd>
          </div>
          <div>
            <dt>预发布</dt>
            <dd>{item.source.prerelease ? '包含' : '不列出'}</dd>
          </div>
        </dl>

        <p className="chart-note">
          {item.usedBy.length > 0 ? (
            <>
              使用中：
              {item.usedBy.map((name) => (
                <span className="badge" key={name}>
                  {name}
                </span>
              ))}
              —— 各自持有一份副本，删掉这里的版本不会动到它们。
            </>
          ) : (
            '还没有实例用它。装到服务器上是在实例的「插件」标签里做的。'
          )}
        </p>
      </section>

      {updatable && item.latest && (
        <div className="alert alert--ok">
          上游最新是 {item.latest.version}
          {item.latest.prerelease && '（预发布）'}，库里还没有。
          <button
            className="link"
            disabled={busy || downloading}
            onClick={() => void plugins.download(item.id, item.latest?.tag ?? '')}
          >
            下载它
          </button>
        </div>
      )}

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">库里的版本</h2>
          <span className="muted">{item.versions.length} 个</span>
        </div>
        {item.versions.length === 0 ? (
          <p className="muted">还没下载过任何版本。在下面的「上游发布」里挑一个。</p>
        ) : (
          <div className="plugin-versions">
            {item.versions.map((version) => (
              <div className="plugin-version" key={version.tag}>
                <span className="plugin-version__name" title={version.fileName}>
                  {version.version}
                  {version.prerelease && <span className="badge badge--warn">预发布</span>}
                </span>
                <span className="plugin-version__meta">
                  {formatBytes(version.size)} · {formatDate(version.publishedAt)}
                </span>
                <button
                  className="link link--danger"
                  disabled={busy || downloading}
                  onClick={() => {
                    if (
                      window.confirm(
                        `确定要删除 ${item.name} ${version.version} 吗？已经装到实例里的副本不受影响。`,
                      )
                    ) {
                      void plugins.removeVersion(item.id, version.tag)
                    }
                  }}
                >
                  删除
                </button>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">上游发布</h2>
          <button
            className="btn"
            type="button"
            disabled={busy || loadingReleases}
            onClick={() => void loadReleases(true)}
          >
            {loadingReleases ? '获取中…' : releases ? '刷新' : '列出版本'}
          </button>
        </div>

        {releaseError && <div className="alert alert--error">{releaseError}</div>}
        {!releases && !releaseError && (
          <p className="chart-note">
            列版本要问一次 GitHub，所以是点出来的而不是打开页面就拉 —— 匿名调用一小时只有 60 次配额。
          </p>
        )}

        {releases && (
          <div className="plugin-versions">
            {releases.map((release) => (
              <div className="plugin-version" key={release.tag}>
                <span className="plugin-version__name" title={release.asset.name}>
                  {release.version}
                  {release.prerelease && <span className="badge badge--warn">预发布</span>}
                  {held.has(release.tag) && <span className="badge">已下载</span>}
                </span>
                <span className="plugin-version__meta">
                  {formatBytes(release.asset.size)} · {formatDate(release.publishedAt)}
                </span>
                <button
                  className="link"
                  disabled={busy || downloading}
                  onClick={() => void plugins.download(item.id, release.tag)}
                >
                  {held.has(release.tag) ? '重新下载' : '下载'}
                </button>
              </div>
            ))}
          </div>
        )}
      </section>

      {editing && (
        <PluginDialog
          item={item}
          busy={busy}
          onCancel={() => setEditing(false)}
          onSubmit={async (input) => {
            const ok = await plugins.edit(item.id, input)
            if (ok) setEditing(false)
            return ok
          }}
        />
      )}
    </Page>
  )
}
