import type { MouseEventHandler, RefObject } from 'react'

import type { InstanceState, User } from '../types'
import { Icon } from './Icon'
import { Menu } from './Menu'
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
  user: User
  /** True while the sidebar is a drawer rather than a rail beside the content. */
  compact: boolean
  navOpen: boolean
  onToggleNav: () => void
  toggleRef: RefObject<HTMLButtonElement>
  /** One step up the trail, or null at the top of it. */
  onBack: (() => void) | null
  /** Where that step lands, so the button is a real link like the trail is. */
  backHref: string | null
  /** Named in the tooltip: "返回" alone is the question, not the answer. */
  backLabel: string | null
  /** ⌘K. On a drawer layout the sidebar's own search button is off screen, so
   *  this is the only one left — which is exactly when it is needed most. */
  onOpenPalette: () => void
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
  user,
  compact,
  navOpen,
  onToggleNav,
  toggleRef,
  onBack,
  backHref,
  backLabel,
  onOpenPalette,
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
          title={backLabel ? `返回${backLabel}` : '返回上一级'}
          aria-label={backLabel ? `返回${backLabel}` : '返回上一级'}
        >
          <Icon name="back" />
        </a>
      ) : (
        // The overview is the top of the trail. The button stays in place
        // rather than being removed, because a strip whose contents shift left
        // on one page out of six is a strip that has to be re-read.
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
              {crumb.state && <span className={`status__dot status__dot--${crumb.state}`} />}
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

      <div className="topbar__right">
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
