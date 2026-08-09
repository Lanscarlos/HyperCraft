/**
 * Where the app is, as a value.
 *
 * The panel has three navigation scopes rather than one flat list — the panel
 * itself, one server, and the machine underneath — and which one you are in is
 * a property of the route, not of a separate piece of state. Deriving it here
 * is what lets the sidebar swap wholesale instead of growing a third level of
 * nesting under 实例.
 *
 * Every route is a real path, so ⌘-click, reload and a pasted link all work.
 * Filters that scope a list live in the query string for the same reason.
 */

/** Pages inside one server. Mirrors the instance-scope sidebar, in order. */
export type InstanceSection =
  | 'console'
  | 'metrics'
  | 'files'
  | 'plugins'
  | 'properties'
  | 'settings'

/** The shared-asset pages. Stock, as opposed to what one server has chosen. */
export type LibrarySection = 'cores' | 'java' | 'plugins'

/** Pages about the machine. `terminal` is the shell and is fenced off. */
export type HostSection = 'metrics' | 'instances' | 'disk' | 'config' | 'terminal'

/** Panel-wide settings. Sections a sub-user should not see live elsewhere. */
export type SettingsSection = 'devices' | 'security' | 'plugin-source' | 'update'

/** Which states the 所有实例 list is showing. Part of the URL. */
export type StateFilter = 'all' | 'live' | 'stopped' | 'problem'

export type Route =
  | { kind: 'overview' }
  | { kind: 'instances'; query: string; state: StateFilter }
  | { kind: 'instance'; id: string; section: InstanceSection }
  | { kind: 'library'; section: LibrarySection; pluginId?: string }
  | { kind: 'host'; section: HostSection }
  | { kind: 'settings'; section: SettingsSection }

/** The three navigation scopes. The sidebar is replaced entirely between them. */
export type Scope = 'global' | 'instance' | 'host'

export const INSTANCE_SECTIONS: { id: InstanceSection; label: string }[] = [
  { id: 'console', label: '控制台' },
  { id: 'metrics', label: '监控' },
  { id: 'files', label: '文件' },
  { id: 'plugins', label: '已装插件' },
  { id: 'properties', label: '服务器配置' },
  { id: 'settings', label: '实例设置' },
]

export const LIBRARY_SECTIONS: { id: LibrarySection; label: string }[] = [
  { id: 'cores', label: '服务端核心' },
  { id: 'java', label: 'Java 环境' },
  { id: 'plugins', label: '插件 / 模组' },
]

export const HOST_SECTIONS: { id: HostSection; label: string }[] = [
  { id: 'metrics', label: '监控' },
  { id: 'instances', label: '实例分布' },
  { id: 'disk', label: '磁盘' },
  { id: 'config', label: '节点配置' },
  { id: 'terminal', label: 'SSH 终端' },
]

export const SETTINGS_SECTIONS: { id: SettingsSection; label: string }[] = [
  { id: 'devices', label: '已配对设备' },
  { id: 'security', label: '登录记录' },
  { id: 'plugin-source', label: '插件源' },
  { id: 'update', label: '面板更新' },
]

const STATE_FILTERS: StateFilter[] = ['all', 'live', 'stopped', 'problem']

function pick<T extends string>(values: { id: T }[], value: string, fallback: T): T {
  return values.some((entry) => entry.id === value) ? (value as T) : fallback
}

export function scopeOf(route: Route): Scope {
  if (route.kind === 'instance') return 'instance'
  if (route.kind === 'host') return 'host'
  return 'global'
}

export function routeFromLocation(): Route {
  const path = window.location.pathname
  const params = new URLSearchParams(window.location.search)

  const instance = path.match(/^\/i\/([^/]+)(?:\/([^/]+))?/)
  if (instance) {
    return {
      kind: 'instance',
      id: decodeURIComponent(instance[1]),
      section: pick(INSTANCE_SECTIONS, instance[2] ?? '', 'console'),
    }
  }

  const host = path.match(/^\/host(?:\/([^/]+))?/)
  if (host) return { kind: 'host', section: pick(HOST_SECTIONS, host[1] ?? '', 'metrics') }

  const library = path.match(/^\/library(?:\/([^/]+))?(?:\/([^/]+))?/)
  if (library) {
    const section = pick(LIBRARY_SECTIONS, library[1] ?? '', 'cores')
    return section === 'plugins' && library[2]
      ? { kind: 'library', section, pluginId: decodeURIComponent(library[2]) }
      : { kind: 'library', section }
  }

  if (path === '/instances' || path.startsWith('/instances/')) {
    const state = params.get('state') ?? 'all'
    return {
      kind: 'instances',
      query: params.get('q') ?? '',
      state: (STATE_FILTERS as string[]).includes(state) ? (state as StateFilter) : 'all',
    }
  }

  const settings = path.match(/^\/settings(?:\/([^/]+))?/)
  if (settings) {
    // The panel used to keep every one of these somewhere else. Old bookmarks
    // and links from an earlier release land on whatever replaced them rather
    // than on a default tab.
    const section = settings[1] ?? ''
    if (section === 'java') return { kind: 'library', section: 'java' }
    if (section === 'cores') return { kind: 'library', section: 'cores' }
    if (section === 'terminal') return { kind: 'host', section: 'config' }
    return { kind: 'settings', section: pick(SETTINGS_SECTIONS, section, 'devices') }
  }

  if (path === '/java') return { kind: 'library', section: 'java' }
  if (path === '/cores') return { kind: 'library', section: 'cores' }
  const legacyPlugin = path.match(/^\/plugins\/([^/]+)/)
  if (legacyPlugin) {
    return { kind: 'library', section: 'plugins', pluginId: decodeURIComponent(legacyPlugin[1]) }
  }
  if (path === '/plugins') return { kind: 'library', section: 'plugins' }
  if (path === '/terminal') return { kind: 'host', section: 'terminal' }

  return { kind: 'overview' }
}

export function pathOf(route: Route): string {
  switch (route.kind) {
    case 'instance':
      return `/i/${encodeURIComponent(route.id)}/${route.section}`
    case 'host':
      return `/host/${route.section}`
    case 'library':
      return route.pluginId
        ? `/library/plugins/${encodeURIComponent(route.pluginId)}`
        : `/library/${route.section}`
    case 'settings':
      return `/settings/${route.section}`
    case 'instances': {
      const params = new URLSearchParams()
      if (route.query.trim() !== '') params.set('q', route.query)
      if (route.state !== 'all') params.set('state', route.state)
      const search = params.toString()
      return search === '' ? '/instances' : `/instances?${search}`
    }
    default:
      return '/'
  }
}

/** True when two routes are the same page, ignoring list filters. */
export function samePage(a: Route, b: Route): boolean {
  if (a.kind !== b.kind) return false
  switch (a.kind) {
    case 'instance':
      return b.kind === 'instance' && a.id === b.id && a.section === b.section
    case 'host':
      return b.kind === 'host' && a.section === b.section
    case 'library':
      return b.kind === 'library' && a.section === b.section && a.pluginId === b.pluginId
    case 'settings':
      return b.kind === 'settings' && a.section === b.section
    default:
      return true
  }
}
