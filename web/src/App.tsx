import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { MouseEvent } from 'react'

import { collectAlerts } from './alerts'
import { ApiError, api } from './api'
import { ChangePasswordDialog } from './components/ChangePasswordDialog'
import { CommandPalette } from './components/CommandPalette'
import { CoreLibraryPage } from './components/CoreLibraryPage'
import { Dashboard } from './components/Dashboard'
import { HostPage } from './components/HostPage'
import { HostTerminal } from './components/HostTerminal'
import { ImportInstanceDialog } from './components/ImportInstanceDialog'
import { InstanceList } from './components/InstanceList'
import { InstanceView } from './components/InstanceView'
import { JavaPage } from './components/JavaPage'
import { Login } from './components/Login'
import { NewInstanceDialog } from './components/NewInstanceDialog'
import { PluginDetailPage } from './components/PluginDetailPage'
import { PluginLibraryPage } from './components/PluginLibraryPage'
import { PluginSourceSettings } from './components/PluginSourceSettings'
import { SettingsPage } from './components/SettingsPage'
import { Sidebar } from './components/Sidebar'
import { TopBar } from './components/TopBar'
import type { Crumb } from './components/TopBar'
import {
  HOST_SECTIONS,
  INSTANCE_SECTIONS,
  LIBRARY_SECTIONS,
  LIBRARY_VIEWS,
  SETTINGS_SECTIONS,
  defaultView,
  navKeyOf,
  parentOf,
  pathOf,
  routeFromLocation,
  scopeOf,
} from './routes'
import type { InstanceSection, LibrarySection, LibraryView, Route, StateFilter } from './routes'
import { captureScope } from './scopeMorph'
import type { InstanceStatus, User } from './types'
import { mergeState } from './types'
import { useCores } from './useCores'
import { useJava } from './useJava'
import { useMediaQuery } from './useMediaQuery'
import { usePlugins } from './usePlugins'
import { useRecents } from './useRecents'
import { useSystem } from './useSystem'
import { useTerminal } from './useTerminal'
import { updateLabel, useUpdate } from './useUpdate'

/** How often the instance list refreshes; the console pushes state instantly,
 *  this is only to keep the sidebar honest for servers you are not watching. */
const POLL_INTERVAL_MS = 5000

/** Below this the sidebar cannot sit beside the content without taking a third
 *  of it, so it becomes a drawer. A phone matters more here than in most back
 *  offices — the person who owns the server reads the alert away from a desk —
 *  so the breakpoint is generous. Kept in step with styles.css: CSS decides how
 *  it looks, this decides what the button does. */
const DRAWER_QUERY = '(max-width: 1024px)'

/** Whether the desktop sidebar is folded to icons. A per-device preference,
 *  like the theme, so it lives next to it in localStorage. */
const RAIL_KEY = 'hypercraft.sidebar'

/**
 * Click handling for a link that navigates inside the panel.
 *
 * The routes are real paths already, so the navigation is an anchor with a
 * real href: ⌘-click opens a server in a second tab, right-click copies its
 * link, the status bar shows where a row goes. Only a plain left click is
 * taken over — every other click is the browser being asked for something it
 * does better than we would.
 */
function follow(go: () => void) {
  return (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    go()
  }
}

function labelOf<T extends string>(list: { id: T; label: string }[], id: T): string {
  return list.find((entry) => entry.id === id)?.label ?? ''
}

/**
 * Where you are, as the top bar says it.
 *
 * Every page scrolls its own heading out of sight, so this is the only thing on
 * screen that still answers "which instance am I in" once you are three screens
 * into a file listing. A step links only when it is somewhere you can go back
 * to — the last one is where you already are.
 */
function crumbsFor(
  route: Route,
  selected: InstanceStatus | null,
  pluginName: string | undefined,
  link: (route: Route) => Pick<Crumb, 'href' | 'onClick'>,
): Crumb[] {
  switch (route.kind) {
    case 'instances':
      return [{ label: '概览', ...link({ kind: 'overview' }) }, { label: '所有实例' }]
    case 'instance': {
      const section = labelOf(INSTANCE_SECTIONS, route.section)
      return [
        { label: '所有实例', ...link({ kind: 'instances', query: '', state: 'all' }) },
        selected
          ? {
              label: selected.name,
              state: selected.state,
              ...link({ kind: 'instance', id: route.id, section: 'console' }),
            }
          : { label: '实例' },
        { label: section },
      ]
    }
    case 'library': {
      const section = labelOf(LIBRARY_SECTIONS, route.section)
      const home: Route = {
        kind: 'library',
        section: route.section,
        view: defaultView(route.section),
      }
      const base: Crumb[] = [{ label: '资源库' }]
      if (pluginName) {
        return [...base, { label: section, ...link(home) }, { label: pluginName }]
      }
      const view = labelOf(LIBRARY_VIEWS[route.section], route.view)
      // The first page of a section is the section: repeating its name under
      // itself would be a step that goes nowhere.
      return route.view === defaultView(route.section)
        ? [...base, { label: section }]
        : [...base, { label: section, ...link(home) }, { label: view }]
    }
    case 'host':
      return [
        { label: '主机', ...link({ kind: 'host', section: 'metrics' }) },
        { label: labelOf(HOST_SECTIONS, route.section) },
      ]
    case 'settings':
      return [{ label: '面板设置' }, { label: labelOf(SETTINGS_SECTIONS, route.section) }]
    default:
      return [{ label: '概览' }]
  }
}

/** What the back button says it goes to. The trail already names every step;
 *  this is the one step that has to fit in a tooltip. */
function labelOfRoute(route: Route, instances: InstanceStatus[]): string {
  switch (route.kind) {
    case 'instances':
      return '所有实例'
    case 'instance':
      return instances.find((item) => item.id === route.id)?.name ?? '实例'
    case 'library':
      return labelOf(LIBRARY_SECTIONS, route.section)
    case 'host':
      return '主机'
    case 'settings':
      return '面板设置'
    default:
      return '概览'
  }
}

export default function App() {
  const [user, setUser] = useState<User | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [instances, setInstances] = useState<InstanceStatus[]>([])
  const [route, setRoute] = useState<Route>(routeFromLocation)
  const [showNew, setShowNew] = useState(false)
  const [showImport, setShowImport] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  const compact = useMediaQuery(DRAWER_QUERY)
  const [navOpen, setNavOpen] = useState(false)
  const [railed, setRailed] = useState(() => window.localStorage.getItem(RAIL_KEY) === 'rail')
  const sidebarRef = useRef<HTMLElement | null>(null)
  const navToggle = useRef<HTMLButtonElement | null>(null)

  const signedIn = Boolean(user)
  // Polled at the app level rather than inside the pages that show them: all
  // four are long-running daemon jobs that keep going after you navigate away,
  // and the sidebar says so while they do.
  const update = useUpdate(signedIn)
  const java = useJava(signedIn)
  const cores = useCores(signedIn)
  const plugins = usePlugins(signedIn)
  const system = useSystem(signedIn)
  // Not polled, unlike the others: nothing turns the terminal on but a person
  // clicking the switch, and that path already refreshes the status.
  const terminal = useTerminal(signedIn)
  const { recents, remember } = useRecents()

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setCheckingSession(false))
  }, [])

  useEffect(() => {
    const onPop = () => setRoute(routeFromLocation())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  const navigate = useCallback((next: Route, replace = false) => {
    setRoute(next)
    const path = pathOf(next)
    if (replace) window.history.replaceState(null, '', path)
    else window.history.pushState(null, '', path)
    // A drawer covers what you just navigated to, so picking something is also
    // how you dismiss it. On a rail there is nothing to dismiss.
    setNavOpen(false)
  }, [])

  useEffect(() => {
    window.localStorage.setItem(RAIL_KEY, railed ? 'rail' : 'full')
  }, [railed])

  // Widening the window puts the sidebar back on screen for good; a drawer left
  // "open" in that state would only mean a stray scrim.
  useEffect(() => {
    if (!compact) setNavOpen(false)
  }, [compact])

  useEffect(() => {
    if (route.kind === 'instance') remember(route.id)
  }, [route, remember])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const typing =
        target?.isContentEditable ||
        (target != null && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))

      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        // Deliberately works while typing: ⌘K from the console command line is
        // exactly when you want to be somewhere else.
        event.preventDefault()
        setPaletteOpen((open) => !open)
        return
      }
      if (event.key === 'Escape') {
        setNavOpen(false)
        return
      }
      if (event.key !== '[' || event.metaKey || event.ctrlKey || event.altKey) return
      // The console command line and the host terminal are both real text
      // inputs where '[' is a character, not a shortcut.
      if (typing) return
      event.preventDefault()
      setRailed((value) => !value)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Focus follows the drawer: into it when it opens, back to the button that
  // opened it when it closes, so a keyboard never lands behind the scrim.
  const wasOpen = useRef(false)
  useEffect(() => {
    if (navOpen) sidebarRef.current?.focus()
    else if (wasOpen.current) navToggle.current?.focus()
    wasOpen.current = navOpen
  }, [navOpen])

  const openInstance = useCallback(
    (id: string, section: InstanceSection = 'console') =>
      navigate({ kind: 'instance', id, section }),
    [navigate],
  )

  const openLibrary = useCallback(
    (section: LibrarySection, view: LibraryView) => navigate({ kind: 'library', section, view }),
    [navigate],
  )

  const refresh = useCallback(async () => {
    try {
      const fetched = await api.listInstances()
      setInstances((prev) => {
        const next = fetched.map((item) => {
          const existing = prev.find((p) => p.id === item.id)
          return existing ? mergeState(existing, item) : item
        })
        // mergeState hands back the object it was given when nothing moved, so
        // an idle poll produces the same instances in the same order — and
        // returning `prev` for that case is what turns it into no render at
        // all. Building `next` first and throwing it away costs a few object
        // spreads every five seconds; not doing it costs a full re-render of
        // whatever page is open, at the same cadence, all day.
        const unchanged =
          next.length === prev.length && next.every((item, index) => item === prev[index])
        return unchanged ? prev : next
      })
      setLoadError(null)
    } catch (err) {
      if (err instanceof ApiError && err.isUnauthorized) {
        setUser(null)
        return
      }
      setLoadError(err instanceof Error ? err.message : '加载失败')
    }
  }, [])

  useEffect(() => {
    if (!user) return
    void refresh()
    const timer = window.setInterval(() => void refresh(), POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [user, refresh])

  // Console state arrives faster than the poll; merge it in without waiting.
  const applyInstance = useCallback((updated: InstanceStatus) => {
    setInstances((prev) =>
      prev.map((item) => (item.id === updated.id ? mergeState(item, updated) : item)),
    )
  }, [])

  const alerts = useMemo(
    () =>
      collectAlerts({
        instances,
        system: system.info,
        update: update.status,
        pluginUpdates: plugins.updates,
      }),
    [instances, system.info, update.status, plugins.updates],
  )

  const signOut = async () => {
    try {
      await api.logout()
    } finally {
      setUser(null)
      setInstances([])
    }
  }

  if (checkingSession) {
    return <div className="boot">正在检查登录状态…</div>
  }
  if (!user) {
    return <Login onSignedIn={setUser} />
  }

  const selectedId = route.kind === 'instance' ? route.id : null
  const selected = instances.find((item) => item.id === selectedId) ?? null
  // Matches the backend's State.Running(), which is what decides the list of
  // servers recorded for resume — so the update dialog promises exactly what
  // will happen.
  const runningNames = instances
    .filter((item) => item.state === 'running' || item.state === 'starting')
    .map((item) => item.name)
  // A plugin id from the URL that no longer exists (deleted, or a stale
  // bookmark) falls back to the list rather than to an error page.
  const openedPlugin =
    route.kind === 'library' && route.section === 'plugins' && route.pluginId
      ? (plugins.plugins.find((item) => item.id === route.pluginId) ?? null)
      : null
  const updateNotice = updateLabel(update.status)
  const crumbs = crumbsFor(route, selected, openedPlugin?.name, (target) => ({
    href: pathOf(target),
    onClick: follow(() => navigate(target)),
  }))
  const scope = scopeOf(route)

  // One step up the trail, for the top bar's back button. When that step leaves
  // the scope it is exactly the movement 返回上级 in the sidebar makes, so it
  // is captured the same way and the header shrinks back into the row it came
  // from — the button being somewhere else does not make it a different act.
  const parent = parentOf(route)
  const goBack = parent
    ? () => {
        if (scopeOf(parent) !== scope) {
          const key = navKeyOf(route)
          if (key) captureScope(key)
        }
        navigate(parent)
      }
    : null

  return (
    <div
      className="app"
      data-nav={compact && navOpen ? 'open' : undefined}
      data-rail={!compact && railed ? 'on' : undefined}
    >
      {/* First thing in the tab order, visible only once it has focus: the
          sidebar is a dozen-odd stops on a keyboard, and the content is behind
          all of them. */}
      <a className="skip" href="#main">
        跳到主内容
      </a>

      <Sidebar
        route={route}
        scope={scope}
        compact={compact}
        railed={!compact && railed}
        onToggleRail={() => setRailed((on) => !on)}
        navigate={navigate}
        follow={follow}
        instances={instances}
        recents={recents}
        user={user}
        system={system.info}
        updateNotice={updateNotice}
        alertCount={alerts.length}
        java={java}
        cores={cores}
        plugins={plugins}
        terminal={terminal}
        onCreate={() => setShowNew(true)}
        onOpenPalette={() => setPaletteOpen(true)}
        sidebarRef={sidebarRef}
      />

      {/* Only ever visible under an open drawer; it is what makes "tap the page
          to dismiss" work, and it stops clicks reaching what it covers. */}
      <div className="scrim" aria-hidden="true" onClick={() => setNavOpen(false)} />

      <div className="shell">
        <TopBar
          crumbs={crumbs}
          user={user}
          compact={compact}
          navOpen={navOpen}
          onToggleNav={() => setNavOpen((open) => !open)}
          toggleRef={navToggle}
          onBack={goBack}
          backHref={parent ? pathOf(parent) : null}
          backLabel={parent ? labelOfRoute(parent, instances) : null}
          onOpenPalette={() => setPaletteOpen(true)}
          onChangePassword={() => setShowPassword(true)}
          onSignOut={() => void signOut()}
        />

        <main className="main" id="main" tabIndex={-1}>
          {loadError && <div className="alert alert--error">{loadError}</div>}

          {route.kind === 'settings' ? (
            <SettingsPage section={route.section} update={update} runningNames={runningNames} />
          ) : route.kind === 'host' ? (
            route.section === 'terminal' ? (
              <HostTerminal
                terminal={terminal}
                onOpenSettings={() => navigate({ kind: 'host', section: 'config' })}
              />
            ) : (
              <HostPage
                section={route.section}
                system={system}
                instances={instances}
                terminal={terminal}
                onNavigate={navigate}
              />
            )
          ) : route.kind === 'library' ? (
            route.section === 'java' ? (
              <JavaPage
                java={java}
                view={route.view}
                onOpenView={(view) => openLibrary('java', view)}
                onOpenCores={() => openLibrary('cores', 'stock')}
              />
            ) : route.section === 'cores' ? (
              <CoreLibraryPage
                cores={cores}
                view={route.view}
                onOpenView={(view) => openLibrary('cores', view)}
                onOpenJava={() => openLibrary('java', 'installed')}
              />
            ) : openedPlugin ? (
              <PluginDetailPage
                key={openedPlugin.id}
                item={openedPlugin}
                plugins={plugins}
                onBack={() => openLibrary('plugins', 'list')}
              />
            ) : route.view === 'source' ? (
              <PluginSourceSettings plugins={plugins} />
            ) : (
              <PluginLibraryPage
                plugins={plugins}
                view={route.view}
                against={route.against}
                instances={instances}
                onOpenView={(view) => openLibrary('plugins', view)}
                onChooseAgainst={(id) =>
                  navigate(
                    { kind: 'library', section: 'plugins', view: 'browse', against: id },
                    true,
                  )
                }
                onOpenPlugin={(id) =>
                  navigate({ kind: 'library', section: 'plugins', view: 'list', pluginId: id })
                }
                onOpenSettings={() => openLibrary('plugins', 'source')}
                onOpenInstance={(id) => openInstance(id, 'plugins')}
              />
            )
          ) : route.kind === 'instances' ? (
            <InstanceList
              instances={instances}
              query={route.query}
              state={route.state}
              onFilter={(next: { query: string; state: StateFilter }) =>
                // Replaces rather than pushes: typing five characters into the
                // search box must not put five entries in the back stack.
                navigate({ kind: 'instances', ...next }, true)
              }
              onNavigate={navigate}
              onCreate={() => setShowNew(true)}
              onImport={() => setShowImport(true)}
              onChanged={applyInstance}
            />
          ) : route.kind === 'instance' ? (
            selected ? (
              <InstanceView
                key={selected.id}
                instance={selected}
                section={route.section}
                cores={cores}
                plugins={plugins}
                onChanged={applyInstance}
                onDeleted={() => {
                  navigate({ kind: 'instances', query: '', state: 'all' })
                  void refresh()
                }}
                onOpenSection={(section) => openInstance(route.id, section)}
                // Acquiring a plugin is a panel-wide act, so it happens in one
                // place. The instance travels along as the compatibility
                // reference, which is the context that would otherwise be lost
                // on the way there.
                onOpenBrowse={() =>
                  navigate({
                    kind: 'library',
                    section: 'plugins',
                    view: 'browse',
                    against: route.id,
                  })
                }
                onOpenCoreLibrary={() => openLibrary('cores', 'stock')}
              />
            ) : (
              <div className="alert">
                找不到这个实例，它可能已经被删除了。
                <button
                  className="link"
                  onClick={() => navigate({ kind: 'instances', query: '', state: 'all' })}
                >
                  回到实例列表
                </button>
              </div>
            )
          ) : (
            <Dashboard
              instances={instances}
              system={system.info}
              alerts={alerts}
              onSelect={openInstance}
              onCreate={() => setShowNew(true)}
              onNavigate={navigate}
              onChanged={applyInstance}
            />
          )}
        </main>
      </div>

      {paletteOpen && (
        <CommandPalette
          instances={instances}
          onClose={() => setPaletteOpen(false)}
          onNavigate={navigate}
          onCreate={() => {
            setPaletteOpen(false)
            setShowNew(true)
          }}
          onImport={() => {
            setPaletteOpen(false)
            setShowImport(true)
          }}
        />
      )}

      {showNew && (
        <NewInstanceDialog
          cores={cores}
          onCancel={() => setShowNew(false)}
          onCreated={(created) => {
            setShowNew(false)
            setInstances((prev) => [...prev, created])
            openInstance(created.id)
          }}
          onOpenLibrary={() => {
            setShowNew(false)
            openLibrary('cores', 'download')
          }}
        />
      )}

      {showImport && (
        <ImportInstanceDialog
          onCancel={() => setShowImport(false)}
          onImported={(created) => {
            setShowImport(false)
            setInstances((prev) => [...prev, created])
            openInstance(created.id)
          }}
        />
      )}

      {showPassword && (
        <ChangePasswordDialog
          onCancel={() => setShowPassword(false)}
          onChanged={() => {
            setShowPassword(false)
            setUser(null)
          }}
        />
      )}
    </div>
  )
}
