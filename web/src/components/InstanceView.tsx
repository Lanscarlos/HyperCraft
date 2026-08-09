import { useEffect, useState } from 'react'

import type { InstanceSection } from '../routes'
import type { InstanceStatus } from '../types'
import type { CoreController } from '../useCores'
import type { PluginController } from '../usePlugins'
import { FileManager } from './FileManager'
import { InstanceCockpit } from './InstanceCockpit'
import { InstancePlugins } from './InstancePlugins'
import { LaunchSettings } from './LaunchSettings'
import { PropertiesEditor } from './PropertiesEditor'
import { ResourcePanel } from './ResourcePanel'

interface Props {
  instance: InstanceStatus
  section: InstanceSection
  cores: CoreController
  /** The panel-wide plugin library, so 已装插件 can add a source itself. */
  plugins: PluginController
  onChanged: (instance: InstanceStatus) => void
  onDeleted: () => void
  onOpenSection: (section: InstanceSection) => void
  /** The panel-wide core library, for "download another one". */
  onOpenCoreLibrary: () => void
  /** The panel-wide plugin library. */
  onOpenPluginLibrary: () => void
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
  onOpenCoreLibrary,
  onOpenPluginLibrary,
}: Props) {
  const [visited, setVisited] = useState<Set<InstanceSection>>(
    () => new Set<InstanceSection>([section]),
  )

  useEffect(() => {
    setVisited((prev) => (prev.has(section) ? prev : new Set(prev).add(section)))
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
      <Pane id="console" active={section === 'console'}>
        <InstanceCockpit
          instance={instance}
          active={section === 'console'}
          onChanged={onChanged}
          onOpenSection={onOpenSection}
        />
      </Pane>

      {visited.has('metrics') && (
        <Pane id="metrics" active={section === 'metrics'} scroll>
          <ResourcePanel instance={instance} active={section === 'metrics'} />
        </Pane>
      )}
      {visited.has('files') && (
        <Pane id="files" active={section === 'files'} scroll>
          <FileManager instance={instance} />
        </Pane>
      )}
      {visited.has('plugins') && (
        <Pane id="plugins" active={section === 'plugins'} scroll>
          <InstancePlugins
            instance={instance}
            plugins={plugins}
            onOpenLibrary={onOpenPluginLibrary}
          />
        </Pane>
      )}
      {visited.has('properties') && (
        <Pane id="properties" active={section === 'properties'} scroll>
          <PropertiesEditor instance={instance} />
        </Pane>
      )}
      {visited.has('settings') && (
        <Pane id="settings" active={section === 'settings'} scroll>
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

/** `hidden` rather than unmounted — and `hidden` specifically, because it is
 *  what keeps a background pane out of the tab order and off a screen reader. */
function Pane({
  id,
  active,
  scroll,
  children,
}: {
  id: string
  active: boolean
  scroll?: boolean
  children: React.ReactNode
}) {
  return (
    <div
      hidden={!active}
      className={`instance__pane${scroll ? ' instance__pane--scroll' : ''}`}
      id={`instance-panel-${id}`}
    >
      {children}
    </div>
  )
}
