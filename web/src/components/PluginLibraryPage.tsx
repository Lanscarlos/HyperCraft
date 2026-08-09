import { useEffect, useState } from 'react'

import { formatBytes, formatDate } from '../format'
import type { LibraryPlugin, PluginDownloadJob } from '../types'
import { hasPluginUpdate } from '../types'
import type { PluginController } from '../usePlugins'
import { Page } from './Page'
import { PluginDialog } from './PluginDialog'

/**
 * Panel-wide plugin management: what the panel tracks, and what needs attention.
 *
 * This page is a list and nothing more. Everything you can *do* to one plugin —
 * pick a version, read the release notes, roll back, edit the source — lives on
 * its own page, because those are decisions made about one plugin at a time and
 * they do not fit in a tile that has to sit beside nineteen others. What belongs
 * here is the comparison: which of them have updates, which are unused, how much
 * disk the whole shelf is taking.
 *
 * The split with instances is deliberate too: an instance may take a copy, swap
 * which version it holds, or switch one off — it cannot add a plugin, define a
 * download source or delete a version. A panel where each server manages its own
 * downloads ends up with six subtly different copies of the same plugin and
 * nobody able to say which is which.
 */
export function PluginLibraryPage({
  plugins,
  onOpenPlugin,
  onOpenSettings,
}: {
  plugins: PluginController
  onOpenPlugin: (id: string) => void
  onOpenSettings: () => void
}) {
  const [adding, setAdding] = useState(false)

  const { library, job, downloading, busy } = plugins
  const tracked = plugins.plugins
  const totalSize = tracked.reduce(
    (sum, item) => sum + item.versions.reduce((n, version) => n + version.size, 0),
    0,
  )
  const versionCount = tracked.reduce((sum, item) => sum + item.versions.length, 0)
  // Worth saying out loud rather than leaving as a per-row check failure: a
  // private plugin with no token can neither check nor download, and the reason
  // is one setting away.
  const privateWithoutToken =
    library !== null && !library.tokenConfigured && tracked.some((item) => item.source.private)

  // A finished download adds a version; the listing has to catch up.
  useEffect(() => {
    if (job?.state !== 'done') return
    void plugins.refresh()
  }, [job?.state, job?.tag, plugins])

  return (
    <Page
      wide
      title="插件库"
      lead="插件在这里统一管理：添加下载源、拉取版本、检查更新。实例那边只负责「用哪个插件、用哪个版本、要不要停用」，不能自己下载 —— 这样同一个插件全站只有一份来源，版本回滚时也永远还找得到旧包。下载走服务器自己的网络，关掉网页也会继续。"
      aside={
        <p className="meta-chips">
          <span>{tracked.length > 0 ? `${tracked.length} 个插件` : '插件库还是空的'}</span>
          {versionCount > 0 && <span>{versionCount} 个版本</span>}
          {totalSize > 0 && <span>共 {formatBytes(totalSize)}</span>}
          {library?.root && <span title={library.root}>存放于 {library.root}</span>}
        </p>
      }
    >

      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
      {job && <JobStatus job={job} onCancel={() => void plugins.cancel()} busy={busy} />}

      {privateWithoutToken && (
        <div className="alert alert--warn">
          有插件来自私有仓库，但面板还没有 GitHub 访问令牌 —— 这些插件既检查不到更新也下载不了。
          <button className="link" onClick={onOpenSettings}>
            去「设置 → 插件源」填一个
          </button>
        </div>
      )}

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">已跟踪的插件</h2>
          <div className="field__tools">
            <button className="btn" type="button" onClick={onOpenSettings}>
              插件源设置
            </button>
            <button
              className="btn"
              type="button"
              disabled={busy || tracked.length === 0}
              onClick={() => void plugins.checkAll()}
            >
              检查全部更新
            </button>
            <button className="btn btn--primary" type="button" onClick={() => setAdding(true)}>
              + 添加插件
            </button>
          </div>
        </div>

        {tracked.length === 0 ? (
          <div className="welcome__empty">
            <p>插件库还是空的。</p>
            <p className="muted">
              添加一个插件的 GitHub 仓库（比如 <code>EssentialsX/Essentials</code>，或者直接粘贴仓库地址），
              面板就会从它的 Release 里拉取 jar。自己写的插件发在私有仓库里也可以，先在
              <button className="link" onClick={onOpenSettings}>
                设置 → 插件源
              </button>
              填个访问令牌就行。
            </p>
          </div>
        ) : (
          <div className="plugin-rows">
            {tracked.map((item) => (
              <PluginRow
                key={item.id}
                item={item}
                busy={busy}
                downloading={downloading}
                onOpen={() => onOpenPlugin(item.id)}
                onUpdate={() => void plugins.download(item.id, item.latest?.tag ?? '')}
              />
            ))}
          </div>
        )}
      </section>

      {adding && (
        <PluginDialog
          item={null}
          busy={busy}
          onCancel={() => setAdding(false)}
          onSubmit={async (input) => {
            const ok = await plugins.add(input)
            if (ok) setAdding(false)
            return ok
          }}
        />
      )}
    </Page>
  )
}

/** One line of the list: enough to decide whether this plugin needs opening. */
function PluginRow({
  item,
  busy,
  downloading,
  onOpen,
  onUpdate,
}: {
  item: LibraryPlugin
  busy: boolean
  downloading: boolean
  onOpen: () => void
  onUpdate: () => void
}) {
  const updatable = hasPluginUpdate(item)
  const newest = item.versions[0]

  return (
    <article className="plugin-row">
      <button className="plugin-row__main" onClick={onOpen} title={`打开「${item.name}」`}>
        <span className="asset__tile asset__tile--accent">{item.name.slice(0, 1).toUpperCase()}</span>
        <span className="plugin-row__title">
          <strong>
            {item.name}
            {updatable && <span className="badge badge--update">有更新</span>}
            {item.source.private && <span className="badge">私有</span>}
            {item.targetDir !== 'plugins' && <span className="badge">{item.targetDir}/</span>}
          </strong>
          <small title={item.source.repo}>{item.source.repo}</small>
        </span>
      </button>

      <span className="plugin-row__facts">
        <span title={newest ? `最新已下载 ${newest.version}` : '还没下载过'}>
          {newest ? `已下载 ${newest.version}` : '未下载'}
          {item.versions.length > 1 && ` · 共 ${item.versions.length} 版`}
        </span>
        <small>
          {item.checkError
            ? `检查失败：${item.checkError}`
            : item.latest
              ? `上游 ${item.latest.version} · 检查于 ${formatDate(item.checkedAt ?? '')}`
              : '还没检查过更新'}
        </small>
      </span>

      <span className="plugin-row__users">
        {item.usedBy.length > 0 ? (
          item.usedBy.map((name) => (
            <span className="badge" key={name}>
              {name}
            </span>
          ))
        ) : (
          <span className="muted">没有实例用它</span>
        )}
      </span>

      <span className="plugin-row__actions">
        {updatable && (
          <button className="link" disabled={busy || downloading} onClick={onUpdate}>
            下载新版
          </button>
        )}
        <button className="link" onClick={onOpen}>
          详情
        </button>
      </span>
    </article>
  )
}

function JobStatus({
  job,
  onCancel,
  busy,
}: {
  job: PluginDownloadJob
  onCancel: () => void
  busy: boolean
}) {
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
          正在下载 {job.pluginName} {job.version}（{job.fileName}）
          {job.mirror && ` · 来自 ${mirrorLabel(job.mirror)}`}
          <button className="link link--danger" onClick={onCancel} disabled={busy}>
            取消
          </button>
        </p>
      </div>
    )
  }

  if (job.state === 'done') {
    return (
      <div className="alert alert--ok">
        已下载 {job.pluginName} {job.version}
        {job.mirror && `（来自 ${mirrorLabel(job.mirror)}）`}，现在可以在实例的「插件」标签里装上。
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

/** Names a mirror id for the job line. Unknown ids are custom prefixes, which
 *  are already their own name. */
function mirrorLabel(id: string): string {
  return id === 'direct' ? 'GitHub 直连' : id
}
