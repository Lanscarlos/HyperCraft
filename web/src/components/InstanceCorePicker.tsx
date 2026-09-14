import { useEffect, useState } from 'react'

import { ApiError, api } from '../api'
import { ask } from '../confirm'
import { formatBytes } from '../format'
import type { InstanceStatus, ServerCore } from '../types'
import { toast } from '../toast'
import type { CoreController } from '../useCores'
import { Button } from './Button'
import { Select } from './Select'

interface Props {
  instance: InstanceStatus
  cores: CoreController
  /** Called once a core lands in the directory, with the file name it wrote. */
  onApplied: (fileName: string, instance: InstanceStatus, setAsJar: boolean) => void
  onOpenLibrary: () => void
  /** True when this instance launches through the operator's own script, which
   *  makes 启动 jar a setting nothing reads. Copying a core into the directory
   *  is still perfectly sensible — the script may well launch it — so the copy
   *  stays and only the tick-box changes. */
  jarIgnored?: boolean
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
export function InstanceCorePicker({
  instance,
  cores,
  onApplied,
  onOpenLibrary,
  jarIgnored = false,
}: Props) {
  const [coreId, setCoreId] = useState('')
  const [setAsJar, setSetAsJar] = useState(!jarIgnored)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Shut by default: copying a core is a thing you do once, and this page is
  // mostly opened to change something else.
  const [open, setOpen] = useState(false)

  const available = cores.cores

  useEffect(() => {
    setCoreId((current) =>
      current && available.some((core) => core.id === current) ? current : (available[0]?.id ?? ''),
    )
  }, [available])

  // An error raised while the drawer is shut would be reported into something
  // nobody can see, so surface it. An effect rather than folding error into the
  // open prop: `open={open || Boolean(error)}` also refuses to let the reader
  // shut the drawer again once they have read the thing.
  useEffect(() => {
    if (error) setOpen(true)
  }, [error])

  const apply = async (overwrite: boolean) => {
    if (!coreId) return
    setBusy(true)
    setError(null)
    try {
      const result = await api.applyCore(instance.id, { coreId, setAsJar, overwrite })
      toast(
        setAsJar
          ? `已复制 ${result.fileName} 到实例目录，并设为启动 jar`
          : `已复制 ${result.fileName} 到实例目录`,
        { key: 'core-picker.save' },
      )
      onApplied(result.fileName, result.instance, setAsJar)
    } catch (err) {
      // 409 means the same file name is already in the directory — usually a
      // re-copy to repair a broken jar, worth offering and never worth doing
      // silently to the file a running server was launched from.
      if (err instanceof ApiError && err.status === 409 && !overwrite) {
        const name = available.find((core) => core.id === coreId)?.fileName ?? '该文件'
        const overwriteIt = await ask({
          title: '实例目录里已经有同名文件',
          lead: `${name} 已经在 ${instance.name} 的目录里了。`,
          detail: '覆盖会用库里的这份替换掉它。如果服务器正开着，换掉的是它启动时读的那个 jar。',
          confirmLabel: '覆盖',
          danger: true,
        })
        if (overwriteIt) {
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
    // Controlled rather than a bare <details>, so the effect above can open it
    // when a copy fails.
    <details
      className="corepicker"
      open={open}
      onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}
    >
      <summary>从核心库安装一个核心…</summary>

      <div className="corepicker__body">
        <div className="actions">
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

            <label className="field field--md">
              <span>选择核心</span>
              <Select
                ariaLabel="选择核心"
                value={coreId}
                disabled={busy}
                options={available.map((core) => ({
                  value: core.id,
                  label: coreLabel(core),
                }))}
                onChange={setCoreId}
              />
              {selected && <small>将写入 <code>{selected.fileName}</code></small>}
            </label>

            <label className="checkbox">
              <input
                type="checkbox"
                checked={setAsJar}
                onChange={(e) => setSetAsJar(e.target.checked)}
                disabled={busy}
              />
              <div className="checkbox__text">
                <span>
                  复制后设为启动 jar
                  {selected?.kind === 'proxy' && '（代理端不吃 --nogui，会一并清空服务端参数）'}
                </span>
                {jarIgnored && (
                  <small>
                    这个实例用自己的脚本启动，「启动 jar」没人读 —— 勾了也只是记下来，
                    真正启动什么由脚本决定。
                  </small>
                )}
              </div>
            </label>

            {error && <div className="alert">{error}</div>}

            <div className="actions">
              <Button
                type="button"
                onClick={() => void apply(false)}
                disabled={busy || !coreId}
              >
                {busy ? '复制中…' : '复制到实例'}
              </Button>
              {selected?.kind !== 'proxy' && (
                <span className="muted">
                  别忘了去「服务器配置」同意 EULA，否则服务端启动后会立刻退出。
                </span>
              )}
            </div>
          </>
        )}
      </div>
    </details>
  )
}
