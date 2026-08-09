import { useState } from 'react'

import type { TerminalController } from '../useTerminal'

interface Props {
  terminal: TerminalController
  onOpenTerminal: () => void
}

/**
 * The switch that decides whether the panel hands out a shell.
 *
 * It is off on a fresh install and stays off through an upgrade, because it is
 * the one setting here that changes what the panel password is worth: without
 * it, a stolen password means someone can restart your Minecraft servers; with
 * it, they have the account the panel runs as. The confirmation step exists for
 * that reason and not to be tidy.
 *
 * It is a section rather than a page: enabling a shell is a property of the
 * *machine*, so it lives under 节点配置 alongside the machine's other facts,
 * not in the panel-wide settings where an operator would go looking for their
 * password.
 */
export function TerminalSettings({ terminal, onOpenTerminal }: Props) {
  const [confirming, setConfirming] = useState(false)
  const { status, saving, error } = terminal

  if (!status) {
    return <section className="panel">正在读取终端设置…</section>
  }

  const enable = async () => {
    setConfirming(false)
    await terminal.setEnabled(true)
  }

  return (
    <>
      <div className="stack">
        <h2 className="panel__title">SSH 终端</h2>
        <p className="page__lead">
          在面板里直接开一个本机 shell。它跑在面板所在的这台机器上，权限和面板进程完全一样 ——
          和游戏控制台不是一个量级的东西：后者最多影响一个 Minecraft 进程，前者是整机。
        </p>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      {!status.supported ? (
        <div className="alert">{status.reason}</div>
      ) : (
        <section className="panel">
          <dl className="update__meta">
            <div>
              <dt>运行身份</dt>
              <dd>{status.user || '未知'}</dd>
            </div>
            <div>
              <dt>使用的 shell</dt>
              <dd>{status.shell}</dd>
            </div>
            <div>
              <dt>起始目录</dt>
              <dd>{status.cwd}</dd>
            </div>
            <div>
              <dt>当前会话</dt>
              <dd>{status.live} 个</dd>
            </div>
          </dl>

          {status.enabled ? (
            <>
              <div className="alert alert--warn">
                终端已开启。<strong>任何能登录这个面板的人</strong>
                都可以在这台机器上以 {status.user || '面板用户'} 的身份执行任意命令 ——
                所以面板密码要当成 SSH 密码来管，别把面板裸奔在公网上。
              </div>
              <div className="actions">
                <button className="btn btn--primary" onClick={onOpenTerminal}>
                  打开终端
                </button>
                <button
                  className="btn btn--danger"
                  disabled={saving}
                  onClick={() => void terminal.setEnabled(false)}
                >
                  {saving ? '保存中…' : '关闭终端'}
                </button>
              </div>
              <small className="update__note">
                关闭后入口会立刻消失，已经开着的会话也会被挂断。
              </small>
            </>
          ) : confirming ? (
            <>
              <div className="alert alert--warn">
                开启前先确认一下：这等于把 {status.user || '面板用户'} 的 shell
                交给了面板密码。建议先做到这两件事 ——
                <br />
                1. 面板密码足够强，且已经改过默认生成的那一个；
                <br />
                2. 面板没有直接暴露在公网，或者前面有 HTTPS 反代 + 访问控制。
              </div>
              <div className="actions">
                <button className="btn btn--primary" disabled={saving} onClick={() => void enable()}>
                  {saving ? '保存中…' : '我明白，开启终端'}
                </button>
                <button className="btn" onClick={() => setConfirming(false)}>
                  取消
                </button>
              </div>
            </>
          ) : (
            <>
              <p className="update__note">
                终端默认是关闭的，升级面板也不会把它打开。
              </p>
              <div className="actions">
                <button className="btn btn--primary" onClick={() => setConfirming(true)}>
                  开启终端
                </button>
              </div>
            </>
          )}
        </section>
      )}
    </>
  )
}
