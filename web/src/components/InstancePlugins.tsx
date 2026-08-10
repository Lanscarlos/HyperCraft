import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { InstanceSection, PluginTab } from '../routes'
import { pathOf } from '../routes'
import type {
  InstancePlugin,
  InstancePluginList,
  InstanceStatus,
  LibraryPlugin,
  PendingPluginChange,
} from '../types'
import type { PluginController } from '../usePlugins'
import { PluginBrowse, loaderLabel } from './PluginBrowse'
import { CompatBadge } from './PluginCompat'
import { PluginTabs } from './PluginTabs'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'

/** Which rows the status chips are showing. */
type StatusFilter = 'all' | 'broken' | 'updatable'

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
 */
export function InstancePlugins({
  instance,
  plugins,
  tab,
  onSelectTab,
  onOpenSection,
  onChanged,
}: {
  instance: InstanceStatus
  /** The panel-wide library, for the versions already on hand. */
  plugins: PluginController
  tab: PluginTab
  onSelectTab: (tab: PluginTab) => void
  /** Opens another page of this server — the file manager, for a config dir. */
  onOpenSection: (section: InstanceSection, path?: string) => void
  onChanged: (instance: InstanceStatus) => void
}) {
  const [listing, setListing] = useState<InstancePluginList | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<StatusFilter>('all')

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

  const act = async (action: () => Promise<unknown>, fallback: string) => {
    setBusy(true)
    setError(null)
    try {
      await action()
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : fallback)
    } finally {
      setBusy(false)
    }
  }

  const entries = listing?.entries ?? []
  const broken = entries.filter((entry) => entry.failure).length
  const updatable = entries.filter((entry) => updateFor(entry, listing?.library ?? [])).length
  const pending = listing?.pending ?? []

  if (tab === 'browse') {
    return (
      <div className="stack">
        <PluginTabs
          tab={tab}
          onSelect={onSelectTab}
          hrefFor={(next) =>
            pathOf({
              kind: 'instance',
              id: instance.id,
              section: 'plugins',
              tab: next === 'browse' ? 'browse' : undefined,
            })
          }
        />
        <PluginBrowse instanceId={instance.id} />
      </div>
    )
  }

  if (loading) {
    return (
      <SkeletonScreen label="正在读取插件…">
        <SkeletonPanel title={false}>
          <div className="chart-head">
            <Skeleton w="72px" h={15} />
            <Skeleton w="52%" h={12} />
          </div>
          <SkeletonRows rows={5} />
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  const shown = entries.filter((entry) => {
    if (filter === 'broken') return Boolean(entry.failure)
    if (filter === 'updatable') return Boolean(updateFor(entry, listing?.library ?? []))
    return true
  })

  return (
    <div className="stack">
      <PluginTabs
        tab={tab}
        onSelect={onSelectTab}
        hrefFor={(next) =>
          pathOf({
            kind: 'instance',
            id: instance.id,
            section: 'plugins',
            tab: next === 'browse' ? 'browse' : undefined,
          })
        }
        badges={{
          installed:
            broken > 0 ? <span className="badge badge--danger">{broken}</span> : undefined,
        }}
      />

      <header className="chart-head">
        <h2 className="panel__title">
          已装插件 <span className="muted">{entries.length}</span>
        </h2>
        <div className="chart-head__actions">
          <button className="btn" onClick={() => onOpenSection('files', listing?.entries[0]?.dir)}>
            上传 jar
          </button>
          <button className="btn btn--primary" onClick={() => onSelectTab('browse')}>
            搜索安装
          </button>
        </div>
      </header>

      {error && <div className="alert alert--error">{error}</div>}
      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}

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

      <TargetLine listing={listing} />

      {entries.length > 0 && (
        <div className="filters__chips">
          <Chip active={filter === 'all'} onClick={() => setFilter('all')}>
            全部 {entries.length}
          </Chip>
          <Chip active={filter === 'broken'} onClick={() => setFilter('broken')} tone="danger">
            异常 {broken}
          </Chip>
          <Chip active={filter === 'updatable'} onClick={() => setFilter('updatable')}>
            可更新 {updatable}
          </Chip>
        </div>
      )}

      {entries.length === 0 ? (
        <div className="welcome__empty">
          <p>这台服务器还没有插件。</p>
          <p className="muted">
            用上面的「搜索安装」从 Modrinth、Hangar 或 SpigotMC 找一个装上，
            或者把 jar 直接传进 <code>plugins/</code>，面板会认出来。
          </p>
        </div>
      ) : (
        <div className="plugin-table" role="table" aria-label="已装插件">
          <div className="plugin-table__head" role="row">
            <span role="columnheader">插件</span>
            <span role="columnheader">状态</span>
            <span role="columnheader">版本</span>
            <span role="columnheader">操作</span>
          </div>
          {shown.map((entry) => (
            <PluginRow
              key={entry.key}
              entry={entry}
              library={listing?.library ?? []}
              busy={busy}
              live={listing?.live ?? false}
              onOpenConfig={() => onOpenSection('files', entry.configDir)}
              onOpenConsole={() => onOpenSection('console')}
              onFindDependency={() => onSelectTab('browse')}
              onSwitchVersion={(tag) =>
                void act(
                  () => api.installInstancePlugin(instance.id, entry.pluginId ?? '', tag),
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
              onRemove={() => {
                if (
                  !window.confirm(
                    `确定要从这台服务器移除「${entry.name}」吗？插件自己的配置目录会留着。`,
                  )
                ) {
                  return
                }
                void act(() => api.uninstallInstancePlugin(instance.id, entry.key), '移除失败')
              }}
            />
          ))}
          {shown.length === 0 && (
            <p className="plugin-table__empty muted">这个筛选下没有插件。</p>
          )}
        </div>
      )}

      {listing && !listing.logAvailable && entries.length > 0 && (
        <p className="chart-note">
          这台服务器自面板启动以来没跑过，读不到启动日志 —— 插件有没有加载失败，要开起来才知道。
        </p>
      )}
    </div>
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
      <button className="btn btn--primary" disabled={busy} onClick={onRestart}>
        立即重启
      </button>
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
  tone?: 'danger'
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      className={`chip${active ? ' chip--active' : ''}${tone === 'danger' ? ' chip--danger' : ''}`}
      onClick={onClick}
    >
      {children}
    </button>
  )
}

/** The newest library version this row is not already on, or null. */
function updateFor(entry: InstancePlugin, library: LibraryPlugin[]) {
  if (!entry.managed || !entry.pluginId) return null
  const item = library.find((candidate) => candidate.id === entry.pluginId)
  const newest = item?.versions[0]
  if (!newest || newest.tag === entry.tag) return null
  return newest
}

function PluginRow({
  entry,
  library,
  busy,
  live,
  onOpenConfig,
  onOpenConsole,
  onFindDependency,
  onSwitchVersion,
  onSetEnabled,
  onAdopt,
  onRemove,
}: {
  entry: InstancePlugin
  library: LibraryPlugin[]
  busy: boolean
  live: boolean
  onOpenConfig: () => void
  onOpenConsole: () => void
  onFindDependency: () => void
  onSwitchVersion: (tag: string) => void
  onSetEnabled: (enabled: boolean) => void
  onAdopt: () => void
  onRemove: () => void
}) {
  const update = updateFor(entry, library)
  const item = library.find((candidate) => candidate.id === entry.pluginId)
  const versions = item?.versions ?? []

  return (
    <div
      className={`plugin-table__row${entry.failure ? ' plugin-table__row--broken' : ''}`}
      role="row"
    >
      <div className="plugin-table__cell plugin-table__name" role="cell">
        <strong>{entry.name}</strong>
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
        {!entry.managed && <span className="badge">自行放入</span>}
      </div>

      <div className="plugin-table__cell plugin-table__status" role="cell">
        <StatusCell entry={entry} live={live} />
        <CompatBadge compat={entry.compat} />
      </div>

      <div className="plugin-table__cell plugin-table__version" role="cell">
        {entry.managed && versions.length > 0 ? (
          <select
            className="input-slim"
            value={entry.tag ?? ''}
            disabled={busy}
            aria-label={`${entry.name} 的版本`}
            onChange={(event) => {
              if (event.target.value !== entry.tag) onSwitchVersion(event.target.value)
            }}
          >
            {!versions.some((version) => version.tag === entry.tag) && (
              <option value={entry.tag ?? ''}>{entry.version ?? '未知'}（库里已删除）</option>
            )}
            {versions.map((version) => (
              <option key={version.tag} value={version.tag}>
                {version.version}
              </option>
            ))}
          </select>
        ) : (
          <span>{entry.version || entry.jar?.version || '未知版本'}</span>
        )}
        {update && (
          <button
            className="badge badge--update plugin-table__upgrade"
            disabled={busy}
            onClick={() => onSwitchVersion(update.tag)}
            title={`升级到 ${update.version}`}
          >
            → {update.version}
          </button>
        )}
      </div>

      <div className="plugin-table__cell plugin-table__actions" role="cell">
        {/* A broken row offers what fixes this particular break. A generic
            "详情" here would be a click that leads to another click. */}
        {entry.failure?.kind === 'dependency' && (
          <button className="link" onClick={onFindDependency}>
            安装依赖
          </button>
        )}
        {entry.failure && (
          <button className="link" onClick={onOpenConsole}>
            查看日志
          </button>
        )}
        {!entry.failure && (
          <>
            <button className="link" onClick={onOpenConfig} title={entry.configDir}>
              配置
            </button>
            <button
              className="link"
              disabled={busy || entry.missing}
              onClick={() => onSetEnabled(!entry.enabled)}
            >
              {entry.enabled ? '停用' : '启用'}
            </button>
          </>
        )}
        {entry.adoptable && (
          <button
            className="link"
            disabled={busy}
            title={`按 SHA-256 校验，这就是插件库里的 ${entry.adoptable.name} ${entry.adoptable.version}`}
            onClick={onAdopt}
          >
            接管
          </button>
        )}
        <button className="link link--danger" disabled={busy} onClick={onRemove}>
          移除
        </button>
      </div>
    </div>
  )
}

function StatusCell({ entry, live }: { entry: InstancePlugin; live: boolean }) {
  if (entry.missing) {
    return <span className="badge badge--danger">文件不见了</span>
  }
  if (entry.failure) {
    return <span className="badge badge--danger">加载失败</span>
  }
  // A change the running process has not seen. Never "已停用" on its own while
  // the old jar is still loaded in memory — that is the lie the switch used to
  // tell.
  if (entry.pendingAction && live) {
    return (
      <span className="badge badge--warn" title="这台服务器启动之后改的，进程还没看到">
        {entry.enabled ? '已启用' : '已禁用'} · 待重启
      </span>
    )
  }
  if (!entry.enabled) {
    return <span className="badge badge--warn">已停用</span>
  }
  if (!live) {
    return <span className="badge">未运行</span>
  }
  return (
    <span className="badge badge--live">
      <span className="status__dot status__dot--running" />
      运行中
    </span>
  )
}

