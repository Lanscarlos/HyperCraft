import { useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import type { InstanceStatus } from '../types'
import { isLive } from '../types'
import { useUptime } from '../useUptime'
import { Menu } from './Menu'

type PowerAction = 'start' | 'stop' | 'restart' | 'kill'

interface Props {
  instance: InstanceStatus
  onChanged: (instance: InstanceStatus) => void
  /** `full` is the cockpit's bar — 重启 gets its own button and 强制结束 hides
   *  behind the ⋯. `compact` is an overview card, where there is room for one
   *  primary action and a menu. */
  variant?: 'full' | 'compact'
  onError?: (message: string | null) => void
}

/**
 * The power buttons, as a state machine rather than a row of four.
 *
 * A stopped server has exactly one thing you can do to it and a running one
 * has two, so that is what is on screen; 启动 greyed out beside 停止 greyed out
 * makes the reader work out which half applies. While a request is in flight
 * every button is disabled and the one that was pressed says so, because the
 * daemon takes about a second to spawn a JVM and an enabled 启动 during that
 * second is an invitation to press it twice.
 *
 * 强制结束 is never a button. A kill the same size as 启动, one row from the
 * pointer that just started the server, is how a world gets lost to a slip.
 */
export function PowerControls({ instance, onChanged, variant = 'full', onError }: Props) {
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState<PowerAction | null>(null)
  const uptime = useUptime(instance.startedAt, isLive(instance.state))

  const power = async (action: PowerAction) => {
    if (!(await confirmPower(action, instance, uptime))) return
    setBusy(true)
    setPending(action)
    onError?.(null)
    try {
      onChanged(await api.power(instance.id, action))
    } catch (err) {
      onError?.(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
      setPending(null)
    }
  }

  const transitioning = instance.state === 'starting' || instance.state === 'stopping'
  const running = instance.state === 'running'
  // A crashed server is stopped as far as the buttons are concerned: the only
  // thing to do with it is start it again.
  const down = instance.state === 'stopped' || instance.state === 'crashed'

  const more = (
    <Menu
      className="btn btn--icon"
      title="更多操作"
      ariaLabel="更多操作"
      items={[
        {
          label: '重启',
          onSelect: () => void power('restart'),
          disabled: busy || !isLive(instance.state),
        },
        {
          label: '强制结束',
          onSelect: () => void power('kill'),
          disabled: busy || !isLive(instance.state),
          danger: true,
        },
      ]}
    >
      ⋯
    </Menu>
  )

  return (
    <div className={`power power--${variant}`}>
      {transitioning ? (
        <button className="btn" disabled aria-busy="true">
          {instance.state === 'starting' ? '启动中…' : '停止中…'}
        </button>
      ) : down ? (
        <button
          className="btn btn--primary"
          onClick={() => void power('start')}
          disabled={busy}
          aria-busy={pending === 'start' || undefined}
        >
          启动
        </button>
      ) : (
        <>
          {variant === 'full' && (
            <button
              className="btn"
              onClick={() => void power('restart')}
              disabled={busy}
              aria-busy={pending === 'restart' || undefined}
            >
              重启
            </button>
          )}
          <button
            className="btn"
            onClick={() => void power('stop')}
            disabled={busy}
            aria-busy={pending === 'stop' || undefined}
          >
            停止
          </button>
        </>
      )}
      {(running || transitioning) && more}
      {/* Keeps the row the same height whether or not the menu is there, so a
          card does not twitch when its server comes up. */}
      {down && variant === 'compact' && <span className="power__spacer" aria-hidden="true" />}
    </div>
  )
}

/**
 * The confirmation, spelling out what is about to happen to *this* server.
 *
 * "确定要停止吗？" is a box people click through. A box that names the server,
 * says how long it has been up and says the players on it are about to be
 * disconnected is one they read.
 */
function confirmPower(
  action: PowerAction,
  instance: InstanceStatus,
  uptime: string | null,
): Promise<boolean> {
  if (action === 'start' || action === 'restart') return Promise.resolve(true)

  const ran = uptime ? `已运行 ${uptime}。` : ''
  if (action === 'stop') {
    return ask({
      title: `停止「${instance.name}」？`,
      lead: `${ran}服务器会先执行 stop 保存世界再退出。`,
      detail: '当前连着它的玩家会全部掉线。',
      confirmLabel: '停止',
    })
  }
  return ask({
    title: `强制结束「${instance.name}」？`,
    lead: (
      <>
        进程{instance.pid ? `（PID ${instance.pid}）` : ''}会被直接杀掉，不执行保存。
      </>
    ),
    detail:
      '上一次自动保存之后的世界改动全部丢失，正在写入的区块还可能损坏。只有在「停止」已经卡住不动时才用它。',
    confirmLabel: '强制结束',
    danger: true,
  })
}
