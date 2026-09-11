import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import type {
  InstanceInput,
  InstanceStatus,
  JavaRuntime,
  JVMArgs,
  LaunchCheck,
  LaunchIssue,
  SystemJava,
} from '../types'
import { ENCODING_OPTIONS, isLive, LOADER_OPTIONS } from '../types'
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
    loader: instance.loader ?? '',
    gameVersion: instance.gameVersion ?? '',
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
    javaToolOptions: instance.javaToolOptions ?? true,
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
  // Which of the two launch modes the form is showing. Seeded from whether a
  // command is stored — that is the only record of it — but held here so that
  // switching to 脚本 on an instance with no command yet does not snap back.
  const [scriptMode, setScriptMode] = useState(
    () => (instance.command?.length ?? 0) > 0,
  )
  const [check, setCheck] = useState<LaunchCheck | null>(null)
  const [checkRev, setCheckRev] = useState(0)
  const [fixing, setFixing] = useState(false)
  const [jvm, setJvm] = useState<JVMArgs | null>(null)
  const [jvmMin, setJvmMin] = useState(0)
  const [jvmMax, setJvmMax] = useState(0)
  const [jvmBusy, setJvmBusy] = useState(false)
  const [jvmStatus, setJvmStatus] = useState<string | null>(null)

  // A proxy launches differently enough to be worth saying so in two
  // placeholders: it answers "end" rather than "stop", and it exits on the
  // --nogui every Minecraft server wants.
  const proxy = instance.kind === 'proxy'

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
    setScriptMode((instance.command?.length ?? 0) > 0)
  }, [instance.id])

  // The check reads the script off disk, so it is re-run after every save and
  // after the one repair the panel offers, not just on arrival.
  useEffect(() => {
    let cancelled = false
    api
      .launchCheck(instance.id)
      .then((result) => {
        if (!cancelled) setCheck(result)
      })
      .catch(() => {
        if (!cancelled) setCheck(null)
      })
    return () => {
      cancelled = true
    }
  }, [instance.id, checkRev])

  // Forge's user_jvm_args.txt. Absent for anything that is not Forge-shaped,
  // which is the normal case and not an error.
  useEffect(() => {
    let cancelled = false
    api
      .jvmArgs(instance.id)
      .then((result) => {
        if (cancelled) return
        setJvm(result)
        setJvmMin(result.minMemoryMB)
        setJvmMax(result.maxMemoryMB)
      })
      .catch(() => {
        if (!cancelled) setJvm(null)
      })
    return () => {
      cancelled = true
    }
  }, [instance.id, checkRev])

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
    const command = scriptMode ? fromLines(commandText) : []
    if (scriptMode && command.length === 0) {
      setError('选了脚本启动，就得填启动命令 —— 第一行是可执行文件。')
      return
    }
    setBusy(true)
    setError(null)
    setStatus(null)
    try {
      const payload: InstanceInput = {
        ...form,
        jvmArgs: fromLines(jvmText),
        serverArgs: fromLines(serverText),
        // An empty command is how the daemon is told to build the command line
        // itself, so switching back to jar mode has to send one.
        command,
      }
      onSaved(await api.updateInstance(instance.id, payload))
      setStatus(
        isLive(instance.state)
          ? '已保存，将在下次启动时生效'
          : '已保存',
      )
      setCheckRev((rev) => rev + 1)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const applyFix = async (action: string) => {
    setFixing(true)
    setError(null)
    try {
      setCheck(await api.fixLaunch(instance.id, action))
    } catch (err) {
      setError(err instanceof Error ? err.message : '修复失败')
    } finally {
      setFixing(false)
    }
  }

  // The heap of a script-launched server lives in a file, not in the instance
  // config, so it saves to its own endpoint rather than riding along with the
  // form — a half-applied save across two writes would be worse than two
  // buttons.
  const saveJVMArgs = async () => {
    setJvmBusy(true)
    setError(null)
    setJvmStatus(null)
    try {
      const saved = await api.saveJVMArgs(instance.id, {
        minMemoryMB: jvmMin,
        maxMemoryMB: jvmMax,
      })
      setJvm(saved)
      setJvmMin(saved.minMemoryMB)
      setJvmMax(saved.maxMemoryMB)
      setJvmStatus(
        isLive(instance.state) ? '已写入，下次启动生效' : '已写入 ' + saved.fileName,
      )
      // The instance's reported heap ceiling comes from this file.
      onSaved(await api.getInstance(instance.id))
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setJvmBusy(false)
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

        <div className="field-row">
          <label className="field">
            <span>服务端类型</span>
            <Select
              ariaLabel="服务端类型"
              value={form.loader}
              options={LOADER_OPTIONS.map((entry) => ({
                value: entry.value,
                label: entry.label,
                note: entry.note,
              }))}
              onChange={(next) => update('loader', next)}
            />
          </label>
          <label className="field">
            <span>游戏版本</span>
            <input
              value={form.gameVersion}
              onChange={(e) => update('gameVersion', e.target.value)}
              placeholder="1.20.1"
              spellCheck={false}
            />
          </label>
        </div>

        <p className="muted">
          面板先从目录和 jar 名认，认不出来才用这里填的。
          <strong>脚本启动的服基本都认不出来</strong> —— 没有 jar 名可读，
          <code>version_history.json</code> 也只有 Paper 系才写。认不出来的后果很具体：
          mod 会被装进 <code>plugins/</code> 而不是 <code>mods/</code>，插件市场里每一条也都标成「未知」。
        </p>
      </section>

      <InstanceCorePicker
        instance={instance}
        cores={cores}
        onApplied={onCoreApplied}
        onOpenLibrary={onOpenLibrary}
        jarIgnored={scriptMode}
      />

      <LaunchCheckPanel
        check={check}
        busy={fixing}
        onFix={applyFix}
        onRecheck={() => setCheckRev((rev) => rev + 1)}
      />

      <section className="panel panel--form">
        <h3 className="panel__title">启动方式</h3>

        <div className="segmented" role="group" aria-label="启动方式">
          {[
            {
              value: false,
              label: '面板拼命令',
              note: 'java -Xmx… -jar server.jar',
            },
            {
              value: true,
              label: '用我的脚本',
              note: 'Forge 的 run.sh、基岩版、start.sh',
            },
          ].map((entry) => (
            <button
              key={String(entry.value)}
              type="button"
              className={`segmented__option${
                scriptMode === entry.value ? ' segmented__option--active' : ''
              }`}
              aria-pressed={scriptMode === entry.value}
              onClick={() => setScriptMode(entry.value)}
            >
              <strong>{entry.label}</strong>
              <small>{entry.note}</small>
            </button>
          ))}
        </div>

        {/* The Java choice applies in both modes, but reaches the server by
            two different routes, and saying which one matters: in script mode
            the panel cannot put a path on a command line it did not build, so
            it sets JAVA_HOME and puts the JDK first on PATH instead. */}
        <label className="field">
          <span>Java 环境</span>
          <Select
            ariaLabel="Java 环境"
            value={showCustomJava ? CUSTOM_JAVA : form.java}
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
              spellCheck={false}
            />
          )}
          <small>
            {scriptMode ? (
              <>
                脚本里那句 <code>java</code> 走的是 PATH，所以面板会把选中的 JDK 放到
                <code> PATH</code> 最前面并设好 <code>JAVA_HOME</code>，脚本一个字都不用改。
                选「系统 java」就完全交给这台机器自己决定。
              </>
            ) : runtimes.length > 0 ? (
              '面板装的 Java 在这里直接选；「资源库 → Java 环境」可以再装别的版本。'
            ) : (
              '「资源库 → Java 环境」可以一键装一个，装完这里就能选。'
            )}
          </small>
        </label>

        {scriptMode ? (
          <>
            <label className="field field--full">
              <span>启动命令</span>
              <textarea
                rows={4}
                value={commandText}
                onChange={(e) => setCommandText(e.target.value)}
                placeholder={'./run.sh'}
                spellCheck={false}
              />
              <small>
                一行一个参数，第一行是可执行文件，相对路径从实例目录算起 ——
                <code>./run.sh</code> 才是这个目录里的文件，<code>run.sh</code> 会被当成
                PATH 里的命令去找。
                <strong>脚本必须自己把 JVM 跑在前台</strong>：<code>nohup</code>、行尾的
                <code> &amp;</code>、screen、tmux 都会让面板在一秒内认为服务器已经退出，
                而它其实还开着。写成 <code>exec java …</code> 最省事。
              </small>
            </label>

            <label className="checkbox field--full">
              <input
                type="checkbox"
                checked={form.javaToolOptions}
                onChange={(e) => update('javaToolOptions', e.target.checked)}
              />
              <span>把编码参数传给脚本（推荐）</span>
              <small>
                面板没法往你的命令行里加参数，所以这些参数通过
                <code> JAVA_TOOL_OPTIONS</code> 传进去 —— 主要是
                <code> -Dfile.encoding=UTF-8</code> 那一组，中文输出乱码基本都是少了它们。
                代价是每次启动控制台第一行会多出一句
                <code> Picked up JAVA_TOOL_OPTIONS</code>。脚本里自己写的参数优先级更高，
                随时能盖掉这里的。
              </small>
            </label>

            <ScriptMemory
              jvm={jvm}
              min={jvmMin}
              max={jvmMax}
              busy={jvmBusy}
              status={jvmStatus}
              onMin={setJvmMin}
              onMax={setJvmMax}
              onSave={saveJVMArgs}
            />
          </>
        ) : (
          <>
            <label className="field">
              <span>服务端 jar</span>
              <input
                value={form.jar}
                onChange={(e) => update('jar', e.target.value)}
                placeholder="server.jar"
                list={`jars-${instance.id}`}
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
              />
              <small>一行一个参数，会放在 -jar 之前。</small>
            </label>

            <label className="field field--full">
              <span>服务端参数</span>
              <textarea
                rows={2}
                value={serverText}
                onChange={(e) => setServerText(e.target.value)}
                placeholder={proxy ? '' : '--nogui'}
              />
              <small>
                一行一个参数，会放在 jar 之后。
                {proxy && ' Velocity 遇到不认识的参数会直接退出，一般这里留空。'}
              </small>
            </label>
          </>
        )}
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
            UTF-8 输出，同时对不是 UTF-8 的行按系统编码兜底。用自己的脚本启动时，
            「让 JVM 用 UTF-8」这半件事要靠上面那个 <code>JAVA_TOOL_OPTIONS</code> 开关；
            那个关着、中文 Windows 上又乱码的话，这里改成 GBK 通常就好了。
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
            用自己的脚本启动时，这两个参数走
            <code> JAVA_TOOL_OPTIONS</code> 送进去，要在上面把那个开关留着。
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
              placeholder={proxy ? 'end' : 'stop'}
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

/** How each level of finding is introduced. Named for what it means to the
 *  person about to press 启动, not for a severity scale. */
const LEVEL_LABELS: Record<LaunchIssue['level'], string> = {
  fatal: '起不来',
  warn: '能起来，但面板管不住',
  info: '提示',
}

/**
 * What the panel expects to happen when this instance is started.
 *
 * It exists for one failure in particular, and that failure is invisible from
 * everywhere else: a start script that backgrounds the JVM starts the server
 * perfectly well and hands the panel a process that exits a second later. The
 * panel then shows 已停止 for a server everyone can see running, the console
 * goes nowhere, 停止 does nothing, and 崩溃自动重启 starts a second copy on the
 * same world. Nothing in that chain points at the script, so the panel says it
 * here, before the first start, instead of leaving it to be discovered.
 */
function LaunchCheckPanel({
  check,
  busy,
  onFix,
  onRecheck,
}: {
  check: LaunchCheck | null
  busy: boolean
  onFix: (action: string) => void
  onRecheck: () => void
}) {
  if (check === null) return null

  return (
    <section className="panel panel--form">
      <h3 className="panel__title">开服前检查</h3>

      {check.issues.length === 0 ? (
        <p className="muted">
          没发现问题。
          {check.mode === 'script' && check.script
            ? `启动命令指得到，${check.script} 也没有把服务端丢到后台的写法。`
            : '核心和目录都对得上。'}
        </p>
      ) : (
        <ul className="launchcheck">
          {check.issues.map((issue) => (
            <li
              key={issue.code}
              className={`launchcheck__item launchcheck__item--${issue.level}`}
            >
              <strong className="launchcheck__level">{LEVEL_LABELS[issue.level]}</strong>
              <p className="launchcheck__text">{issue.message}</p>
              {issue.detail && (
                <code className="launchcheck__line">
                  {issue.line ? `${check.script ?? ''}:${issue.line}  ` : ''}
                  {issue.detail}
                </code>
              )}
              {issue.fix === 'chmod' && (
                <button
                  className="btn btn--row"
                  type="button"
                  disabled={busy}
                  onClick={() => onFix('chmod')}
                >
                  加上执行位
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      <div className="actions">
        <button className="btn btn--row" type="button" disabled={busy} onClick={onRecheck}>
          重新检查
        </button>
      </div>
    </section>
  )
}

/**
 * The heap of a script-launched server, which is not in the instance config.
 *
 * The memory fields above build a -Xmx onto a command line; a script owns its
 * own, so that number never reaches the JVM. Forge and NeoForge read
 * user_jvm_args.txt for exactly this, so the control moves there rather than
 * disappearing — and where there is no such file the panel says so instead of
 * showing a slider that changes nothing.
 */
function ScriptMemory({
  jvm,
  min,
  max,
  busy,
  status,
  onMin,
  onMax,
  onSave,
}: {
  jvm: JVMArgs | null
  min: number
  max: number
  busy: boolean
  status: string | null
  onMin: (value: number) => void
  onMax: (value: number) => void
  onSave: () => void
}) {
  if (!jvm?.exists) {
    return (
      <p className="muted field--full">
        内存由你的脚本自己决定，面板不去猜 —— 上面那组内存设置只对「面板拼命令」有效。
        Forge / NeoForge 的服务端可以把 <code>-Xmx</code> 写进
        <code> user_jvm_args.txt</code>，那个文件在时这里会直接变成可编辑的。
      </p>
    )
  }

  const others = jvm.args.filter((arg) => !/^-X(mx|ms)/.test(arg))

  return (
    <>
      <div className="field-row">
        <label className="field">
          <span>最小内存 (MB)</span>
          <input
            type="number"
            min={0}
            step={256}
            value={min}
            onChange={(e) => onMin(Number(e.target.value))}
          />
        </label>
        <label className="field">
          <span>最大内存 (MB)</span>
          <input
            type="number"
            min={0}
            step={256}
            value={max}
            onChange={(e) => onMax(Number(e.target.value))}
          />
        </label>
      </div>

      <p className="muted field--full">
        这两个数写进 <code>{jvm.fileName}</code>，也就是 Forge 的
        <code> run.sh</code> 真正会读的那个文件 —— 文件里的注释和其他参数都会原样保留，
        填 0 是删掉这一行。
        {others.length > 0 && (
          <>
            {' '}
            文件里还有面板没在这儿提供开关的参数：<code>{others.join(' ')}</code>，
            要改去「文件」页。
          </>
        )}
      </p>

      <div className="actions">
        <button className="btn btn--row" type="button" disabled={busy} onClick={onSave}>
          写入 {jvm.fileName}
        </button>
        {status && <span className="muted">{status}</span>}
      </div>
    </>
  )
}
