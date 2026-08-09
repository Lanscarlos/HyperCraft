import type { MouseEvent, ReactNode, Ref } from 'react'

import type { Route, Scope } from '../routes'
import { HOST_SECTIONS, INSTANCE_SECTIONS, pathOf, samePage } from '../routes'
import type { InstanceStatus, SystemInfo, User } from '../types'
import { STATE_LABELS, isLive } from '../types'
import type { CoreController } from '../useCores'
import type { JavaController } from '../useJava'
import type { PluginController } from '../usePlugins'
import type { TerminalController } from '../useTerminal'
import { Icon } from './Icon'
import type { IconName } from './Icon'

interface Props {
  route: Route
  scope: Scope
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
  cores: CoreController
  plugins: PluginController
  terminal: TerminalController
  onCreate: () => void
  onOpenPalette: () => void
  sidebarRef: Ref<HTMLElement>
}

/**
 * The panel's navigation, in whichever of its three forms applies.
 *
 * There are three kinds of thing to manage here and they do not belong in one
 * flat list: the panel and its shared stock, one server, and the machine
 * underneath. Their lifetimes differ by orders of magnitude — you visit the
 * host page monthly and the console hourly — so entering a server or the host
 * *replaces* this list rather than expanding an item inside it. Nesting them
 * would put the console three levels deep, and a console three levels deep is
 * a console nobody uses.
 *
 * What the two inner scopes owe the reader in exchange is a way out and a way
 * across: every one of them opens with 返回上级 and the name of the thing you
 * are inside.
 */
export function Sidebar(props: Props) {
  const { scope, sidebarRef } = props

  return (
    <aside className="sidebar" id="sidebar" ref={sidebarRef} tabIndex={-1} data-scope={scope}>
      {scope === 'instance' ? (
        <InstanceScope {...props} />
      ) : scope === 'host' ? (
        <HostScope {...props} />
      ) : (
        <GlobalScope {...props} />
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
              href={pathOf({ kind: 'instance', id: item.id, section: 'console' })}
              onClick={follow(() =>
                navigate({ kind: 'instance', id: item.id, section: 'console' }),
              )}
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
            gets neither, and hiding a group is one check instead of six. */}
        <Group label="资源库" />
        <nav className="sidebar__nav" aria-label="资源库导航">
          <NavLink
            {...props}
            icon="cores"
            label="服务端核心"
            target={{ kind: 'library', section: 'cores' }}
            badge={cores.downloading ? <span className="badge badge--update">下载中</span> : null}
          />
          <NavLink
            {...props}
            icon="java"
            label="Java 环境"
            target={{ kind: 'library', section: 'java' }}
            badge={java.installing ? <span className="badge badge--update">安装中</span> : null}
          />
          <NavLink
            {...props}
            icon="plugins"
            label="插件 / 模组"
            target={{ kind: 'library', section: 'plugins' }}
            active={route.kind === 'library' && route.section === 'plugins'}
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
          <NavLink {...props} icon="host" label="主机" target={{ kind: 'host', section: 'metrics' }} />
          <NavLink
            {...props}
            icon="settings"
            label="面板设置"
            target={{ kind: 'settings', section: 'devices' }}
            active={route.kind === 'settings'}
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

// ----------------------------------------------------------------- pieces

/** The fixed top of an inner scope: the way out, and what you are inside. */
function ScopeHead({
  follow,
  navigate,
  onOpenPalette,
  backLabel,
  backTo,
  switcherLabel,
  name,
  meta,
  dot,
}: Props & {
  backLabel: string
  backTo: Route
  switcherLabel?: string
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
        onClick={follow(() => navigate(backTo))}
        title={backLabel}
      >
        <Icon name="back" />
        <span className="sidebar__name">返回上级</span>
      </a>

      {switcherLabel ? (
        <button
          className="sidebar__entity sidebar__entity--switch"
          onClick={onOpenPalette}
          title={switcherLabel}
        >
          {body}
        </button>
      ) : (
        <div className="sidebar__entity">{body}</div>
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
  badge,
}: Props & {
  icon: IconName
  label: string
  target: Route
  active?: boolean
  badge?: ReactNode
}) {
  const current = active ?? samePage(route, target)
  return (
    <a
      className={`sidebar__link${current ? ' sidebar__link--active' : ''}`}
      href={pathOf(target)}
      onClick={follow(() => navigate(target))}
      title={label}
      aria-current={current ? 'page' : undefined}
    >
      <Icon name={icon} />
      <span className="sidebar__name">{label}</span>
      {badge}
    </a>
  )
}
