import { useEffect, useRef, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { HostInspection, InstanceStatus } from '../types'
import { Modal } from './Modal'
import { DirectoryField } from './PathPicker'

interface Props {
  onImported: (instance: InstanceStatus) => void
  onCancel: () => void
}

/** How long the operator has to stop typing before the directory is inspected. */
const DEBOUNCE_MS = 400

/**
 * Adopts a server that already exists on this machine.
 *
 * 新建 and 导入 are the same POST underneath, and the temptation is to make them
 * the same dialog with a checkbox. They are not the same act: creating asks
 * what you want, importing asks what is already there — so this one is mostly
 * read-out rather than input. Point it at a directory and it says what it
 * found: the jar it would launch, the world names, how many plugins, whether
 * the EULA was already accepted. Nothing is written until 导入 is pressed, and
 * even then only the panel's own instance list changes: the directory is
 * adopted exactly as it is, no core copied in, no properties rewritten.
 *
 * Someone doing this has a server they care about — usually the one they have
 * been running by hand for a year — so the failure this is built to avoid is
 * silently pointing a second instance at a live world. That is the one thing
 * here that refuses rather than warns.
 */
export function ImportInstanceDialog({ onImported, onCancel }: Props) {
  const [directory, setDirectory] = useState('')
  const [name, setName] = useState('')
  const [jar, setJar] = useState('')
  const [maxMemoryMB, setMaxMemoryMB] = useState(4096)
  const [found, setFound] = useState<HostInspection | null>(null)
  const [scanning, setScanning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // Set once the operator edits a field, so a later scan of another directory
  // stops overwriting what they typed.
  const touchedName = useRef(false)
  const touchedJar = useRef(false)

  useEffect(() => {
    const dir = directory.trim()
    if (dir === '') {
      setFound(null)
      return
    }

    let live = true
    setScanning(true)
    const timer = window.setTimeout(() => {
      api
        .inspectHost(dir)
        .then((inspection) => {
          if (!live) return
          setFound(inspection)
          setError(null)
          if (!touchedName.current) setName(inspection.name)
          if (!touchedJar.current) setJar(inspection.jar ?? '')
        })
        .catch((err) => {
          if (!live) return
          setFound(null)
          setError(err instanceof Error ? err.message : '读不了这个目录')
        })
        .finally(() => live && setScanning(false))
    }, DEBOUNCE_MS)

    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [directory])

  const taken = found?.takenBy
  const missing = found !== null && !found.exists
  const blocked = directory.trim() === '' || Boolean(taken) || missing

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (blocked) return
    setBusy(true)
    setError(null)
    try {
      // A directory with a velocity.toml in it is a proxy, and adopting it as
      // a server would hand it --nogui, "stop" and a 服务器配置 page about a
      // file it does not have.
      const proxy = found?.proxy === true
      const created = await api.createInstance({
        kind: proxy ? 'proxy' : 'server',
        name: name.trim(),
        directory: directory.trim(),
        jar: jar.trim(),
        maxMemoryMB,
        minMemoryMB: Math.min(1024, maxMemoryMB),
        serverArgs: proxy ? [] : ['--nogui'],
      })
      onImported(created)
    } catch (err) {
      setError(err instanceof Error ? err.message : '导入失败')
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onCancel} busy={busy}>
      <form className="modal__card modal__card--wide" onSubmit={submit}>
        <h2 className="modal__title">导入现有目录</h2>
        <p className="modal__lead">
          机器上已经有一个服务端目录 —— 手动跑了很久的服，或者从别的面板搬过来的 ——
          选中它，面板就接管它，目录里的世界、插件、配置一个都不会动。
        </p>

        <DirectoryField
          value={directory}
          onChange={setDirectory}
          label="服务器目录"
          placeholder="/opt/minecraft/survival"
          hint="填服务端 jar 所在的那一层目录，也就是 server.properties 和 world/ 的旁边。"
        />

        {directory.trim() !== '' && (
          <Inspection found={found} scanning={scanning} />
        )}

        {found?.exists && (
          <>
            <label className="field">
              <span>实例名称</span>
              <input
                value={name}
                onChange={(e) => {
                  touchedName.current = true
                  setName(e.target.value)
                }}
                placeholder="生存服"
                required
              />
              <small>面板里怎么称呼它，随时可以改，和目录名无关。</small>
            </label>

            <label className="field">
              <span>服务端 jar 文件名</span>
              <input
                value={jar}
                onChange={(e) => {
                  touchedJar.current = true
                  setJar(e.target.value)
                }}
                placeholder="server.jar"
                list="import-instance-jars"
                spellCheck={false}
              />
              <datalist id="import-instance-jars">
                {(found.jars ?? []).map((entry) => (
                  <option key={entry.name} value={entry.name} />
                ))}
              </datalist>
              <small>
                {found.jar
                  ? `目录里挑出来的是 ${found.jar}，不对的话点输入框换一个。`
                  : '目录里没找到 jar —— 导入之后可以在「实例设置」里指定，或者从核心库装一个。'}
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
              <small>原来的启动脚本用多少就填多少；导入不会去读那个脚本。</small>
            </label>
          </>
        )}

        {error && <div className="alert alert--error">{error}</div>}

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button className="btn btn--primary" type="submit" disabled={busy || blocked}>
            {busy ? '导入中…' : '导入'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

/**
 * What the panel found in the directory.
 *
 * Deliberately a read-out and not a validator: the only thing it refuses is a
 * directory another instance already owns. A folder with no jar, or one that
 * has never been started, is a perfectly ordinary thing to adopt — the point
 * is that the operator can see which it is before pressing the button.
 */
function Inspection({ found, scanning }: { found: HostInspection | null; scanning: boolean }) {
  if (!found) {
    return <p className="muted">{scanning ? '正在查看这个目录…' : '还没有读到这个目录。'}</p>
  }
  if (found.takenBy) {
    return (
      <div className="alert alert--error">
        实例「{found.takenBy}」已经在用这个目录了。两个实例指向同一个世界，一起开服会把存档写坏
        —— 换一个目录，或者直接去用那个实例。
      </div>
    )
  }
  if (!found.exists) {
    return (
      <div className="alert alert--error">
        这个目录不存在。导入是接管已有的服务端；要新开一个服，用「新建实例」。
      </div>
    )
  }
  if (found.error) {
    return <div className="alert alert--error">读不了这个目录：{found.error}</div>
  }

  const facts = [
    found.jar
      ? `服务端 ${found.jar}${jarSize(found)}`
      : found.jars.length > 0
        ? `${found.jars.length} 个 jar，认不出哪个是服务端`
        : '没有 jar',
    // A proxy has neither of these, and saying it has no world reads as a
    // problem with the directory rather than as what a proxy is.
    found.proxy
      ? ''
      : found.worlds && found.worlds.length > 0
        ? `世界 ${found.worlds.join('、')}`
        : '还没有世界存档',
    found.plugins > 0 ? `${found.plugins} 个插件` : '',
    found.mods > 0 ? `${found.mods} 个模组` : '',
    found.properties?.port ? `端口 ${found.properties.port}` : '',
    found.proxy
      ? ''
      : found.eula === 'accepted'
        ? 'EULA 已同意'
        : found.eula === 'declined'
          ? 'EULA 未同意'
          : '',
  ].filter(Boolean)

  return (
    <div className={found.server ? 'alert alert--ok' : 'alert'}>
      {found.proxy
        ? '这看着是一个 Velocity 代理端目录，会按代理端导入。'
        : found.server
          ? '这看着是一个服务端目录。'
          : '目录里没有服务端的痕迹，确认一下是不是选错了。'}
      <p className="meta-chips">
        {facts.map((fact) => (
          <span key={fact}>{fact}</span>
        ))}
      </p>
      {found.properties?.motd && <p className="chart-note">MOTD：{found.properties.motd}</p>}
      {found.eula !== 'accepted' && found.server && !found.proxy && (
        <p className="chart-note">
          EULA 还没同意，导入之后在「服务器配置」页勾一下就能启动。
        </p>
      )}
    </div>
  )
}

function jarSize(found: HostInspection): string {
  const entry = found.jars.find((item) => item.name === found.jar)
  return entry ? `（${formatBytes(entry.size)}）` : ''
}
