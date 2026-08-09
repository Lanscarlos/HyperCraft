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
 */
export function TerminalSettings({ terminal, onOpenTerminal }: Props) {
  const [confirming, setConfirming] = useState(false)
  const { status, saving, error } = terminal

  if (!status) {
    return <div className="page">正在读取终端设置…</div>
  }

  const enable = async () => {
    setConfirming(false)
    await terminal.setEnabled(true)
  }

  return (
    <div className="page">
      <h1>终端</h1>
      <p className="page__lead">
        在面板里直接开一个本机 shell，装插件、看日志、改配置不用再单独 SSH 上来。
        这里跑的就是面板所在的这台机器，权限和面板进程完全一样 —— 不是连到别的服务器。
      </p>

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
              <div className="settings__actions">
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
              <div className="settings__actions">
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
              <div className="settings__actions">
                <button className="btn btn--primary" onClick={() => setConfirming(true)}>
                  开启终端
                </button>
              </div>
            </>
          )}
        </section>
      )}
    </div>
  )
}
