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
  | 'config-history'
  | 'settings'

/** The shared-asset pages. Stock, as opposed to what one server has chosen. */
export type LibrarySection = 'cores' | 'java' | 'database' | 'plugins' | 'schematics'

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
  | 'queue'
  | 'databases'
  | 'engines'

/** Pages about the machine. `terminal` is the shell and is fenced off. */
export type HostSection = 'metrics' | 'instances' | 'disk' | 'config' | 'terminal'

/** Panel-wide settings. Sections a sub-user should not see live elsewhere. */
export type SettingsSection = 'devices' | 'security' | 'update' | 'plugins'

/** Which states the 所有实例 list is showing. Part of the URL. */
export type StateFilter = 'all' | 'live' | 'stopped' | 'problem'

export type Route =
  | { kind: 'overview' }
  | { kind: 'instances'; query: string; state: StateFilter }
  /**
   * Which proxy stands in front of which servers, as a picture you draw on.
   *
   * A page of its own rather than a section of one instance, because a link is
   * a fact about two instances and neither of them owns it — putting it on the
   * proxy would hide it from everyone looking at the server, and the other way
   * round would be worse.
   */
  | { kind: 'network' }
  /**
   * The creation wizard. A page rather than a dialog because it is five steps
   * long and two of them start a download that outlives the click — a modal
   * you can dismiss by pressing Escape is the wrong container for that.
   */
  | { kind: 'new-instance' }
  | { kind: 'instance'; id: string; section: InstanceSection }
  | {
      kind: 'library'
      section: LibrarySection
      view: LibraryView
      pluginId?: string
      /**
       * Which build the 建筑列表 page has open, as a drawer over the list.
       *
       * Its own field rather than sharing pluginId: the two open different
       * drawers on different pages, and one name covering both is the kind of
       * saving that turns into "why does a plugin id open a schematic".
       */
      schemId?: string
      /**
       * Which instances 插件市场 judges compatibility against.
       *
       * A view filter, not a destination — nothing is installed anywhere from
       * that page. It lives in the URL because "兼容" is not a property of a
       * plugin but of a plugin and a server, so a link to the discovery page
       * without it is a link to a page with no badges on it at all.
       *
       * Plural because one operator's answer is often "does this fit the four
       * servers I run", and because a single-server reference made the common
       * case — a fleet on the same version — into four separate visits.
       */
      against?: string[]
    }
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
  { id: 'plugins', label: '插件' },
  { id: 'properties', label: '服务器配置' },
  // Right after 服务器配置, because that is where the question comes from:
  // you edit a file, the server stops booting, and the next thing you want is
  // what the file looked like yesterday.
  { id: 'config-history', label: '配置历史' },
  { id: 'settings', label: '实例设置' },
]

/**
 * The same list, named for what this instance actually is.
 *
 * Only one label differs, and it is the one that would otherwise lie: a proxy
 * has no server.properties, so 服务器配置 points at a page about velocity.toml.
 * The section id stays 'properties' — it is in every bookmark, and renaming a
 * route to fix a label would break links for the servers it was right about.
 */
export function instanceSections(
  kind: string | undefined,
): { id: InstanceSection; label: string }[] {
  if (kind !== 'proxy') return INSTANCE_SECTIONS
  return INSTANCE_SECTIONS.map((section) =>
    section.id === 'properties' ? { ...section, label: '代理配置' } : section,
  )
}

/** In build order: Java runs the core, the core loads the plugins, and the
 *  database is what a plugin asks for once it is loaded. The navigation group
 *  and the command palette both read this, so someone setting a server up for
 *  the first time meets the four in the order they need them. */
export const LIBRARY_SECTIONS: { id: LibrarySection; label: string }[] = [
  { id: 'java', label: 'Java 环境' },
  { id: 'cores', label: '服务端核心' },
  { id: 'database', label: '数据库环境' },
  { id: 'plugins', label: '插件库' },
  // Last, because it is the only shelf a server does not need to start: Java,
  // the core and the plugins are what a server *is*, and a building is what
  // somebody puts inside one afterwards.
  { id: 'schematics', label: '建筑库' },
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
  // Three pages, and the order is the order of the questions: what databases
  // do I have, what engines are they built on, and how do I get another engine.
  // The engine list comes second because after the first install it is the page
  // you never open again — it is where you go to free disk space.
  database: [
    { id: 'databases', label: '我的数据库' },
    { id: 'engines', label: '已装引擎' },
    { id: 'install', label: '安装引擎' },
  ],
  // Three pages, and they are three questions rather than three lists: 插件列表
  // is "what is the state of what I run", 插件市场 is "is this worth
  // installing", 下载队列 is "where did the five I just asked for get to".
  //
  // The third only became a page when downloads stopped being one at a time.
  // A single download is a status line; five of them, some queued, some done,
  // one failed an hour ago, is a list — and a list that appeared and vanished
  // inside another page would take that page's layout with it every time.
  //
  // 插件源 used to be one of these, and it was not a page — it was two unrelated
  // things wearing one heading. Adding a GitHub repository is an *action*, and
  // it belongs with the other three ways a plugin gets into the library, which
  // is the + 添加插件 menu on 插件列表. The token, the download mirror and the
  // retention default are *configuration*, panel-wide, changed once, and they
  // belong in panel settings with the rest of the things you set once.
  plugins: [
    { id: 'list', label: '插件列表' },
    { id: 'browse', label: '插件市场' },
    { id: 'queue', label: '下载队列' },
  ],
  // The same three questions the plugin shelf asks, minus the queue: a
  // schematic is a few hundred kilobytes, so a download is over before there is
  // anything to queue. 索引源 is a page rather than panel settings — unlike the
  // GitHub token, which is one credential shared by everything, a build source
  // is a *shelf* somebody added, and adding one is browsing rather than
  // configuring.
  schematics: [
    { id: 'list', label: '建筑列表' },
    { id: 'browse', label: '建筑市场' },
    { id: 'source', label: '索引源' },
  ],
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

export const SETTINGS_SECTIONS: {
  id: SettingsSection
  label: string
  /** Extra terms the command palette matches on, for a page people look for
   *  under a word that is not in its name — nobody hunting for where the
   *  access token lives searches for "集成". */
  keywords?: string
}[] = [
  { id: 'devices', label: '已配对设备' },
  { id: 'security', label: '登录记录' },
  { id: 'plugins', label: 'GitHub 集成', keywords: 'github token 令牌 私有仓库 下载源 镜像' },
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
 * One step up the trail.
 *
 * Not what the top bar's 返回 does — that undoes your last move — but where it
 * points when there is no last move to undo: a pasted link, a fresh tab, the
 * first page after a reload. Every scope has a first page that stands for the
 * whole thing — the console, the shelf, 监控 — so climbing out of one lands
 * there first and leaves the scope on the next press.
 */
export function parentOf(route: Route): Route | null {
  switch (route.kind) {
    case 'instances':
    case 'network':
      return { kind: 'overview' }
    case 'new-instance':
      return { kind: 'instances', query: '', state: 'all' }
    case 'instance':
      return route.section === 'console'
        ? { kind: 'instances', query: '', state: 'all' }
        : { kind: 'instance', id: route.id, section: 'console' }
    case 'library': {
      const home = defaultView(route.section)
      if (route.pluginId || route.schemId) {
        return { kind: 'library', section: route.section, view: 'list' }
      }
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
  return readRoute(window.location.pathname, window.location.search)
}

/**
 * The same reading, for a path the browser is not currently at.
 *
 * Used for the entry you came from, which the top bar's 返回 has to be able to
 * name and link to while you are still standing somewhere else.
 */
export function routeFromPath(href: string): Route {
  const cut = href.indexOf('?')
  return cut === -1 ? readRoute(href, '') : readRoute(href.slice(0, cut), href.slice(cut))
}

function readRoute(path: string, search: string): Route {
  const params = new URLSearchParams(search)

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

  const library = path.match(/^\/library(?:\/([^/]+))?(?:\/([^/]+))?(?:\/([^/]+))?/)
  if (library) {
    const section = pick(LIBRARY_SECTIONS, library[1] ?? '', 'cores')
    const second = library[2] ?? ''
    // Bookmarks and links from before 插件源 was taken apart. Its two halves
    // went to two different places; the configuration half is the one anybody
    // had a link to.
    if (section === 'plugins' && second === 'source') {
      return { kind: 'settings', section: 'plugins' }
    }
    const view = LIBRARY_VIEWS[section].find((entry) => entry.id === second)?.id
    if (view) {
      if (section === 'plugins' && view === 'list' && library[3]) {
        return { kind: 'library', section, view, pluginId: decodeURIComponent(library[3]) }
      }
      if (section === 'schematics' && view === 'list' && library[3]) {
        return { kind: 'library', section, view, schemId: decodeURIComponent(library[3]) }
      }
      const against = (params.get('against') ?? '')
        .split(',')
        .map((entry) => decodeURIComponent(entry).trim())
        .filter((entry) => entry !== '')
      return view === 'browse' && against.length > 0
        ? { kind: 'library', section, view, against }
        : { kind: 'library', section, view }
    }
    // A plugin used to live directly under its section — /library/plugins/<id>
    // — which is what every bookmark and every link in an older release says.
    // The views took that slot, so anything that is not one of them is still
    // read as a plugin id. A build never lived there, but the same shape is
    // the obvious thing to type by hand, so it is read the same way.
    if (section === 'plugins' && second) {
      return { kind: 'library', section, view: 'list', pluginId: decodeURIComponent(second) }
    }
    if (section === 'schematics' && second) {
      return { kind: 'library', section, view: 'list', schemId: decodeURIComponent(second) }
    }
    return { kind: 'library', section, view: defaultView(section) }
  }

  if (path === '/network') return { kind: 'network' }

  // Ahead of the list below, which would otherwise swallow it: /instances/…
  // is the list with a filter, and 新建 is not a filter.
  if (path === '/instances/new') return { kind: 'new-instance' }

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
    // The plugin source was a page of its own twice — under 面板设置, then
    // under 插件库 — before it turned out to be two things: an action, which
    // is now a menu item on 插件列表, and configuration, which is here.
    if (section === 'plugin-source') return { kind: 'settings', section: 'plugins' }
    return { kind: 'settings', section: pick(SETTINGS_SECTIONS, section, 'devices') }
  }

  if (path === '/java') return { kind: 'library', section: 'java', view: 'installed' }
  if (path === '/databases' || path === '/database') {
    return { kind: 'library', section: 'database', view: 'databases' }
  }
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
  if (path === '/schematics') return { kind: 'library', section: 'schematics', view: 'list' }
  if (path === '/terminal') return { kind: 'host', section: 'terminal' }

  return { kind: 'overview' }
}

export function pathOf(route: Route): string {
  switch (route.kind) {
    case 'instance':
      return `/i/${encodeURIComponent(route.id)}/${route.section}`
    case 'host':
      return `/host/${route.section}`
    case 'library': {
      if (route.pluginId) {
        return `/library/plugins/list/${encodeURIComponent(route.pluginId)}`
      }
      if (route.schemId) {
        return `/library/schematics/list/${encodeURIComponent(route.schemId)}`
      }
      const base = `/library/${route.section}/${route.view}`
      return route.view === 'browse' && route.against?.length
        ? `${base}?against=${route.against.map(encodeURIComponent).join(',')}`
        : base
    }
    case 'settings':
      return `/settings/${route.section}`
    case 'new-instance':
      return '/instances/new'
    case 'network':
      return '/network'
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
      return (
        b.kind === 'library' &&
        a.section === b.section &&
        a.view === b.view &&
        a.pluginId === b.pluginId &&
        a.schemId === b.schemId
      )
    case 'settings':
      return b.kind === 'settings' && a.section === b.section
    default:
      return true
  }
}
