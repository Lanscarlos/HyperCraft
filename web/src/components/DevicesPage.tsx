import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import type { Device } from '../types'
import { Page } from './Page'

/**
 * The native clients holding a device token.
 *
 * Pairing itself is not offered here. It takes the password rather than a
 * session, and the token it returns is meant to be stored by an app — handing
 * one to a browser that has nowhere safe to keep it would undo the reason the
 * two credential kinds are separate. The README documents the pairing call;
 * this page is the other half of it: seeing what holds a token, and taking one
 * away.
 */
export function DevicesPage() {
  const [devices, setDevices] = useState<Device[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busyID, setBusyID] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setDevices(await api.listDevices())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取失败')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const unpair = async (device: Device) => {
    const ok = await ask({
      title: `解除「${device.name}」的配对？`,
      lead: '该设备上的客户端会立刻退出登录。',
      detail: '要再连上得重新配对一次，这台设备之后不会自动回来。',
      confirmLabel: '解除配对',
      danger: true,
    })
    if (!ok) return
    setBusyID(device.id)
    setError(null)
    try {
      await api.deleteDevice(device.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '解除失败')
    } finally {
      setBusyID(null)
    }
  }

  return (
    <Page
      title="已配对设备"
      lead="桌面端和手机 App 用设备令牌登录，不像浏览器会话那样面板一重启就失效，所以自动更新不会把你从 App 里登出。配对方法见 README；改密码会解除所有设备的配对。"
    >

      <section className="panel">
        <h3 className="panel__title">设备</h3>

        {error && <div className="alert alert--error">{error}</div>}

        {devices === null ? (
          <p className="muted">正在读取…</p>
        ) : devices.length === 0 ? (
          <p className="muted">还没有配对过任何设备。</p>
        ) : (
          <div className="device-list">
            {devices.map((device) => (
              <div className="device-row" key={device.id}>
                <div className="device-row__main">
                  <strong>{device.name}</strong>
                  {device.current && <span className="badge">当前设备</span>}
                  <span className="device-row__spacer" />
                  <button
                    className="link link--danger"
                    onClick={() => void unpair(device)}
                    disabled={busyID !== null}
                  >
                    {busyID === device.id ? '解除中…' : '解除配对'}
                  </button>
                </div>
                <div className="device-row__meta">
                  配对于 {formatDay(device.createdAt)}
                  {' · '}
                  {device.lastUsed
                    ? `最后使用 ${formatDay(device.lastUsed)}`
                    : '还没用过'}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>
    </Page>
  )
}

/**
 * Day precision on purpose: "last used" is written to disk on a slow timer, so
 * showing minutes would imply a freshness the value does not have.
 */
function formatDay(iso: string): string {
  return new Date(iso).toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
}
