import { useState } from 'react'

import { api } from '../api'
import type { InstanceStatus } from '../types'

interface Props {
  onCreated: (instance: InstanceStatus) => void
  onCancel: () => void
}

export function NewInstanceDialog({ onCreated, onCancel }: Props) {
  const [name, setName] = useState('')
  const [directory, setDirectory] = useState('')
  const [jar, setJar] = useState('server.jar')
  const [maxMemoryMB, setMaxMemoryMB] = useState(4096)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      onCreated(
        await api.createInstance({
          name: name.trim(),
          directory: directory.trim(),
          jar: jar.trim(),
          maxMemoryMB,
          minMemoryMB: Math.min(1024, maxMemoryMB),
          serverArgs: ['--nogui'],
        }),
      )
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

        <label className="field">
          <span>服务器目录（留空自动生成）</span>
          <input
            value={directory}
            onChange={(e) => setDirectory(e.target.value)}
            placeholder="留空则放在面板数据目录的 servers/ 下"
            spellCheck={false}
          />
        </label>

        <label className="field">
          <span>服务端 jar 文件名</span>
          <input
            value={jar}
            onChange={(e) => setJar(e.target.value)}
            placeholder="server.jar"
            spellCheck={false}
          />
          <small>
            创建后把服务端 jar 放进上面的目录即可，也可以之后在「启动设置」里改。
          </small>
        </label>

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
