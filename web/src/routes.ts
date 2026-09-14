import type { Capability, DownloadKind } from './types'
import { CAP } from './useCan'

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
  | 'network'
  | 'properties'
  | 'config-history'
  | 'startup'
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
 * for, was the one you had to scroll past the others to read. So they became
 * pages of their own, and opening a section opens *them* (see Scope).
 *
 * Java 环境, 数据库环境 and 服务端核心 have since gone back to one page each,
 * because the thing that made the scroll long was the catalogue's own layout
 * rather than the number of jobs on the page — see the note on LIBRARY_VIEWS.
 * The union keeps the ids they retired — 'download', 'install', 'engines':
 * nothing renders them, but LIBRARY_VIEWS is what parse() matches against, so
 * an old /library/cores/download falls through to defaultView and redirects
 * instead of 404ing. 'source' and the rest are still live on other shelves.
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

/**
 * Panel-wide settings.
 *
 * `devices` is first and has no capability: every account manages its own
 * pairings, so there is always somewhere for 面板设置 to lead. `appearance` has
 * none either, for the same reason from the other end — it is a preference in
 * one browser, it changes nothing anyone else can see, and the account that can
 * do the least still has to be able to turn the pixel face off.
 */
export type SettingsSection =
  | 'devices'
  | 'security'
  | 'users'
  | 'update'
  | 'plugins'
  | 'appearance'

/** Which states the 所有实例 list is showing. Part of the URL. */
export type StateFilter = 'all' | 'live' | 'stopped' | 'problem'

export type Route =
  | { kind: 'overview' }
  | { kind: 'instances'; query: string; state: StateFilter }
  /**
   * Which proxy stands in front of which servers — the whole machine at once.
   *
   * This was the panel-wide page, on the strength of a link being a fact about
   * two instances that neither of them owns. That was right about the fact and
   * wrong about the navigation: it put a page about proxies at the top level of
   * a panel most of whose users run none, and it was the one destination up
   * there that was not a place but a relationship.
   *
   * A link is now a section of *both* ends — see INSTANCE_SECTIONS — which is
   * what the original objection actually asked for: the proxy sees what is
   * behind it, the server sees what is in front of it, and neither owns the
   * link. What is left here is the path itself, kept because it is in
   * bookmarks and in the command palette; App redirects it to whichever end is
   * the obvious one to open (see the effect there).
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
  /**
   * What the panel is downloading. Panel-wide, because a download belongs to
   * no scope — see Scope below — and it used to live inside 插件库 as 下载队列
   * while three other shelves each kept their own invisible one.
   */
  | { kind: 'downloads'; only?: DownloadKind }
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

export const INSTANCE_SECTIONS: { id: InstanceSection; label: string; cap: Capability }[] = [
  // Each names what it takes to *open* the page, not what every button on it
  // does. 控制台 is the clearest case: watching is 查看服务器, typing is a
  // second capability the socket checks per message, so a role with only the
  // first still belongs here — it just gets a read-only console.
  { id: 'console', label: '控制台', cap: CAP.instanceView },
  { id: 'metrics', label: '监控', cap: CAP.instanceView },
  { id: 'files', label: '文件', cap: CAP.instanceFilesRead },
  { id: 'plugins', label: '插件', cap: CAP.instanceView },
  // Before 服务器配置 rather than after it, so the pair below stays a pair:
  // connecting an instance to a proxy is six edits to the two ends' config
  // files, so this is the page you visit *instead of* hand-editing them, and
  // it belongs on the way in rather than between the file and its history.
  //
  // Its capability is a panel-wide one even though the page now lives inside an
  // instance: a link is a fact about two servers and neither owns it, so it is
  // not narrowed by the instance grant either. See docs/security.md.
  { id: 'network', label: '代理连线', cap: CAP.panelNetwork },
  { id: 'properties', label: '服务器配置', cap: CAP.instanceView },
  // Right after 服务器配置, because that is where the question comes from:
  // you edit a file, the server stops booting, and the next thing you want is
  // what the file looked like yesterday.
  { id: 'config-history', label: '配置历史', cap: CAP.instanceHistory },
  // Before 实例设置 and not inside it: what a server runs is a different
  // question from what it is called, and it is the only half of that form
  // anybody opens twice — once to pick a jar, then again on every heap and
  // GC change after that. They stay adjacent because whoever wants one
  // often wants the other in the same sitting.
  { id: 'startup', label: '启动方式', cap: CAP.instanceSettings },
  { id: 'settings', label: '实例设置', cap: CAP.instanceSettings },
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
): { id: InstanceSection; label: string; cap: Capability }[] {
  if (kind !== 'proxy') return INSTANCE_SECTIONS
  return INSTANCE_SECTIONS.map((section) =>
    section.id === 'properties' ? { ...section, label: '代理配置' } : section,
  )
}

/** In build order: Java runs the core, the core loads the plugins, and the
 *  database is what a plugin asks for once it is loaded. The navigation group
 *  and the command palette both read this, so someone setting a server up for
 *  the first time meets the four in the order they need them. */
export const LIBRARY_SECTIONS: { id: LibrarySection; label: string; cap: Capability }[] = [
  { id: 'java', label: 'Java 环境', cap: CAP.panelJava },
  { id: 'cores', label: '服务端核心', cap: CAP.libraryCores },
  { id: 'database', label: '数据库环境', cap: CAP.panelDatabases },
  { id: 'plugins', label: '插件库', cap: CAP.libraryPlugins },
  // Last, because it is the only shelf a server does not need to start: Java,
  // the core and the plugins are what a server *is*, and a building is what
  // somebody puts inside one afterwards.
  { id: 'schematics', label: '建筑库', cap: CAP.librarySchematics },
]

/** The pages inside each library section, in order. The first is the default —
 *  always "what you already have", never a form. */
export const LIBRARY_VIEWS: Record<LibrarySection, { id: LibraryView; label: string }[]> = {
  // One page, back from two, and for the same reason as the two below.
  //
  // 核心库 first and 下载核心 as a page of its own was right about the ordering
  // and wrong about the cost: it left the shelf alone on a 1440px screen with
  // two rows on it, and put a navigation step in front of the one thing you
  // come to this shelf to do. What made the split look necessary elsewhere —
  // a catalogue a screen tall — was never true here: Paper and Velocity are
  // the whole project list (see internal/serverjar), so the chooser is one row
  // of two tiles, and the versions under it are capped at 168px. The order of
  // the cards carries what the split was protecting: what you have first,
  // never a form.
  cores: [{ id: 'stock', label: '服务端核心' }],
  // One page, back from three.
  //
  // Splitting them (see the note on LibraryView) was right about the ordering
  // and wrong about the cost: with 已安装 first and 安装新版本 / 下载设置 as
  // pages of their own, every one of the three carried a single card and left
  // two thirds of a 1440px screen empty. What made the split necessary was
  // that the catalogue and the settings were each a screen tall — a grid of
  // large chooser tiles, and a form. They are not any more: the catalogue is
  // one line per build, and the distribution and mirror are two selects in
  // that card's head. So all of it fits above the fold, and what the split was
  // protecting — 已安装 first, never a form — is protected by the order of the
  // cards instead.
  java: [{ id: 'installed', label: 'Java 环境' }],
  database: [{ id: 'databases', label: '数据库环境' }],
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
  /** What an account needs to open this page. Absent means everybody. */
  cap?: Capability
}[] = [
  { id: 'devices', label: '已配对设备' },
  { id: 'security', label: '登录记录', cap: CAP.panelSecurity },
  {
    id: 'users',
    label: '账号与角色',
    keywords: '用户 权限 成员 运维 开发 授权 实例授权',
    cap: CAP.panelUsers,
  },
  {
    id: 'plugins',
    label: 'GitHub 集成',
    keywords: 'github token 令牌 私有仓库 下载源 镜像',
    cap: CAP.panelSettings,
  },
  { id: 'update', label: '面板更新', cap: CAP.panelUpdate },
  { id: 'appearance', label: '外观', keywords: '字体 像素 minecraft 主题 深色 浅色' },
]

/**
 * Which capability each 主机 page needs. 主机 is one sidebar row leading to
 * five pages, so the row points at the first of these the account can open.
 */
export const HOST_ENTRY_CAPS: [HostSection, Capability][] = [
  ['metrics', CAP.panelSystem],
  ['instances', CAP.panelSystem],
  ['disk', CAP.panelSystem],
  ['config', CAP.panelTerminal],
  ['terminal', CAP.panelTerminal],
]

const STATE_FILTERS: StateFilter[] = ['all', 'live', 'stopped', 'problem']

/** The shelves 下载 can be narrowed to. Mirrors download.Kind. */
const DOWNLOAD_KINDS: DownloadKind[] = ['core', 'java', 'database', 'plugin']

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
 * and the two ways back out (the exit at the foot of the sidebar and the top
 * bar's back button) all have to agree on the string or the animation silently
 * does nothing.
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
    case 'downloads':
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
    // 下载队列 left 插件库 for the top level: it was never only about plugins,
    // and three other shelves were quietly keeping their own.
    //
    // This has to come *before* the plugin-id fallback further down, not just
    // instead of an entry in LIBRARY_VIEWS. Without it an old bookmark falls
    // through to that branch and the panel goes looking for a plugin called
    // "queue".
    if (section === 'plugins' && second === 'queue') {
      return { kind: 'downloads' }
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

  if (path === '/downloads') {
    // The filter is a query parameter, like every other list filter here: it
    // scopes what you are looking at rather than naming a different page.
    const only = params.get('kind') ?? ''
    return DOWNLOAD_KINDS.includes(only as DownloadKind)
      ? { kind: 'downloads', only: only as DownloadKind }
      : { kind: 'downloads' }
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
    case 'downloads':
      return route.only ? `/downloads?kind=${route.only}` : '/downloads'
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
    case 'downloads':
      // The filter is a list filter, so it does not make a different page —
      // same rule as the instance list's search and state chips.
      return b.kind === 'downloads'
    default:
      return true
  }
}
