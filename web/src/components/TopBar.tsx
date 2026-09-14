import type { MouseEventHandler, RefObject } from 'react'

import { formatBytes, formatPercent } from '../format'
import type { InstanceMetrics, InstanceState, InstanceStatus, User } from '../types'
import { STATE_LABELS, isLive } from '../types'
import type { DownloadController } from '../useDownloads'
import { useUptime } from '../useUptime'
import { DownloadTray } from './DownloadTray'
import { Icon } from './Icon'
import { Menu } from './Menu'
import { PowerControls } from './PowerControls'
import { StatusDot } from './StatusDot'
import { ThemeToggle } from './ThemeToggle'

/** One step of the trail. The last one is where you are and never links. */
export interface Crumb {
  label: string
  /** Set together with onClick: the step is a real link to a real path. */
  href?: string
  onClick?: MouseEventHandler<HTMLAnchorElement>
  /** Shown as a dot before the label, for the crumb that names an instance. */
  state?: InstanceState
}

interface Props {
  crumbs: Crumb[]
  /** The instance the current page belongs to, or null on a panel-wide page.
   *  When it is set the strip appears; when it is not the bar is what it was. */
  instance: InstanceStatus | null
  /** Samples for that instance, polled once in App. */
  metrics: InstanceMetrics | null
  onInstanceChanged: (instance: InstanceStatus) => void
  /** A failed start or stop. Raised to App rather than shown here: the strip is
   *  32px tall and the message is a sentence, and it has to survive being read
   *  — see the note on the banner in App. */
  onPowerError: (message: string) => void
  user: User
  /** True while the sidebar is a drawer rather than a rail beside the content. */
  compact: boolean
  navOpen: boolean
  onToggleNav: () => void
  toggleRef: RefObject<HTMLButtonElement>
  /** Back to the page you came from, or null when nothing came before this one. */
  onBack: (() => void) | null
  /** Where that lands, so the button is a real link like the trail is. */
  backHref: string | null
  /** Named in the tooltip: "返回" alone is the question, not the answer. */
  backLabel: string | null
  /** ⌘K. On a drawer layout the sidebar's own search button is off screen, so
   *  this is the only one left — which is exactly when it is needed most. */
  onOpenPalette: () => void
  /** The panel-wide download queue, for the tray. */
  downloads: DownloadController
  /** Where 查看全部 in the tray leads. */
  onOpenDownloads: () => void
  onChangePassword: () => void
  onSignOut: () => void
}

/**
 * The one strip that is on screen no matter which page is.
 *
 * It exists because every page in the panel scrolls its own title away, so
 * after two screens of a file listing nothing on screen says which instance
 * you are looking at. The trail answers that, and the room left over is where
 * the account controls went — they used to sit at the bottom of the sidebar,
 * which is both the last place you look and the first thing a drawer hides.
 */
export function TopBar({
  crumbs,
  instance,
  metrics,
  onInstanceChanged,
  onPowerError,
  user,
  compact,
  navOpen,
  onToggleNav,
  toggleRef,
  onBack,
  backHref,
  backLabel,
  onOpenPalette,
  downloads,
  onOpenDownloads,
  onChangePassword,
  onSignOut,
}: Props) {
  // The corner every browser, every phone and every file manager puts 返回 in.
  // It used to hold the sidebar's fold — a chevron pointing left, which is the
  // back arrow's own shape — and people pressed it expecting to leave the page.
  // The fold moved to the foot of the sidebar it folds; this is what they were
  // reaching for. On a drawer layout the sidebar is off screen and nothing else
  // can open it, so there the corner still belongs to the drawer.
  const drawerLabel = navOpen ? '关闭导航' : '打开导航'

  return (
    <header className="topbar">
      {compact ? (
        <button
          ref={toggleRef}
          className="topbar__toggle"
          onClick={onToggleNav}
          title={drawerLabel}
          aria-label={drawerLabel}
          aria-expanded={navOpen}
          aria-controls="sidebar"
        >
          <Icon name="menu" />
        </button>
      ) : onBack && backHref ? (
        <a
          className="topbar__toggle"
          href={backHref}
          onClick={(event) => {
            if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
            event.preventDefault()
            onBack()
          }}
          title={backLabel ? `返回${backLabel}` : '返回上一页'}
          aria-label={backLabel ? `返回${backLabel}` : '返回上一页'}
        >
          <Icon name="back" />
        </a>
      ) : (
        // Nowhere to go back to: the overview reached with nothing before it.
        // The button stays in place rather than being removed, because a strip
        // whose contents shift left on one page out of six is a strip that has
        // to be re-read.
        <span className="topbar__toggle topbar__toggle--idle" aria-hidden="true">
          <Icon name="back" />
        </span>
      )}

      <nav className="crumbs" aria-label="当前位置">
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1
          return (
            <span className="crumbs__step" key={`${crumb.label}-${index}`}>
              {index > 0 && (
                <span className="crumbs__sep" aria-hidden="true">
                  /
                </span>
              )}
              {/* The strip carries the dot now, with the label beside it that
                  says what the colour means; a second one 40px away in the
                  trail was the same fact told worse. It stays for a trail
                  rendered without a strip. */}
              {crumb.state && !instance && (
                <StatusDot state={crumb.state} />
              )}
              {crumb.href && !last ? (
                <a className="crumbs__link" href={crumb.href} onClick={crumb.onClick}>
                  {crumb.label}
                </a>
              ) : (
                <span className="crumbs__here" aria-current={last ? 'page' : undefined}>
                  {crumb.label}
                </span>
              )}
            </span>
          )
        })}
      </nav>

      {instance && (
        <InstanceStrip
          instance={instance}
          metrics={metrics}
          onChanged={onInstanceChanged}
          onError={onPowerError}
        />
      )}

      <div className="topbar__right">
        {/* First, and the only one of these that is a *state* rather than an
            action: what the panel is doing, then what you can do to it, then
            who you are. */}
        <DownloadTray downloads={downloads} onOpenPage={onOpenDownloads} />
        <button
          className="topbar__search"
          onClick={onOpenPalette}
          title="搜索与跳转（⌘K / Ctrl+K）"
          aria-label="搜索与跳转"
        >
          <Icon name="search" />
        </button>
        <ThemeToggle />
        <UserMenu user={user} onChangePassword={onChangePassword} onSignOut={onSignOut} />
      </div>
    </header>
  )
}

/** The account button and the two things you can do to an account. */
function UserMenu({
  user,
  onChangePassword,
  onSignOut,
}: {
  user: User
  onChangePassword: () => void
  onSignOut: () => void
}) {
  return (
    <Menu
      className="usermenu__button"
      title={user.username}
      items={[
        { label: '修改密码', onSelect: onChangePassword },
        { label: '退出登录', onSelect: onSignOut },
      ]}
    >
      <span className="usermenu__avatar" aria-hidden="true">
        {user.username.slice(0, 1).toUpperCase()}
      </span>
      <span className="usermenu__name">{user.username}</span>
    </Menu>
  )
}

/**
 * Whether the server is up, and the two numbers that say how hard it is
 * working — on every page of the instance, not just its console.
 *
 * This used to live only in the cockpit's own header, which meant that three
 * screens into a file listing there was nothing on the page that said the
 * server had crashed, and stopping it was a trip back through 控制台. The trail
 * beside it had the room: on a wide window it ends well short of the account
 * controls.
 *
 * What it does *not* show is TPS and the player count, which is what a panel
 * like this normally puts here. The daemon reads the server's stdout; it has no
 * player registry and no tick timing, so both would have to be invented. A
 * number that is wrong is worse than a number that is missing.
 */
function InstanceStrip({
  instance,
  metrics,
  onChanged,
  onError,
}: {
  instance: InstanceStatus
  metrics: InstanceMetrics | null
  onChanged: (instance: InstanceStatus) => void
  onError: (message: string) => void
}) {
  const live = isLive(instance.state)
  const uptime = useUptime(instance.startedAt, live)
  const latest = metrics?.samples[metrics.samples.length - 1] ?? null

  // An em dash rather than 0: a stopped server has not got a CPU figure of
  // zero, it has not got one at all. Same rule the cockpit's tiles follow.
  const cpu = latest ? formatPercent(latest.cpuPercent) : '—'
  const memory = latest ? formatBytes(latest.memoryBytes) : '—'

  return (
    // The power buttons sit beside the readout rather than over with the
    // account controls: 停止 one gap away from 退出登录 is a slip waiting to
    // happen, and the thing you stop is the thing this strip is describing.
    <div className="topbar__instance">
      <div className="topbar__status">
        <StatusDot state={instance.state} />
        <b className="topbar__state">{STATE_LABELS[instance.state]}</b>
        {/* Only while there is something to report. A stopped server has no
            uptime, no CPU and no memory — three em dashes in a row is a row of
            noise, and the state beside them already said why. As the window
            narrows the live ones drop one at a time, least useful first; all
            three are still on the cockpit, so nothing here is the only copy. */}
        {live && (
          <>
            <span className="topbar__fact topbar__fact--uptime">
              已运行 <b>{uptime ?? '—'}</b>
            </span>
            <span className="topbar__fact">
              CPU <b>{cpu}</b>
            </span>
            <span className="topbar__fact">
              内存 <b>{memory}</b>
            </span>
          </>
        )}
      </div>
      {/* A failed start used to print into the cockpit's own error slot, which
          is off screen from every page but 控制台 — and this control is on all
          of them. It goes up to App, which has a banner over whatever page is
          showing. */}
      <PowerControls instance={instance} onChanged={onChanged} variant="compact" onError={onError} />
    </div>
  )
}
