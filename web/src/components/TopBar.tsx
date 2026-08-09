import { useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

import type { InstanceState, User } from '../types'
import { Icon } from './Icon'
import { ThemeToggle } from './ThemeToggle'

/** One step of the trail. The last one is where you are and never links. */
export interface Crumb {
  label: string
  onClick?: () => void
  /** Shown as a dot before the label, for the crumb that names an instance. */
  state?: InstanceState
}

interface Props {
  crumbs: Crumb[]
  user: User
  /** True while the sidebar is a drawer rather than a rail beside the content. */
  compact: boolean
  /** True while the desktop sidebar is collapsed to icons. */
  railed: boolean
  navOpen: boolean
  onToggleNav: () => void
  toggleRef: RefObject<HTMLButtonElement>
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
  railed,
  navOpen,
  onToggleNav,
  toggleRef,
  onChangePassword,
  onSignOut,
}: Props) {
  // One button, two jobs: it opens the drawer where there is no room for the
  // sidebar and folds the rail where there is. Labelling it for the wrong one
  // is worse than having two buttons, so the label follows the layout.
  const toggleLabel = compact
    ? navOpen
      ? '关闭导航'
      : '打开导航'
    : railed
      ? '展开侧边栏（[）'
      : '收起侧边栏（[）'

  return (
    <header className="topbar">
      <button
        ref={toggleRef}
        className="topbar__toggle"
        onClick={onToggleNav}
        title={toggleLabel}
        aria-label={toggleLabel}
        aria-expanded={compact ? navOpen : !railed}
        aria-controls="sidebar"
      >
        <Icon name={compact ? 'menu' : railed ? 'expand' : 'collapse'} />
      </button>

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
              {crumb.onClick && !last ? (
                <button className="crumbs__link" onClick={crumb.onClick}>
                  {crumb.label}
                </button>
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
  const [open, setOpen] = useState(false)
  const wrap = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (event: MouseEvent) => {
      if (!wrap.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    // Pointerdown rather than click: a menu that survives until mouseup looks
    // stuck when you click straight through to something behind it.
    window.addEventListener('pointerdown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('pointerdown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="usermenu" ref={wrap}>
      <button
        className="usermenu__button"
        onClick={() => setOpen((value) => !value)}
        aria-haspopup="menu"
        aria-expanded={open}
        title={user.username}
      >
        <span className="usermenu__avatar" aria-hidden="true">
          {user.username.slice(0, 1).toUpperCase()}
        </span>
        <span className="usermenu__name">{user.username}</span>
      </button>

      {open && (
        <div className="usermenu__sheet" role="menu">
          <button
            role="menuitem"
            onClick={() => {
              setOpen(false)
              onChangePassword()
            }}
          >
            修改密码
          </button>
          <button
            role="menuitem"
            onClick={() => {
              setOpen(false)
              onSignOut()
            }}
          >
            退出登录
          </button>
        </div>
      )}
    </div>
  )
}
