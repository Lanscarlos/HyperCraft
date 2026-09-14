import { EmptyState } from './EmptyState'
import { FileIcon } from './FileIcon'
import { Glyph } from './Glyph'
import { Menu } from './Menu'
import type { MenuItem } from './Menu'

/**
 * The files this instance was last edited through.
 *
 * Kept per instance in this browser rather than on the panel: which files
 * *you* were last in is not a fact about the server, and following an account
 * onto somebody else's screen is not what anyone means by "recent".
 *
 * It is also what ⌘P shows before anything is typed, which is the other half
 * of why it is worth keeping — the file you were just in is nearly always the
 * file you are looking for.
 */
export function FileRecentPanel({
  paths,
  activePath,
  onOpen,
  onForget,
  onClear,
}: {
  paths: string[]
  activePath: string | null
  onOpen: (path: string) => void
  onForget: (path: string) => void
  onClear: () => void
}) {
  if (paths.length === 0) {
    return (
      <EmptyState inline title="还没有打开过文件">
        打开过的文件会按顺序记在这里，最多 20 个。
      </EmptyState>
    )
  }

  const menu = (path: string): MenuItem[] => [
    { label: '复制路径', onSelect: () => void navigator.clipboard?.writeText(path) },
    { label: '从最近移除', onSelect: () => onForget(path) },
    { label: '清空最近', danger: true, onSelect: onClear },
  ]

  return (
    <div className="frecent">
      {paths.map((path) => (
        <div
          className={`frecent__row${path === activePath ? ' frecent__row--on' : ''}`}
          key={path}
        >
          <button
            type="button"
            className="frecent__pick"
            onClick={() => onOpen(path)}
            title={path}
          >
            <FileIcon name={baseName(path)} />
            <span className="frecent__text">
              <span className="frecent__name">{baseName(path)}</span>
              <span className="frecent__dir">{dirOf(path)}</span>
            </span>
          </button>
          <Menu
            className="iconbtn frecent__more"
            items={menu(path)}
            ariaLabel={`${baseName(path)} 的操作`}
          >
            <Glyph name="ellipsis" />
          </Menu>
        </div>
      ))}
    </div>
  )
}

function baseName(path: string): string {
  const at = path.lastIndexOf('/')
  return at < 0 ? path : path.slice(at + 1)
}

function dirOf(path: string): string {
  const at = path.lastIndexOf('/')
  return at < 0 ? '实例根目录' : path.slice(0, at)
}
