import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { LibraryView } from '../routes'
import type {
  BulkImpact,
  InstanceStatus,
  LibraryPlugin,
  PluginDownloadJob,
  PluginOverview,
  PluginOverviewRow,
  PluginUse,
} from '../types'
import type { PluginController } from '../usePlugins'
import { Modal } from './Modal'
import { Page } from './Page'
import { PluginBrowse, sourceLabel } from './PluginBrowse'
import { PluginDialog } from './PluginDialog'
import { PluginInstallDialog } from './PluginInstallDialog'

/**
 * The panel-wide plugin library.
 *
 * This page exists for exactly one question: are my servers running the same
 * versions of the same plugins. Everything else it could show — the version
 * list, the source, the update check — is reachable from inside one server, and
 * a library page that could not say "生存服 is two versions behind 创造服" would
 * be a second list of the same plugins with a different heading.
 *
 * So the layout puts its weight on the last column. 使用中的实例 is nearly half
 * the row, and it expands itself when the versions disagree: that is the state
 * worth reading, and folding it away behind an arrow would hide the one thing
 * this page is for. A row where everything agrees collapses to a count,
 * because "four servers, all current" is a fact you take in and move past.
 *
 * The other reason it earns its place is the opposite of usage: a jar nobody
 * references. A plugin uninstalled from every server keeps its download
 * forever, and this is the only page in the panel that would ever mention it
 * again.
 */
export function PluginLibraryPage({
  plugins,
  view,
  against,
  recents,
  instances,
  onOpenPlugin,
  onOpenSettings,
  onOpenView,
  onChooseAgainst,
  onOpenInstance,
}: {
  plugins: PluginController
  view: LibraryView
  /** Which instances 获取插件 judges compatibility against. */
  against?: string[]
  /** Most recently opened servers, newest first — 获取插件 defaults its
   *  compatibility reference to the first of these that still exists. */
  recents: string[]
  instances: InstanceStatus[]
  onOpenPlugin: (id: string) => void
  onOpenSettings: () => void
  onOpenView: (view: LibraryView) => void
  onChooseAgainst: (ids: string[]) => void
  onOpenInstance: (id: string) => void
}) {
  const [overview, setOverview] = useState<PluginOverview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [picked, setPicked] = useState<string[]>([])
  const [confirming, setConfirming] = useState<BulkImpact | null>(null)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [installing, setInstalling] = useState<LibraryPlugin | null>(null)

  const { job, library } = plugins

  const refresh = useCallback(async () => {
    try {
      setOverview(await api.pluginOverview())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取插件总览失败')
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // A finished download adds a version, which can move a row from 版本不一致
  // to 全部最新 — or the other way round.
  useEffect(() => {
    if (job?.state !== 'done') return
    void refresh()
    void plugins.refresh()
  }, [job?.state, job?.tag, refresh, plugins])

  const rows = overview?.rows ?? []
  const mixed = rows.filter((row) => row.status === 'mixed').length

  if (view === 'browse') {
    return (
      <Page
        wide
        title="获取插件"
        lead="从 Modrinth、Hangar 和 SpigotMC 里找插件，下载到面板插件库。这里不会装进任何一台服务器 —— 装到哪几台是「插件列表」和实例自己的「已装插件」上的事。左边勾几台服只是为了让兼容性徽章有参照。"
      >
        <PluginBrowse
          against={against ?? []}
          recents={recents}
          onChooseAgainst={onChooseAgainst}
          onOpenLibrary={() => onOpenView('list')}
        />
      </Page>
    )
  }

  const bulkable = picked.filter((id) => rows.find((row) => row.id === id)?.status === 'mixed')

  return (
    <Page
      wide
      title="插件库"
      lead="按插件看，而不是按服务器看：哪个插件在哪几台服上、版本对不对得上、哪些下载了却没人用。单台服的增删启停在实例自己的「已装插件」里。"
      aside={
        <p className="meta-chips">
          <span>{rows.length > 0 ? `${rows.length} 个插件` : '插件库还是空的'}</span>
          {mixed > 0 && <span className="meta-chips__warn">{mixed} 个版本不一致</span>}
          {(overview?.unused ?? 0) > 0 && (
            <span>
              {overview?.unused} 个没人用 · {formatBytes(overview?.unusedSize ?? 0)}
            </span>
          )}
          {library?.root && <span title={library.root}>存放于 {library.root}</span>}
        </p>
      }
    >
      {error && <div className="alert alert--error">{error}</div>}
      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
      {result && <div className="alert alert--ok">{result}</div>}
      {job && <JobStatus job={job} onCancel={() => void plugins.cancel()} busy={plugins.busy} />}

      <div className="chart-head">
        <h2 className="panel__title">跨实例总览</h2>
        <div className="chart-head__actions">
          <button className="btn" onClick={onOpenSettings}>
            插件源
          </button>
          <button
            className="btn"
            disabled={plugins.busy || rows.length === 0}
            onClick={() => void plugins.checkAll().then(refresh)}
          >
            检查全部更新
          </button>
          {/* The three registries cover almost everything, but not a plugin
              published only to its author's GitHub releases — including the
              operator's own, in a private repository. That path stays here
              rather than in 获取插件: it is a source being registered, not a
              catalogue being searched. */}
          <button className="btn" onClick={() => setAdding(true)}>
            + GitHub 仓库
          </button>
          <button className="btn btn--primary" onClick={() => onOpenView('browse')}>
            获取插件
          </button>
        </div>
      </div>

      {picked.length > 0 && (
        <div className="bulkbar">
          <span>已选 {picked.length} 项</span>
          <span className="device-row__spacer" />
          <button
            className="btn btn--primary"
            disabled={busy || bulkable.length === 0}
            title={bulkable.length === 0 ? '选中的插件版本都是一致的，没有要升级的' : undefined}
            onClick={() =>
              void (async () => {
                setBusy(true)
                try {
                  setConfirming(await api.bulkUpgradePreview(bulkable))
                } catch (err) {
                  setError(err instanceof Error ? err.message : '读取影响范围失败')
                } finally {
                  setBusy(false)
                }
              })()
            }
          >
            批量升级…
          </button>
          <button
            className="btn"
            disabled={busy}
            onClick={() => void cleanCache(picked, rows, setBusy, setError, setResult, refresh, setPicked)}
          >
            清理缓存
          </button>
          <button className="link" onClick={() => setPicked([])}>
            取消选择
          </button>
        </div>
      )}

      {rows.length === 0 ? (
        <div className="welcome__empty">
          <p>插件库还是空的。</p>
          <p className="muted">
            去
            <button className="link" onClick={() => onOpenView('browse')}>
              获取插件
            </button>
            找一个装上，或者在
            <button className="link" onClick={onOpenSettings}>
              插件源
            </button>
            里加个 GitHub 仓库跟着它的 Release 走。
          </p>
        </div>
      ) : (
        <div className="overview-table">
          <div className="overview-table__head">
            <span />
            <span>插件</span>
            <span>最新版本</span>
            <span>使用中的实例</span>
            <span />
          </div>
          {rows.map((row) => (
            <OverviewRow
              key={row.id}
              row={row}
              picked={picked.includes(row.id)}
              onPick={() =>
                setPicked((current) =>
                  current.includes(row.id)
                    ? current.filter((id) => id !== row.id)
                    : [...current, row.id],
                )
              }
              onOpen={() => onOpenPlugin(row.id)}
              onOpenInstance={onOpenInstance}
              onInstall={() => {
                const item = plugins.plugins.find((entry) => entry.id === row.id)
                if (item) setInstalling(item)
              }}
              onDropCache={() =>
                void (async () => {
                  if (!window.confirm(`删除「${row.name}」的 ${row.versions} 个已下载版本？没有实例在用它。`)) {
                    return
                  }
                  setBusy(true)
                  try {
                    await api.deletePlugin(row.id)
                    setResult(`已清理 ${row.name}，释放 ${formatBytes(row.size)}`)
                    await refresh()
                    await plugins.refresh()
                  } catch (err) {
                    setError(err instanceof Error ? err.message : '清理失败')
                  } finally {
                    setBusy(false)
                  }
                })()
              }
            />
          ))}
        </div>
      )}

      {installing && (
        <PluginInstallDialog
          item={installing}
          instances={instances}
          onCancel={() => setInstalling(null)}
          onInstalled={(summary) => {
            setInstalling(null)
            setResult(summary)
            void refresh()
          }}
        />
      )}

      {adding && (
        <PluginDialog
          item={null}
          busy={plugins.busy}
          onCancel={() => setAdding(false)}
          onSubmit={async (input) => {
            const ok = await plugins.add(input)
            if (ok) {
              setAdding(false)
              await refresh()
            }
            return ok
          }}
        />
      )}

      {confirming && (
        <BulkConfirm
          impact={confirming}
          busy={busy}
          onCancel={() => setConfirming(null)}
          onConfirm={() =>
            void (async () => {
              setBusy(true)
              try {
                const outcome = await api.bulkUpgrade(bulkable)
                setResult(
                  outcome.failures.length === 0
                    ? `已升级 ${outcome.applied} 处`
                    : `升级了 ${outcome.applied} 处，${outcome.failures.length} 处失败：` +
                        outcome.failures.map((f) => `${f.instance} / ${f.plugin}`).join('、'),
                )
                setConfirming(null)
                setPicked([])
                await refresh()
              } catch (err) {
                setError(err instanceof Error ? err.message : '批量升级失败')
              } finally {
                setBusy(false)
              }
            })()
          }
        />
      )}
    </Page>
  )
}

/** Deletes the downloads of every selected plugin nothing is using. Selected
 *  rows that *are* in use are skipped and said to be skipped, rather than the
 *  whole action refusing. */
async function cleanCache(
  picked: string[],
  rows: PluginOverviewRow[],
  setBusy: (busy: boolean) => void,
  setError: (error: string | null) => void,
  setResult: (result: string | null) => void,
  refresh: () => Promise<void>,
  setPicked: (picked: string[]) => void,
) {
  const unused = rows.filter((row) => picked.includes(row.id) && row.status === 'unused')
  if (unused.length === 0) {
    setError('选中的插件都还有实例在用，没有可以清理的缓存。')
    return
  }
  const freed = unused.reduce((sum, row) => sum + row.size, 0)
  if (
    !window.confirm(
      `删除 ${unused.map((row) => row.name).join('、')} 的下载，释放 ${formatBytes(freed)}？`,
    )
  ) {
    return
  }

  setBusy(true)
  setError(null)
  try {
    for (const row of unused) await api.deletePlugin(row.id)
    setResult(`已清理 ${unused.length} 个插件，释放 ${formatBytes(freed)}`)
    setPicked([])
    await refresh()
  } catch (err) {
    setError(err instanceof Error ? err.message : '清理失败')
  } finally {
    setBusy(false)
  }
}

function OverviewRow({
  row,
  picked,
  onPick,
  onOpen,
  onOpenInstance,
  onInstall,
  onDropCache,
}: {
  row: PluginOverviewRow
  picked: boolean
  onPick: () => void
  onOpen: () => void
  onOpenInstance: (id: string) => void
  onInstall: () => void
  onDropCache: () => void
}) {
  // Expanded when the versions disagree, folded when they do not. The default
  // is the answer to "does this row need me", so the page can be scanned down
  // its last column without opening anything.
  const [open, setOpen] = useState(row.status === 'mixed')

  return (
    <div className={`overview-row${row.status === 'mixed' ? ' overview-row--mixed' : ''}`}>
      <label className="overview-row__pick">
        <input type="checkbox" checked={picked} onChange={onPick} aria-label={`选择 ${row.name}`} />
      </label>

      <button className="overview-row__name" onClick={onOpen} title={`打开「${row.name}」`}>
        <strong>{row.name}</strong>
        <small>
          {sourceLabel(row.kind)} · {row.repo}
        </small>
      </button>

      <div className="overview-row__version">
        <span>{row.newest ?? '未下载'}</span>
        <StatusLine row={row} />
      </div>

      <div className="overview-row__uses">
        {row.used.length === 0 ? (
          <span className="overview-row__unused">
            未被使用
            <button className="link link--danger" onClick={onDropCache}>
              删除缓存
            </button>
            <small>{formatBytes(row.size)}</small>
          </span>
        ) : open ? (
          <ul className="overview-row__list">
            {row.used.map((use) => (
              <UseLine key={use.instanceId} use={use} onOpen={() => onOpenInstance(use.instanceId)} />
            ))}
            {row.status !== 'mixed' && (
              <li>
                <button className="link" onClick={() => setOpen(false)}>
                  收起
                </button>
              </li>
            )}
          </ul>
        ) : (
          <button className="link overview-row__fold" onClick={() => setOpen(true)}>
            {row.used.length} 个实例 ▾
          </button>
        )}
      </div>

      {/* One of the two places a jar leaves the library for a server. The
          other is the server's own page; both open the same dialog, because
          it is the same decision from two directions. */}
      <div className="overview-row__act">
        <button
          className="link"
          disabled={row.versions === 0}
          title={row.versions === 0 ? '还没下载过这个插件的任何版本' : undefined}
          onClick={onInstall}
        >
          装到实例…
        </button>
      </div>
    </div>
  )
}


function StatusLine({ row }: { row: PluginOverviewRow }) {
  if (row.status === 'unused') {
    return <small className="overview-row__status overview-row__status--idle">未被使用</small>
  }
  if (row.status === 'mixed') {
    return <small className="overview-row__status overview-row__status--warn">版本不一致</small>
  }
  return <small className="overview-row__status overview-row__status--ok">全部最新</small>
}

function UseLine({ use, onOpen }: { use: PluginUse; onOpen: () => void }) {
  return (
    <li>
      <button className="link overview-row__instance" onClick={onOpen}>
        <span className={`status__dot status__dot--${use.state}`} />
        {use.name}
      </button>
      {use.outdated ? (
        <span className="badge badge--update">{use.version} →</span>
      ) : (
        <span className="overview-row__ver">{use.version}</span>
      )}
    </li>
  )
}

/**
 * Confirming a bulk upgrade.
 *
 * A cross-instance operation reaches much further than the same click on one
 * server, so the confirmation has to be correspondingly heavier: which servers,
 * which plugins, and — the part that actually costs something — how many of
 * those servers are live and will have to be restarted before any of it takes
 * effect. "确定要升级吗" over the top of five running servers is not a
 * confirmation, it is a formality.
 */
function BulkConfirm({
  impact,
  busy,
  onCancel,
  onConfirm,
}: {
  impact: BulkImpact
  busy: boolean
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <Modal onClose={onCancel} busy={busy} label="批量升级">
      <div className="modal__card">
        <h2>批量升级</h2>

        <ul className="bulk-confirm__plugins">
          {impact.plugins.map((entry) => (
            <li key={entry.id}>
              将 <strong>{entry.name}</strong> 升级至 <strong>{entry.to}</strong>
              <small>当前：{Array.from(new Set(entry.from)).join('、')}</small>
            </li>
          ))}
        </ul>

        <p className="bulk-confirm__scope">
          影响 {impact.instances.length} 台实例：
          {impact.instances.map((entry) => entry.name).join('、')}
        </p>

        {impact.restarts > 0 ? (
          <div className="alert alert--warn">
            其中 {impact.restarts} 台正在运行，
            <strong>升级后必须重启才会生效</strong> —— 重启期间在线玩家会被断开。
            面板不会自动重启，升完之后请自己挑时间。
          </div>
        ) : (
          <p className="muted">这些实例都没在运行，下次启动时自然生效，不需要额外操作。</p>
        )}

        <div className="modal__actions">
          <button className="btn" disabled={busy} onClick={onCancel}>
            取消
          </button>
          <button
            className="btn btn--primary"
            disabled={busy || impact.plugins.length === 0}
            onClick={onConfirm}
          >
            {busy ? '升级中…' : '确认升级'}
          </button>
        </div>
      </div>
    </Modal>
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
        {job.mirror && `（来自 ${mirrorLabel(job.mirror)}）`}。
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

/**
 * Names a mirror id for the job line. Unknown ids are custom prefixes, which
 * are already their own name.
 *
 * "源站直连" rather than "GitHub 直连": the mirrors are GitHub proxies, but a
 * download from Modrinth or Hangar is direct by construction — it never sees a
 * proxy — and reporting it as coming from GitHub would name the wrong host.
 */
function mirrorLabel(id: string): string {
  return id === 'direct' ? '源站直连' : id
}
