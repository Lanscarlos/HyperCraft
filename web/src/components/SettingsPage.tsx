import type { CoreController } from '../useCores'
import type { JavaController } from '../useJava'
import type { TerminalController } from '../useTerminal'
import { updateLabel } from '../useUpdate'
import type { UpdateController } from '../useUpdate'
import { CoreLibraryPage } from './CoreLibraryPage'
import { JavaPage } from './JavaPage'
import { TerminalSettings } from './TerminalSettings'
import { UpdatePanel } from './UpdatePanel'

/** Which settings page is open. Part of the URL, so it survives a reload. */
export type SettingsSection = 'java' | 'cores' | 'terminal' | 'update'

export const SETTINGS_SECTIONS: { id: SettingsSection; label: string }[] = [
  { id: 'java', label: 'Java 运行时' },
  { id: 'cores', label: '服务端核心' },
  { id: 'terminal', label: '终端' },
  { id: 'update', label: '面板更新' },
]

export function isSettingsSection(value: string): value is SettingsSection {
  return SETTINGS_SECTIONS.some((section) => section.id === value)
}

interface Props {
  section: SettingsSection
  onSection: (section: SettingsSection) => void
  java: JavaController
  cores: CoreController
  terminal: TerminalController
  update: UpdateController
  /** Jumps to the terminal page once the operator has switched it on. */
  onOpenTerminal: () => void
  /** Instances that would be stopped by a panel update. */
  runningNames: string[]
}

/**
 * Everything that belongs to the panel rather than to one server.
 *
 * These are all shared assets — a Java runtime, a server core, the panel binary
 * itself — so they live together here instead of being scattered across the
 * overview and the per-instance tabs. An instance only ever picks from what
 * this page manages.
 */
export function SettingsPage({
  section,
  onSection,
  java,
  cores,
  terminal,
  update,
  onOpenTerminal,
  runningNames,
}: Props) {
  return (
    <div className="settings-page">
      <nav className="tabs">
        {SETTINGS_SECTIONS.map((entry) => (
          <button
            key={entry.id}
            className={`tabs__tab${section === entry.id ? ' tabs__tab--active' : ''}`}
            onClick={() => onSection(entry.id)}
          >
            {entry.label}
            {entry.id === 'java' && java.installing && <span className="badge">安装中</span>}
            {entry.id === 'cores' && cores.downloading && <span className="badge">下载中</span>}
            {entry.id === 'terminal' && terminal.status?.enabled && (
              <span className="badge">已开启</span>
            )}
            {entry.id === 'update' && updateLabel(update.status) && (
              <span className="badge badge--update">{updateLabel(update.status)}</span>
            )}
          </button>
        ))}
      </nav>

      <div className="settings-page__body">
        {section === 'java' && <JavaPage java={java} />}
        {section === 'cores' && <CoreLibraryPage cores={cores} />}
        {section === 'terminal' && (
          <TerminalSettings terminal={terminal} onOpenTerminal={onOpenTerminal} />
        )}
        {section === 'update' && (
          <div className="page">
            <h1>面板更新</h1>
            <p className="page__lead">
              面板可以自己下载并替换掉自己的二进制，然后重启 —— 更新前正在运行的服务器会先优雅停止，
              升级完成后再自动拉起来。快照通道能提前拿到 main 上每个通过 CI 的构建。
            </p>
            <UpdatePanel update={update} runningNames={runningNames} />
          </div>
        )}
      </div>
    </div>
  )
}
