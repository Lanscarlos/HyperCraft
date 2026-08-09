import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import type { Device } from '../types'

interface Props {
  onClose: () => void
}

/**
 * Lists the paired native clients so the operator can see what holds a token
 * and take one away.
 *
 * Pairing itself is not here. It takes the password rather than a session, and
 * the token it returns is meant for an app to store — handing it to a browser
 * that has no safe place to keep it would be the wrong shape. The panel's
 * README documents the pairing call; this dialog is the other half.
 */
export function DevicesDialog({ onClose }: Props) {
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
    if (
      !window.confirm(
        `解除「${device.name}」的配对？该设备上的客户端会立刻退出登录，需要重新配对才能再连上。`,
      )
    ) {
      return
    }
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
    <div className="modal" role="dialog" aria-modal="true">
      <div className="modal__card">
        <h2 className="modal__title">已配对设备</h2>
        <p className="modal__lead">
          桌面端和手机 App 用设备令牌登录，不受面板重启影响。配对方法见 README。
        </p>

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

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onClose}>
            关闭
          </button>
        </div>
      </div>
    </div>
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
