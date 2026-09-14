import type { InstanceStatus } from '../types'
import { PageHead } from './Page'

/**
 * How this server is started: which Java, which jar, how much heap.
 *
 * Its own page rather than a section of 实例设置 because it is the only part
 * of that form anybody opens twice — and because it is the only part with a
 * right-hand answer to show (the command, and what is wrong with it), which a
 * single reading column has nowhere to put.
 *
 * `.stack` and not `.stack--narrow`: an instance pane's plain stack is the
 * tile measure, which is what the second column needs. 实例设置 keeps the
 * narrow one, being a form from top to bottom.
 */
export function StartupSettings({ instance }: { instance: InstanceStatus }) {
  return (
    <div className="stack">
      <PageHead
        title="启动方式"
        lead="用哪个 Java、跑哪个 jar、给多少内存，以及面板据此拼出的那条命令。"
      />
      <p className="muted">{instance.name}</p>
    </div>
  )
}
