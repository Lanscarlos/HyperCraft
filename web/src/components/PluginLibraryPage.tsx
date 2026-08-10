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
import { isLive } from '../types'
import type { PluginController } from '../usePlugins'
import { Menu } from './Menu'
import { Modal } from './Modal'
import { Page } from './Page'
import { PluginBrowse, sourceLabel } from './PluginBrowse'
import { PluginIcon } from './PluginIcon'
import { PluginInstallDialog } from './PluginInstallDialog'
import { Toast } from './Toast'

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
  const [installing, setInstalling] = useState<LibraryPlugin | null>(null)
  const [bulkInstall, setBulkInstall] = useState<LibraryPlugin[] | null>(null)

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
        // The page's own state, and the one action that leaves it. The other
        // three buttons that used to sit by the section heading were not
        // siblings of each other: 插件源 duplicated a sidebar entry, and
        // + GitHub 仓库 is source management, which now lives on the 插件源
        // page with the rest of it. What is left beside the heading is the one
        // thing that acts on this table.
        <div className="page__actions">
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
          <button className="btn btn--primary" onClick={() => onOpenView('browse')}>
            获取插件
          </button>
        </div>
      }
    >
      {error && <div className="alert alert--error">{error}</div>}
      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
      {job && <JobStatus job={job} onCancel={() => void plugins.cancel()} busy={plugins.busy} />}
      {/* An outcome, not a state: see Toast. A finished download comes through
          here too — JobStatus keeps only the progress bar and the failure,
          which are the two that are still true while you read them. */}
      {result && <Toast message={result} onDone={() => setResult(null)} />}

      <div className="chart-head">
        <h2 className="panel__title">跨实例总览</h2>
        <div className="chart-head__actions">
          <button
            className="btn"
            disabled={plugins.busy || rows.length === 0}
            onClick={() => void plugins.checkAll().then(refresh)}
          >
            检查全部更新
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
            disabled={busy || picked.length === 0}
            onClick={() => {
              const chosen = plugins.plugins.filter(
                (entry) => picked.includes(entry.id) && entry.versions.length > 0,
              )
              if (chosen.length === 0) {
                setError('选中的插件一个版本都还没下载，没有可以装的。')
                return
              }
              setBulkInstall(chosen)
            }}
          >
            批量装到实例…
          </button>
          <button
            className="btn"
            disabled={plugins.busy || picked.length === 0}
            onClick={() =>
              void (async () => {
                for (const id of picked) await plugins.check(id)
                await refresh()
                setResult(`已检查 ${picked.length} 个插件的更新`)
              })()
            }
          >
            检查更新
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
            这里是面板的公共缓存：一个 jar 下载一次，想装几台服就复制几份，
            于是「同一个插件在五台服上」是一份文件一个校验和，而不是五份谁也认不出彼此的下载。
          </p>
          <p className="muted">
            去
            <button className="link" onClick={() => onOpenView('browse')}>
              获取插件
            </button>
            找一个下载下来，或者在
            <button className="link" onClick={onOpenSettings}>
              插件源
            </button>
            里加个 GitHub 仓库跟着它的 Release 走 —— 私有仓库也行。
          </p>
        </div>
      ) : (
        <div className="plugin-cards">
          {rows.map((row) => (
            <PluginCard
              key={row.id}
              row={row}
              busy={busy || plugins.busy}
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
              onCheck={() =>
                void plugins.check(row.id).then(async () => {
                  await refresh()
                  setResult(`已检查 ${row.name} 的更新`)
                })
              }
              onInstall={() => {
                const item = plugins.plugins.find((entry) => entry.id === row.id)
                if (item) setInstalling(item)
              }}
              onDropCache={() =>
                void (async () => {
                  // Says what goes and what stays. The copies already inside a
                  // server are separate files and are not touched — which is
                  // the whole question anyone hesitating over this button has.
                  const kept =
                    row.used.length > 0
                      ? `\n\n${row.used.length} 台服上已经装好的副本不受影响 —— 那是各自目录里的另一份文件。`
                      : ''
                  if (
                    !window.confirm(
                      `从库里删掉「${row.name}」的 ${row.versions} 个已下载版本，释放 ${formatBytes(row.size)}？${kept}`,
                    )
                  ) {
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

      {bulkInstall && (
        <BulkInstallDialog
          items={bulkInstall}
          instances={instances}
          onCancel={() => setBulkInstall(null)}
          onInstalled={(summary) => {
            setBulkInstall(null)
            setResult(summary)
            setPicked([])
            void refresh()
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

/**
 * One plugin in the library, as a card that takes the whole row.
 *
 * It was a five-column table, and the columns were the problem. 使用中的实例
 * held three unrelated things at once — a status (未被使用), an action
 * (删除缓存) and a size (1.4 MB) — while the status also appeared one column to
 * the left, and 装到实例 was a text link sitting where a table puts its least
 * important cell. Nothing was wrong with any single cell; the grid was making
 * decisions about importance that it had no way to express.
 *
 * A card can. The identity is on the top line with its face beside it, the
 * facts about the download are one quiet line under it, the servers get a
 * block of their own with nothing else in it, and the two actions sit at the
 * end at the two weights they actually have: one button, and everything else
 * behind ⋯.
 *
 * Full width rather than a grid of tiles, for the reason the search results
 * are rows: what this page answers — which servers are on which version — is a
 * list per plugin, and a list does not fit in a tile.
 */
function PluginCard({
  row,
  picked,
  busy,
  onPick,
  onOpen,
  onOpenInstance,
  onCheck,
  onInstall,
  onDropCache,
}: {
  row: PluginOverviewRow
  picked: boolean
  busy: boolean
  onPick: () => void
  onOpen: () => void
  onOpenInstance: (id: string) => void
  onCheck: () => void
  onInstall: () => void
  onDropCache: () => void
}) {
  // Expanded when the versions disagree, folded when they do not. The default
  // is the answer to "does this row need me", so the page can be scanned
  // without opening anything.
  const [open, setOpen] = useState(row.status === 'mixed')
  const behind = row.upstream !== undefined && row.upstream !== row.newest

  return (
    <article
      className={`plugin-card${row.status === 'mixed' ? ' plugin-card--mixed' : ''}${
        picked ? ' plugin-card--picked' : ''
      }`}
    >
      <label className="plugin-card__pick">
        <input type="checkbox" checked={picked} onChange={onPick} aria-label={`选择 ${row.name}`} />
      </label>

      <PluginIcon className="plugin-card__icon" src={row.iconUrl} name={row.name} />

      <div className="plugin-card__body">
        <div className="plugin-card__head">
          <button className="plugin-card__name" onClick={onOpen} title={`打开「${row.name}」`}>
            {row.name}
          </button>
          <span className="badge">{sourceLabel(row.kind)}</span>
          <StatusChip row={row} />
          {behind && <span className="badge badge--update">上游 {row.upstream}</span>}
        </div>

        <p className="plugin-card__repo">
          {row.repo}
          {row.note && ` · ${row.note}`}
        </p>

        {/* The facts about the download itself. One line, quiet, and it is
            where 体积 lives now — it is a property of the cache, not of the
            servers, and it was being read as one because it shared a cell with
            them. */}
        <p className="plugin-card__facts">
          <span>库里最新 {row.newest ?? '未下载'}</span>
          <span>
            {row.versions} 个版本 · {formatBytes(row.size)}
          </span>
          {behind && <span className="plugin-card__behind">上游已经到 {row.upstream}</span>}
        </p>

        {/* Servers, and nothing but servers — and nothing at all when there
            are none, because 未被使用 is already on the line above and saying
            it twice is how the old cell ended up holding three things. */}
        {row.used.length > 0 && (
        <div className="plugin-card__uses">
          {open ? (
            <ul className="plugin-card__list">
              {row.used.map((use) => (
                <UseLine
                  key={use.instanceId}
                  use={use}
                  onOpen={() => onOpenInstance(use.instanceId)}
                />
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
            <button className="link plugin-card__fold" onClick={() => setOpen(true)}>
              {row.used.length} 个实例 ▾
            </button>
          )}
        </div>
        )}
      </div>

      {/* One of the two places a jar leaves the library for a server. The
          other is the server's own page; both open the same dialog, because
          it is the same decision from two directions. */}
      <div className="plugin-card__act">
        <button
          className="btn btn--primary"
          disabled={row.versions === 0 || busy}
          title={row.versions === 0 ? '还没下载过这个插件的任何版本' : undefined}
          onClick={onInstall}
        >
          装到实例…
        </button>
        <Menu
          className="btn btn--icon"
          title="更多操作"
          ariaLabel={`${row.name} 的更多操作`}
          items={[
            { label: '查看详情与版本', onSelect: onOpen },
            { label: '检查更新', onSelect: onCheck, disabled: busy },
            {
              label: '从库中移除',
              onSelect: onDropCache,
              danger: true,
              disabled: busy || row.versions === 0,
            },
          ]}
        >
          ⋯
        </Menu>
      </div>
    </article>
  )
}

function StatusChip({ row }: { row: PluginOverviewRow }) {
  if (row.status === 'unused') return <span className="badge badge--muted">未被使用</span>
  if (row.status === 'mixed') return <span className="badge badge--warn">版本不一致</span>
  return <span className="badge badge--ok">全部最新</span>
}

/**
 * Handing several library plugins to several servers at once.
 *
 * The single-plugin dialog says which servers are live and will need
 * restarting, and that warning matters more here, not less: this is the same
 * decision multiplied, and the number of servers it takes down is the number
 * the operator has to plan a window around. Each plugin goes at the newest
 * version the library holds — a bulk action is not the place to pin versions
 * one at a time, and the per-plugin dialog is one click away for that.
 */
function BulkInstallDialog({
  items,
  instances,
  onCancel,
  onInstalled,
}: {
  items: LibraryPlugin[]
  instances: InstanceStatus[]
  onCancel: () => void
  onInstalled: (summary: string) => void
}) {
  const [chosen, setChosen] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const targets = instances.filter((instance) => chosen.includes(instance.id))
  const live = targets.filter((instance) => isLive(instance.state))

  const install = async () => {
    setBusy(true)
    setError(null)

    // Carries on past a failure, like every other fan-out here: the servers it
    // already reached have the new jars either way.
    const failures: string[] = []
    let applied = 0
    for (const instance of targets) {
      for (const item of items) {
        try {
          await api.installInstancePlugin(instance.id, item.id, item.versions[0].tag)
          applied++
        } catch (err) {
          failures.push(`${instance.name} / ${item.name}：${err instanceof Error ? err.message : '安装失败'}`)
        }
      }
    }
    setBusy(false)

    if (failures.length > 0) {
      setError(failures.join('；'))
      return
    }
    onInstalled(
      `已把 ${items.length} 个插件装到 ${targets.map((target) => target.name).join('、')}，共 ${applied} 处` +
        (live.length > 0 ? '，重启后生效' : ''),
    )
  }

  return (
    <Modal onClose={onCancel} busy={busy} label="批量装到实例">
      <div className="modal__card">
        <h2 className="modal__title">把 {items.length} 个插件装到实例</h2>

        <ul className="bulk-confirm__plugins">
          {items.map((item) => (
            <li key={item.id}>
              <strong>{item.name}</strong>
              <small>装 {item.versions[0]?.version ?? '未下载'} —— 库里最新的那个</small>
            </li>
          ))}
        </ul>

        <div className="field">
          <span>装到哪几台</span>
          <div className="pick-list pick-list--tall">
            {instances.map((instance) => (
              <label className="pick-list__row" key={instance.id}>
                <input
                  type="checkbox"
                  checked={chosen.includes(instance.id)}
                  disabled={busy}
                  onChange={() =>
                    setChosen((current) =>
                      current.includes(instance.id)
                        ? current.filter((id) => id !== instance.id)
                        : [...current, instance.id],
                    )
                  }
                />
                <span className={`status__dot status__dot--${instance.state}`} />
                <span>{instance.name}</span>
              </label>
            ))}
          </div>
        </div>

        {live.length > 0 && (
          <div className="alert alert--warn">
            选中的 {live.length} 台正在运行，
            <strong>装完必须重启才会生效</strong> —— 重启期间在线玩家会被断开。
            面板不会自动重启，装完之后请自己挑时间。
          </div>
        )}

        {error && <div className="alert alert--error">{error}</div>}

        <div className="modal__actions">
          <button className="btn" disabled={busy} onClick={onCancel}>
            取消
          </button>
          <button
            className="btn btn--primary"
            disabled={busy || targets.length === 0}
            onClick={() => void install()}
          >
            {busy ? '安装中…' : `装到 ${targets.length} 台`}
          </button>
        </div>
      </div>
    </Modal>
  )
}

function UseLine({ use, onOpen }: { use: PluginUse; onOpen: () => void }) {
  return (
    <li>
      <button className="link plugin-card__instance" onClick={onOpen}>
        <span className={`status__dot status__dot--${use.state}`} />
        {use.name}
      </button>
      {use.outdated ? (
        <span className="badge badge--update">{use.version} →</span>
      ) : (
        <span className="plugin-card__ver">{use.version}</span>
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

  // Done and cancelled are both finished news. They are reported by the toast
  // the page raises when the job flips, not by a block that then sits on top
  // of the list it was about until the next navigation.
  if (job.state === 'done' || job.state === 'cancelled') return null

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
