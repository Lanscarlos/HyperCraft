import { Button } from './Button'
import { Modal } from './Modal'

interface Props {
  onCreate: () => void
  onImport: () => void
  onCancel: () => void
}

/**
 * Asks which of the two ways to add an instance this is.
 *
 * The two used to be entrances at different ranks: 新建实例 was the button on
 * 概览 and in the sidebar, and 导入现有目录 was reachable only from 所有实例.
 * On a machine that has been running servers for a year that ranking is
 * backwards — adopting one is the normal case — and the failure it caused was
 * pressing 新建实例 and only then remembering it was the wrong one.
 *
 * So neither is the default here and neither is filled: this is a fork, not a
 * call to action, and what it buys is the half-second in which you read which
 * one you are about to take. 导入 is listed first for the same reason.
 */
export function AddInstanceDialog({ onCreate, onImport, onCancel }: Props) {
  return (
    <Modal onClose={onCancel}>
      <div className="modal__card">
        <h2 className="modal__title">添加实例</h2>
        <p className="modal__lead">
          机器上已经有服务端目录的话走「导入」，目录里的世界、插件、配置一个都不会动。
        </p>

        <div className="choice-grid choice-grid--wide">
          <button type="button" className="choice" onClick={onImport}>
            <span className="choice__label">导入现有目录</span>
            <span className="choice__note">
              接管这台机器上已有的服务端 —— 手动跑了很久的服，或者从别的面板搬过来的。面板只
              把它记进实例列表，目录原样不动。
            </span>
          </button>
          <button type="button" className="choice" onClick={onCreate}>
            <span className="choice__label">新建实例</span>
            <span className="choice__note">
              从头开一个新服：选核心和 Java，面板下好核心、建好目录、写好配置。
            </span>
          </button>
        </div>

        <div className="modal__actions">
          <Button type="button" onClick={onCancel}>
            取消
          </Button>
        </div>
      </div>
    </Modal>
  )
}
