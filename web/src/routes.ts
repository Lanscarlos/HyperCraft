/**
 * Where the app is, as a value.
 *
 * The panel has a handful of navigation scopes rather than one flat list — the
 * panel itself, one server, one shelf of the library, the machine underneath —
 * and which one you are in is a property of the route, not of a separate piece
 * of state. Deriving it here is what lets the sidebar swap wholesale instead of
 * growing a third level of nesting under 实例.
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

/**
 * The pages inside one library section.
 *
 * Each of the three used to be a single page that stacked everything it could
 * do into one scroll: what you already have, the catalogue to download from,
 * and the settings that govern the downloading. Three different jobs with
 * three different frequencies — you look at the shelf weekly, download monthly
 * and choose a mirror once — and the first of them, the one you actually came
 * for, was the one you had to scroll past the others to read. They are pages of
 * their own now, and opening a section opens *them* (see Scope).
 */
export type LibraryView =
  | 'stock'
  | 'download'
  | 'installed'
  | 'install'
  | 'source'
  | 'list'
  | 'browse'

/**
 * Which half of a plugin page is showing.
 *
 * 获取插件 is one component with two entrances — from the panel, where it asks
 * which servers to install into, and from inside a server, where that is
 * already answered. It is a tab rather than a page of its own because it is
 * the other half of a question: 已装插件 answers "what is on this server", and
 * 获取插件 answers "what should be". Splitting them across the navigation
 * would mean leaving the server to add something to it.
 *
 * A real path segment either way, so a link to the discovery page with a
 * server already selected is something you can paste to someone.
 */
export type PluginTab = 'installed' | 'browse'

/** Pages about the machine. `terminal` is the shell and is fenced off. */
export type HostSection = 'metrics' | 'instances' | 'disk' | 'config' | 'terminal'

/** Panel-wide settings. Sections a sub-user should not see live elsewhere. */
export type SettingsSection = 'devices' | 'security' | 'update'

/** Which states the 所有实例 list is showing. Part of the URL. */
export type StateFilter = 'all' | 'live' | 'stopped' | 'problem'

export type Route =
  | { kind: 'overview' }
  | { kind: 'instances'; query: string; state: StateFilter }
  | { kind: 'instance'; id: string; section: InstanceSection; tab?: PluginTab }
  | { kind: 'library'; section: LibrarySection; view: LibraryView; pluginId?: string }
  | { kind: 'host'; section: HostSection }
  | { kind: 'settings'; section: SettingsSection }

/**
 * The navigation scopes. The sidebar is replaced entirely between them.
 *
 * 面板设置 became one of these rather than staying a page with a tab strip.
 * Four tabs across the top of a page is a second navigation in a panel that
 * already has one, and it was the only place in here where the way to a page
 * depended on which page you were already on — every other destination is
 * reachable from the sidebar, and now so are these.
 *
 * The three library shelves followed, and they are the reason the list is not
 * "three scopes and one exception": their pages used to hang off the parent row
 * as an indented strip, which meant the panel had two ways of showing a second
 * level — a strip for these, a whole scope for everything else — and the strip
 * was the one that arrived without any movement to say it had. One shape, one
 * animation, one way out.
 */
export type Scope = 'global' | 'instance' | 'library' | 'host' | 'settings'

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
  { id: 'plugins', label: '插件库' },
]

/** The pages inside each library section, in order. The first is the default —
 *  always "what you already have", never a form. */
export const LIBRARY_VIEWS: Record<LibrarySection, { id: LibraryView; label: string }[]> = {
  cores: [
    { id: 'stock', label: '核心库' },
    { id: 'download', label: '下载核心' },
  ],
  java: [
    { id: 'installed', label: '已安装' },
    { id: 'install', label: '安装新版本' },
    { id: 'source', label: '下载源' },
  ],
  plugins: [
    { id: 'list', label: '插件列表' },
    { id: 'source', label: '插件源' },
  ],
}

/**
 * The two tabs a plugin page carries, in both scopes.
 *
 * Not a LIBRARY_VIEWS entry, deliberately: 获取插件 is not a third shelf beside
 * 插件列表 and 插件源, it is the other half of the first one, and the instance
 * scope has to show the same pair with no sidebar to hang it on. One strip,
 * one set of labels, both places.
 */
export const PLUGIN_TABS: { id: PluginTab; label: string }[] = [
  { id: 'installed', label: '已装插件' },
  { id: 'browse', label: '获取插件' },
]

/** The tab a library plugin view is showing, so one strip drives both scopes. */
export function pluginTabOf(view: LibraryView): PluginTab {
  return view === 'browse' ? 'browse' : 'installed'
}

export function defaultView(section: LibrarySection): LibraryView {
  return LIBRARY_VIEWS[section][0].id
}

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
  { id: 'update', label: '面板更新' },
]

const STATE_FILTERS: StateFilter[] = ['all', 'live', 'stopped', 'problem']

function pick<T extends string>(values: { id: T }[], value: string, fallback: T): T {
  return values.some((entry) => entry.id === value) ? (value as T) : fallback
}

export function scopeOf(route: Route): Scope {
  if (route.kind === 'instance') return 'instance'
  if (route.kind === 'library') return 'library'
  if (route.kind === 'host') return 'host'
  if (route.kind === 'settings') return 'settings'
  return 'global'
}

/**
 * Pairs a row with the scope header it becomes, across the two sidebars.
 *
 * Shared rather than spelled out at each end: the row, the header it flies to,
 * and the two ways back out (the sidebar's 返回上级 and the top bar's) all have
 * to agree on the string or the animation silently does nothing.
 */
export function navKeyOf(route: Route): string | null {
  switch (route.kind) {
    case 'instance':
      return `instance:${route.id}`
    case 'library':
      return `library:${route.section}`
    case 'host':
      return 'host'
    case 'settings':
      return 'settings'
    default:
      return null
  }
}

/**
 * One step up, as the trail in the top bar reads it.
 *
 * Every scope has a first page that stands for the whole thing — the console,
 * the shelf, 监控 — so going up from anywhere inside one lands there first and
 * leaves the scope on the next press. Two presses out of a file listing rather
 * than one, deliberately: the alternative is a button that sometimes moves you
 * one page and sometimes throws away the whole context you were in, with
 * nothing on screen saying which it will be this time.
 */
export function parentOf(route: Route): Route | null {
  switch (route.kind) {
    case 'instances':
      return { kind: 'overview' }
    case 'instance':
      // 获取插件 goes back to the list it was opened from rather than out to
      // the console: the operator is mid-comparison, and one press should
      // return them to what they already have installed.
      if (route.section === 'plugins' && route.tab === 'browse') {
        return { kind: 'instance', id: route.id, section: 'plugins' }
      }
      return route.section === 'console'
        ? { kind: 'instances', query: '', state: 'all' }
        : { kind: 'instance', id: route.id, section: 'console' }
    case 'library': {
      const home = defaultView(route.section)
      if (route.pluginId) return { kind: 'library', section: route.section, view: 'list' }
      // Same rule as the instance scope: the tab goes back to its other half.
      if (route.view === 'browse') return { kind: 'library', section: route.section, view: 'list' }
      return route.view === home
        ? { kind: 'overview' }
        : { kind: 'library', section: route.section, view: home }
    }
    case 'host':
      return route.section === 'metrics'
        ? { kind: 'overview' }
        : { kind: 'host', section: 'metrics' }
    case 'settings':
      return route.section === 'devices'
        ? { kind: 'overview' }
        : { kind: 'settings', section: 'devices' }
    default:
      return null
  }
}

export function routeFromLocation(): Route {
  const path = window.location.pathname
  const params = new URLSearchParams(window.location.search)

  const instance = path.match(/^\/i\/([^/]+)(?:\/([^/]+))?(?:\/([^/]+))?/)
  if (instance) {
    const section = pick(INSTANCE_SECTIONS, instance[2] ?? '', 'console')
    // Only 插件 has a second level, and only the discovery half of it needs a
    // segment: /i/<id>/plugins is the installed list, as it always was.
    return section === 'plugins' && instance[3] === 'browse'
      ? { kind: 'instance', id: decodeURIComponent(instance[1]), section, tab: 'browse' }
      : { kind: 'instance', id: decodeURIComponent(instance[1]), section }
  }

  const host = path.match(/^\/host(?:\/([^/]+))?/)
  if (host) return { kind: 'host', section: pick(HOST_SECTIONS, host[1] ?? '', 'metrics') }

  const library = path.match(/^\/library(?:\/([^/]+))?(?:\/([^/]+))?(?:\/([^/]+))?/)
  if (library) {
    const section = pick(LIBRARY_SECTIONS, library[1] ?? '', 'cores')
    const second = library[2] ?? ''
    // 获取插件 is a tab on the plugin page rather than a sidebar row, so it is
    // not in LIBRARY_VIEWS and has to be recognised here.
    if (section === 'plugins' && second === 'browse') {
      return { kind: 'library', section, view: 'browse' }
    }
    const view = LIBRARY_VIEWS[section].find((entry) => entry.id === second)?.id
    if (view) {
      return section === 'plugins' && view === 'list' && library[3]
        ? { kind: 'library', section, view, pluginId: decodeURIComponent(library[3]) }
        : { kind: 'library', section, view }
    }
    // A plugin used to live directly under its section — /library/plugins/<id>
    // — which is what every bookmark and every link in an older release says.
    // The views took that slot, so anything that is not one of them is still
    // read as a plugin id.
    return section === 'plugins' && second
      ? { kind: 'library', section, view: 'list', pluginId: decodeURIComponent(second) }
      : { kind: 'library', section, view: defaultView(section) }
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
    if (section === 'java') return { kind: 'library', section: 'java', view: 'installed' }
    if (section === 'cores') return { kind: 'library', section: 'cores', view: 'stock' }
    if (section === 'terminal') return { kind: 'host', section: 'config' }
    // The plugin source moved under 插件库, where the rest of the plugin
    // machinery already was.
    if (section === 'plugin-source') return { kind: 'library', section: 'plugins', view: 'source' }
    return { kind: 'settings', section: pick(SETTINGS_SECTIONS, section, 'devices') }
  }

  if (path === '/java') return { kind: 'library', section: 'java', view: 'installed' }
  if (path === '/cores') return { kind: 'library', section: 'cores', view: 'stock' }
  const legacyPlugin = path.match(/^\/plugins\/([^/]+)/)
  if (legacyPlugin) {
    return {
      kind: 'library',
      section: 'plugins',
      view: 'list',
      pluginId: decodeURIComponent(legacyPlugin[1]),
    }
  }
  if (path === '/plugins') return { kind: 'library', section: 'plugins', view: 'list' }
  if (path === '/terminal') return { kind: 'host', section: 'terminal' }

  return { kind: 'overview' }
}

export function pathOf(route: Route): string {
  switch (route.kind) {
    case 'instance':
      return route.section === 'plugins' && route.tab === 'browse'
        ? `/i/${encodeURIComponent(route.id)}/plugins/browse`
        : `/i/${encodeURIComponent(route.id)}/${route.section}`
    case 'host':
      return `/host/${route.section}`
    case 'library':
      return route.pluginId
        ? `/library/plugins/list/${encodeURIComponent(route.pluginId)}`
        : `/library/${route.section}/${route.view}`
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
      return (
        b.kind === 'instance' && a.id === b.id && a.section === b.section && a.tab === b.tab
      )
    case 'host':
      return b.kind === 'host' && a.section === b.section
    case 'library':
      return (
        b.kind === 'library' &&
        a.section === b.section &&
        a.view === b.view &&
        a.pluginId === b.pluginId
      )
    case 'settings':
      return b.kind === 'settings' && a.section === b.section
    default:
      return true
  }
}
