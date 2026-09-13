import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { ask, askWithToggle } from '../confirm'
import { formatBytes } from '../format'
import type { InstanceSection } from '../routes'
import type {
  InstancePlugin,
  InstancePluginList,
  InstanceStatus,
  LibraryPlugin,
  PendingPluginChange,
} from '../types'
import type { PluginController } from '../usePlugins'
import { Badge } from './Badge'
import { Button } from './Button'
import { DataTable, DataTableHead, DataTableRow } from './DataTable'
import { InstancePluginDrawer } from './InstancePluginDrawer'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'
import { Modal } from './Modal'
import { PageHead } from './Page'
import { PluginBrowse, loaderLabel } from './PluginBrowse'
import { CompatBadge } from './PluginCompat'
import { PluginInstallDialog, loaderNote } from './PluginInstallDialog'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'

/** Which rows the status chips are showing, inside 已安装. */
type StatusFilter = 'all' | 'broken' | 'duplicate'

/**
 * The three things someone comes to this page to do.
 *
 * 已安装 and 可更新 are the same table twice — the second is the first with a
 * filter that people were reaching for often enough to deserve its own place.
 * 市场 is 插件市场 rendered here with this server as the compatibility
 * reference, and that is all it is: **downloading is still a panel-wide act**
 * and what it downloads still lands in the panel's library, not in this
 * server's plugins directory. The two layers stay apart — see the note at the
 * top of this file — and the tab says so in as many words, because a 市场 tab
 * inside a server is exactly where somebody would assume otherwise.
 */
type Tab = 'installed' | 'updatable' | 'market'

/**
 * One server's plugins.
 *
 * The design centre of this page is exposing problems, not listing inventory.
 * A Minecraft server that cannot load a plugin says so once, in a stack trace
 * between two thousand startup lines, and then reports itself perfectly
 * healthy — so the operator's first sign that their permissions plugin never
 * loaded is a player asking why they lost their rank. Nothing in the plugins
 * directory records it: the jar is there, correctly named, enabled, and looks
 * exactly like the ones that worked.
 *
 * So the failures the daemon read out of the console are the first thing on
 * the page, in red, with the reason written out and an action that fixes that
 * specific reason — 安装依赖 for a missing dependency, not a generic 详情.
 *
 * The other thing this page refuses to do is pretend. Every change here lands
 * in a directory the running server read once, at startup: installing,
 * removing, swapping a version and switching a plugin off all take effect on
 * the next boot. An on/off switch that flips instantly would be lying about
 * every one of them, so a disabled plugin enters 已禁用 · 待重启 and is counted
 * in the banner at the top until the server is restarted.
 *
 * What this page cannot do is acquire a plugin. It hands this server things
 * the library already holds; downloading one is a panel-wide act with its own
 * page, and 去插件市场 goes there carrying this server as the compatibility
 * reference so the trip costs the context and nothing else.
 */
export function InstancePlugins({
  instance,
  plugins,
  onOpenBrowse,
  onOpenLibraryList,
  onOpenSection,
  onChanged,
}: {
  instance: InstanceStatus
  /** The panel-wide library: what this server can be given. */
  plugins: PluginController
  /** Opens 插件市场 in the library, with this server as the compatibility
   *  reference. Still reachable from the 市场 tab's own empty state: the tab
   *  here is a lens on the same catalogue, not a replacement for the page. */
  onOpenBrowse: () => void
  /** Opens 插件列表 in the library — where anything downloaded from the 市场
   *  tab actually lands, which is the one thing that tab has to be clear about. */
  onOpenLibraryList: () => void
  /** Opens another page of this server — the file manager, for a config dir. */
  onOpenSection: (section: InstanceSection, path?: string) => void
  onChanged: (instance: InstanceStatus) => void
}) {
  const [listing, setListing] = useState<InstancePluginList | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [filter, setFilter] = useState<StatusFilter>('all')
  const [tab, setTab] = useState<Tab>('installed')
  // The library plugin being handed to this server, or null. Opened from the
  // header and from the empty state, which are the two places the thought
  // "this server needs something" occurs.
  const [installing, setInstalling] = useState<LibraryPlugin | null>(null)
  const [picking, setPicking] = useState(false)
  // The row whose jar is open in the detail drawer, by key. Held as a key
  // rather than as the row itself so a refresh behind the drawer updates what
  // it is showing instead of freezing it at whatever was true on open.
  const [detail, setDetail] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setListing(await api.instancePlugins(instance.id))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取插件列表失败')
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    void refresh()
  }, [refresh])

  /** Runs one action, refreshes, and reports whatever it has to say. An action
   *  that resolves to a string is one whose outcome is not visible in the list
   *  it just refreshed — a reconciliation that found nothing, say. */
  const act = async (action: () => Promise<unknown>, fallback = '操作失败') => {
    setBusy(true)
    setError(null)
    try {
      const said = await action()
      await refresh()
      if (typeof said === 'string') setStatus(said)
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
    } finally {
      setBusy(false)
    }
  }

  const entries = listing?.entries ?? []
  const broken = entries.filter((entry) => entry.failure).length
  const updatable = entries.filter((entry) => entry.update).length
  // Jars whose plugin.yml declares a name another enabled jar here also
  // declares. The server loads one of them and refuses the rest, so this is a
  // failure that has not happened yet rather than untidiness — see
  // plugin.markConflicts.
  const duplicate = entries.filter((entry) => (entry.conflicts?.length ?? 0) > 0).length
  const pending = listing?.pending ?? []

  // What the library holds that this server does not have yet, and has a jar
  // for. A tracked plugin nobody has downloaded cannot be copied anywhere.
  const installedIDs = new Set(entries.filter((e) => e.managed).map((e) => e.pluginId))
  const available = (listing?.library ?? []).filter(
    (item) => item.versions.length > 0 && !installedIDs.has(item.id),
  )

  // The head is the one part of this section that never waits for the
  // listing: the count is the only thing on it that does, and it is blank
  // rather than a placeholder bar so the title does not jump when it lands.
  const head = (
    <PageHead
      title={
        <>
          插件 {!loading && <span className="muted">{entries.length}</span>}
        </>
      }
      lead="这台服务器目录里的插件：哪些能更新、哪些出了问题。「市场」按这台服的核心和版本判兼容性，不过下载来的东西进的是面板插件库。"
      aside={
        !loading && (
          <div className="page__actions">
            {/* Every version number on this page comes out of the panel's own
                records. This is the button that checks the records still
                describe the directory — see plugin/reconcile.go. */}
            <Button
              disabled={busy}
              title="把插件目录逐个文件算 SHA-256，跟面板的账本比一遍"
              onClick={() =>
                void act(async () => {
                  const report = await api.reconcileInstancePlugins(instance.id)
                  const bad = report.drift + report.missing + report.foreign
                  return bad === 0
                    ? `对完了 ${report.checked} 条记录，账本和目录一致`
                    : `对完了 ${report.checked} 条记录，${bad} 处对不上`
                })
              }
            >
              对账
            </Button>
            <Button onClick={() => onOpenSection('files', listing?.entries[0]?.dir)}>
              上传 jar
            </Button>
            {/* Two different acts, and the panel keeps them apart. This one
                copies something the library already holds; the link beside it
                goes off to acquire one. */}
            <Button
              variant="primary"
              disabled={available.length === 0}
              title={available.length === 0 ? '插件库里没有这台服还没装的插件' : undefined}
              onClick={() => setPicking(true)}
            >
              从插件库安装
            </Button>
            {/* 市场 is a tab on this page now, so the link out is to the
                shelf the downloads land on — which is the half of the trip
                this page cannot do. */}
            <button className="link" onClick={onOpenLibraryList}>
              去插件库
            </button>
          </div>
        )
      }
    />
  )

  if (loading) {
    return (
      <div className="stack">
        {head}
        <SkeletonScreen inPage label="正在读取插件…">
          <SkeletonPanel title={false}>
            <div className="chart-head">
              <Skeleton w="72px" h={15} />
              <Skeleton w="52%" h={12} />
            </div>
            <SkeletonRows rows={5} />
          </SkeletonPanel>
        </SkeletonScreen>
      </div>
    )
  }

  const shown = entries.filter((entry) => {
    if (tab === 'updatable') return Boolean(entry.update)
    if (filter === 'broken') return Boolean(entry.failure)
    if (filter === 'duplicate') return (entry.conflicts?.length ?? 0) > 0
    return true
  })
  const opened = entries.find((entry) => entry.key === detail) ?? null

  return (
    <div className="stack">
      {head}

      {error && <div className="alert alert--error">{error}</div>}
      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
      {status && <div className="alert alert--ok">{status}</div>}

      <RestartBanner
        pending={pending}
        live={listing?.live ?? false}
        busy={busy}
        onRestart={() =>
          void act(async () => {
            onChanged(await api.power(instance.id, 'restart'))
          }, '重启失败')
        }
      />

      {/* Above the table, because a name clash is not a property of one row:
          two rows are fighting over one name and the server has already picked
          a winner without telling anybody which. */}
      {duplicate > 0 && (
        <div className="alert alert--warn">
          {/* Boxed, because .alert is a wrapping flex row and the heading would
              otherwise sit beside its own explanation. */}
          <div>
            <strong>有 {duplicate} 个 jar 跟别的重名</strong>
            <p className="restart-banner__list">
              插件叫什么由 jar 里的 plugin.yml 说了算，跟文件名无关。重名的几个里服务端只会加载一个，
              剩下的会被拒绝 —— 下面标黄的行就是，点开看具体撞的是哪个文件。
            </p>
          </div>
        </div>
      )}

      {/* Above the tabs, not inside 已安装. A plugin that did not load is the
          one thing on this page that is wrong right now, and switching to
          市场 must not be a way to stop seeing it. */}
      {broken > 0 && (
        <div className="alert alert--error">
          <div>
            <strong>有 {broken} 个插件没能加载</strong>
            <p className="restart-banner__list">
              服务端把原因写在启动日志里，然后照常报告自己健康 —— 所以这一条是面板替你从控制台里捞
              出来的。点开对应的行看具体是哪一个、为什么。
            </p>
          </div>
          <Button
            size="small"
            onClick={() => {
              setTab('installed')
              setFilter('broken')
            }}
          >
            去看
          </Button>
        </div>
      )}

      <div className="tabs" role="tablist" aria-label="插件">
        <Tab on={tab === 'installed'} onClick={() => setTab('installed')}>
          已安装 {entries.length}
        </Tab>
        <Tab on={tab === 'updatable'} onClick={() => setTab('updatable')}>
          可更新 {updatable}
        </Tab>
        <Tab on={tab === 'market'} onClick={() => setTab('market')}>
          市场
        </Tab>
      </div>

      {tab === 'market' ? (
        <>
          <div className="alert">
            <div>
              <strong>这里下载的插件进的是面板的插件库，不是这台服务器</strong>
              <p className="restart-banner__list">
                下载和安装是两件事：插件库是这台机器上所有服务器共用的一份收藏，装到这台服要回
                「已安装」用「从插件库安装」挑一个版本。下面的兼容性徽章按 {instance.name} 算。
              </p>
            </div>
          </div>
          <PluginBrowse
            against={[instance.id]}
            recents={[instance.id]}
            onChooseAgainst={() => {
              /* No URL to keep in step with here: on this page the reference is
                 the server you are already in. The rail still lets it be
                 changed for a one-off comparison. */
            }}
            onOpenLibrary={onOpenLibraryList}
          />
        </>
      ) : (
        <>

      <TargetLine listing={listing} />

      {tab === 'installed' && entries.length > 0 && (
        <div className="filters__chips">
          <Chip active={filter === 'all'} onClick={() => setFilter('all')}>
            全部 {entries.length}
          </Chip>
          <Chip active={filter === 'broken'} onClick={() => setFilter('broken')} tone="danger">
            异常 {broken}
          </Chip>
          {duplicate > 0 && (
            <Chip active={filter === 'duplicate'} onClick={() => setFilter('duplicate')} tone="warn">
              重名 {duplicate}
            </Chip>
          )}
        </div>
      )}

      {entries.length === 0 ? (
        <div className="welcome__empty">
          <p>这台服务器还没有插件。</p>
          <p className="muted">
            {available.length > 0 ? (
              <>
                插件库里有 {available.length} 个可以装的，用上面的「从插件库安装」挑一个。
              </>
            ) : (
              <>
                插件库还是空的，先在上面的
                <button className="link" onClick={() => setTab('market')}>
                  「市场」
                </button>
                下载一个到插件库，再回这里装。
              </>
            )}
            也可以把 jar 直接传进 <code>plugins/</code>，面板会认出来。
          </p>
        </div>
      ) : (
        <DataTable className="plugin-table" role="table" aria-label="已装插件">
          {/* 版本 sits next to 插件 because they are one fact — which build of
              what is in this directory — and the status column used to be
              wedged between the two halves of it. */}
          <DataTableHead className="plugin-table__head" role="row">
            <span role="columnheader">插件</span>
            <span role="columnheader">版本</span>
            <span role="columnheader">状态</span>
            <span role="columnheader">操作</span>
          </DataTableHead>
          {shown.map((entry) => (
            <PluginRow
              key={entry.key}
              entry={entry}
              library={listing?.library ?? []}
              busy={busy}
              live={listing?.live ?? false}
              onOpenDetail={() => setDetail(entry.key)}
              onOpenConfig={() => onOpenSection('files', entry.configDir)}
              onOpenConsole={() => onOpenSection('console')}
              onFindDependency={() => {
                // The missing dependency may already be in the library, in
                // which case installing it is two clicks away and needs no
                // network. Only send them out to acquire one when it is not.
                const missing = entry.failure?.missing ?? []
                const held = available.find((item) =>
                  missing.some((name) => name.toLowerCase() === item.name.toLowerCase()),
                )
                if (held) setInstalling(held)
                else onOpenBrowse()
              }}
              onSwitchVersion={(tag, sha) =>
                void act(
                  () => api.installInstancePlugin(instance.id, entry.pluginId ?? '', tag, sha),
                  '切换版本失败',
                )
              }
              onSetEnabled={(enabled) =>
                void act(
                  () => api.setInstancePluginEnabled(instance.id, entry.key, enabled),
                  '切换失败',
                )
              }
              onAdopt={() =>
                void act(() => api.adoptInstancePlugin(instance.id, entry.key), '接管失败')
              }
              onImportToLibrary={() => {
                void ask({
                  title: `把「${entry.name}」导入插件库？`,
                  lead: '面板会读出这个 jar，算一次校验和，把它作为一个版本存进插件库。',
                  detail:
                    '这台服上的文件不动，还是原来那一份在跑。进库之后它和下载来的插件一样：能装到别的服、' +
                    '能在总览里比版本、能回滚 —— 只是没有上游，所以永远不会有更新提示。',
                  confirmLabel: '导入插件库',
                }).then((ok) => {
                  if (!ok) return
                  void act(async () => {
                    const row = await api.importInstancePluginToLibrary(instance.id, entry.key)
                    // The library changed, and this page is not the only thing
                    // showing it — the picker above reads plugins.
                    void plugins.refresh()
                    return `已把 ${row.name} ${row.version || ''} 存进插件库`.trim()
                  }, '导入插件库失败')
                })
              }}
              onRollback={() => {
                // One card, two decisions. It used to be two stacked boxes and
                // the second one — 确定 = 连配置一起, 取消 = 只换 jar — asked
                // the operator to read a legend to find out which button meant
                // what. A checkbox says the same thing without one, and it can
                // sit unticked, which is the answer most of the time.
                void askWithToggle({
                  title: `回滚「${entry.name}」？`,
                  lead: '换回上一次升级之前的那个 jar。',
                  detail: '升级时留下的快照就是回滚的来源，回滚之后它还在，可以再滚回来。',
                  confirmLabel: '回滚',
                  toggle: {
                    label: '连配置目录一起还原',
                    note: '插件的配置目录会退回升级前的样子，升级之后写进去的数据会丢。多数情况不用勾。',
                  },
                }).then(({ ok, toggled }) => {
                  if (!ok) return
                  void act(
                    () => api.rollbackInstancePlugin(instance.id, entry.pluginId ?? '', toggled),
                    '回滚失败',
                  )
                })
              }}
              onRemove={() => {
                // The config directory is offered rather than assumed, and the
                // checkbox sits unticked: keeping it is right for the common
                // case, which is troubleshooting a version rather than being
                // done with the plugin. It is also the only one of the two
                // that can be undone.
                //
                // The panel only offers it when it knows which directory it
                // is. Bukkit names that directory after plugin.yml rather than
                // after the jar, so a row whose descriptor would not read has
                // nothing to go on — and neither does one sharing its declared
                // name with another jar here, where the answer is a coin flip
                // with somebody's permission groups on it.
                const named = entry.jar?.name ?? ''
                const known = named !== '' && (entry.conflicts?.length ?? 0) === 0
                const question = {
                  title: `从这台服移除「${entry.name}」？`,
                  lead: `会删掉 ${instance.name} 插件目录里的这个 jar。`,
                  confirmLabel: '移除',
                  danger: true,
                }
                const answer = known
                  ? askWithToggle({
                      ...question,
                      toggle: {
                        label: `连配置目录 ${entry.dir}/${named} 一起删掉`,
                        note: '里面通常不只是设置 —— 经济余额、领地、权限组这些也在这个目录。删掉之后重装是一份全新的配置，找不回来。',
                      },
                    })
                  : ask({
                      ...question,
                      detail:
                        '插件自己的配置目录会留着 —— 面板读不出这个 jar 声明的名字，或者有别的 jar 也叫这个名字，没法确定哪个目录是它的。',
                    }).then((ok) => ({ ok, toggled: false }))
                void answer.then(({ ok, toggled }) => {
                  if (!ok) return
                  void act(
                    () => api.uninstallInstancePlugin(instance.id, entry.key, toggled),
                    '移除失败',
                  )
                })
              }}
            />
          ))}
          {shown.length === 0 && (
            <p className="plugin-table__empty muted">这个筛选下没有插件。</p>
          )}
        </DataTable>
      )}

      {listing && !listing.logAvailable && entries.length > 0 && (
        <p className="chart-note">
          这台服务器自面板启动以来没跑过，读不到启动日志 —— 插件有没有加载失败，要开起来才知道。
        </p>
      )}
        </>
      )}

      {/* Outside the tabs: a dialog opened from 已安装 must survive the tab it
          was opened from, and all three of these are modal anyway. */}
      {picking && (
        <LibraryPicker
          available={available}
          onCancel={() => setPicking(false)}
          onPick={(item) => {
            setPicking(false)
            setInstalling(item)
          }}
          onOpenBrowse={() => {
            setPicking(false)
            onOpenBrowse()
          }}
        />
      )}

      {opened && (
        <InstancePluginDrawer
          entry={opened}
          siblings={entries}
          onClose={() => setDetail(null)}
          onOpenConfig={() => onOpenSection('files', opened.configDir)}
          onOpenConsole={() => onOpenSection('console')}
        />
      )}

      {installing && (
        <PluginInstallDialog
          item={installing}
          instances={[instance]}
          preselect={instance.id}
          onCancel={() => setInstalling(null)}
          onInstalled={(summary) => {
            setInstalling(null)
            setStatus(summary)
            void refresh()
          }}
        />
      )}
    </div>
  )
}

/**
 * Picking which library plugin this server gets.
 *
 * A list rather than a dropdown because the choice is made on what a plugin is
 * — its source, how many versions are held, who else runs it — and a
 * `<select>` of bare names makes that invisible. Only plugins with a jar on
 * disk and not already here: a tracked source nobody has downloaded cannot be
 * copied anywhere, and offering it would be an entry that fails on click.
 */
function LibraryPicker({
  available,
  onCancel,
  onPick,
  onOpenBrowse,
}: {
  available: LibraryPlugin[]
  onCancel: () => void
  onPick: (item: LibraryPlugin) => void
  onOpenBrowse: () => void
}) {
  const [query, setQuery] = useState('')
  const shown = available.filter((item) =>
    `${item.name} ${item.source.repo}`.toLowerCase().includes(query.trim().toLowerCase()),
  )

  return (
    <Modal onClose={onCancel} label="从插件库安装">
      <div className="modal__card modal__card--wide">
        <h2 className="modal__title">从插件库安装</h2>
        <p className="modal__lead">
          这里只列插件库里已经下载好的。想要的不在里面，就去
          <button className="link" onClick={onOpenBrowse}>
            插件市场
          </button>
          下载。
        </p>

        <input
          className="filters__search"
          value={query}
          placeholder="按名称筛选"
          onChange={(event) => setQuery(event.target.value)}
          aria-label="按名称筛选"
          autoFocus
        />

        <div className="pick-list pick-list--tall">
          {shown.map((item) => (
            <button className="pick-list__row pick-list__row--button" key={item.id} onClick={() => onPick(item)}>
              <span className="pick-list__name">
                <strong>{item.name}</strong>
                <small>
                  {item.source.repo} · 库里 {item.versions.length} 个版本
                  {item.usedBy.length > 0 && ` · ${item.usedBy.join('、')} 在用`}
                </small>
              </span>
              <Badge>{item.versions[0]?.version}</Badge>
            </button>
          ))}
          {shown.length === 0 && <p className="muted">没有匹配的插件。</p>}
        </div>

        <div className="modal__actions">
          <Button onClick={onCancel}>
            取消
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * N 项变更待重启生效.
 *
 * Only rendered when there is something pending, and only for a running
 * server: a stopped one will pick everything up on its next start, and telling
 * someone to restart a server that is not running is noise. The list is named
 * rather than counted alone — "3 项变更" without saying which three is a number
 * you cannot act on.
 */
function RestartBanner({
  pending,
  live,
  busy,
  onRestart,
}: {
  pending: PendingPluginChange[]
  live: boolean
  busy: boolean
  onRestart: () => void
}) {
  if (pending.length === 0 || !live) return null

  return (
    <div className="alert alert--warn restart-banner">
      <div>
        <strong>{pending.length} 项变更待重启生效</strong>
        <p className="restart-banner__list">
          {pending.slice(0, 4).map((change) => change.label).join('、')}
          {pending.length > 4 && ` 等 ${pending.length} 项`}
        </p>
      </div>
      {/* Plain inside a coloured alert. The banner already is the emphasis —
          it has a warn surface, a border and the top of the pane. A filled
          button on top of that is the same claim made twice, and it would be
          the second filled button on a screen whose first one is 从插件库安装. */}
      <Button disabled={busy} onClick={onRestart}>
        立即重启
      </Button>
    </div>
  )
}

/** What the compatibility badges on this page were judged against. Worth
 *  saying: 未知兼容性 caused by the panel not recognising the server jar looks
 *  identical to 未知兼容性 caused by a plugin publishing nothing. */
function TargetLine({ listing }: { listing: InstancePluginList | null }) {
  const target = listing?.target
  if (!target?.loader && !target?.mcVersion) {
    return (
      <p className="chart-note">
        没认出这台服务器的核心和版本，所以兼容性一栏都是「未知」。
        换成面板下载的核心，或者启动一次让它写下 <code>version_history.json</code>，就能判断了。
      </p>
    )
  }
  return (
    <p className="chart-note">
      按 {loaderLabel(target.loader)} {target.mcVersion} 判断兼容性
      {target.source === 'jar-name' && '（从核心文件名猜的，可能不准）'}
    </p>
  )
}

function Chip({
  active,
  tone,
  onClick,
  children,
}: {
  active: boolean
  tone?: 'danger' | 'warn'
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      className={`chip${active ? ' chip--active' : ''}${tone ? ` chip--${tone}` : ''}`}
      onClick={onClick}
    >
      {children}
    </button>
  )
}

function PluginRow({
  entry,
  library,
  busy,
  live,
  onOpenDetail,
  onOpenConfig,
  onOpenConsole,
  onFindDependency,
  onSwitchVersion,
  onSetEnabled,
  onAdopt,
  onImportToLibrary,
  onRollback,
  onRemove,
}: {
  entry: InstancePlugin
  library: LibraryPlugin[]
  busy: boolean
  live: boolean
  onOpenDetail: () => void
  onOpenConfig: () => void
  onOpenConsole: () => void
  onFindDependency: () => void
  /** The digest is optional and names which jar of that release: the update
   *  badge already knows, having been told by the panel. Picking a version out
   *  of the dropdown leaves it to the server to resolve. */
  onSwitchVersion: (tag: string, sha?: string) => void
  onSetEnabled: (enabled: boolean) => void
  onAdopt: () => void
  onImportToLibrary: () => void
  onRollback: () => void
  onRemove: () => void
}) {
  // Whether there is anything to move up to is the server's answer, not the
  // library's: see InstancePlugin.update.
  const update = entry.update
  const item = library.find((candidate) => candidate.id === entry.pluginId)
  const versions = item?.versions ?? []
  const clashes = entry.conflicts ?? []

  return (
    <DataTableRow
      className={`plugin-table__row${entry.failure ? ' plugin-table__row--broken' : ''}${
        clashes.length > 0 ? ' plugin-table__row--clash' : ''
      }`}
      role="row"
    >
      <div className="plugin-table__cell plugin-table__name" role="cell">
        {/* The name opens the jar's own account of itself. It is the thing on
            the row an operator points at when the question is "what is this",
            and it was, until now, the one thing on the row that did nothing. */}
        <button className="plugin-table__open" onClick={onOpenDetail}>
          {entry.name}
        </button>
        {/* The reason goes directly under the name rather than behind a
            tooltip or a details page: it is the whole content of the row. */}
        {entry.failure ? (
          <span className="plugin-table__reason">{entry.failure.reason}</span>
        ) : (
          <span className="plugin-table__path">
            {entry.dir}/{entry.fileName}
            {entry.size > 0 && ` · ${formatBytes(entry.size)}`}
          </span>
        )}
        {(!entry.managed || clashes.length > 0) && (
          <span className="plugin-table__tags">
            {!entry.managed && <Badge>自行放入</Badge>}
            {clashes.length > 0 && (
              <Badge
                tone="warn"
                title={`跟这些文件声明了同一个插件名：${clashes.join('、')}`}
              >
                重名 {clashes.length + 1}
              </Badge>
            )}
          </span>
        )}
      </div>

      <div className="plugin-table__cell plugin-table__version" role="cell">
        {entry.managed && versions.length > 0 ? (
          <Select
            className="input-slim"
            value={entry.tag ?? ''}
            disabled={busy}
            ariaLabel={`${entry.name} 的版本`}
            options={[
              // An installed version whose jar has since been deleted from the
              // library is still the version this server is running. It is not
              // in the list, so it is put back at the top of it — dropping it
              // would silently reassign the row to whatever sorts first.
              ...(versions.some((version) => version.tag === entry.tag)
                ? []
                : [
                    {
                      value: entry.tag ?? '',
                      label: `${entry.version ?? '未知'}（库里已删除）`,
                    },
                  ]),
              ...versions.map((version) => ({
                value: version.tag,
                label: version.version,
                // A plugin that supports several platforms holds one jar per
                // platform under the same release number, and the version
                // string alone does not say which of them this is.
                note: loaderNote(version.loaders),
              })),
            ]}
            onChange={(next) => {
              if (next !== entry.tag) onSwitchVersion(next)
            }}
          />
        ) : (
          <span>{entry.version || entry.jar?.version || '未知版本'}</span>
        )}
        {update && (
          <button
            className="badge badge--update plugin-table__upgrade"
            disabled={busy}
            onClick={() => onSwitchVersion(update.tag, update.sha256)}
            title={
              update.platform
                ? `升级到 ${update.version}，用其中的 ${loaderLabel(update.platform)} 构建`
                : `升级到 ${update.version}`
            }
          >
            → {update.version}
          </button>
        )}
      </div>

      <div className="plugin-table__cell plugin-table__status" role="cell">
        <StatusCell entry={entry} live={live} />
        <CompatBadge compat={entry.compat} />
      </div>

      {/* Two buttons and a menu, the same two slots on every row, so the column
          reads as a column. They used to be five underlined links laid out by
          whichever of them happened to apply, which is why no two rows lined up
          and why none of them looked pressable. What stays out front is the
          action this particular row is asking for; everything rarer — 接管,
          回滚, and 移除, which is not something to put under a stray click —
          lives behind the ⋯. */}
      <div className="plugin-table__cell plugin-table__actions" role="cell">
        {entry.failure ? (
          <>
            {/* A broken row offers what fixes this particular break. A generic
                "详情" here would be a click that leads to another click. */}
            {entry.failure.kind === 'dependency' && (
              <Button size="row" onClick={onFindDependency}>
                安装依赖
              </Button>
            )}
            <Button size="row" onClick={onOpenConsole}>
              查看日志
            </Button>
          </>
        ) : (
          <>
            <Button size="row" onClick={onOpenConfig} title={entry.configDir}>
              配置
            </Button>
            <Button
              size="row"
              disabled={busy || entry.missing}
              onClick={() => onSetEnabled(!entry.enabled)}
            >
              {entry.enabled ? '停用' : '启用'}
            </Button>
          </>
        )}

        <Menu
          className="btn btn--icon btn--row"
          ariaLabel={`${entry.name} 的更多操作`}
          title="更多操作"
          items={moreActions({
            entry,
            busy,
            onOpenDetail,
            onOpenConfig,
            onAdopt,
            onImportToLibrary,
            onRollback,
            onRemove,
          })}
        >
          ⋯
        </Menu>
      </div>
    </DataTableRow>
  )
}

/** What is behind the ⋯. Built rather than written inline so the order is one
 *  list to read: identify it, then the two recoveries, then the destructive one
 *  last and marked. */
function moreActions({
  entry,
  busy,
  onOpenDetail,
  onOpenConfig,
  onAdopt,
  onImportToLibrary,
  onRollback,
  onRemove,
}: {
  entry: InstancePlugin
  busy: boolean
  onOpenDetail: () => void
  onOpenConfig: () => void
  onAdopt: () => void
  onImportToLibrary: () => void
  onRollback: () => void
  onRemove: () => void
}): MenuItem[] {
  const items: MenuItem[] = [{ label: '插件详情', onSelect: onOpenDetail }]
  // The failing row spent its two buttons on the failure, so its config link
  // comes back here rather than disappearing.
  if (entry.failure) {
    items.push({ label: '打开配置目录', onSelect: onOpenConfig })
  }
  // The two ways a hand-placed jar stops being one, and which of them applies
  // is not the operator's problem to work out: 接管 when the library already
  // holds these exact bytes and only the record is missing, 导入插件库 when it
  // has never seen them. Exactly one of the two is ever offered.
  if (entry.adoptable) {
    items.push({ label: '接管', onSelect: onAdopt, disabled: busy })
  } else if (!entry.managed) {
    items.push({ label: '导入插件库', onSelect: onImportToLibrary, disabled: busy })
  }
  // Why old versions are kept at all. It reads the upgrade snapshot rather than
  // the library, so it works even after a retention policy has pruned the
  // version it puts back.
  if (entry.managed && entry.pluginId) {
    items.push({ label: '回滚到上个版本', onSelect: onRollback, disabled: busy })
  }
  items.push({ label: '移除', onSelect: onRemove, danger: true, disabled: busy })
  return items
}

/** Twelve hex characters — enough to compare two digests by eye, short enough
 *  to fit in a tooltip beside the sentence that needs them. */
function short(sha?: string): string {
  return sha ? sha.slice(0, 12) : '（无）'
}

function StatusCell({ entry, live }: { entry: InstancePlugin; live: boolean }) {
  if (entry.missing) {
    return <Badge tone="danger">文件不见了</Badge>
  }
  if (entry.failure) {
    return <Badge tone="danger">加载失败</Badge>
  }
  // The books and the file disagree. Above every other state on this row: what
  // is loaded is not what the record says is loaded, so "运行中 2.3.66" would
  // be a version number about a different file.
  if (entry.recon === 'drift') {
    return (
      <Badge
        tone={entry.selfUpdate ? 'muted' : 'danger'}
        title={
          entry.selfUpdate
            ? `这个插件被允许自更新，所以只记录不告警。磁盘上是 ${short(entry.sha256)}，账本里是 ${short(entry.recordSha)}。`
            : `磁盘上的 jar 已经不是面板放进去的那一份了。磁盘 ${short(entry.sha256)}，账本 ${short(entry.recordSha)}。`
        }
      >
        {entry.selfUpdate ? '已自更新' : '哈希不匹配'}
      </Badge>
    )
  }
  // A change the running process has not seen. Never "已停用" on its own while
  // the old jar is still loaded in memory — that is the lie the switch used to
  // tell.
  if (entry.pendingAction && live) {
    return (
      <Badge tone="warn" title="这台服务器启动之后改的，进程还没看到">
        {entry.enabled ? '已启用' : '已禁用'} · 待重启
      </Badge>
    )
  }
  if (!entry.enabled) {
    return <Badge tone="warn">已停用</Badge>
  }
  if (!live) {
    return <Badge>未运行</Badge>
  }
  return (
    <Badge tone="live">
      <span className="status__dot status__dot--running" />
      运行中
    </Badge>
  )
}

/** One of the three panes. Built on the chip vocabulary the rest of the panel
 *  uses rather than a tab component of its own — there is one tab bar in this
 *  codebase and this is it. */
function Tab({
  on,
  onClick,
  children,
}: {
  on: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={on}
      className={`tabs__tab${on ? ' tabs__tab--on' : ''}`}
      onClick={onClick}
    >
      {children}
    </button>
  )
}
