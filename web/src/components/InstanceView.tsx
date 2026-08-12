import { useEffect, useLayoutEffect, useRef, useState } from 'react'

import { DUR } from '../motion'
import type { InstanceSection } from '../routes'
import type { InstanceStatus } from '../types'
import type { CoreController } from '../useCores'
import type { PluginController } from '../usePlugins'
import { ConfigHistory } from './ConfigHistory'
import type { ConfigHistoryFocus } from './ConfigHistory'
import { ErrorBoundary } from './ErrorBoundary'
import type { FileJump } from './FileManager'
import { FileManager } from './FileManager'
import { InstanceCockpit } from './InstanceCockpit'
import { InstancePlugins } from './InstancePlugins'
import { LaunchSettings } from './LaunchSettings'
import { PropertiesEditor } from './PropertiesEditor'
import { ResourcePanel } from './ResourcePanel'
import { VelocityConfig } from './VelocityConfig'

interface Props {
  instance: InstanceStatus
  section: InstanceSection
  cores: CoreController
  /** The panel-wide plugin library: what this server can be given. */
  plugins: PluginController
  onChanged: (instance: InstanceStatus) => void
  onDeleted: () => void
  onOpenSection: (section: InstanceSection) => void
  /** Opens 插件市场 in the library, with this server as the compatibility
   *  reference. Downloading is panel-wide and happens there. */
  onOpenBrowse: () => void
  /** The panel-wide core library, for "download another one". */
  onOpenCoreLibrary: () => void
}

/**
 * One server, with its sidebar's section on screen.
 *
 * Sections used to be tabs inside this component; they are sidebar entries now
 * (see Sidebar), which is what makes the console a page rather than a tab and
 * keeps every part of one server at the same depth. What has not changed is
 * that a section, once opened, stays mounted behind whatever replaced it: 文件
 * and 监控 are two clicks apart and are exactly the pair you bounce between
 * while a server starts, and paying the fetch twice — plus losing the scroll
 * position and any open editor — was the whole cost of the old teardown.
 *
 * Lazily, though: mounting all six up front would fire six requests for panels
 * most sessions never open.
 */
export function InstanceView({
  instance,
  section,
  cores,
  plugins,
  onChanged,
  onDeleted,
  onOpenSection,
  onOpenBrowse,
  onOpenCoreLibrary,
}: Props) {
  const [visited, setVisited] = useState<Set<InstanceSection>>(
    () => new Set<InstanceSection>([section]),
  )
  // A directory another pane asked the file manager to open — the plugin
  // list's 配置 shortcut, which is most of what saves an operator from
  // hand-walking plugins/<Name>/ twenty times a week.
  const [jump, setJump] = useState<FileJump | undefined>()
  // The same handoff in the other direction: a file 配置历史 should answer for.
  // The two panes are two views of one directory — what it holds now, and what
  // it held before — and every question that starts in one ends in the other.
  const [historyFocus, setHistoryFocus] = useState<ConfigHistoryFocus | undefined>()

  const openSectionAt = (section: InstanceSection, mark: () => void) => {
    setVisited((prev) => new Set(prev).add(section))
    mark()
    onOpenSection(section)
  }
  // The section that was on screen a moment ago, kept visible over the new one
  // for the length of its exit. Without it a switch in the sidebar was a cut:
  // the outgoing pane was `hidden` in the same frame the incoming one appeared,
  // so the only motion in the whole content area was whatever the arriving
  // pane's own entrance had time to show underneath it.
  const [leaving, setLeaving] = useState<InstanceSection | null>(null)
  const shown = useRef(section)

  useEffect(() => {
    setVisited((prev) => (prev.has(section) ? prev : new Set(prev).add(section)))
  }, [section])

  useLayoutEffect(() => {
    if (shown.current === section) return
    const previous = shown.current
    shown.current = section
    setLeaving(previous)
    const timer = window.setTimeout(() => setLeaving(null), DUR.fast)
    return () => window.clearTimeout(timer)
  }, [section])

  // Switching servers is a remount — App keys this component on the instance
  // id — so nothing carries over and there is no reset to do here. There used
  // to be one, and on mount it raced the effect above and threw away the very
  // section the URL had asked for: a reload of /i/<id>/plugins rendered an
  // empty pane.

  return (
    <div className="instance">
      {/* The console is never torn down: its websocket and scrollback are the
          one thing in the panel that is expensive to lose. */}
      <Pane id="console" active={section === 'console'} leaving={leaving === 'console'}>
        <InstanceCockpit
          instance={instance}
          active={section === 'console'}
          onChanged={onChanged}
          onOpenSection={onOpenSection}
        />
      </Pane>

      {visited.has('metrics') && (
        <Pane id="metrics" active={section === 'metrics'} leaving={leaving === 'metrics'} scroll>
          <ResourcePanel instance={instance} active={section === 'metrics'} />
        </Pane>
      )}
      {visited.has('files') && (
        <Pane id="files" active={section === 'files'} leaving={leaving === 'files'} scroll>
          <FileManager
            instance={instance}
            jump={jump}
            onOpenHistory={(path) =>
              openSectionAt('config-history', () =>
                setHistoryFocus({ path, token: Date.now() }),
              )
            }
          />
        </Pane>
      )}
      {visited.has('plugins') && (
        <Pane id="plugins" active={section === 'plugins'} leaving={leaving === 'plugins'} scroll>
          <InstancePlugins
            instance={instance}
            plugins={plugins}
            onOpenBrowse={onOpenBrowse}
            onChanged={onChanged}
            onOpenSection={(target, path) => {
              if (target === 'files' && path) {
                setVisited((prev) => new Set(prev).add('files'))
                setJump({ path, token: Date.now() })
              }
              onOpenSection(target)
            }}
          />
        </Pane>
      )}
      {visited.has('properties') && (
        <Pane
          id="properties"
          active={section === 'properties'}
          leaving={leaving === 'properties'}
          scroll
        >
          {/* One instance answers to one of these, never to both: a proxy has
              no server.properties and a server has no velocity.toml, and the
              daemon refuses the wrong pair outright. */}
          {instance.kind === 'proxy' ? (
            <VelocityConfig instance={instance} />
          ) : (
            <PropertiesEditor instance={instance} />
          )}
        </Pane>
      )}
      {visited.has('config-history') && (
        <Pane
          id="config-history"
          active={section === 'config-history'}
          leaving={leaving === 'config-history'}
          scroll
        >
          <ConfigHistory
            instance={instance}
            focus={historyFocus}
            onOpenInFiles={(path) =>
              openSectionAt('files', () =>
                setJump({ path: parentOf(path), file: path, token: Date.now() }),
              )
            }
          />
        </Pane>
      )}
      {visited.has('settings') && (
        <Pane id="settings" active={section === 'settings'} leaving={leaving === 'settings'} scroll>
          <LaunchSettings
            instance={instance}
            cores={cores}
            onSaved={onChanged}
            onDeleted={onDeleted}
            onOpenLibrary={onOpenCoreLibrary}
          />
        </Pane>
      )}
    </div>
  )
}

/** The directory a file lives in, empty for one in the server root. Paths here
 *  are always relative and slash-separated — they come from the API, not from
 *  the host — so this needs no path library. */
function parentOf(path: string): string {
  const cut = path.lastIndexOf('/')
  return cut < 0 ? '' : path.slice(0, cut)
}

/** `hidden` rather than unmounted — and `hidden` specifically, because it is
 *  what keeps a background pane out of the tab order and off a screen reader.
 *  A pane on its way out is the one exception, and only for a tenth of a
 *  second: it is a picture of the page you just left, laid over the one
 *  arriving, so it is taken out of the tab order by hand instead. */
function Pane({
  id,
  active,
  leaving,
  scroll,
  children,
}: {
  id: InstanceSection
  active: boolean
  leaving: boolean
  scroll?: boolean
  children: React.ReactNode
}) {
  // Every other view in the panel gets its entrance from the fact that it was
  // just created; these were created the first time you opened them and are
  // only being un-hidden, which no CSS animation fires on. So the switch marks
  // the pane that has come forward for the length of one entrance and then
  // takes the mark off — and taking it off is the point, because a class that
  // stayed would mean the animation never ran a second time.
  //
  // Before paint, not after. As a passive effect the mark landed one frame late
  // — the pane had already painted where the animation was about to put it, so
  // the first thing the reader saw was the finished state, and the entrance
  // then started over from underneath it. That single wrong frame is most of
  // why switching sections looked like it had no transition at all.
  const [entering, setEntering] = useState(false)
  useLayoutEffect(() => {
    if (!active) return
    setEntering(true)
    const timer = window.setTimeout(() => setEntering(false), DUR.mid)
    return () => window.clearTimeout(timer)
  }, [active])

  return (
    <div
      hidden={!active && !leaving}
      aria-hidden={leaving ? true : undefined}
      className={`instance__pane${scroll ? ' instance__pane--scroll' : ''}${
        entering ? ' instance__pane--entering' : ''
      }${leaving ? ' instance__pane--leaving' : ''}`}
      id={`instance-panel-${id}`}
    >
      {/* Per pane, not per instance: the panes stay mounted behind each other,
          so one section that throws would otherwise take the console — the one
          thing here that must not go down — with it. */}
      <ErrorBoundary>{children}</ErrorBoundary>
    </div>
  )
}
