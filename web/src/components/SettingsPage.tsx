import { SETTINGS_SECTIONS } from '../routes'
import type { SettingsSection } from '../routes'
import type { PluginController } from '../usePlugins'
import { updateLabel } from '../useUpdate'
import type { UpdateController } from '../useUpdate'
import { DevicesPage } from './DevicesPage'
import { Page } from './Page'
import { Tabs } from './Tabs'
import { PluginSourceSettings } from './PluginSourceSettings'
import { SecurityPage } from './SecurityPage'
import { UpdatePanel } from './UpdatePanel'

interface Props {
  section: SettingsSection
  onSection: (section: SettingsSection) => void
  update: UpdateController
  /** The plugin library, for the download source and token settings. */
  plugins: PluginController
  /** Instances that would be stopped by a panel update. */
  runningNames: string[]
}

/**
 * The panel's own settings: the switches you flip once and then forget about.
 *
 * Everything that turned out to be about something else has moved out. Java
 * runtimes and server cores are stock, so they are in 资源库; the host shell is
 * a property of the machine, so it is under 主机 → 节点配置. What is left is
 * genuinely panel-wide, which is also why it is the last group in the sidebar.
 */
export function SettingsPage({ section, onSection, update, plugins, runningNames }: Props) {
  return (
    <div className="settings-page">
      <Tabs
        items={SETTINGS_SECTIONS.map((entry) => ({
          ...entry,
          badge:
            entry.id === 'update' && updateLabel(update.status) ? (
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
        {section === 'devices' && <DevicesPage />}
        {section === 'security' && <SecurityPage />}
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
