import { useCallback, useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import { formatBytes, formatDate } from '../format'
import type { LibraryView } from '../routes'
import type {
  BulkImpact,
  ForeignJar,
  InstanceStatus,
  LibraryPlugin,
  PluginDownloadJob,
  PluginFilter,
  PluginOverview,
  PluginOverviewRow,
  PluginStatus,
  PluginUse,
} from '../types'
import { isLive, isReconStatus, statusLabel } from '../types'
import type { PluginController } from '../usePlugins'
import { Menu } from './Menu'
import { Modal } from './Modal'
import { Page } from './Page'
import { PluginBrowse, sourceLabel } from './PluginBrowse'
import { PluginIcon } from './PluginIcon'
import { PluginImportDialog } from './PluginImportDialog'
import { PluginInstallDialog } from './PluginInstallDialog'
import { PluginLibraryDrawer } from './PluginLibraryDrawer'
import { PluginSourceDialog } from './PluginSourceDialog'
import { Toast } from './Toast'

/**
 * 插件列表 — every plugin the panel holds, and what is actually running.
 *
 * The page answers one question and the whole shape follows from it: *is what
 * my servers are running what I think they are running*. That has two halves
 * and they are not equal. The first is whether the panel's books describe the
 * directories at all — a jar somebody uploaded by hand, a plugin that rewrote
 * its own file, a record whose file has been deleted. The second is version
 * arithmetic: upstream is ahead of the library, or the library is ahead of a
 * server. The first outranks the second everywhere on this page, because a
 * version number derived from a record that does not match the disk is not a
 * version number, it is a guess with a decimal point in it.
 *
 * So: dense rows, not cards. A card gives every plugin the same weight and the
 * same 90px of vertical space, which is the wrong answer for a library where
 * twenty rows are fine and two need doing — you scroll past the twenty to find
 * the two. At 52px a screen holds the whole library, the status column reads
 * as a column, and the two rows that need attention are the two rows that are
 * not grey.
 *
 * The chips across the top are the same list, counted. They are the summary
 * and they are the navigation: "3 有更新" is the sentence you came to read and
 * clicking it is what you were going to do next.
 */
export function PluginLibraryPage({
  plugins,
  view,
  against,
  recents,
  instances,
  openPluginId,
  onOpenPlugin,
  onOpenSettings,
  onOpenView,
  onChooseAgainst,
  onOpenInstance,
}: {
  plugins: PluginController
  view: LibraryView
  /** Which instances 插件市场 judges compatibility against. */
  against?: string[]
  /** Most recently opened servers, newest first — 插件市场 defaults its
   *  compatibility reference to the first of these that still exists. */
  recents: string[]
  instances: InstanceStatus[]
  /** The plugin whose drawer is open, from the URL. */
  openPluginId?: string
  onOpenPlugin: (id: string | null) => void
  onOpenSettings: () => void
  onOpenView: (view: LibraryView) => void
  onChooseAgainst: (ids: string[]) => void
  onOpenInstance: (id: string) => void
}) {
  const [overview, setOverview] = useState<PluginOverview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<PluginFilter>('all')
  const [picked, setPicked] = useState<string[]>([])
  const [confirming, setConfirming] = useState<BulkImpact | null>(null)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<string | null>(null)
  const [installing, setInstalling] = useState<LibraryPlugin | null>(null)
  const [bulkInstall, setBulkInstall] = useState<LibraryPlugin[] | null>(null)
  const [importing, setImporting] = useState(false)
  const [addingSource, setAddingSource] = useState(false)

  const { job } = plugins

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

  // A finished download adds a version, which can move a row out of 库有更新.
  useEffect(() => {
    if (job?.state !== 'done') return
    void refresh()
    void plugins.refresh()
  }, [job?.state, job?.tag, refresh, plugins])

  const rows = useMemo(() => overview?.rows ?? [], [overview])
  const foreign = overview?.foreign ?? []
  const counts = useMemo(() => countStatuses(rows, foreign.length), [rows, foreign.length])
  const shown = useMemo(
    () => (filter === 'all' ? rows : rows.filter((row) => row.status === filter)),
    [rows, filter],
  )

  if (view === 'browse') {
    return (
      <Page
        wide
        title="插件市场"
        lead="从 Modrinth、Hangar 和 SpigotMC 里找插件。下载到的是面板插件库，不会装进任何一台服务器 —— 装到哪几台是「插件列表」和实例自己的「插件」页上的事。"
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

  /** Everything selected that a bulk upgrade would actually move. */
  const bulkable = picked.filter((id) => {
    const row = rows.find((entry) => entry.id === id)
    return row?.status === 'behind' && !row.pinned
  })

  const reconcileAll = async () => {
    setBusy(true)
    setError(null)
    try {
      // One server at a time. Each pass reads every jar on that server end to
      // end, and firing eight of those at one disk makes all eight slow.
      let bad = 0
      for (const instance of instances) {
        const report = await api.reconcileInstancePlugins(instance.id)
        bad += report.drift + report.missing + report.foreign
      }
      await refresh()
      setResult(
        bad === 0
          ? `对完了 ${instances.length} 台实例，账本和目录一致`
          : `对完了 ${instances.length} 台实例，${bad} 处对不上`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : '对账失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Page
      wide
      title="插件列表"
      lead="按插件看，而不是按服务器看：哪个插件在哪几台服上、版本对不对得上、账本和实例目录里的文件是不是同一份。单台服的增删启停在实例自己的「插件」页里。"
      aside={
        <div className="page__actions">
          <button
            className="btn"
            disabled={plugins.busy || rows.length === 0}
            title="逐个问上游有没有新版本。要花 GitHub API 配额 —— 匿名一小时 60 次。"
            onClick={() => void plugins.checkAll().then(refresh)}
          >
            检查全部更新
          </button>
          {/* Every way a plugin gets into the library, in one place. They were
              scattered across three pages and a settings tab, which meant the
              answer to "how do I add this jar" depended on where the jar came
              from — a distinction that matters to the panel and to nobody
              standing in front of it. */}
          <Menu
            className="btn btn--primary"
            title="添加插件"
            ariaLabel="添加插件"
            items={[
              { label: '从市场搜索…', onSelect: () => onOpenView('browse') },
              { label: '从 GitHub 仓库…', onSelect: () => setAddingSource(true) },
              { label: '导入本地 jar…', onSelect: () => setImporting(true) },
              {
                label: '扫描库外来源…',
                onSelect: () => void reconcileAll(),
                disabled: busy || instances.length === 0,
              },
            ]}
          >
            + 添加插件 ▾
          </Menu>
        </div>
      }
    >
      {error && <div className="alert alert--error">{error}</div>}
      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
      {job && <JobStatus job={job} onCancel={() => void plugins.cancel()} busy={plugins.busy} />}
      {result && <Toast message={result} onDone={() => setResult(null)} />}

      {rows.length === 0 && foreign.length === 0 ? (
        <EmptyLibrary
          onBrowse={() => onOpenView('browse')}
          onImport={() => setImporting(true)}
          onAddSource={() => setAddingSource(true)}
        />
      ) : (
        <>
          <FilterChips counts={counts} filter={filter} onFilter={setFilter} />

          {picked.length > 0 && (
            <BulkBar
              picked={picked}
              bulkable={bulkable}
              busy={busy || plugins.busy}
              onClear={() => setPicked([])}
              onUpgrade={() =>
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
              onInstall={() => {
                const chosen = plugins.plugins.filter(
                  (entry) => picked.includes(entry.id) && entry.versions.length > 0,
                )
                if (chosen.length === 0) {
                  setError('选中的插件一个版本都还没下载，没有可以装的。')
                  return
                }
                setBulkInstall(chosen)
              }}
              onCheck={() =>
                void (async () => {
                  for (const id of picked) await plugins.check(id)
                  await refresh()
                  setResult(`已检查 ${picked.length} 个插件的更新`)
                })()
              }
              onClean={() =>
                void cleanCache(picked, rows, setBusy, setError, setResult, refresh, setPicked)
              }
            />
          )}

          <div className="ptable" role="table" aria-label="插件库">
            <div className="ptable__head" role="row">
              <span className="ptable__cell ptable__cell--pick" />
              <span className="ptable__cell" role="columnheader">
                插件
              </span>
              <span className="ptable__cell ptable__cell--ver" role="columnheader">
                库内最新
              </span>
              <span className="ptable__cell ptable__cell--ver" role="columnheader">
                上游最新
              </span>
              <span className="ptable__cell" role="columnheader">
                部署
              </span>
              <span className="ptable__cell ptable__cell--status" role="columnheader">
                状态
              </span>
              <span className="ptable__cell ptable__cell--act" />
            </div>

            {shown.map((row) => (
              <PluginRow
                key={row.id}
                row={row}
                busy={busy || plugins.busy}
                picked={picked.includes(row.id)}
                open={row.id === openPluginId}
                onPick={() =>
                  setPicked((current) =>
                    current.includes(row.id)
                      ? current.filter((id) => id !== row.id)
                      : [...current, row.id],
                  )
                }
                onOpen={() => onOpenPlugin(row.id)}
                onOpenInstance={onOpenInstance}
                onDownload={() =>
                  void plugins.download(row.id, '').then(async () => {
                    await refresh()
                    setResult(`正在把 ${row.name} 的新版本下载到库里`)
                  })
                }
                onAlign={() =>
                  void (async () => {
                    setBusy(true)
                    try {
                      setConfirming(await api.bulkUpgradePreview([row.id]))
                    } catch (err) {
                      setError(err instanceof Error ? err.message : '读取影响范围失败')
                    } finally {
                      setBusy(false)
                    }
                  })()
                }
                onInstall={() => {
                  const item = plugins.plugins.find((entry) => entry.id === row.id)
                  if (item) setInstalling(item)
                }}
                onDrop={() => void dropPlugin(row, setBusy, setError, setResult, refresh, plugins.refresh)}
              />
            ))}

            {shown.length === 0 && (
              <p className="ptable__empty">
                没有「{filter === 'all' ? '' : statusLabel(filter as PluginStatus)}」状态的插件。
                <button className="link" onClick={() => setFilter('all')}>
                  看全部 {rows.length} 个
                </button>
              </p>
            )}
          </div>

          {(filter === 'all' || filter === 'foreign') && foreign.length > 0 && (
            <ForeignSection
              jars={foreign}
              busy={busy}
              onOpenInstance={onOpenInstance}
              onAdopt={(jar) =>
                void adoptForeign(jar, setBusy, setError, setResult, refresh, plugins.refresh)
              }
            />
          )}

          <LibraryFooter
            overview={overview}
            instances={instances.length}
            busy={busy}
            onReconcile={() => void reconcileAll()}
            onClean={() => void cleanUnused(rows, setBusy, setError, setResult, refresh, plugins.refresh)}
            onOpenSettings={onOpenSettings}
          />
        </>
      )}

      {openPluginId && (
        <PluginLibraryDrawer
          pluginId={openPluginId}
          rows={rows}
          instances={instances}
          plugins={plugins}
          onClose={() => onOpenPlugin(null)}
          onStep={(delta) => {
            const index = rows.findIndex((row) => row.id === openPluginId)
            const next = rows[index + delta]
            if (next) onOpenPlugin(next.id)
          }}
          onOpenInstance={onOpenInstance}
          onChanged={() => {
            void refresh()
            void plugins.refresh()
          }}
          onReport={setResult}
        />
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

      {importing && (
        <PluginImportDialog
          onCancel={() => setImporting(false)}
          onImported={(summary) => {
            setImporting(false)
            setResult(summary)
            void refresh()
            void plugins.refresh()
          }}
        />
      )}

      {addingSource && (
        <PluginSourceDialog
          busy={plugins.busy}
          tokens={plugins.library?.tokens ?? []}
          onCancel={() => setAddingSource(false)}
          onSubmit={async (input) => {
            const ok = await plugins.add(input)
            if (ok) {
              setAddingSource(false)
              setResult(`已把 ${input.repo} 加进库，正在跟它的 Release 走`)
              void refresh()
            }
            return ok
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
                const ids =
                  bulkable.length > 0 ? bulkable : confirming.plugins.map((entry) => entry.id)
                const outcome = await api.bulkUpgrade(ids)
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

// ----------------------------------------------------------------- chips

/** Every chip that will ever be shown, in the order they resolve. Reconcile
 *  states first, because that is the order the row's status is decided in. */
const CHIPS: PluginStatus[] = ['update', 'behind', 'unused', 'drift', 'missing', 'foreign', 'ok']

function countStatuses(rows: PluginOverviewRow[], foreign: number): Record<PluginFilter, number> {
  const counts = {
    all: rows.length,
    ok: 0,
    update: 0,
    behind: 0,
    unused: 0,
    drift: 0,
    missing: 0,
    foreign,
  } as Record<PluginFilter, number>
  for (const row of rows) counts[row.status]++
  return counts
}

/**
 * The summary and the navigation, as one row of controls.
 *
 * They were static pills reading "24 个插件 · 3 个版本不一致". That sentence is
 * the reason the page was opened, and it was not clickable — so reading it was
 * followed by scrolling the whole list looking for the three. A count that
 * filters to what it counted is the same information doing the obvious next
 * thing.
 *
 * A chip that counts zero is dropped rather than greyed. An empty filter is
 * not a state worth offering a way into, and seven permanently-present chips
 * make the two that matter today harder to find.
 */
function FilterChips({
  counts,
  filter,
  onFilter,
}: {
  counts: Record<PluginFilter, number>
  filter: PluginFilter
  onFilter: (filter: PluginFilter) => void
}) {
  const live = CHIPS.filter((status) => counts[status] > 0)

  return (
    <div className="chips" role="group" aria-label="按状态筛选">
      <button
        className={`chip${filter === 'all' ? ' chip--on' : ''}`}
        aria-pressed={filter === 'all'}
        onClick={() => onFilter('all')}
      >
        全部 <b>{counts.all}</b>
      </button>
      {live.map((status) => (
        <button
          key={status}
          className={`chip chip--${status}${filter === status ? ' chip--on' : ''}`}
          aria-pressed={filter === status}
          onClick={() => onFilter(filter === status ? 'all' : status)}
        >
          <span className={`pdot pdot--${status}`} aria-hidden="true" />
          {statusLabel(status)} <b>{counts[status]}</b>
        </button>
      ))}
    </div>
  )
}

// ------------------------------------------------------------------ rows

/**
 * One plugin, at 52px.
 *
 * The 部署 column is where this page earns its keep, and its rule is: say only
 * what is not obvious. Four servers all on the library's newest version is one
 * fact — "4 台 · 一致" — and printing four identical chips to convey it wastes
 * the row's whole width on agreement. One server behind, or one whose file no
 * longer matches the record, is a different fact and gets named: the server,
 * and what is wrong with it. The row is quiet exactly to the extent that
 * nothing is wrong with it.
 */
function PluginRow({
  row,
  picked,
  busy,
  open,
  onPick,
  onOpen,
  onOpenInstance,
  onDownload,
  onAlign,
  onInstall,
  onDrop,
}: {
  row: PluginOverviewRow
  picked: boolean
  busy: boolean
  open: boolean
  onPick: () => void
  onOpen: () => void
  onOpenInstance: (id: string) => void
  onDownload: () => void
  onAlign: () => void
  onInstall: () => void
  onDrop: () => void
}) {
  const bad = isReconStatus(row.status)

  return (
    <div
      className={`ptable__row${bad ? ' ptable__row--bad' : ''}${picked ? ' ptable__row--picked' : ''}${
        open ? ' ptable__row--open' : ''
      }`}
      role="row"
    >
      <label className="ptable__cell ptable__cell--pick">
        <input type="checkbox" checked={picked} onChange={onPick} aria-label={`选择 ${row.name}`} />
      </label>

      <button className="ptable__cell ptable__name" onClick={onOpen} title={`打开「${row.name}」`}>
        <PluginIcon className="ptable__icon" src={row.iconUrl} name={row.name} />
        <span className="ptable__name-body">
          <span className="ptable__name-line">
            <strong>{row.name}</strong>
            {row.pinned && (
              <span className="badge badge--muted" title={`锁定在 ${row.pinned}，不跟上游走`}>
                已锁定
              </span>
            )}
            {row.selfUpdate && (
              <span className="badge badge--muted" title="这个插件被允许自己改写 jar，哈希漂移只记录不告警">
                允许自更新
              </span>
            )}
          </span>
          <span className="ptable__sub">
            {sourceLabel(row.kind)} · {row.repo}
          </span>
        </span>
      </button>

      <span className="ptable__cell ptable__cell--ver" title={versionTitle(row)}>
        <span className="ptable__num">{row.newest ?? '—'}</span>
        {/* Same version, several jars. An explanation and never a warning:
            one build per platform is what a correctly published release of a
            cross-platform plugin looks like. */}
        {row.variants && (
          <small className="ptable__variants" title={`这个版本有 ${row.artifacts} 个 jar`}>
            {row.variants.join(' / ')}
          </small>
        )}
      </span>

      <span className="ptable__cell ptable__cell--ver">
        {row.upstream ? (
          <span className={`ptable__num${row.status === 'update' ? ' ptable__num--ahead' : ''}`}>
            {row.upstream}
          </span>
        ) : (
          <span className="muted" title={row.kind === 'local' ? '手动导入的 jar 没有上游可查' : '还没检查过'}>
            {row.kind === 'local' ? '无上游' : '未检查'}
          </span>
        )}
      </span>

      <span className="ptable__cell ptable__deploy">
        <Deployment row={row} onOpenInstance={onOpenInstance} />
      </span>

      <span className="ptable__cell ptable__cell--status">
        <span className={`pstate pstate--${row.status}`}>
          <span className={`pdot pdot--${row.status}`} aria-hidden="true" />
          {statusLabel(row.status)}
        </span>
      </span>

      <span className="ptable__cell ptable__cell--act">
        <PrimaryAction
          row={row}
          busy={busy}
          onOpen={onOpen}
          onDownload={onDownload}
          onAlign={onAlign}
          onInstall={onInstall}
        />
        <Menu
          className="btn btn--icon"
          title="更多操作"
          ariaLabel={`${row.name} 的更多操作`}
          items={[
            { label: '详情与版本', onSelect: onOpen },
            { label: '装到实例…', onSelect: onInstall, disabled: busy || row.versions === 0 },
            { label: '从库中移除', onSelect: onDrop, danger: true, disabled: busy },
          ]}
        >
          ⋯
        </Menu>
      </span>
    </div>
  )
}

function versionTitle(row: PluginOverviewRow): string {
  const releases = `${row.versions} 个版本`
  const jars = row.artifacts > row.versions ? ` · ${row.artifacts} 个 jar` : ''
  return `${releases}${jars} · ${formatBytes(row.size)}`
}

/**
 * The 部署 cell: which servers, and only what is off-nominal about them.
 *
 * Every copy that is on the library's newest version with a file that matches
 * its record collapses into a count. What is left is named.
 */
function Deployment({
  row,
  onOpenInstance,
}: {
  row: PluginOverviewRow
  onOpenInstance: (id: string) => void
}) {
  // Nothing to say. 未部署 is already the status word one column to the right,
  // and repeating it here is how a cell ends up holding a state instead of the
  // thing the column is for.
  if (row.used.length === 0) {
    return <span className="ptable__none">—</span>
  }

  const notable = row.used.filter((use) => use.outdated || (use.recon && use.recon !== 'ok'))
  const quiet = row.used.length - notable.length

  if (notable.length === 0) {
    return (
      <span className="ptable__agree">
        {row.used.length} 台 · 都是 <span className="ptable__num">{row.newest}</span>
      </span>
    )
  }

  return (
    <span className="ptable__uses">
      {notable.map((use) => (
        <UseChip key={use.instanceId} use={use} onOpen={() => onOpenInstance(use.instanceId)} />
      ))}
      {quiet > 0 && <span className="ptable__agree">+{quiet} 台一致</span>}
    </span>
  )
}

function UseChip({ use, onOpen }: { use: PluginUse; onOpen: () => void }) {
  const trouble = use.recon && use.recon !== 'ok' ? (use.recon as PluginStatus) : null
  return (
    <button
      className={`usechip${trouble ? ` usechip--${trouble}` : ' usechip--behind'}`}
      onClick={onOpen}
      title={
        trouble
          ? `${use.name}：${statusLabel(trouble)}（${use.fileName ?? ''}）`
          : `${use.name} 上是 ${use.version}，比库里旧`
      }
    >
      <span className={`status__dot status__dot--${use.state}`} />
      {use.name}
      <span className="usechip__what">{trouble ? statusLabel(trouble) : use.version}</span>
    </button>
  )
}

/**
 * What the row's one button says.
 *
 * It follows the status rather than being 装到实例 everywhere, because
 * 装到实例 is the right next step for exactly one of the seven states. On a
 * row whose file does not match its record, offering to copy another file onto
 * that server is the wrong offer at the wrong moment — the button there opens
 * the drawer, where both digests and both plugin.yml versions are side by side
 * and there is something to decide with.
 */
function PrimaryAction({
  row,
  busy,
  onOpen,
  onDownload,
  onAlign,
  onInstall,
}: {
  row: PluginOverviewRow
  busy: boolean
  onOpen: () => void
  onDownload: () => void
  onAlign: () => void
  onInstall: () => void
}) {
  switch (row.status) {
    case 'drift':
    case 'missing':
      return (
        <button className="btn btn--primary btn--small" disabled={busy} onClick={onOpen}>
          处理
        </button>
      )
    case 'update':
      return (
        <button
          className="btn btn--primary btn--small"
          disabled={busy}
          title={`把 ${row.upstream} 下载到库里。装到实例是下一步。`}
          onClick={onDownload}
        >
          更新入库
        </button>
      )
    case 'behind':
      return (
        <button
          className="btn btn--primary btn--small"
          disabled={busy}
          title={`把落后的实例升到库内最新的 ${row.newest}`}
          onClick={onAlign}
        >
          对齐
        </button>
      )
    case 'unused':
      return (
        <button
          className="btn btn--small"
          disabled={busy || row.versions === 0}
          onClick={onInstall}
        >
          装到实例…
        </button>
      )
    default:
      return null
  }
}

// --------------------------------------------------------------- foreign

/**
 * Jars on servers that belong to nothing in the library.
 *
 * Below the table rather than in it, because they are not library rows: they
 * have no source, no version history and no update path, and putting them in
 * the table would be the panel claiming to know something about a file it has
 * never seen. What it does know is what the jar declares itself to be, which
 * is enough to decide between the two offers.
 */
function ForeignSection({
  jars,
  busy,
  onAdopt,
  onOpenInstance,
}: {
  jars: ForeignJar[]
  busy: boolean
  onAdopt: (jar: ForeignJar) => void
  onOpenInstance: (id: string) => void
}) {
  return (
    <section className="foreign">
      <h2 className="foreign__title">
        <span className="pdot pdot--foreign" aria-hidden="true" />
        库外来源 · {jars.length} 个 jar
      </h2>
      <p className="foreign__lead">
        这些文件在实例的插件目录里，但库里没有它们的记录 —— 手动传上去的，或者从备份还原来的。
        面板读了它们的 plugin.yml 才知道是什么；收编进库之后就跟别的插件一样能查更新、能回滚。
      </p>

      <div className="foreign__rows">
        {jars.map((jar) => (
          <div className="foreign__row" key={`${jar.instanceId}:${jar.dir}/${jar.fileName}`}>
            <span className="foreign__what">
              <strong>{jar.name}</strong>
              {jar.version && <span className="ptable__num">{jar.version}</span>}
            </span>
            <button className="link foreign__where" onClick={() => onOpenInstance(jar.instanceId)}>
              {jar.instance}
            </button>
            <code className="foreign__file">
              {jar.dir}/{jar.fileName}
            </code>
            {jar.adoptable ? (
              <span className="badge badge--ok" title={`和库里的 ${jar.adoptable.name} ${jar.adoptable.version} 校验和一致`}>
                是库里的 {jar.adoptable.version}
              </span>
            ) : (
              <span className="badge badge--muted">不在库中</span>
            )}
            <button className="btn btn--small" disabled={busy} onClick={() => onAdopt(jar)}>
              收编进库
            </button>
          </div>
        ))}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------- footer

/**
 * What the page is standing on, at the bottom where read-only facts belong.
 *
 * The storage root used to be up beside the title, where it was the fourth
 * thing read on every visit and never once acted on. What does belong here is
 * the last reconciliation: everything above is computed from the ledger, and
 * how long ago anybody checked that the ledger is true is the footnote on all
 * of it.
 */
function LibraryFooter({
  overview,
  instances,
  busy,
  onReconcile,
  onClean,
  onOpenSettings,
}: {
  overview: PluginOverview | null
  instances: number
  busy: boolean
  onReconcile: () => void
  onClean: () => void
  onOpenSettings: () => void
}) {
  if (!overview) return null

  return (
    <footer className="libfoot">
      <span className="libfoot__size">
        共 {formatBytes(overview.totalSize)}
        {overview.unusedSize > 0 && (
          <>
            {' · '}
            <span className="libfoot__reclaim">可回收 {formatBytes(overview.unusedSize)}</span>
          </>
        )}
      </span>

      {overview.unused > 0 && (
        <button className="link" disabled={busy} onClick={onClean}>
          清理 {overview.unused} 个未引用的
        </button>
      )}

      <span className="libfoot__spacer" />

      <span className="libfoot__recon">
        {overview.reconciledAt ? (
          <>上次对账 {formatDate(overview.reconciledAt)}</>
        ) : (
          <>还没对过账</>
        )}
        {overview.unchecked > 0 && instances > 0 && (
          <span className="libfoot__warn"> · {overview.unchecked} 台从没对过</span>
        )}
      </span>
      <button
        className="btn btn--small"
        disabled={busy || instances === 0}
        title="把每台实例的插件目录逐个文件算哈希，跟库里的账本比一遍"
        onClick={onReconcile}
      >
        {busy ? '对账中…' : '对账'}
      </button>
      <button className="link" onClick={onOpenSettings}>
        GitHub 集成
      </button>
    </footer>
  )
}

function EmptyLibrary({
  onBrowse,
  onImport,
  onAddSource,
}: {
  onBrowse: () => void
  onImport: () => void
  onAddSource: () => void
}) {
  return (
    <div className="welcome__empty">
      <p>插件库还是空的。</p>
      <p className="muted">
        这里是面板的公共缓存：一个 jar 下载一次，想装几台服就复制几份，
        于是「同一个插件在五台服上」是一份文件一个校验和，而不是五份谁也认不出彼此的下载。
      </p>
      <p className="muted">
        去
        <button className="link" onClick={onBrowse}>
          插件市场
        </button>
        找一个，
        <button className="link" onClick={onImport}>
          导入一个本地 jar
        </button>
        ，或者
        <button className="link" onClick={onAddSource}>
          加个 GitHub 仓库
        </button>
        跟着它的 Release 走 —— 私有仓库也行。
      </p>
    </div>
  )
}

// ----------------------------------------------------------------- bulk

function BulkBar({
  picked,
  bulkable,
  busy,
  onClear,
  onUpgrade,
  onInstall,
  onCheck,
  onClean,
}: {
  picked: string[]
  bulkable: string[]
  busy: boolean
  onClear: () => void
  onUpgrade: () => void
  onInstall: () => void
  onCheck: () => void
  onClean: () => void
}) {
  return (
    <div className="bulkbar">
      <span>已选 {picked.length} 项</span>
      <span className="device-row__spacer" />
      <button
        className="btn btn--primary"
        disabled={busy || bulkable.length === 0}
        title={bulkable.length === 0 ? '选中的插件没有落后于库的实例，没有要对齐的' : undefined}
        onClick={onUpgrade}
      >
        批量对齐…
      </button>
      <button className="btn" disabled={busy} onClick={onInstall}>
        批量装到实例…
      </button>
      <button className="btn" disabled={busy} onClick={onCheck}>
        检查更新
      </button>
      <button className="btn" disabled={busy} onClick={onClean}>
        清理缓存
      </button>
      <button className="link" onClick={onClear}>
        取消选择
      </button>
    </div>
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
  const ok = await ask({
    title: `清理 ${unused.length} 个插件的下载？`,
    lead: (
      <>
        {unused.map((row) => row.name).join('、')} —— 共 {formatBytes(freed)}。
      </>
    ),
    detail: '这些插件目前没有任何实例在用，删掉的是库里的 jar。',
    confirmLabel: `清理 ${formatBytes(freed)}`,
    danger: true,
  })
  if (!ok) return

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

/** The footer's one-click version of the same thing, across every unused row. */
async function cleanUnused(
  rows: PluginOverviewRow[],
  setBusy: (busy: boolean) => void,
  setError: (error: string | null) => void,
  setResult: (result: string | null) => void,
  refresh: () => Promise<void>,
  refreshLibrary: () => Promise<void>,
) {
  const unused = rows.filter((row) => row.status === 'unused')
  const freed = unused.reduce((sum, row) => sum + row.size, 0)
  const ok = await ask({
    title: `清理 ${unused.length} 个没人在用的插件？`,
    lead: (
      <>
        {unused.map((row) => row.name).join('、')} —— 共 {formatBytes(freed)}。
      </>
    ),
    detail: '实例目录里已经装好的副本不受影响 —— 那是各自目录里的另一份文件。',
    confirmLabel: `清理 ${formatBytes(freed)}`,
    danger: true,
  })
  if (!ok) return
  setBusy(true)
  setError(null)
  try {
    for (const row of unused) await api.deletePlugin(row.id)
    setResult(`已清理 ${unused.length} 个插件，释放 ${formatBytes(freed)}`)
    await refresh()
    await refreshLibrary()
  } catch (err) {
    setError(err instanceof Error ? err.message : '清理失败')
  } finally {
    setBusy(false)
  }
}

async function dropPlugin(
  row: PluginOverviewRow,
  setBusy: (busy: boolean) => void,
  setError: (error: string | null) => void,
  setResult: (result: string | null) => void,
  refresh: () => Promise<void>,
  refreshLibrary: () => Promise<void>,
) {
  const ok = await ask({
    title: `从库里移除「${row.name}」？`,
    lead: `会删掉库里的 ${row.versions} 个版本，释放 ${formatBytes(row.size)}。`,
    detail:
      row.used.length > 0
        ? `${row.used.length} 台服上已经装好的副本不受影响 —— 那是各自目录里的另一份文件。`
        : '没有实例在用它，删掉不影响任何一台服。',
    confirmLabel: '移除',
    danger: true,
  })
  if (!ok) return
  setBusy(true)
  try {
    await api.deletePlugin(row.id)
    setResult(`已清理 ${row.name}，释放 ${formatBytes(row.size)}`)
    await refresh()
    await refreshLibrary()
  } catch (err) {
    setError(err instanceof Error ? err.message : '清理失败')
  } finally {
    setBusy(false)
  }
}

/**
 * Taking a foreign jar into the library.
 *
 * Two different acts wearing one button, and which one it is depends on what
 * the reconciliation found. A jar that matched a library download by checksum
 * only needs the record written — the file is already right and already
 * loading. A jar the library has never seen has to be read out of the server
 * and imported as a version of its own, which is what makes it updatable
 * afterwards. The operator asked for the same thing either way.
 */
async function adoptForeign(
  jar: ForeignJar,
  setBusy: (busy: boolean) => void,
  setError: (error: string | null) => void,
  setResult: (result: string | null) => void,
  refresh: () => Promise<void>,
  refreshLibrary: () => Promise<void>,
) {
  if (!jar.adoptable) {
    setError(
      `${jar.name} 不是库里任何一个版本 —— 先在 ${jar.instance} 的插件页上把它导入插件库，` +
        '那里能把文件读出来算校验和。',
    )
    return
  }
  setBusy(true)
  setError(null)
  try {
    await api.adoptInstancePlugin(jar.instanceId, `file:${jar.dir}/${jar.fileName}`)
    setResult(`已把 ${jar.instance} 上的 ${jar.name} 记到 ${jar.adoptable.name} ${jar.adoptable.version} 名下`)
    await refresh()
    await refreshLibrary()
  } catch (err) {
    setError(err instanceof Error ? err.message : '收编失败')
  } finally {
    setBusy(false)
  }
}

/**
 * Handing several library plugins to several servers at once.
 *
 * The single-plugin dialog says which servers are live and will need
 * restarting, and that warning matters more here, not less: this is the same
 * decision multiplied, and the number of servers it takes down is the number
 * the operator has to plan a window around.
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
    // already reached have the new jars either way. Serial by instance, so two
    // upgrade transactions never work on one plugins/ directory at once.
    const failures: string[] = []
    let applied = 0
    for (const instance of targets) {
      for (const item of items) {
        try {
          await api.installInstancePlugin(instance.id, item.id, item.versions[0].tag)
          applied++
        } catch (err) {
          failures.push(
            `${instance.name} / ${item.name}：${err instanceof Error ? err.message : '安装失败'}`,
          )
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

/**
 * Confirming a bulk upgrade.
 *
 * A cross-instance operation reaches much further than the same click on one
 * server, so the confirmation has to be correspondingly heavier: which servers,
 * which plugins, and — the part that actually costs something — how many of
 * those servers are live and will have to be restarted before any of it takes
 * effect.
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
        <h2>把实例对齐到库内版本</h2>

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

        {/* What the transaction will do, said before it does it. The sweep is
            the part nobody expects and the part that makes the upgrade safe:
            every jar declaring this plugin's name goes, not just the one the
            panel put there. */}
        <p className="muted">
          每一台都是一次事务：先备份旧 jar 和插件的配置目录，删掉目录里所有声明同一个插件名的 jar，
          再放新的进去。任一步失败就还原，服务器停在升级前的样子。旧版本留在快照里，随时能回滚。
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
 */
function mirrorLabel(id: string): string {
  return id === 'direct' ? '源站直连' : id
}
