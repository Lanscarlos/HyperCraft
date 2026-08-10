import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'

import { api } from '../api'
import { ask } from '../confirm'
import { formatBytes, formatDate } from '../format'
import type {
  InstanceStatus,
  LibraryPlugin,
  PluginArtifact,
  PluginOverviewRow,
  PluginPolicy,
  PluginRelease,
  PluginStatus,
  PluginTokenInfo,
  PluginUpdateMode,
  PluginUse,
  PluginVersion,
} from '../types'
import { pluginArtifacts, statusLabel, versionSize } from '../types'
import { useDismiss } from '../useDismiss'
import type { PluginController } from '../usePlugins'
import { loaderLabel, sourceLabel } from './PluginBrowse'
import { PluginIcon } from './PluginIcon'

/**
 * One plugin's detail, over the list rather than instead of it.
 *
 * It was a page. A page throws away the thing you were doing — the filter, the
 * scroll position, the row you were comparing this one against — and the work
 * this view is for is comparative: you open a plugin because of something you
 * saw in the list, and the next thing you want is usually the row underneath.
 * So it opens over the list, ↑↓ walks the rows without closing, and Esc puts
 * you back exactly where you were.
 *
 * Four tabs, and they are four different questions rather than four sections of
 * one answer:
 *
 *   概览  what is the state of this plugin right now, across every server
 *   版本  what exists, what is held, and which jar is which
 *   设置  what may the panel do with it without being asked
 *   依赖  what does it need in order to load
 *
 * 依赖 hides itself when neither source has anything to say. A permanently
 * empty tab is a tab that trains people not to click tabs.
 */

type Tab = 'overview' | 'versions' | 'settings' | 'deps'

export function PluginLibraryDrawer({
  pluginId,
  rows,
  instances,
  plugins,
  onClose,
  onStep,
  onOpenInstance,
  onChanged,
  onReport,
}: {
  pluginId: string
  rows: PluginOverviewRow[]
  instances: InstanceStatus[]
  plugins: PluginController
  onClose: () => void
  /** ±1 through the list behind the drawer, keeping it open. */
  onStep: (delta: number) => void
  onOpenInstance: (id: string) => void
  onChanged: () => void
  onReport: (message: string) => void
}) {
  const { leaving, close } = useDismiss(onClose)
  const [tab, setTab] = useState<Tab>('overview')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const row = rows.find((entry) => entry.id === pluginId) ?? null
  const item = plugins.plugins.find((entry) => entry.id === pluginId) ?? null

  // Every jar's declared dependencies, merged across the versions held. The
  // registry publishes a list of its own and it is a different claim — see
  // DependencyTab — so the two are kept apart rather than unioned.
  const declared = useMemo(() => declaredDeps(item), [item])
  const published = item?.latest?.dependencies ?? []
  const hasDeps = declared.depend.length + declared.softDepend.length + published.length > 0

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (busy) return
      if (event.key === 'Escape') {
        event.preventDefault()
        close()
        return
      }
      // Not while something is being typed into: ↓ inside the retention field
      // should move the caret, not the drawer.
      const active = document.activeElement
      if (active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement) return
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        onStep(event.key === 'ArrowDown' ? 1 : -1)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [close, onStep, busy])

  // Keeping the tab across ↑↓ is right — you are comparing the same question
  // across plugins — except for a tab the next plugin does not have.
  useEffect(() => {
    if (tab === 'deps' && !hasDeps) setTab('overview')
  }, [tab, hasDeps])

  if (!row || !item) return null

  const position = rows.findIndex((entry) => entry.id === pluginId) + 1

  const act = async (what: () => Promise<string>) => {
    setBusy(true)
    setError(null)
    try {
      onReport(await what())
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  return createPortal(
    <div className={`drawer drawer--wide${leaving ? ' drawer--leaving' : ''}`}>
      <div className="drawer__scrim" onClick={() => !busy && close()} aria-hidden="true" />
      <aside className="drawer__panel" role="dialog" aria-label={row.name}>
        <header className="drawer__head">
          <div className="drawer__title">
            <PluginIcon className="browse-row__icon" src={row.iconUrl} name={row.name} />
            <div>
              <h2>
                {row.name}
                <span className={`pstate pstate--${row.status}`}>
                  <span className={`pdot pdot--${row.status}`} aria-hidden="true" />
                  {statusLabel(row.status)}
                </span>
              </h2>
              <p className="drawer__sub">
                <span>{sourceLabel(row.kind)}</span>
                <span>{row.repo}</span>
                {row.note && <span>{row.note}</span>}
              </p>
            </div>
          </div>

          {/* Where you are in the list, and the two keys that move it. The
              drawer is a viewport onto the table behind it, and saying so is
              what makes ↑↓ discoverable rather than a secret. */}
          <div className="drawer__step">
            <span className="drawer__step-count">
              {position} / {rows.length}
            </span>
            <button
              className="btn btn--icon"
              onClick={() => onStep(-1)}
              disabled={position <= 1}
              aria-label="上一个插件"
              title="上一个（↑）"
            >
              ↑
            </button>
            <button
              className="btn btn--icon"
              onClick={() => onStep(1)}
              disabled={position >= rows.length}
              aria-label="下一个插件"
              title="下一个（↓）"
            >
              ↓
            </button>
            <button className="btn btn--icon" onClick={() => !busy && close()} aria-label="关闭（Esc）">
              ✕
            </button>
          </div>
        </header>

        <nav className="drawer__tabs" role="tablist">
          <TabButton id="overview" tab={tab} onPick={setTab}>
            概览
          </TabButton>
          <TabButton id="versions" tab={tab} onPick={setTab}>
            版本 <small>{row.versions}</small>
          </TabButton>
          <TabButton id="settings" tab={tab} onPick={setTab}>
            设置
          </TabButton>
          {hasDeps && (
            <TabButton id="deps" tab={tab} onPick={setTab}>
              依赖
            </TabButton>
          )}
        </nav>

        <div className="drawer__body">
          {error && <div className="alert alert--error">{error}</div>}
          {item.checkError && (
            <div className="alert alert--warn">检查更新失败：{item.checkError}</div>
          )}

          {tab === 'overview' && (
            <OverviewTab
              row={row}
              item={item}
              instances={instances}
              busy={busy || plugins.busy}
              onOpenInstance={onOpenInstance}
              // To the version this server was offered, and the jar of it the
              // panel picked — not to whatever is at the top of the library's
              // list, which on a cross-platform plugin is regularly a build
              // this server cannot load. See plugin.UpdateFor.
              onUpgrade={(use) =>
                void act(async () => {
                  if (!use.update) return `${use.name} 上的 ${row.name} 已经是它能用的最新版本了`
                  await api.installInstancePlugin(
                    use.instanceId,
                    row.id,
                    use.update.tag,
                    use.update.sha256,
                  )
                  return `已把 ${use.name} 上的 ${row.name} 升到 ${use.update.version}，重启后生效`
                })
              }
              onRollback={(use, withConfig) =>
                void act(async () => {
                  const entry = await api.rollbackInstancePlugin(use.instanceId, row.id, withConfig)
                  return `已把 ${use.name} 上的 ${row.name} 回滚到 ${entry.version}${
                    withConfig ? '，配置目录一并还原' : ''
                  }`
                })
              }
              onReconcile={(use) =>
                void act(async () => {
                  const report = await api.reconcileInstancePlugins(use.instanceId)
                  const bad = report.drift + report.missing + report.foreign
                  return bad === 0
                    ? `${use.name} 对完了，账本和目录一致`
                    : `${use.name} 对完了，${bad} 处对不上`
                })
              }
              // Puts the recorded version back, byte for byte — the same
              // transaction an upgrade runs, aimed at the version already on
              // the record rather than a newer one.
              onRepush={(use) =>
                void act(async () => {
                  await api.installInstancePlugin(use.instanceId, row.id, use.tag)
                  return `已把 ${row.name} ${use.version} 重新推送到 ${use.name}`
                })
              }
              onAccept={(use) =>
                void act(async () => {
                  const entry = await api.acceptInstancePlugin(use.instanceId, row.id)
                  return `已按 ${use.name} 上的文件更新账本：${entry.version ?? ''} ${(entry.sha256 ?? '').slice(0, 12)}`
                })
              }
            />
          )}

          {tab === 'versions' && (
            <VersionsTab
              item={item}
              row={row}
              busy={busy || plugins.busy || plugins.downloading}
              onDownload={(tag, asset) => void plugins.download(item.id, tag, asset)}
              onDropArtifact={(version, artifact) =>
                void act(async () => {
                  await api.deletePluginVersion(item.id, version.tag, artifact.sha256)
                  return `已从库里删掉 ${artifact.fileName}`
                })
              }
            />
          )}

          {tab === 'settings' && (
            <SettingsTab
              item={item}
              tokens={plugins.library?.tokens ?? []}
              busy={busy || plugins.busy}
              onSaveSource={(input) => plugins.edit(item.id, input)}
              onSavePolicy={(policy) =>
                act(async () => {
                  await api.setPluginPolicy(item.id, policy)
                  return `已保存 ${item.name} 的更新策略`
                })
              }
            />
          )}

          {tab === 'deps' && (
            <DependencyTab declared={declared} published={published} latest={item.latest} />
          )}
        </div>
      </aside>
    </div>,
    document.body,
  )
}

function TabButton({
  id,
  tab,
  onPick,
  children,
}: {
  id: Tab
  tab: Tab
  onPick: (tab: Tab) => void
  children: React.ReactNode
}) {
  return (
    <button
      role="tab"
      className={`drawer__tab${tab === id ? ' drawer__tab--on' : ''}`}
      aria-selected={tab === id}
      onClick={() => onPick(id)}
    >
      {children}
    </button>
  )
}

// -------------------------------------------------------------- overview

/**
 * Four facts and a matrix.
 *
 * The matrix is the reason this tab exists. Every other view in the panel
 * shows one server's plugins or one plugin's versions; this is the only place
 * that shows one plugin across every server at once, which is where "生存服 is
 * two versions behind 创造服" becomes a thing you can see rather than a thing
 * you work out.
 *
 * A row per instance, and the columns are the questions in the order they get
 * asked: which server, is it switched on, which release and which jar of it,
 * does the file still match the record, and what can be done about it.
 */
function OverviewTab({
  row,
  item,
  instances,
  busy,
  onOpenInstance,
  onUpgrade,
  onRollback,
  onReconcile,
  onRepush,
  onAccept,
}: {
  row: PluginOverviewRow
  item: LibraryPlugin
  instances: InstanceStatus[]
  busy: boolean
  onOpenInstance: (id: string) => void
  onUpgrade: (use: PluginUse) => void
  onRollback: (use: PluginUse, withConfig: boolean) => void
  onReconcile: (use: PluginUse) => void
  onRepush: (use: PluginUse) => void
  onAccept: (use: PluginUse) => void
}) {
  const held = item.versions.reduce((sum, version) => sum + versionSize(version), 0)

  return (
    <>
      <section className="drawer__section">
        <div className="statgrid">
          <Stat label="库内最新" value={row.newest ?? '未下载'} note={`${row.versions} 个版本 · ${formatBytes(held)}`} />
          <Stat
            label="上游最新"
            value={row.upstream ?? (row.kind === 'local' ? '无上游' : '未检查')}
            note={item.checkedAt && !item.checkedAt.startsWith('0001-') ? `检查于 ${formatDate(item.checkedAt)}` : '从未检查'}
            warn={row.status === 'update'}
          />
          <Stat
            label="部署"
            value={row.used.length === 0 ? '没有实例在用' : `${row.used.length} 台`}
            note={row.used.length > 0 ? row.used.map((use) => use.name).join('、') : '库里留着，没人装'}
          />
          <Stat
            label="对账"
            value={reconSummary(row)}
            note={lastChecked(row)}
            warn={row.status === 'drift' || row.status === 'missing'}
          />
        </div>
      </section>

      <section className="drawer__section">
        <h3>部署矩阵</h3>
        {row.used.length === 0 ? (
          <p className="muted">
            这个插件在库里，但没有任何实例装着它。
            {instances.length > 0 && '「装到实例…」在列表行的操作菜单里。'}
          </p>
        ) : (
          <div className="matrix" role="table">
            <div className="matrix__head" role="row">
              <span role="columnheader">实例</span>
              <span role="columnheader">版本 · 构件</span>
              <span role="columnheader">哈希</span>
              <span role="columnheader" />
            </div>
            {row.used.map((use) => (
              <MatrixRow
                key={use.instanceId}
                use={use}
                busy={busy}
                onOpen={() => onOpenInstance(use.instanceId)}
                onUpgrade={() => onUpgrade(use)}
                onRollback={(withConfig) => onRollback(use, withConfig)}
                onReconcile={() => onReconcile(use)}
                onRepush={() => onRepush(use)}
                onAccept={() => onAccept(use)}
              />
            ))}
          </div>
        )}
        <p className="chart-note">
          升级是一次事务：备份旧 jar 和配置目录，删掉目录里所有声明同一插件名的 jar，再放新的进去。
          回滚读的是那份快照，不是库里还留着旧版本 —— 所以保留策略清掉旧版本也不影响回滚。
        </p>
      </section>
    </>
  )
}

function MatrixRow({
  use,
  busy,
  onOpen,
  onUpgrade,
  onRollback,
  onReconcile,
  onRepush,
  onAccept,
}: {
  use: PluginUse
  busy: boolean
  onOpen: () => void
  onUpgrade: () => void
  onRollback: (withConfig: boolean) => void
  onReconcile: () => void
  onRepush: () => void
  onAccept: () => void
}) {
  const trouble = use.recon && use.recon !== 'ok' ? (use.recon as PluginStatus) : null

  return (
    <div className={`matrix__row${trouble ? ' matrix__row--bad' : ''}`} role="row">
      <span className="matrix__inst">
        <button className="link" onClick={onOpen}>
          <span className={`status__dot status__dot--${use.state}`} />
          {use.name}
        </button>
        {use.present && !use.enabled && (
          <span className="badge badge--muted" title="jar 被改名成 .disabled，服务端不会加载">
            已停用
          </span>
        )}
      </span>

      <span className="matrix__ver">
        <span className="ptable__num">{use.version}</span>
        {use.update && <span className="matrix__arrow">→ {use.update.version}</span>}
        {/* Ellipsised the moment a row has an upgrade to offer as well, so the
            whole name has to be reachable somewhere. */}
        {use.fileName && (
          <code className="matrix__file" title={use.fileName}>
            {use.fileName}
          </code>
        )}
      </span>

      <span className="matrix__recon">
        {trouble ? (
          <span className={`pstate pstate--${trouble}`}>
            <span className={`pdot pdot--${trouble}`} aria-hidden="true" />
            {statusLabel(trouble)}
          </span>
        ) : use.checkedAt ? (
          <span className="pstate pstate--ok">
            <span className="pdot pdot--ok" aria-hidden="true" />
            一致
          </span>
        ) : (
          <span className="muted" title="这台还没对过账，所以「一致」是没有依据的">
            未对账
          </span>
        )}
      </span>

      {/* What a finding offers, and the two answers are genuinely different
          decisions rather than a confirm and a cancel. A missing file wants
          putting back. A drifted one wants either the library's copy restored
          — the file was tampered with — or the file on disk adopted as the new
          truth, which is what a plugin that legitimately updated itself needs
          and the only way to clear that finding without overwriting a jar
          somebody wanted. */}
      <span className="matrix__act">
        {trouble === 'missing' && (
          <button className="btn btn--small btn--primary" disabled={busy} onClick={onRepush}>
            重新推送
          </button>
        )}
        {trouble === 'drift' && (
          <>
            <button className="btn btn--small" disabled={busy} onClick={onAccept}>
              以文件为准
            </button>
            <button className="btn btn--small btn--primary" disabled={busy} onClick={onRepush}>
              恢复库内版本
            </button>
          </>
        )}
        {/* Deliberately not a third button beside those two. Re-checking is
            what you do after fixing something by hand, and it lives where that
            happens — this server's own plugin page, and the library footer. */}
        {trouble && (
          <button className="link matrix__recheck" disabled={busy} onClick={onReconcile}>
            重新对账
          </button>
        )}
        {use.update && (
          <button className="btn btn--small btn--primary" disabled={busy} onClick={onUpgrade}>
            升到 {use.update.version}
          </button>
        )}
        {use.rollbackTo && (
          <button
            className="btn btn--small"
            disabled={busy}
            title={
              use.configSaved
                ? `回滚到 ${use.rollbackTo}。按住 Shift 点击会连配置目录一起还原。`
                : `回滚到 ${use.rollbackTo}。${use.rollbackNote || '这次快照没有备份配置目录，配置保持现状。'}`
            }
            onClick={(event) => onRollback(event.shiftKey && (use.configSaved ?? false))}
          >
            回滚 {use.rollbackTo}
          </button>
        )}
      </span>
    </div>
  )
}

function Stat({
  label,
  value,
  note,
  warn,
}: {
  label: string
  value: string
  note?: string
  warn?: boolean
}) {
  return (
    <div className={`statgrid__cell${warn ? ' statgrid__cell--warn' : ''}`}>
      <span className="statgrid__label">{label}</span>
      <strong className="statgrid__value">{value}</strong>
      {note && <small className="statgrid__note">{note}</small>}
    </div>
  )
}

function reconSummary(row: PluginOverviewRow): string {
  const bad = row.used.filter((use) => use.recon && use.recon !== 'ok')
  if (bad.length > 0) return statusLabel(bad[0].recon as PluginStatus)
  if (row.used.length === 0) return '无需对账'
  return row.used.some((use) => use.checkedAt) ? '一致' : '未对账'
}

function lastChecked(row: PluginOverviewRow): string {
  const stamps = row.used.map((use) => use.checkedAt).filter(Boolean) as string[]
  if (stamps.length === 0) return '还没有任何一台对过账'
  const oldest = stamps.sort()[0]
  return `最早一台对于 ${formatDate(oldest)}`
}

// -------------------------------------------------------------- versions

/**
 * Releases, with the jars folded inside them.
 *
 * The nesting is the correction. A release is what upstream published and the
 * jars under it are how they packaged it, and flattening the two is what made
 * LuckPerms look like it had shipped twice as many versions as it had. So the
 * outer row is the release — one number, one date, one changelog — and opening
 * it shows the jars, each with the digest that identifies it and the name its
 * own plugin.yml declares, which is the pair everything else in here keys on.
 *
 * Upstream's full list is fetched on demand. It costs an API call, the
 * anonymous quota is sixty an hour, and a page that spent one on every visit
 * would be a page that stops working on the twentieth.
 */
function VersionsTab({
  item,
  row,
  busy,
  onDownload,
  onDropArtifact,
}: {
  item: LibraryPlugin
  row: PluginOverviewRow
  busy: boolean
  onDownload: (tag: string, asset?: string) => void
  onDropArtifact: (version: PluginVersion, artifact: PluginArtifact) => void
}) {
  const [releases, setReleases] = useState<PluginRelease[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [fetchError, setFetchError] = useState<string | null>(null)
  const [open, setOpen] = useState<string | null>(item.versions[0]?.tag ?? null)

  const load = async () => {
    if (loading) return
    setLoading(true)
    setFetchError(null)
    try {
      setReleases(await api.pluginReleases(item.id))
    } catch (err) {
      setFetchError(err instanceof Error ? err.message : '获取版本列表失败')
    } finally {
      setLoading(false)
    }
  }

  const rows = mergeVersions(item, releases)

  return (
    <section className="drawer__section">
      <div className="chart-head">
        <h3>
          版本
          <span className="muted">
            {releases ? ` 上游 ${rows.length} 个 · 库里 ${item.versions.length} 个` : ` 库里 ${item.versions.length} 个`}
          </span>
        </h3>
        <div className="chart-head__actions">
          <button
            className="btn btn--icon"
            disabled={busy || loading || item.source.kind === 'local'}
            title={
              item.source.kind === 'local'
                ? '手动导入的 jar 没有上游可列'
                : '把上游还发布过哪些版本一起列出来。要问一次上游 —— 匿名调用 GitHub 一小时只有 60 次配额，所以是点出来的。'
            }
            onClick={() => void load()}
          >
            {loading ? '…' : '⟳'}
          </button>
        </div>
      </div>

      {fetchError && <div className="alert alert--error">{fetchError}</div>}
      {!releases && !fetchError && item.source.kind !== 'local' && (
        <p className="chart-note">
          现在列的只是库里已经下载的。点 ⟳ 把上游发布过的版本一起列出来。
        </p>
      )}

      <div className="vlist">
        {rows.map((entry) => (
          <VersionGroup
            key={entry.tag}
            entry={entry}
            open={open === entry.tag}
            pinned={row.pinned === entry.tag}
            busy={busy}
            onToggle={() => setOpen(open === entry.tag ? null : entry.tag)}
            onDownload={(asset) => onDownload(entry.tag, asset)}
            onDropArtifact={(artifact) => entry.held && onDropArtifact(entry.held, artifact)}
          />
        ))}
        {rows.length === 0 && <p className="muted">还没下载过任何版本，也还没列过上游有哪些。</p>}
      </div>
    </section>
  )
}

/** One release, held or merely published. */
interface VersionEntry {
  tag: string
  version: string
  prerelease: boolean
  publishedAt: string
  notes?: string
  held?: PluginVersion
  release?: PluginRelease
}

function mergeVersions(item: LibraryPlugin, releases: PluginRelease[] | null): VersionEntry[] {
  const byTag = new Map<string, VersionEntry>()
  for (const version of item.versions) {
    byTag.set(version.tag, {
      tag: version.tag,
      version: version.version,
      prerelease: version.prerelease,
      publishedAt: version.publishedAt,
      notes: version.notes,
      held: version,
    })
  }
  for (const release of releases ?? []) {
    const existing = byTag.get(release.tag)
    if (existing) {
      existing.release = release
      existing.notes = existing.notes || release.notes
      continue
    }
    byTag.set(release.tag, {
      tag: release.tag,
      version: release.version,
      prerelease: release.prerelease,
      publishedAt: release.publishedAt,
      notes: release.notes,
      release,
    })
  }
  return Array.from(byTag.values()).sort(
    (a, b) => new Date(b.publishedAt).getTime() - new Date(a.publishedAt).getTime(),
  )
}

function VersionGroup({
  entry,
  open,
  pinned,
  busy,
  onToggle,
  onDownload,
  onDropArtifact,
}: {
  entry: VersionEntry
  open: boolean
  pinned: boolean
  busy: boolean
  onToggle: () => void
  onDownload: (asset?: string) => void
  onDropArtifact: (artifact: PluginArtifact) => void
}) {
  const artifacts = entry.held ? pluginArtifacts(entry.held) : []
  const size = entry.held ? versionSize(entry.held) : (entry.release?.asset.size ?? 0)

  // What upstream published under this release that the library does not hold.
  //
  // This is where "one release, several jars" stops being a statement and
  // becomes something to act on: a plugin that ships a paper build and a
  // velocity build has two rows here, and a fleet with a proxy in it needs
  // both. Matched by file name because that is what the library files them
  // under — the digest is not known until the bytes have arrived.
  //
  // Only the builds whose platform the source named. A GitHub release's assets
  // are a pile of files nobody has labelled — the jar, the sources jar, the
  // javadoc jar — and offering to download each of them by name would be the
  // panel presenting its own ignorance as a choice. Those keep the one button
  // they had, which fetches whatever the asset pattern picked.
  const held = new Set(artifacts.map((artifact) => artifact.fileName.toLowerCase()))
  const offered = (entry.release?.assets ?? [])
    .filter((asset) => asset.platform)
    .filter((asset) => !held.has(asset.name.toLowerCase()))

  return (
    <div className={`vgroup${entry.held ? ' vgroup--held' : ''}`}>
      <button className="vgroup__head" onClick={onToggle} aria-expanded={open}>
        <span className="vgroup__caret" aria-hidden="true">
          {open ? '▾' : '▸'}
        </span>
        <span className="ptable__num vgroup__ver">{entry.version}</span>
        {entry.prerelease && <span className="badge badge--warn">预发布</span>}
        {pinned && <span className="badge">已锁定</span>}
        {entry.held ? (
          <span className="badge badge--ok">
            库里已有{artifacts.length > 1 && ` · ${artifacts.length} 个 jar`}
          </span>
        ) : (
          <span className="badge badge--muted">未下载</span>
        )}
        <span className="vgroup__meta">
          {formatDate(entry.publishedAt)}
          {size > 0 && ` · ${formatBytes(size)}`}
        </span>
      </button>

      {open && (
        <div className="vgroup__body">
          {artifacts.length > 0 ? (
            <div className="alist">
              {artifacts.map((artifact) => (
                <ArtifactRow
                  key={artifact.sha256 || artifact.fileName}
                  artifact={artifact}
                  removable={artifacts.length > 1 || true}
                  busy={busy}
                  onDrop={() => {
                    void ask({
                      title: '从库里删掉这个 jar？',
                      lead: `${artifact.fileName}（${formatBytes(artifact.size)}）`,
                      detail: '已经装到实例里的副本不受影响，回滚快照也不受影响。',
                      confirmLabel: '删除',
                      danger: true,
                    }).then((ok) => {
                      if (ok) onDropArtifact(artifact)
                    })
                  }}
                />
              ))}
            </div>
          ) : (
            <p className="muted">这个版本还没下载到库里。</p>
          )}

          {offered.length > 0 ? (
            <div className="offers">
              {offered.map((asset) => (
                <div className="offers__row" key={asset.name}>
                  {asset.platform ? (
                    <span className="badge">{loaderLabel(asset.platform)}</span>
                  ) : (
                    <span className="badge badge--muted">未标平台</span>
                  )}
                  <code className="offers__file" title={asset.name}>
                    {asset.name}
                  </code>
                  {asset.size > 0 && (
                    <span className="offers__size">{formatBytes(asset.size)}</span>
                  )}
                  <button
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => onDownload(asset.name)}
                  >
                    下载到库
                  </button>
                </div>
              ))}
            </div>
          ) : (
            !entry.held && (
              <button className="btn btn--small" disabled={busy} onClick={() => onDownload()}>
                下载到库
              </button>
            )
          )}

          {entry.notes?.trim() && (
            <details className="vgroup__notes">
              <summary>发布说明</summary>
              <pre>{entry.notes.slice(0, 4000)}</pre>
            </details>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * One jar.
 *
 * The digest comes first because it is the identity — everything else on this
 * line is description. Twelve hex characters is enough to compare against a
 * reconciliation finding by eye and short enough not to swamp the row; the
 * full value is in the title for anyone checking properly.
 *
 * The declared name is printed even when it matches the plugin's own name,
 * because the case that matters is when it does not: a jar whose plugin.yml
 * says something else is a jar the server will file under a different name and
 * put its config somewhere else.
 */
function ArtifactRow({
  artifact,
  removable,
  busy,
  onDrop,
}: {
  artifact: PluginArtifact
  removable: boolean
  busy: boolean
  onDrop: () => void
}) {
  return (
    <div className="arow">
      <code className="arow__sha" title={artifact.sha256 || '这份记录里没有校验和'}>
        {artifact.sha256 ? artifact.sha256.slice(0, 12) : '无校验和'}
      </code>
      <span className="arow__body">
        <span className="arow__file" title={artifact.fileName}>
          {artifact.fileName}
        </span>
        <span className="arow__declared">
          {artifact.pluginName ? (
            <>
              plugin.yml：<b>{artifact.pluginName}</b>
              {artifact.pluginVer && ` ${artifact.pluginVer}`}
            </>
          ) : (
            <span className="muted">读不出这个 jar 的描述符</span>
          )}
          {artifact.apiVersion && ` · api-version ${artifact.apiVersion}`}
        </span>
      </span>
      <span className="arow__tags">
        {artifact.platform && <span className="badge">{loaderLabel(artifact.platform)}</span>}
        {artifact.loaders
          ?.filter((loader) => loader !== artifact.platform)
          .map((loader) => (
            <span className="badge" key={loader}>
              {loaderLabel(loader)}
            </span>
          ))}
        <span className="arow__size">{formatBytes(artifact.size)}</span>
      </span>
      {removable && (
        <button className="link link--danger" disabled={busy} onClick={onDrop}>
          删除
        </button>
      )}
    </div>
  )
}

// -------------------------------------------------------------- settings

const MODES: { id: PluginUpdateMode; label: string; note: string }[] = [
  { id: '', label: '手动', note: '什么都不做，等你来点。' },
  { id: 'notify', label: '仅提示', note: '定期检查，有新版本就在列表上标出来。' },
  { id: 'fetch', label: '自动入库', note: '新版本自动下载到库里。实例上什么都不动 —— 你决定时它已经在了。' },
  {
    id: 'push',
    label: '自动推送实例',
    note: '还会复制到每台装了它的实例，下次重启生效。唯一会动到正在跑的服的一档。',
  },
]

function SettingsTab({
  item,
  tokens,
  busy,
  onSaveSource,
  onSavePolicy,
}: {
  item: LibraryPlugin
  tokens: PluginTokenInfo[]
  busy: boolean
  onSaveSource: (input: {
    name: string
    repo: string
    assetPattern?: string
    prerelease?: boolean
    private?: boolean
    tokenId?: string
    targetDir?: string
    note?: string
  }) => Promise<boolean>
  onSavePolicy: (policy: PluginPolicy) => Promise<void>
}) {
  const [targetDir, setTargetDir] = useState(item.targetDir)
  const [assetPattern, setAssetPattern] = useState(item.source.assetPattern ?? '')
  const [prerelease, setPrerelease] = useState(item.source.prerelease ?? false)
  const [tokenId, setTokenId] = useState(item.source.tokenId ?? '')
  const [update, setUpdate] = useState<PluginUpdateMode>(item.policy?.update ?? '')
  const [pin, setPin] = useState(item.policy?.pin ?? '')
  const [keep, setKeep] = useState(String(item.policy?.keep ?? 0))
  const [selfUpdate, setSelfUpdate] = useState(item.policy?.allowSelfUpdate ?? false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    setTargetDir(item.targetDir)
    setAssetPattern(item.source.assetPattern ?? '')
    setPrerelease(item.source.prerelease ?? false)
    setTokenId(item.source.tokenId ?? '')
    setUpdate(item.policy?.update ?? '')
    setPin(item.policy?.pin ?? '')
    setKeep(String(item.policy?.keep ?? 0))
    setSelfUpdate(item.policy?.allowSelfUpdate ?? false)
  }, [item])

  const sourceDirty =
    targetDir !== item.targetDir ||
    assetPattern !== (item.source.assetPattern ?? '') ||
    prerelease !== (item.source.prerelease ?? false) ||
    tokenId !== (item.source.tokenId ?? '')
  // A token the panel no longer holds: the plugin goes on naming it, and goes
  // on failing, until someone points it at another. Saying so is the only way
  // that gets noticed.
  const missingToken = tokenId !== '' && !tokens.some((token) => token.id === tokenId)
  const policyDirty =
    update !== (item.policy?.update ?? '') ||
    pin !== (item.policy?.pin ?? '') ||
    Number(keep || 0) !== (item.policy?.keep ?? 0) ||
    selfUpdate !== (item.policy?.allowSelfUpdate ?? false)

  const save = async () => {
    setSaving(true)
    if (sourceDirty) {
      await onSaveSource({
        name: item.name,
        repo: item.source.repo,
        assetPattern,
        prerelease,
        private: item.source.private,
        tokenId,
        targetDir,
        note: item.note,
      })
    }
    if (policyDirty) {
      await onSavePolicy({ update, pin, keep: Number(keep || 0), allowSelfUpdate: selfUpdate })
    }
    setSaving(false)
  }

  return (
    <>
      <section className="drawer__section">
        <h3>从哪里来，落到哪里</h3>
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
              一次 Release 发好几个 jar 时用它挑，比如 <code>*-bukkit.jar</code>。
              留空就让面板自己选 —— 跳过 sources、javadoc 这类附带包，从剩下的里挑最大的。
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
          <small>关掉的话，标了 prerelease 的 Release 不会出现在版本列表里，也不算「有更新」。</small>
        </label>

        {/* Only for a GitHub source, and only when there is a choice to make:
            the插件站 are public catalogues with nothing to authenticate to, and
            a picker with one option is a question with one answer. A source
            whose token was deleted still shows it — that is what has to be
            fixed, so hiding it would hide the fix. */}
        {item.source.kind === 'github' && (tokens.length > 1 || missingToken) && (
          <label className="field">
            <span>用哪个令牌读</span>
            <select
              value={tokenId}
              disabled={busy || saving}
              onChange={(event) => setTokenId(event.target.value)}
            >
              <option value="">默认{tokens.length > 0 ? `（${tokens[0].name}）` : ''}</option>
              {tokens.map((token) => (
                <option key={token.id} value={token.id}>
                  {token.name}
                  {token.hint ? ` ···${token.hint}` : ''}
                </option>
              ))}
              {missingToken && <option value={tokenId}>已删除的令牌</option>}
            </select>
            <small>
              {missingToken ? (
                <>这个插件指定的令牌已经不在了，检查更新和下载都会失败 —— 挑一个现有的。</>
              ) : (
                <>私有仓库只有能看见它的那个账号的令牌读得到。公开仓库随便挑，令牌只起提额度的作用。</>
              )}
            </small>
          </label>
        )}
      </section>

      <section className="drawer__section">
        <h3>更新策略</h3>
        <div className="modes">
          {MODES.map((mode) => (
            <label
              className={`modes__row${update === mode.id ? ' modes__row--on' : ''}`}
              key={mode.id || 'manual'}
            >
              <input
                type="radio"
                name="update-mode"
                checked={update === mode.id}
                disabled={busy || saving}
                onChange={() => setUpdate(mode.id)}
              />
              <span className="modes__body">
                <span>{mode.label}</span>
                <small>{mode.note}</small>
              </span>
            </label>
          ))}
        </div>

        <div className="field-row">
          <label className="field">
            <span>版本锁定</span>
            <select
              value={pin}
              disabled={busy || saving}
              onChange={(event) => setPin(event.target.value)}
            >
              <option value="">不锁定</option>
              {item.versions.map((version) => (
                <option value={version.tag} key={version.tag}>
                  {version.version}
                </option>
              ))}
            </select>
            <small>
              锁上之后这个插件不再报「有更新」，批量升级也会跳过它 ——
              给「5.4.2 是最后一个能配我们那套改动的版本」用的。
            </small>
          </label>

          <label className="field">
            <span>保留版本数</span>
            <input
              type="number"
              min={0}
              value={keep}
              disabled={busy || saving}
              onChange={(event) => setKeep(event.target.value)}
            />
            <small>
              库里留几个旧版本，0 是全留。任何实例在用的版本永远不会被清掉；
              回滚读的是升级快照而不是库，所以清理旧版本也不影响回滚。
            </small>
          </label>
        </div>

        <label className="checkbox checkbox--stacked">
          <input
            type="checkbox"
            checked={selfUpdate}
            disabled={busy || saving}
            onChange={(event) => setSelfUpdate(event.target.checked)}
          />
          <span>允许自更新</span>
          <small>
            有些插件真的会自己改写 jar —— 部分反作弊会拉签名库，某些 Geyser 构建也会。
            打开之后这个插件的哈希漂移只记录不告警。别的插件出现漂移仍然会报出来。
          </small>
        </label>
      </section>

      {(sourceDirty || policyDirty) && (
        <div className="drawer__save">
          <button
            className="btn"
            disabled={saving}
            onClick={() => {
              setTargetDir(item.targetDir)
              setAssetPattern(item.source.assetPattern ?? '')
              setPrerelease(item.source.prerelease ?? false)
              setUpdate(item.policy?.update ?? '')
              setPin(item.policy?.pin ?? '')
              setKeep(String(item.policy?.keep ?? 0))
              setSelfUpdate(item.policy?.allowSelfUpdate ?? false)
            }}
          >
            撤销
          </button>
          <button className="btn btn--primary" disabled={saving} onClick={() => void save()}>
            {saving ? '保存中…' : '保存'}
          </button>
        </div>
      )}
    </>
  )
}

// ------------------------------------------------------------------ deps

interface Declared {
  depend: string[]
  softDepend: string[]
}

function declaredDeps(item: LibraryPlugin | null): Declared {
  const depend = new Set<string>()
  const softDepend = new Set<string>()
  for (const version of item?.versions ?? []) {
    for (const artifact of version.artifacts ?? []) {
      artifact.depend?.forEach((name) => depend.add(name))
      artifact.softDepend?.forEach((name) => softDepend.add(name))
    }
  }
  // Something both required and optional across versions is required: the
  // stricter of two claims is the one that decides whether a server boots.
  for (const name of depend) softDepend.delete(name)
  return { depend: Array.from(depend), softDepend: Array.from(softDepend) }
}

/**
 * What this plugin needs, from two sources that are not the same claim.
 *
 * The registry's list is what the author wrote on the listing page. The jar's
 * `depend` block is what the server will actually refuse to enable the plugin
 * over. They usually agree; when they do not, the second one is the one that
 * decides whether tonight's restart comes back up, so they are shown side by
 * side and neither is presented as the other.
 */
function DependencyTab({
  declared,
  published,
  latest,
}: {
  declared: Declared
  published: { name: string; required: boolean; url?: string }[]
  latest?: PluginRelease
}) {
  return (
    <>
      {(declared.depend.length > 0 || declared.softDepend.length > 0) && (
        <section className="drawer__section">
          <h3>jar 里声明的</h3>
          <p className="chart-note">
            从库里每个 jar 的 plugin.yml 解出来的。<code>depend</code> 装不上，服务端启动时这个插件直接
            加载失败；<code>softdepend</code> 只影响加载顺序。
          </p>
          <ul className="drawer__deps">
            {declared.depend.map((name) => (
              <li key={name}>
                <span>{name}</span>
                <span className="badge badge--warn">必需</span>
              </li>
            ))}
            {declared.softDepend.map((name) => (
              <li key={name}>
                <span>{name}</span>
                <span className="badge badge--muted">可选</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {published.length > 0 && (
        <section className="drawer__section">
          <h3>来源页面上写的</h3>
          <p className="chart-note">
            {latest?.version ? `${latest.version} 的元数据。` : ''}
            这是作者在发布页上填的，和 jar 里的声明可能对不上 —— 真出问题时以上面那份为准。
          </p>
          <ul className="drawer__deps">
            {published.map((dep) => (
              <li key={dep.name}>
                {dep.url ? (
                  <a href={dep.url} target="_blank" rel="noreferrer">
                    {dep.name}
                  </a>
                ) : (
                  <span>{dep.name}</span>
                )}
                <span className={`badge ${dep.required ? 'badge--warn' : 'badge--muted'}`}>
                  {dep.required ? '必需' : '可选'}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  )
}
