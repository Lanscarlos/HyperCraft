import { useLayoutEffect, useRef } from 'react'
import type { MouseEvent, ReactNode, Ref } from 'react'

import { DUR } from '../motion'
import type { LibrarySection, Route, Scope } from '../routes'
import {
  HOST_SECTIONS,
  INSTANCE_SECTIONS,
  LIBRARY_SECTIONS,
  LIBRARY_VIEWS,
  SETTINGS_SECTIONS,
  defaultView,
  pathOf,
  samePage,
} from '../routes'
import { captureScope, playScope } from '../scopeMorph'
import type { InstanceStatus, SystemInfo, User } from '../types'
import { STATE_LABELS, isLive } from '../types'
import type { CoreController } from '../useCores'
import type { DatabaseController } from '../useDatabases'
import type { JavaController } from '../useJava'
import type { PluginController } from '../usePlugins'
import type { TerminalController } from '../useTerminal'
import { Icon } from './Icon'
import type { IconName } from './Icon'

interface Props {
  route: Route
  scope: Scope
  /** True while the sidebar is a drawer; the fold is a desktop-only idea. */
  compact: boolean
  railed: boolean
  onToggleRail: () => void
  navigate: (route: Route) => void
  /** A plain left click on a sidebar link; every other click stays the
   *  browser's, because these are real hrefs. */
  follow: (go: () => void) => (event: MouseEvent<HTMLAnchorElement>) => void
  instances: InstanceStatus[]
  /** Instance ids, most recently opened first. */
  recents: string[]
  user: User
  system: SystemInfo | null
  updateNotice: string | null
  alertCount: number
  java: JavaController
  databases: DatabaseController
  cores: CoreController
  plugins: PluginController
  terminal: TerminalController
  onCreate: () => void
  onOpenPalette: () => void
  sidebarRef: Ref<HTMLElement>
}

/**
 * The panel's navigation, in whichever of its forms applies.
 *
 * There are several kinds of thing to manage here and they do not belong in one
 * flat list: the panel, one server, one shelf of the shared library, the machine
 * underneath, and the panel's own settings. Their lifetimes differ by orders
 * of magnitude — you visit the host page monthly and the console hourly — so
 * entering an inner one *replaces* this list rather than expanding an item
 * inside it. Nesting them would put the console three levels deep, and a
 * console three levels deep is a console nobody uses.
 *
 * What an inner scope owes the reader in exchange is a way out and a way
 * across: every one of them opens with 返回上级 and the name of the thing you
 * are inside. And it does not simply appear — the row you clicked flies up to
 * become that header while the list it belonged to clears out of the way, so
 * the replacement is something you watched happen rather than something you
 * have to re-read the sidebar to understand. See scopeMorph.ts.
 */
export function Sidebar(props: Props) {
  const { scope, sidebarRef, compact, railed, onToggleRail } = props
  const self = useRef<HTMLElement | null>(null)
  const previous = useRef<Scope>(scope)

  // Entering or leaving a scope is the one navigation in the panel that
  // replaces the whole sidebar, so it is the one that has to be shown rather
  // than simply performed — see scopeMorph.ts, which has already taken a copy
  // of the outgoing list by the time this runs. `useLayoutEffect`, not
  // `useEffect`: the incoming header has to be measured and moved back to
  // where the row was in the same frame it is committed, or the first frame
  // paints it at its destination and the movement starts from the wrong place.
  useLayoutEffect(() => {
    const el = self.current
    if (previous.current === scope || !el) return
    previous.current = scope
    playScope(el)
    el.dataset.entering = ''
    const timer = window.setTimeout(() => delete el.dataset.entering, DUR.slow)
    return () => window.clearTimeout(timer)
  }, [scope])

  return (
    <aside
      className="sidebar"
      id="sidebar"
      ref={(node) => {
        self.current = node
        if (typeof sidebarRef === 'function') sidebarRef(node)
        else if (sidebarRef) (sidebarRef as { current: HTMLElement | null }).current = node
      }}
      tabIndex={-1}
      data-scope={scope}
    >
      {scope === 'instance' ? (
        <InstanceScope {...props} />
      ) : scope === 'library' ? (
        <LibraryScope {...props} />
      ) : scope === 'host' ? (
        <HostScope {...props} />
      ) : scope === 'settings' ? (
        <SettingsScope {...props} />
      ) : (
        <GlobalScope {...props} />
      )}

      {/* The fold, at the foot of the column it folds.
          It used to be the leftmost button in the top bar — a chevron pointing
          left, in the corner every browser and every phone puts 返回 in — so
          the control that narrows the navigation was sitting exactly where a
          reader expects the control that leaves the page. Moved down here it is
          attached to the thing it acts on, and that corner is free for the back
          button people were already trying to press. */}
      {!compact && (
        <button
          className="sidebar__fold"
          onClick={onToggleRail}
          title={railed ? '展开侧边栏（[）' : '收起侧边栏（[）'}
          aria-label={railed ? '展开侧边栏' : '收起侧边栏'}
          aria-expanded={!railed}
          aria-controls="sidebar"
        >
          <Icon name={railed ? 'expand' : 'collapse'} />
          <span className="sidebar__name">收起侧边栏</span>
          <kbd className="sidebar__kbd">[</kbd>
        </button>
      )}
    </aside>
  )
}

// ------------------------------------------------------------------ global

function GlobalScope(props: Props) {
  const {
    route,
    follow,
    navigate,
    instances,
    recents,
    user,
    updateNotice,
    alertCount,
    java,
    databases,
    cores,
    plugins,
    onCreate,
  } = props

  const running = instances.filter((item) => isLive(item.state)).length
  // Recently opened first, then whatever is on fire — a crashed server the
  // operator has not been in yet still has to be one click away.
  const shortlist = pickShortlist(instances, recents)

  return (
    <>
      <a
        className="sidebar__brand"
        href={pathOf({ kind: 'overview' })}
        onClick={follow(() => navigate({ kind: 'overview' }))}
      >
        <span className="sidebar__logo">⛏</span>
        <div className="sidebar__title">
          <strong>HyperCraft</strong>
          <small>
            {user.version}
            {updateNotice && <span className="badge badge--update">{updateNotice}</span>}
          </small>
        </div>
      </a>

      <SearchButton onOpen={props.onOpenPalette} />

      <div className="sidebar__scroll">
        <nav className="sidebar__nav" aria-label="面板导航">
          <NavLink
            {...props}
            icon="dashboard"
            label="概览"
            target={{ kind: 'overview' }}
            badge={alertCount > 0 ? <span className="badge badge--alert">{alertCount}</span> : null}
          />
        </nav>

        <Group label="实例" count={instances.length > 0 ? `${running}/${instances.length}` : null} />
        <nav className="sidebar__nav" aria-label="实例导航">
          <NavLink
            {...props}
            icon="instances"
            label="所有实例"
            target={{ kind: 'instances', query: '', state: 'all' }}
            active={route.kind === 'instances'}
          />
          {shortlist.map((item) => (
            <a
              key={item.id}
              className="sidebar__link sidebar__link--instance"
              // Half of a pair: the scope header this row becomes carries the
              // same key, which is how the two are matched up and animated
              // into one another.
              data-nav-key={`instance:${item.id}`}
              href={pathOf({ kind: 'instance', id: item.id, section: 'console' })}
              onClick={follow(() => {
                captureScope(`instance:${item.id}`)
                navigate({ kind: 'instance', id: item.id, section: 'console' })
              })}
              title={`${item.name} · ${STATE_LABELS[item.state]}`}
            >
              <span className="sidebar__initial" aria-hidden="true">
                {item.name.slice(0, 1)}
              </span>
              <span className={`status__dot status__dot--${item.state}`} />
              <span className="sidebar__name">{item.name}</span>
              <span className="sidebar__state">{STATE_LABELS[item.state]}</span>
            </a>
          ))}
          {instances.length === 0 && <p className="sidebar__empty">还没有实例，先新建一个吧。</p>}
        </nav>

        {/* Two whole groups rather than six loose entries: a non-admin sub-user
            gets neither, and hiding a group is one check instead of six.

            The three read in the order a server actually gets built: Java runs
            the core, the core loads the plugins. Someone setting up for the
            first time can work straight down the group. */}
        <Group label="资源库" />
        <nav className="sidebar__nav" aria-label="资源库导航">
          <NavLink
            {...props}
            icon="java"
            label="Java 环境"
            target={{ kind: 'library', section: 'java', view: 'installed' }}
            navKey="library:java"
            badge={java.installing ? <span className="badge badge--update">安装中</span> : null}
          />
          <NavLink
            {...props}
            icon="cores"
            label="服务端核心"
            target={{ kind: 'library', section: 'cores', view: 'stock' }}
            navKey="library:cores"
            badge={cores.downloading ? <span className="badge badge--update">下载中</span> : null}
          />
          <NavLink
            {...props}
            icon="database"
            label="数据库环境"
            target={{ kind: 'library', section: 'database', view: 'databases' }}
            navKey="library:database"
            badge={
              databases.installing ? (
                <span className="badge badge--update">安装中</span>
              ) : null
            }
          />
          <NavLink
            {...props}
            icon="plugins"
            label="插件库"
            target={{ kind: 'library', section: 'plugins', view: 'list' }}
            navKey="library:plugins"
            badge={
              plugins.downloading ? (
                <span className="badge badge--update">下载中</span>
              ) : plugins.updates > 0 ? (
                <span className="badge badge--update">{plugins.updates}</span>
              ) : null
            }
          />
        </nav>

        <Group label="系统" />
        <nav className="sidebar__nav" aria-label="系统导航">
          <NavLink
            {...props}
            icon="host"
            label="主机"
            target={{ kind: 'host', section: 'metrics' }}
            navKey="host"
          />
          <NavLink
            {...props}
            icon="settings"
            label="面板设置"
            target={{ kind: 'settings', section: 'devices' }}
            active={route.kind === 'settings'}
            navKey="settings"
            badge={updateNotice ? <span className="badge badge--update">1</span> : null}
          />
        </nav>
      </div>

      <button className="btn btn--primary sidebar__new" onClick={onCreate} title="新建实例">
        <span aria-hidden="true">+</span>
        <span className="sidebar__name">新建实例</span>
      </button>
    </>
  )
}

/**
 * The 3–5 servers worth a permanent seat.
 *
 * Recently-opened first because that is what "take me back" means, then
 * anything crashed, then whatever is running. Never the whole list: with
 * thirty instances the navigation would be below the fold, and the entry you
 * want would not be the one on top anyway.
 */
function pickShortlist(instances: InstanceStatus[], recents: string[]): InstanceStatus[] {
  const byId = new Map(instances.map((item) => [item.id, item]))
  const picked: InstanceStatus[] = []
  const take = (item: InstanceStatus | undefined) => {
    if (!item || picked.length >= 5 || picked.includes(item)) return
    picked.push(item)
  }
  for (const id of recents) take(byId.get(id))
  for (const item of instances) if (item.state === 'crashed') take(item)
  for (const item of instances) if (isLive(item.state)) take(item)
  for (const item of instances) take(item)
  return picked
}

// ---------------------------------------------------------------- instance

function InstanceScope(props: Props) {
  const { route, instances, follow, navigate, plugins } = props
  const id = route.kind === 'instance' ? route.id : ''
  const instance = instances.find((item) => item.id === id) ?? null

  return (
    <>
      <ScopeHead
        {...props}
        backLabel="返回实例列表"
        backTo={{ kind: 'instances', query: '', state: 'all' }}
        switcherLabel="切换实例（⌘K）"
        navKey={`instance:${id}`}
        name={instance?.name ?? '实例'}
        meta={instance ? STATE_LABELS[instance.state] : '找不到这个实例'}
        dot={instance ? <span className={`status__dot status__dot--${instance.state}`} /> : null}
      />

      <div className="sidebar__scroll">
        <nav className="sidebar__nav" aria-label="实例页面">
          {INSTANCE_SECTIONS.map((section) => (
            <a
              key={section.id}
              className={`sidebar__link${
                route.kind === 'instance' && route.section === section.id
                  ? ' sidebar__link--active'
                  : ''
              }`}
              href={pathOf({ kind: 'instance', id, section: section.id })}
              onClick={follow(() => navigate({ kind: 'instance', id, section: section.id }))}
              title={section.label}
              aria-current={
                route.kind === 'instance' && route.section === section.id ? 'page' : undefined
              }
            >
              <Icon name={INSTANCE_ICONS[section.id]} />
              <span className="sidebar__name">{section.label}</span>
              {section.id === 'plugins' && plugins.updates > 0 && (
                <span className="badge badge--update">{plugins.updates}</span>
              )}
            </a>
          ))}
        </nav>
      </div>
    </>
  )
}

const INSTANCE_ICONS: Record<string, IconName> = {
  console: 'terminal',
  metrics: 'chart',
  files: 'files',
  plugins: 'plugins',
  properties: 'properties',
  settings: 'settings',
}

// ----------------------------------------------------------------- library

/**
 * One shelf of the shared library, with its pages as the whole navigation.
 *
 * These three were the panel's last second-level list: an indented strip that
 * unfolded under 服务端核心 while you were in it. The strip was not wrong about
 * the structure — the shelf, the catalogue and the download settings really are
 * pages *of* one entry — it was wrong about the depth it implied and about the
 * fact that it arrived out of nowhere. Everything else in here that has pages
 * inside it takes the column over and says so by moving; a strip that appears
 * under a row you clicked is a fourth thing the reader has to have learned.
 *
 * So a shelf is a scope now, on exactly the terms an instance is: the row flies
 * up and becomes this header, its pages come up underneath, and 返回上级 puts
 * the row back where it was.
 */
function LibraryScope(props: Props) {
  const { route, follow, navigate, java, cores, plugins } = props
  const section: LibrarySection = route.kind === 'library' ? route.section : 'cores'
  const view = route.kind === 'library' ? route.view : defaultView(section)
  const entry = LIBRARY_SECTIONS.find((item) => item.id === section)

  return (
    <>
      <ScopeHead
        {...props}
        backLabel="返回面板"
        backTo={{ kind: 'overview' }}
        switcherLabel={undefined}
        navKey={`library:${section}`}
        name={entry?.label ?? '资源库'}
        meta={libraryMeta(props, section)}
        dot={<Icon name={LIBRARY_ICONS[section]} />}
      />

      <div className="sidebar__scroll">
        <nav className="sidebar__nav" aria-label={`${entry?.label ?? '资源库'}页面`}>
          {LIBRARY_VIEWS[section].map((page) => {
            const current = view === page.id
            const badge =
              section === 'cores' && page.id === 'download' && cores.downloading ? (
                <span className="badge badge--update">下载中</span>
              ) : section === 'java' && page.id === 'install' && java.installing ? (
                <span className="badge badge--update">安装中</span>
              ) : section === 'plugins' && page.id === 'list' && plugins.updates > 0 ? (
                <span className="badge badge--update">{plugins.updates}</span>
              ) : null

            return (
              <a
                key={page.id}
                className={`sidebar__link${current ? ' sidebar__link--active' : ''}`}
                href={pathOf({ kind: 'library', section, view: page.id })}
                onClick={follow(() => navigate({ kind: 'library', section, view: page.id }))}
                title={page.label}
                aria-current={current ? 'page' : undefined}
              >
                <Icon name={LIBRARY_VIEW_ICONS[page.id]} />
                <span className="sidebar__name">{page.label}</span>
                {badge}
              </a>
            )
          })}
        </nav>

        {section === 'plugins' && <ApiBudget plugins={plugins} />}
      </div>
    </>
  )
}

/**
 * How much GitHub quota is left, where the buttons that spend it are.
 *
 * Anonymous callers get sixty API calls an hour. 检查全部更新 across twenty
 * plugins spends twenty of them, listing one plugin's releases spends another,
 * and the failure when it runs out arrives as "检查更新失败" on every row at
 * once — which reads like the plugins are broken, not like the panel is out of
 * calls. This is the one number that turns that into something you can see
 * coming, and it costs nothing to show: it is read off the headers of calls the
 * panel was making anyway.
 *
 * Only shown once GitHub has actually said something. A meter reading 0/0 on a
 * panel that has never called out is a warning about nothing.
 */
function ApiBudget({ plugins }: { plugins: PluginController }) {
  const budget = plugins.library?.budget
  if (!budget || budget.limit === 0) return null

  const fraction = Math.max(0, Math.min(1, budget.remaining / budget.limit))
  const low = fraction <= 0.2

  return (
    <div className={`apibudget${low ? ' apibudget--low' : ''}`}>
      <span className="apibudget__label">
        GitHub API
        <b>
          {budget.remaining} / {budget.limit}
        </b>
      </span>
      <span className="apibudget__track" aria-hidden="true">
        <span className="apibudget__fill" style={{ width: `${Math.round(fraction * 100)}%` }} />
      </span>
      <small className="apibudget__note">
        {budget.authenticated ? '已用令牌' : '匿名调用，一小时 60 次'}
        {budget.resetAt && ` · ${resetLabel(budget.resetAt)}回满`}
      </small>
    </div>
  )
}

function resetLabel(at: string): string {
  const when = new Date(at)
  if (Number.isNaN(when.getTime())) return ''
  return when.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

/** What is on the shelf, under its name — the same job the instance header's
 *  state line does: enough to know whether you need to be here at all. */
function libraryMeta({ java, databases, cores, plugins }: Props, section: LibrarySection): string {
  switch (section) {
    case 'cores':
      return cores.cores.length > 0 ? `${cores.cores.length} 个核心` : '还没有核心'
    case 'java': {
      const count = java.overview?.runtimes.length ?? 0
      return count > 0 ? `${count} 个运行时` : '还没装 Java'
    }
    case 'database': {
      const services = databases.overview?.services ?? []
      const live = services.filter((service) => service.state === 'running').length
      if (services.length === 0) return '还没建数据库'
      return live > 0 ? `${services.length} 个库，${live} 个在跑` : `${services.length} 个库，都停着`
    }
    default:
      return plugins.plugins.length > 0 ? `${plugins.plugins.length} 个插件` : '还没有插件'
  }
}

const LIBRARY_ICONS: Record<LibrarySection, IconName> = {
  cores: 'cores',
  java: 'java',
  database: 'database',
  plugins: 'plugins',
}

/** A page inside a shelf is one of three things: what you have, where to get
 *  more, and where the getting is configured. The icons say which. */
const LIBRARY_VIEW_ICONS: Record<string, IconName> = {
  stock: 'cores',
  installed: 'java',
  list: 'plugins',
  browse: 'search',
  download: 'update',
  install: 'update',
  source: 'settings',
  databases: 'database',
  engines: 'cores',
}

// -------------------------------------------------------------------- host

function HostScope(props: Props) {
  const { route, follow, navigate, system, terminal } = props
  const section = route.kind === 'host' ? route.section : 'metrics'
  // The shell is the one entry here that is not about looking at a number, and
  // it is a shell on the whole machine rather than inside a Minecraft process.
  // It gets a rule above it and a padlock so it never reads as one more tab.
  const shellReachable = Boolean(terminal.status?.supported)

  return (
    <>
      <ScopeHead
        {...props}
        backLabel="返回面板"
        backTo={{ kind: 'overview' }}
        switcherLabel={undefined}
        navKey="host"
        name={system?.host.hostname || '本机'}
        meta={system ? `${system.host.platform} · ${system.host.cpuCores} 核` : '正在读取…'}
        dot={<Icon name="host" />}
      />

      <div className="sidebar__scroll">
        <nav className="sidebar__nav" aria-label="主机页面">
          {HOST_SECTIONS.filter((entry) => entry.id !== 'terminal').map((entry) => (
            <a
              key={entry.id}
              className={`sidebar__link${section === entry.id ? ' sidebar__link--active' : ''}`}
              href={pathOf({ kind: 'host', section: entry.id })}
              onClick={follow(() => navigate({ kind: 'host', section: entry.id }))}
              title={entry.label}
              aria-current={section === entry.id ? 'page' : undefined}
            >
              <Icon name={HOST_ICONS[entry.id]} />
              <span className="sidebar__name">{entry.label}</span>
            </a>
          ))}
        </nav>

        {shellReachable && (
          <nav className="sidebar__nav sidebar__nav--fenced" aria-label="特权操作">
            <a
              className={`sidebar__link sidebar__link--shell${
                section === 'terminal' ? ' sidebar__link--active' : ''
              }`}
              href={pathOf({ kind: 'host', section: 'terminal' })}
              onClick={follow(() => navigate({ kind: 'host', section: 'terminal' }))}
              title="SSH 终端 · 整机 shell"
              aria-current={section === 'terminal' ? 'page' : undefined}
            >
              <Icon name="lock" />
              <span className="sidebar__name">SSH 终端</span>
              {!terminal.status?.enabled && <span className="badge">未开启</span>}
            </a>
          </nav>
        )}
      </div>
    </>
  )
}

const HOST_ICONS: Record<string, IconName> = {
  metrics: 'chart',
  instances: 'instances',
  disk: 'disk',
  config: 'settings',
  terminal: 'lock',
}

// ---------------------------------------------------------------- settings

/**
 * The panel's own settings, as a scope rather than as a page with tabs.
 *
 * These four used to be a tab strip across the top of one page, which made
 * 面板设置 the only destination in the panel you could not reach from the
 * sidebar — you navigated to a page and then navigated again inside it, and
 * which of the four you landed on depended on where you had been last. A
 * second navigation for four items, sitting under a first navigation that
 * already had a shape for exactly this.
 */
function SettingsScope(props: Props) {
  const { route, follow, navigate, user, updateNotice } = props
  const section = route.kind === 'settings' ? route.section : 'devices'

  return (
    <>
      <ScopeHead
        {...props}
        backLabel="返回面板"
        backTo={{ kind: 'overview' }}
        switcherLabel={undefined}
        navKey="settings"
        name="面板设置"
        meta={user.version}
        dot={<Icon name="settings" />}
      />

      <div className="sidebar__scroll">
        <nav className="sidebar__nav" aria-label="面板设置">
          {SETTINGS_SECTIONS.map((entry) => (
            <a
              key={entry.id}
              className={`sidebar__link${section === entry.id ? ' sidebar__link--active' : ''}`}
              href={pathOf({ kind: 'settings', section: entry.id })}
              onClick={follow(() => navigate({ kind: 'settings', section: entry.id }))}
              title={entry.label}
              aria-current={section === entry.id ? 'page' : undefined}
            >
              <Icon name={SETTINGS_ICONS[entry.id]} />
              <span className="sidebar__name">{entry.label}</span>
              {entry.id === 'update' && updateNotice && (
                <span className="badge badge--update">{updateNotice}</span>
              )}
            </a>
          ))}
        </nav>
      </div>
    </>
  )
}

const SETTINGS_ICONS: Record<string, IconName> = {
  devices: 'devices',
  security: 'lock',
  plugins: 'github',
  update: 'update',
}

// ----------------------------------------------------------------- pieces

/** The fixed top of an inner scope: the way out, and what you are inside. */
function ScopeHead({
  follow,
  navigate,
  onOpenPalette,
  backLabel,
  backTo,
  switcherLabel,
  navKey,
  name,
  meta,
  dot,
}: Props & {
  backLabel: string
  backTo: Route
  switcherLabel?: string
  /** Pairs this header with the row it came from, in both directions. */
  navKey: string
  name: string
  meta: string
  dot: ReactNode
}) {
  const body = (
    <>
      <span className="sidebar__entity-dot">{dot}</span>
      <span className="sidebar__entity-text">
        <strong>{name}</strong>
        <small>{meta}</small>
      </span>
      {switcherLabel && (
        <span className="sidebar__entity-hint" aria-hidden="true">
          ⌘K
        </span>
      )}
    </>
  )

  return (
    <div className="sidebar__scope">
      <a
        className="sidebar__back"
        href={pathOf(backTo)}
        // The same capture as the row that opened this scope, which is what
        // makes leaving the exact reverse: the header shrinks back down into
        // the row it came from, and the list it displaced comes back in from
        // above and below.
        onClick={follow(() => {
          captureScope(navKey)
          navigate(backTo)
        })}
        title={backLabel}
      >
        <Icon name="back" />
        <span className="sidebar__name">返回上级</span>
      </a>

      {switcherLabel ? (
        <button
          className="sidebar__entity sidebar__entity--switch"
          data-nav-key={navKey}
          onClick={onOpenPalette}
          title={switcherLabel}
        >
          {body}
        </button>
      ) : (
        <div className="sidebar__entity" data-nav-key={navKey}>
          {body}
        </div>
      )}
    </div>
  )
}

function Group({ label, count }: { label: string; count?: string | null }) {
  return (
    <div className="sidebar__group">
      <span>{label}</span>
      {count && <span className="sidebar__count">{count}</span>}
    </div>
  )
}

function SearchButton({ onOpen }: { onOpen: () => void }) {
  return (
    <button className="sidebar__search-btn" onClick={onOpen} title="搜索与跳转（⌘K / Ctrl+K）">
      <Icon name="search" />
      <span className="sidebar__name">搜索实例、页面…</span>
      <kbd className="sidebar__kbd">⌘K</kbd>
    </button>
  )
}

function NavLink({
  route,
  follow,
  navigate,
  icon,
  label,
  target,
  active,
  navKey,
  badge,
}: Props & {
  icon: IconName
  label: string
  target: Route
  active?: boolean
  /** Set on the two rows that open a scope of their own, and matching the key
   *  on the header they become. Everything else navigates in place. */
  navKey?: string
  badge?: ReactNode
}) {
  const current = active ?? samePage(route, target)
  return (
    <a
      className={`sidebar__link${current ? ' sidebar__link--active' : ''}`}
      data-nav-key={navKey}
      href={pathOf(target)}
      onClick={follow(() => {
        if (navKey) captureScope(navKey)
        navigate(target)
      })}
      title={label}
      aria-current={current ? 'page' : undefined}
    >
      <Icon name={icon} />
      <span className="sidebar__name">{label}</span>
      {badge}
    </a>
  )
}
