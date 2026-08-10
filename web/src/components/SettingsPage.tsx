import type { SettingsSection } from '../routes'
import type { PluginController } from '../usePlugins'
import type { UpdateController } from '../useUpdate'
import { DevicesPage } from './DevicesPage'
import { Page } from './Page'
import { PluginSourceSettings } from './PluginSourceSettings'
import { SecurityPage } from './SecurityPage'
import { UpdatePanel } from './UpdatePanel'

interface Props {
  section: SettingsSection
  update: UpdateController
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
 *
 * GitHub 集成 arrived here going the other way, and for the same test. It was
 * a page under 插件库 called 插件源 holding two unrelated things: adding a
 * repository, which is an action and now lives in that page's + 添加插件 menu,
 * and the access tokens, the download mirror and the retention default — which
 * are settings, panel-wide, touched once. Only the second half is a page, and
 * this is where pages like that live.
 *
 * The sections used to be a tab strip across the top of this component.
 * They are sidebar entries now (see Sidebar's settings scope), which leaves
 * this as what it should always have been: a switch on the section, and the
 * section's own page. A page that carries its own second navigation is a page
 * you have to arrive at before you can navigate — and these are exactly the
 * kind of thing you want to go straight to.
 */
export function SettingsPage({ section, update, plugins, runningNames }: Props) {
  switch (section) {
    case 'security':
      return <SecurityPage />
    case 'plugins':
      return <PluginSourceSettings plugins={plugins} />
    case 'update':
      return (
        <Page
          title="面板更新"
          lead="面板可以自己下载并替换掉自己的二进制，然后重启。更新分两步：下载新版本和停止服务器同时进行，等服务器全部停好之后才替换文件并重启，升级完成后服务器自动拉起来。快照通道能提前拿到 main 上每个通过 CI 的构建。"
        >
          <UpdatePanel update={update} runningNames={runningNames} />
        </Page>
      )
    default:
      return <DevicesPage />
  }
}
