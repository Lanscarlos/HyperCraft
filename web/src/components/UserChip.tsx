import { Menu } from './Menu'
import type { User } from '../types'

/**
 * The account button, at the foot of the rail.
 *
 * It used to sit in the top bar's right-hand corner beside the theme switch.
 * It moved because the corner it was in belongs to the server: on an instance
 * page the top bar says what that server is doing and offers the buttons that
 * change it, and "who is signed in" is the one thing up there that was not
 * about the thing on screen. The rail is where the panel keeps what is true on
 * every page regardless of what is open, and this is one of those facts.
 */
export function UserChip({
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
      className="userchip"
      title={user.username}
      ariaLabel={`账户 ${user.username}`}
      items={[
        { label: '修改密码', onSelect: onChangePassword },
        { label: '退出登录', onSelect: onSignOut },
      ]}
    >
      <span className="userchip__avatar" aria-hidden="true">
        {user.username.slice(0, 1).toUpperCase()}
      </span>
      <span className="userchip__name">{user.username}</span>
    </Menu>
  )
}
