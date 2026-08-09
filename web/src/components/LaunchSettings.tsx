import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import type {
  InstanceInput,
  InstanceStatus,
  JarInfo,
  JavaRuntime,
  SystemJava,
} from '../types'
import { isLive } from '../types'
import { CoreDownloader } from './CoreDownloader'

interface Props {
  instance: InstanceStatus
  onSaved: (updated: InstanceStatus) => void
  onDeleted: () => void
}

function toInput(instance: InstanceStatus): InstanceInput {
  return {
    name: instance.name,
    directory: instance.directory,
    java: instance.java,
    jar: instance.jar,
    minMemoryMB: instance.minMemoryMB,
    maxMemoryMB: instance.maxMemoryMB,
    jvmArgs: instance.jvmArgs ?? [],
    serverArgs: instance.serverArgs ?? [],
    command: instance.command ?? [],
    autoStart: instance.autoStart,
    autoRestart: instance.autoRestart,
    stopCommand: instance.stopCommand,
    stopTimeoutSec: instance.stopTimeoutSec,
  }
}

/** Sentinel for the "type a path yourself" option in the Java picker. */
const CUSTOM_JAVA = '__custom__'

/** Args are edited as one-per-line text, which is far easier than a list UI. */
const toLines = (args: string[]) => args.join('\n')
const fromLines = (text: string) =>
  text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)

export function LaunchSettings({ instance, onSaved, onDeleted }: Props) {
  const [form, setForm] = useState<InstanceInput>(() => toInput(instance))
  const [jvmText, setJvmText] = useState(() => toLines(instance.jvmArgs ?? []))
  const [serverText, setServerText] = useState(() =>
    toLines(instance.serverArgs ?? []),
  )
  const [commandText, setCommandText] = useState(() =>
    toLines(instance.command ?? []),
  )
  const [jars, setJars] = useState<JarInfo[]>([])
  const [runtimes, setRuntimes] = useState<JavaRuntime[]>([])
  const [systemJava, setSystemJava] = useState<SystemJava | null>(null)
  const [javaLoaded, setJavaLoaded] = useState(false)
  const [customJava, setCustomJava] = useState(false)
  // Bumped after a core download so the jar list picks up the new file.
  const [jarsRev, setJarsRev] = useState(0)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setForm(toInput(instance))
    setJvmText(toLines(instance.jvmArgs ?? []))
    setServerText(toLines(instance.serverArgs ?? []))
    setCommandText(toLines(instance.command ?? []))
  }, [instance.id])

  useEffect(() => {
    api
      .listJars(instance.id)
      .then(setJars)
      .catch(() => setJars([]))
  }, [instance.id, instance.directory, jarsRev])

  useEffect(() => {
    api
      .javaOverview()
      .then((overview) => {
        setRuntimes(overview.runtimes)
        setSystemJava(overview.system)
      })
      .catch(() => undefined)
      .finally(() => setJavaLoaded(true))
  }, [instance.id])

  // A finished download has already been applied server-side, so the form only
  // has to catch up on the fields the daemon touched — anything else the
  // operator was editing stays as they left it.
  const onDownloaded = useCallback(
    async (fileName: string, appliedAsJar: boolean) => {
      setJarsRev((rev) => rev + 1)
      if (!appliedAsJar) return
      try {
        const fresh = await api.getInstance(instance.id)
        onSaved(fresh)
        setForm((prev) => ({ ...prev, jar: fresh.jar }))
        setServerText(toLines(fresh.serverArgs ?? []))
        setStatus(`已下载 ${fileName} 并设为启动 jar`)
        setError(null)
      } catch {
        // The jar is on disk either way; a failed refresh is not worth a banner.
      }
    },
    [instance.id, onSaved],
  )

  const update = <K extends keyof InstanceInput>(
    key: K,
    value: InstanceInput[K],
  ) => setForm((prev) => ({ ...prev, [key]: value }))

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    setStatus(null)
    try {
      const payload: InstanceInput = {
        ...form,
        jvmArgs: fromLines(jvmText),
        serverArgs: fromLines(serverText),
        command: fromLines(commandText),
      }
      onSaved(await api.updateInstance(instance.id, payload))
      setStatus(
        isLive(instance.state)
          ? '已保存，将在下次启动时生效'
          : '已保存',
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const remove = async (deleteFiles: boolean) => {
    const warning = deleteFiles
      ? `确定要删除实例「${instance.name}」并永久删除目录 ${instance.directory} 下的所有文件（含存档）吗？此操作不可撤销。`
      : `确定要从面板移除实例「${instance.name}」吗？服务器文件会保留在磁盘上。`
    if (!window.confirm(warning)) return

    setBusy(true)
    try {
      await api.deleteInstance(instance.id, deleteFiles)
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
      setBusy(false)
    }
  }

  const usingCustomCommand = fromLines(commandText).length > 0
  // Anything that is not the system java or a managed runtime is a path the
  // operator typed, so the text box stays visible for it.
  const knownJava = form.java === 'java' || runtimes.some((r) => r.javaPath === form.java)
  const showCustomJava = customJava || (javaLoaded && !knownJava)

  return (
    <form className="settings" onSubmit={save}>
      <section className="panel">
        <h3 className="panel__title">基本信息</h3>

        <label className="field">
          <span>实例名称</span>
          <input
            value={form.name}
            onChange={(e) => update('name', e.target.value)}
            required
          />
        </label>

        <label className="field">
          <span>服务器目录</span>
          <input
            value={form.directory}
            onChange={(e) => update('directory', e.target.value)}
            disabled={isLive(instance.state)}
          />
          <small>
            服务端 jar、存档和配置都放在这里。
            {isLive(instance.state) && ' 服务器运行时无法修改。'}
          </small>
        </label>
      </section>

      <CoreDownloader instance={instance} onDownloaded={onDownloaded} />

      <section className="panel">
        <h3 className="panel__title">启动方式</h3>

        <label className="field">
          <span>Java 运行时</span>
          <select
            value={showCustomJava ? CUSTOM_JAVA : form.java}
            onChange={(e) => {
              if (e.target.value === CUSTOM_JAVA) {
                setCustomJava(true)
                return
              }
              setCustomJava(false)
              update('java', e.target.value)
            }}
            disabled={usingCustomCommand}
          >
            <option value="java">
              系统 java（PATH{systemJava?.major ? `，Java ${systemJava.major}` : ''}）
            </option>
            {runtimes.map((runtime) => (
              <option key={runtime.id} value={runtime.javaPath}>
                Java {runtime.major} · {runtime.version} ·{' '}
                {runtime.imageType.toUpperCase()}（面板安装）
              </option>
            ))}
            <option value={CUSTOM_JAVA}>自定义路径…</option>
          </select>
          {showCustomJava && (
            <input
              value={form.java}
              onChange={(e) => update('java', e.target.value)}
              placeholder="/usr/lib/jvm/java-21-openjdk/bin/java"
              disabled={usingCustomCommand}
              spellCheck={false}
            />
          )}
          <small>
            {runtimes.length > 0
              ? '面板装的 Java 在这里直接选；总览页可以再装别的版本。'
              : '总览页的「Java 运行时」里可以一键装一个，装完这里就能选。'}
          </small>
        </label>

        <label className="field">
          <span>服务端 jar</span>
          <input
            value={form.jar}
            onChange={(e) => update('jar', e.target.value)}
            placeholder="server.jar"
            list={`jars-${instance.id}`}
            disabled={usingCustomCommand}
          />
          <datalist id={`jars-${instance.id}`}>
            {jars.map((jar) => (
              <option key={jar.name} value={jar.name} />
            ))}
          </datalist>
          <small>
            {jars.length > 0
              ? `目录下找到 ${jars.length} 个 jar 文件`
              : '目录下暂时没有 jar 文件，上面下一个或自己传一个'}
          </small>
        </label>

        <div className="field-row">
          <label className="field">
            <span>最小内存 (MB)</span>
            <input
              type="number"
              min={0}
              step={256}
              value={form.minMemoryMB}
              onChange={(e) => update('minMemoryMB', Number(e.target.value))}
              disabled={usingCustomCommand}
            />
          </label>
          <label className="field">
            <span>最大内存 (MB)</span>
            <input
              type="number"
              min={0}
              step={256}
              value={form.maxMemoryMB}
              onChange={(e) => update('maxMemoryMB', Number(e.target.value))}
              disabled={usingCustomCommand}
            />
          </label>
        </div>

        <label className="field">
          <span>JVM 参数</span>
          <textarea
            rows={4}
            value={jvmText}
            onChange={(e) => setJvmText(e.target.value)}
            placeholder={'-XX:+UseG1GC\n-XX:MaxGCPauseMillis=200'}
            disabled={usingCustomCommand}
          />
          <small>一行一个参数，会放在 -jar 之前。</small>
        </label>

        <label className="field">
          <span>服务端参数</span>
          <textarea
            rows={2}
            value={serverText}
            onChange={(e) => setServerText(e.target.value)}
            placeholder="--nogui"
            disabled={usingCustomCommand}
          />
          <small>一行一个参数，会放在 jar 之后。</small>
        </label>

        <label className="field">
          <span>自定义启动命令（可选）</span>
          <textarea
            rows={3}
            value={commandText}
            onChange={(e) => setCommandText(e.target.value)}
            placeholder={'./bedrock_server'}
          />
          <small>
            填了这里就完全接管启动方式，上面的 Java / jar / 内存设置全部忽略。
            一行一个参数，第一行是可执行文件。适合基岩版服务端或 start.sh 之类的启动脚本。
          </small>
        </label>
      </section>

      <section className="panel">
        <h3 className="panel__title">进程管理</h3>

        <label className="checkbox">
          <input
            type="checkbox"
            checked={form.autoStart}
            onChange={(e) => update('autoStart', e.target.checked)}
          />
          <span>面板启动时自动启动该服务器</span>
        </label>

        <label className="checkbox">
          <input
            type="checkbox"
            checked={form.autoRestart}
            onChange={(e) => update('autoRestart', e.target.checked)}
          />
          <span>崩溃后自动重启（连续失败 5 次后放弃）</span>
        </label>

        <div className="field-row">
          <label className="field">
            <span>停服命令</span>
            <input
              value={form.stopCommand}
              onChange={(e) => update('stopCommand', e.target.value)}
              placeholder="stop"
            />
          </label>
          <label className="field">
            <span>停服超时 (秒)</span>
            <input
              type="number"
              min={1}
              value={form.stopTimeoutSec}
              onChange={(e) => update('stopTimeoutSec', Number(e.target.value))}
            />
            <small>超时后发送终止信号，再等 15 秒强制结束。</small>
          </label>
        </div>
      </section>

      {error && <div className="alert alert--error">{error}</div>}
      {status && <div className="alert alert--ok">{status}</div>}

      <div className="settings__actions">
        <button className="btn btn--primary" type="submit" disabled={busy}>
          保存设置
        </button>
        <div className="settings__danger">
          <button
            className="btn"
            type="button"
            onClick={() => remove(false)}
            disabled={busy || isLive(instance.state)}
          >
            从面板移除
          </button>
          <button
            className="btn btn--danger"
            type="button"
            onClick={() => remove(true)}
            disabled={busy || isLive(instance.state)}
          >
            删除实例及所有文件
          </button>
        </div>
      </div>
    </form>
  )
}
