import type { PluginController } from '../usePlugins'
import type { TerminalController } from '../useTerminal'
import { updateLabel } from '../useUpdate'
import type { UpdateController } from '../useUpdate'
import { DevicesPage } from './DevicesPage'
import { Page } from './Page'
import { Tabs } from './Tabs'
import { PluginSourceSettings } from './PluginSourceSettings'
import { TerminalSettings } from './TerminalSettings'
import { UpdatePanel } from './UpdatePanel'

/** Which settings page is open. Part of the URL, so it survives a reload. */
export type SettingsSection = 'terminal' | 'devices' | 'plugin-source' | 'update'

/**
 * Java runtimes and server cores used to live here too. They moved out to
 * their own sidebar entries: both are things an operator goes to *do* something
 * with — install a runtime, download a core — several times a week, which is a
 * poor fit for a page you reach by first deciding to open 设置.
 */
export const SETTINGS_SECTIONS: { id: SettingsSection; label: string }[] = [
  { id: 'terminal', label: '终端' },
  { id: 'devices', label: '已配对设备' },
  { id: 'plugin-source', label: '插件源' },
  { id: 'update', label: '面板更新' },
]

export function isSettingsSection(value: string): value is SettingsSection {
  return SETTINGS_SECTIONS.some((section) => section.id === value)
}

interface Props {
  section: SettingsSection
  onSection: (section: SettingsSection) => void
  terminal: TerminalController
  update: UpdateController
  /** The plugin library, for the download source and token settings. */
  plugins: PluginController
  /** Jumps to the terminal page once the operator has switched it on. */
  onOpenTerminal: () => void
  /** Instances that would be stopped by a panel update. */
  runningNames: string[]
}

/**
 * Panel-wide settings: the switches you flip once and then forget about.
 *
 * What is left here after the asset pages moved out is deliberately the rare
 * stuff — turning the host shell on, revoking a paired device, updating the
 * binary — so nothing an operator needs weekly is buried behind it.
 */
export function SettingsPage({
  section,
  onSection,
  terminal,
  update,
  plugins,
  onOpenTerminal,
  runningNames,
}: Props) {
  return (
    <div className="settings-page">
      <Tabs
        items={SETTINGS_SECTIONS.map((entry) => ({
          ...entry,
          badge:
            entry.id === 'terminal' && terminal.status?.enabled ? (
              <span className="badge">已开启</span>
            ) : entry.id === 'update' && updateLabel(update.status) ? (
              <span className="badge badge--update">{updateLabel(update.status)}</span>
            ) : undefined,
        }))}
        active={section}
        onSelect={onSection}
        label="设置分区"
        idPrefix="settings"
      />

      <div
        className="settings-page__body"
        id={`settings-panel-${section}`}
        role="tabpanel"
        aria-labelledby={`settings-tab-${section}`}
      >
        {section === 'terminal' && (
          <TerminalSettings terminal={terminal} onOpenTerminal={onOpenTerminal} />
        )}
        {section === 'devices' && <DevicesPage />}
        {section === 'plugin-source' && <PluginSourceSettings plugins={plugins} />}
        {section === 'update' && (
          <Page
            title="面板更新"
            lead="面板可以自己下载并替换掉自己的二进制，然后重启 —— 更新前正在运行的服务器会先优雅停止，升级完成后再自动拉起来。快照通道能提前拿到 main 上每个通过 CI 的构建。"
          >
            <UpdatePanel update={update} runningNames={runningNames} />
          </Page>
        )}
      </div>
    </div>
  )
}
