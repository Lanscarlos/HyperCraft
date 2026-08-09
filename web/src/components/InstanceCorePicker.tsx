import { useEffect, useState } from 'react'

import { ApiError, api } from '../api'
import { formatBytes } from '../format'
import type { InstanceStatus, ServerCore } from '../types'
import type { CoreController } from '../useCores'

interface Props {
  instance: InstanceStatus
  cores: CoreController
  /** Called once a core lands in the directory, with the file name it wrote. */
  onApplied: (fileName: string, instance: InstanceStatus, setAsJar: boolean) => void
  onOpenLibrary: () => void
}

function coreLabel(core: ServerCore): string {
  if (core.imported) return `${core.fileName}（自行放入）`
  const parts = [`${core.projectName} ${core.version}`, `构建 #${core.build}`]
  if (core.kind === 'proxy') parts.push('代理端')
  return `${parts.join(' · ')} — ${formatBytes(core.size)}`
}

/**
 * Copies a server core out of the panel-wide library into this instance.
 *
 * A copy, not a shared path: the instance owns the jar it launches, so a later
 * download — or a delete in the library — cannot change what a running server
 * is built from. Downloading new cores happens on the library page, which is
 * also where they are kept; this is only the "give this server one" half.
 */
export function InstanceCorePicker({ instance, cores, onApplied, onOpenLibrary }: Props) {
  const [coreId, setCoreId] = useState('')
  const [setAsJar, setSetAsJar] = useState(true)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const available = cores.cores

  useEffect(() => {
    setCoreId((current) =>
      current && available.some((core) => core.id === current) ? current : (available[0]?.id ?? ''),
    )
  }, [available])

  const apply = async (overwrite: boolean) => {
    if (!coreId) return
    setBusy(true)
    setError(null)
    setStatus(null)
    try {
      const result = await api.applyCore(instance.id, { coreId, setAsJar, overwrite })
      setStatus(
        setAsJar
          ? `已复制 ${result.fileName} 到实例目录，并设为启动 jar`
          : `已复制 ${result.fileName} 到实例目录`,
      )
      onApplied(result.fileName, result.instance, setAsJar)
    } catch (err) {
      // 409 means the same file name is already in the directory — usually a
      // re-copy to repair a broken jar, worth offering and never worth doing
      // silently to the file a running server was launched from.
      if (err instanceof ApiError && err.status === 409 && !overwrite) {
        const name = available.find((core) => core.id === coreId)?.fileName ?? '该文件'
        if (window.confirm(`${name} 已经在实例目录里了，要覆盖吗？`)) {
          setBusy(false)
          await apply(true)
          return
        }
      } else {
        setError(err instanceof Error ? err.message : '复制失败')
      }
    } finally {
      setBusy(false)
    }
  }

  const selected = available.find((core) => core.id === coreId)

  return (
    <section className="panel">
      <div className="chart-head">
        <h3 className="panel__title">从核心库安装</h3>
        <button className="link" type="button" onClick={onOpenLibrary}>
          管理核心库
        </button>
      </div>

      {available.length === 0 ? (
        <p className="chart-note">
          核心库还是空的。去「资源库 → 服务端核心」下载一个 Paper 或 Velocity，下载后在这里就能选；
          也可以自己把 jar 传到实例目录，在下面的「服务端 jar」里填文件名。
        </p>
      ) : (
        <>
          <p className="chart-note">
            从核心库挑一个复制到本实例目录 —— 新服装核心、老服换版本或者修一个坏掉的 jar，都走这里。
            核心只在核心库下载一次，开多少个服就复制多少份。
          </p>

          <label className="field">
            <span>选择核心</span>
            <select
              value={coreId}
              onChange={(e) => setCoreId(e.target.value)}
              disabled={busy}
            >
              {available.map((core) => (
                <option key={core.id} value={core.id}>
                  {coreLabel(core)}
                </option>
              ))}
            </select>
            {selected && <small>将写入 <code>{selected.fileName}</code></small>}
          </label>

          <label className="checkbox">
            <input
              type="checkbox"
              checked={setAsJar}
              onChange={(e) => setSetAsJar(e.target.checked)}
              disabled={busy}
            />
            <span>
              复制后设为启动 jar
              {selected?.kind === 'proxy' && '（代理端不吃 --nogui，会一并清空服务端参数）'}
            </span>
          </label>

          {error && <div className="alert alert--error">{error}</div>}
          {status && <div className="alert alert--ok">{status}</div>}

          <div className="actions">
            <button
              className="btn btn--primary"
              type="button"
              onClick={() => void apply(false)}
              disabled={busy || !coreId}
            >
              {busy ? '复制中…' : '复制到实例'}
            </button>
            {selected?.kind !== 'proxy' && (
              <span className="file-toolbar__hint">
                别忘了去「服务器配置」同意 EULA，否则服务端启动后会立刻退出。
              </span>
            )}
          </div>
        </>
      )}
    </section>
  )
}
