import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import type {
  InstanceInput,
  InstanceStatus,
  JavaRuntime,
  SystemJava,
} from '../types'
import { ENCODING_OPTIONS, isLive } from '../types'
import type { CoreController } from '../useCores'
import { useHostJars } from '../useHostJars'
import { InstanceCorePicker } from './InstanceCorePicker'
import { DirectoryField } from './PathPicker'
import { Select } from './Select'

interface Props {
  instance: InstanceStatus
  cores: CoreController
  onSaved: (updated: InstanceStatus) => void
  onDeleted: () => void
  onOpenLibrary: () => void
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
    encoding: instance.encoding || 'auto',
    tty: instance.tty ?? true,
    forceColor: instance.forceColor ?? true,
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

export function LaunchSettings({
  instance,
  cores,
  onSaved,
  onDeleted,
  onOpenLibrary,
}: Props) {
  const [form, setForm] = useState<InstanceInput>(() => toInput(instance))
  const [jvmText, setJvmText] = useState(() => toLines(instance.jvmArgs ?? []))
  const [serverText, setServerText] = useState(() =>
    toLines(instance.serverArgs ?? []),
  )
  const [commandText, setCommandText] = useState(() =>
    toLines(instance.command ?? []),
  )
  const [runtimes, setRuntimes] = useState<JavaRuntime[]>([])
  const [systemJava, setSystemJava] = useState<SystemJava | null>(null)
  const [javaLoaded, setJavaLoaded] = useState(false)
  const [customJava, setCustomJava] = useState(false)
  // Bumped after a core is copied in so the jar list picks up the new file.
  const [jarsRev, setJarsRev] = useState(0)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // Whether this host has pseudo-terminals at all. A switch that silently
  // falls back is worse than one that is visibly unavailable.
  const ttySupported = instance.ttySupported ?? true

  // The jar list follows the directory field rather than the saved config, so
  // retargeting an instance at an existing server directory offers that
  // directory's jars before the change is even saved.
  const { jars, exists: directoryExists } = useHostJars(form.directory, jarsRev)

  useEffect(() => {
    setForm(toInput(instance))
    setJvmText(toLines(instance.jvmArgs ?? []))
    setServerText(toLines(instance.serverArgs ?? []))
    setCommandText(toLines(instance.command ?? []))
  }, [instance.id])

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

  // The copy has already been applied server-side, so the form only has to
  // catch up on the fields the daemon touched — anything else the operator was
  // editing stays as they left it.
  const onCoreApplied = useCallback(
    (_fileName: string, updated: InstanceStatus, appliedAsJar: boolean) => {
      setJarsRev((rev) => rev + 1)
      if (!appliedAsJar) return
      onSaved(updated)
      setForm((prev) => ({ ...prev, jar: updated.jar }))
      setServerText(toLines(updated.serverArgs ?? []))
      setError(null)
    },
    [onSaved],
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
    const ok = await ask(
      deleteFiles
        ? {
            title: `删除实例「${instance.name}」并抹掉它的文件？`,
            lead: (
              <>
                目录 <code>{instance.directory}</code> 下的所有文件都会被永久删除，存档也在里面。
              </>
            ),
            detail:
              '此操作不可撤销，面板没有为它留回收站。配置历史也会一并删除 —— 那是唯一还能' +
              '看到旧配置的地方。要保留文件请改用「从面板移除」。',
            confirmLabel: '删除实例和文件',
            danger: true,
          }
        : {
            title: `从面板移除实例「${instance.name}」？`,
            lead: '面板不再管理它，列表里也不会再出现。',
            detail: (
              <>
                服务器文件原样留在 <code>{instance.directory}</code>，之后可以用「导入现有目录」再加回来。
                这台实例的配置历史会被删除，它存在面板的数据目录里而不是服务器目录里。
              </>
            ),
            confirmLabel: '移除',
          },
    )
    if (!ok) return

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
    <form className="stack" onSubmit={save}>
      <section className="panel panel--form">
        <h3 className="panel__title">基本信息</h3>

        <label className="field">
          <span>实例名称</span>
          <input
            value={form.name}
            onChange={(e) => update('name', e.target.value)}
            required
          />
        </label>

        <DirectoryField
          className="field--full"
          value={form.directory}
          onChange={(value) => update('directory', value)}
          disabled={isLive(instance.state)}
          hint={
            <>
              服务端 jar、存档和配置都放在这里。「浏览…」可以指到本机任意位置，
              包括一个已经有服务端的目录。
              {!directoryExists && ' 这个目录还不存在，保存后会在启动时创建。'}
              {isLive(instance.state) && ' 服务器运行时无法修改。'}
            </>
          }
        />
      </section>

      <InstanceCorePicker
        instance={instance}
        cores={cores}
        onApplied={onCoreApplied}
        onOpenLibrary={onOpenLibrary}
      />

      <section className="panel panel--form">
        <h3 className="panel__title">启动方式</h3>

        <label className="field">
          <span>Java 环境</span>
          <Select
            ariaLabel="Java 环境"
            value={showCustomJava ? CUSTOM_JAVA : form.java}
            disabled={usingCustomCommand}
            options={[
              {
                value: 'java',
                label: '系统 java（PATH）',
                note: systemJava?.major ? `Java ${systemJava.major}` : undefined,
              },
              ...runtimes.map((runtime) => ({
                value: runtime.javaPath,
                label: `Java ${runtime.major} · ${runtime.version}`,
                note: `${runtime.imageType.toUpperCase()}（面板安装）`,
              })),
              { value: CUSTOM_JAVA, label: '自定义路径…' },
            ]}
            onChange={(next) => {
              if (next === CUSTOM_JAVA) {
                setCustomJava(true)
                return
              }
              setCustomJava(false)
              update('java', next)
            }}
          />
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
              ? '面板装的 Java 在这里直接选；「资源库 → Java 环境」可以再装别的版本。'
              : '「资源库 → Java 环境」可以一键装一个，装完这里就能选。'}
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
              ? `上面这个目录下找到 ${jars.length} 个 jar 文件，点输入框可以直接选`
              : '目录下暂时没有 jar 文件，从上面装一个核心，或自己传一个'}
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

        <label className="field field--full">
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

        <label className="field field--full">
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

        <label className="field field--full">
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

      <section className="panel panel--form">
        <h3 className="panel__title">控制台</h3>

        <label className="field">
          <span>输出编码</span>
          <Select
            ariaLabel="输出编码"
            value={form.encoding}
            options={ENCODING_OPTIONS.map((option) => ({
              value: option.value,
              label: option.label,
            }))}
            onChange={(next) => update('encoding', next)}
          />
          <small>
            控制台按这个编码解读服务器输出、并按同样的编码发送命令。「自动」会让 JVM 用
            UTF-8 输出，同时对不是 UTF-8 的行按系统编码兜底 —— 中文 Windows 上出现乱码时，
            如果用的是自定义启动脚本，改成 GBK 通常就好了。
          </small>
        </label>

        <label className="checkbox">
          <input
            type="checkbox"
            checked={form.tty && ttySupported}
            disabled={!ttySupported}
            onChange={(e) => update('tty', e.target.checked)}
          />
          <span>使用终端模式（推荐）</span>
          <small>
            {ttySupported ? (
              <>
                把服务器跑在伪终端上，就像你自己在 SSH 里开着它一样。这样 Tab 补全由
                <strong>正在运行的服务端</strong>回答（插件命令、真实玩家名都算数），
                进度条不用等换行就能看到，颜色也不需要强制。代价是终端只有一条流，
                stderr 不再单独标红。关掉则回到管道模式。
              </>
            ) : (
              <>本系统没有可用的伪终端（Windows 需要 ConPTY），所有实例都以管道模式运行。</>
            )}
          </small>
        </label>

        <label className="checkbox">
          <input
            type="checkbox"
            checked={form.forceColor}
            disabled={form.tty && ttySupported}
            onChange={(e) => update('forceColor', e.target.checked)}
          />
          <span>强制彩色输出（推荐）</span>
          <small>
            仅在管道模式下有意义：服务端只在检测到终端时才上色，所以管道模式会加上
            <code> -Dterminal.jline=false -Dterminal.ansi=true</code>，让网页控制台和
            cmd 里一样有颜色。终端模式下服务端本来就看得到终端，这两个参数不会被加上
            —— <code>terminal.jline=false</code> 恰好会关掉终端模式想要的那个补全。
            自定义启动命令不受影响，需要自己加。
          </small>
        </label>
      </section>

      <section className="panel panel--form">
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

      <div className="actions">
        <button className="btn btn--primary" type="submit" disabled={busy}>
          保存设置
        </button>
        <div className="actions__danger">
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
