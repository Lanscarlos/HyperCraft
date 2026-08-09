import { useEffect, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { InstanceStatus, ServerCore } from '../types'
import type { CoreController } from '../useCores'
import { useHostJars } from '../useHostJars'
import { DirectoryField } from './PathPicker'

interface Props {
  cores: CoreController
  onCreated: (instance: InstanceStatus) => void
  onCancel: () => void
  onOpenLibrary: () => void
}

/** Sentinel for "do not put a core in, I will sort the jar out myself". */
const NO_CORE = ''

function coreLabel(core: ServerCore): string {
  if (core.imported) return `${core.fileName}（自行放入）`
  const parts = [`${core.projectName} ${core.version}`, `构建 #${core.build}`]
  if (core.kind === 'proxy') parts.push('代理端')
  return `${parts.join(' · ')} — ${formatBytes(core.size)}`
}

export function NewInstanceDialog({ cores, onCreated, onCancel, onOpenLibrary }: Props) {
  const [name, setName] = useState('')
  const [directory, setDirectory] = useState('')
  const [coreId, setCoreId] = useState<string>(NO_CORE)
  const [jar, setJar] = useState('')
  const [maxMemoryMB, setMaxMemoryMB] = useState(4096)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const available = cores.cores
  // Jars already sitting in the chosen directory — the case where someone
  // points the panel at a server they already have.
  const existing = useHostJars(directory)

  useEffect(() => {
    setCoreId((current) =>
      current && available.some((core) => core.id === current)
        ? current
        : (available[0]?.id ?? NO_CORE),
    )
  }, [available])

  const selectedCore = available.find((core) => core.id === coreId)
  // The core decides the jar name, so the field is only asked for when no core
  // is being copied in.
  const jarFromCore = selectedCore?.fileName ?? ''

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const created = await api.createInstance({
        name: name.trim(),
        directory: directory.trim(),
        jar: jarFromCore || jar.trim(),
        maxMemoryMB,
        minMemoryMB: Math.min(1024, maxMemoryMB),
        // A proxy takes no --nogui; anything else is a Minecraft server.
        serverArgs: selectedCore?.kind === 'proxy' ? [] : ['--nogui'],
      })

      if (!selectedCore) {
        onCreated(created)
        return
      }
      // The instance directory only exists once the instance does, so the core
      // is copied in afterwards rather than as part of the create call.
      const applied = await api.applyCore(created.id, { coreId: selectedCore.id, setAsJar: true })
      onCreated(applied.instance)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
      setBusy(false)
    }
  }

  return (
    <div className="modal" role="dialog" aria-modal="true">
      <form className="modal__card" onSubmit={submit}>
        <h2 className="modal__title">新建服务器实例</h2>

        <label className="field">
          <span>实例名称</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="生存服"
            required
            autoFocus
          />
        </label>

        <DirectoryField
          value={directory}
          onChange={setDirectory}
          placeholder="留空则放在面板数据目录的 servers/ 下"
          hint={
            directory.trim() === ''
              ? '留空自动生成：面板数据目录的 servers/ 下，以实例名命名。也可以「浏览…」选本机任意位置 —— 外挂硬盘、NAS 挂载点，或者一个已经有服务端的目录。'
              : existing.exists
                ? `目录已存在${existing.jars.length > 0 ? `，里面有 ${existing.jars.length} 个 jar` : ''}。`
                : '这个目录还不存在，创建实例时会一并建好。'
          }
        />

        <label className="field">
          <span>服务端核心</span>
          <select value={coreId} onChange={(e) => setCoreId(e.target.value)}>
            {available.map((core) => (
              <option key={core.id} value={core.id}>
                {coreLabel(core)}
              </option>
            ))}
            <option value={NO_CORE}>不放核心（自己指定 jar）</option>
          </select>
          <small>
            {available.length > 0 ? (
              <>
                从核心库复制一份到新实例目录。
                <button className="link" type="button" onClick={onOpenLibrary}>
                  去核心库下载别的版本
                </button>
              </>
            ) : (
              <>
                核心库还是空的。
                <button className="link" type="button" onClick={onOpenLibrary}>
                  去下载一个
                </button>
                ，或者在下面直接填目录里已有的 jar 文件名。
              </>
            )}
          </small>
        </label>

        {!selectedCore && (
          <label className="field">
            <span>服务端 jar 文件名</span>
            <input
              value={jar}
              onChange={(e) => setJar(e.target.value)}
              placeholder="server.jar"
              list="new-instance-jars"
              spellCheck={false}
            />
            <datalist id="new-instance-jars">
              {existing.jars.map((entry) => (
                <option key={entry.name} value={entry.name} />
              ))}
            </datalist>
            <small>
              {existing.jars.length > 0
                ? `目录下找到 ${existing.jars.length} 个 jar，点输入框可以直接选。`
                : '创建后也可以在「启动设置 → 从核心库安装」里装一个，或者自己把 jar 传进去。'}
            </small>
          </label>
        )}

        <label className="field">
          <span>最大内存 (MB)</span>
          <input
            type="number"
            min={512}
            step={512}
            value={maxMemoryMB}
            onChange={(e) => setMaxMemoryMB(Number(e.target.value))}
          />
        </label>

        {error && <div className="alert alert--error">{error}</div>}

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button className="btn btn--primary" type="submit" disabled={busy}>
            {busy ? '创建中…' : '创建'}
          </button>
        </div>
      </form>
    </div>
  )
}
