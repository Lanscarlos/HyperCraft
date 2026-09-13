import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import { formatBytes } from '../format'
import type {
  InstanceInput,
  InstanceStatus,
  JavaRuntime,
  JVMArgs,
  LaunchCheck,
  LaunchDraft,
  LaunchIssue,
} from '../types'
import { ENCODING_OPTIONS, isLive, LOADER_OPTIONS } from '../types'
import { JVM_PRESETS } from '../jvmPresets'
import { Button } from './Button'
import { JVMArgsEditor } from './JVMArgsEditor'
import { FieldHelp } from './FieldHelp'
import { ScriptImportDialog } from './ScriptImportDialog'
import type { CoreController } from '../useCores'
import { useHostJars } from '../useHostJars'
import { InstanceCorePicker } from './InstanceCorePicker'
import { PageHead } from './Page'
import { DirectoryField } from './PathPicker'
import { Section } from './Section'
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
    argFiles: instance.argFiles ?? [],
    encoding: instance.encoding || 'auto',
    tty: instance.tty ?? true,
    forceColor: instance.forceColor ?? true,
    autoStart: instance.autoStart,
    autoRestart: instance.autoRestart,
    stopCommand: instance.stopCommand,
    stopTimeoutSec: instance.stopTimeoutSec,
  }
}

/** Where the JVM 参数 view preference is kept. */
const JVM_VIEW_KEY = 'hc.jvmargs.view'

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
  const [argFileText, setArgFileText] = useState(() =>
    toLines(instance.argFiles ?? []),
  )
  const [runtimes, setRuntimes] = useState<JavaRuntime[]>([])
  const [javaLoaded, setJavaLoaded] = useState(false)
  // Bumped after a core is copied in so the jar list picks up the new file.
  const [jarsRev, setJarsRev] = useState(0)
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Which of the two launch targets the form is showing. Seeded from whether
  // argfiles are stored — that is the only record of it — but held here so
  // that switching to 参数文件 on an instance that has none yet does not snap
  // back.
  const [argFileMode, setArgFileMode] = useState(
    () => (instance.argFiles?.length ?? 0) > 0,
  )
  // Reading an existing start script for the settings in it. The form is the
  // preview: nothing is applied until 填进表单 is pressed, and nothing is
  // stored until 保存 is. So every drafted value goes through the same fields,
  // and the same eyes, as one typed by hand.
  const [importing, setImporting] = useState(false)
  // Which preset was last pressed, so its one line of "why you would pick
  // this" can sit under the row instead of on twenty hover targets.
  const [preset, setPreset] = useState<string | null>(null)
  const [check, setCheck] = useState<LaunchCheck | null>(null)
  const [checkRev, setCheckRev] = useState(0)
  const [jvm, setJvm] = useState<JVMArgs | null>(null)
  const [jvmMin, setJvmMin] = useState(0)
  const [jvmMax, setJvmMax] = useState(0)
  const [jvmBusy, setJvmBusy] = useState(false)
  const [jvmStatus, setJvmStatus] = useState<string | null>(null)
  // Rows or the raw text. Remembered because it is a preference about how you
  // read arguments, not about this instance — someone who thinks in text wants
  // text on every instance, and having to say so on each one is the annoying
  // half of offering the choice at all.
  const [jvmRows, setJvmRows] = useState(
    () => window.localStorage.getItem(JVM_VIEW_KEY) !== 'text',
  )

  const setJvmView = (rows: boolean) => {
    setJvmRows(rows)
    window.localStorage.setItem(JVM_VIEW_KEY, rows ? 'rows' : 'text')
  }

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
    setArgFileText(toLines(instance.argFiles ?? []))
  }, [instance.id])

  useEffect(() => {
    setArgFileMode((instance.argFiles?.length ?? 0) > 0)
  }, [instance.id])

  // The check stats the launch target on disk, so it is re-run after every
  // save rather than only on arrival.
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
      .then((overview) => setRuntimes(overview.runtimes))
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

  // What a parsed script is allowed to overwrite here.
  //
  // Not Java and not the jar, even though the script names both. By the time
  // an instance exists the panel owns those two — Java comes from 资源库 →
  // Java 环境 and the jar from the core library — and letting a run.sh from
  // 2019 put /usr/lib/jvm/java-8 back would undo a choice made deliberately
  // in the panel. The dialog reads them out instead, because "this used to run
  // on Java 8" explains a server that will not start on 25.
  //
  // Argfiles are the exception that proves the rule: nothing in the panel
  // writes them. A Forge unix_args.txt path carries the exact version
  // (libraries/net/neoforged/neoforge/21.1.9/unix_args.txt) and there is no
  // picker for it anywhere, so a script is the only place it can come from
  // without being typed by hand.
  const applyDraft = (draft: LaunchDraft) => {
    setForm((prev) => ({
      ...prev,
      minMemoryMB: draft.minMemoryMB,
      maxMemoryMB: draft.maxMemoryMB,
    }))
    setJvmText(toLines(draft.jvmArgs))
    setServerText(toLines(draft.serverArgs))
    setArgFileText(toLines(draft.argFiles))
    // One-way. A script with argfiles has to switch the form over or the value it
    // just filled in is on a tab nobody is looking at; a script without them
    // leaves the mode alone, because switching back to 核心 jar would land on
    // an empty jar field — the one thing this deliberately does not import.
    if (draft.argFiles.length > 0) setArgFileMode(true)
    setError(null)
    setStatus(
      draft.argFiles.length > 0
        ? '已填进表单（Java 和核心没动），确认无误再点保存'
        : '已填进表单（Java 和服务端 jar 没动），确认无误再点保存',
    )
  }

  // Presets replace the box rather than appending to it: these sets are tuned
  // as wholes and half of one merged into half of another is not a third
  // tuning, it is a bug report. Which is also why something already in there
  // is worth one question first.
  const applyPreset = async (id: string) => {
    const chosen = JVM_PRESETS.find((entry) => entry.id === id)
    if (!chosen) return
    if (jvmText.trim() !== '') {
      const ok = await ask({
        title: `用「${chosen.label}」替换现在的 JVM 参数？`,
        lead: '预设是整套替换，不是往后面追加。',
        detail: '现在框里那几行会被清掉。还没保存，觉得不对可以直接改回来或者离开这页。',
        confirmLabel: '替换',
      })
      if (!ok) return
    }
    setJvmText(toLines(chosen.args(form.maxMemoryMB)))
    setPreset(chosen.id)
    setStatus(`已填上「${chosen.label}」，确认无误再点保存`)
  }

  const presetNote = JVM_PRESETS.find((entry) => entry.id === preset)?.note ?? null

  // The one server argument worth a shortcut. --forceUpgrade and --eraseCache
  // are deliberately not offered: they are one-shot conversions, and a control
  // that remembers one is a control that runs it again on every restart.
  const hasNogui = fromLines(serverText).includes('--nogui')
  const toggleNogui = () =>
    setServerText((text) => {
      const args = fromLines(text)
      return toLines(
        hasNogui ? args.filter((arg) => arg !== '--nogui') : [...args, '--nogui'],
      )
    })

  // Aikar's set assumes -Xms equals -Xmx. Checked against the box rather than
  // against `preset`, so it also catches the case that actually bites: the
  // flags arrived by reading somebody's run.sh, not by pressing the button.
  const aikarNeedsEqualHeap =
    jvmText.includes('using.aikars.flags') && form.minMemoryMB !== form.maxMemoryMB

  // What 保存 would send, against what is stored. Built exactly the way save()
  // builds its payload, so the bar cannot claim there is nothing to save while
  // the button would still write something.
  //
  // Field by field rather than JSON.stringify on the two objects: the payload is
  // a spread with three keys reassigned, and leaning on a spread to preserve key
  // order for a string comparison is a bug waiting for someone to reorder
  // toInput().
  const stored = toInput(instance)
  const pending: InstanceInput = {
    ...form,
    jvmArgs: fromLines(jvmText),
    serverArgs: fromLines(serverText),
    argFiles: argFileMode ? fromLines(argFileText) : [],
  }
  const dirty = (Object.keys(stored) as (keyof InstanceInput)[]).some((key) => {
    const a = stored[key]
    const b = pending[key]
    if (Array.isArray(a) && Array.isArray(b)) {
      return a.length !== b.length || a.some((item, at) => item !== b[at])
    }
    return a !== b
  })

  // Back to what is stored. The same setters the instance-change effect uses, so
  // 放弃 and switching instances land in exactly the same state.
  const revert = () => {
    setForm(toInput(instance))
    setJvmText(toLines(instance.jvmArgs ?? []))
    setServerText(toLines(instance.serverArgs ?? []))
    setArgFileText(toLines(instance.argFiles ?? []))
    setArgFileMode((instance.argFiles?.length ?? 0) > 0)
    setError(null)
    setStatus(null)
  }

  const update = <K extends keyof InstanceInput>(
    key: K,
    value: InstanceInput[K],
  ) => setForm((prev) => ({ ...prev, [key]: value }))

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    const argFiles = argFileMode ? fromLines(argFileText) : []
    if (argFileMode && argFiles.length === 0) {
      setError('选了参数文件启动，就得至少填一个文件 —— 一行一个，路径从实例目录算起。')
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
        // An empty list is how the daemon is told this is a jar launch, so
        // switching back to 核心 jar has to send one.
        argFiles,
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



  // The heap of an argfile-launched server lives in a file, not in the
  // instance config, so it saves to its own endpoint rather than riding along with the
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

  // The Java dropdown, and the one patch that keeps it honest.
  //
  // An instance can point at a path that is not in the list: one the startup
  // migration could not probe, or one whose runtime has since been deleted.
  // Without a row for it the select would render blank and the first save
  // would silently rewrite java to whichever option happens to be first — a
  // change to what the server executes that nobody asked for and nobody sees.
  // So the current value always has a row, marked for what it is.
  //
  // Guarded on javaLoaded: before the list arrives every value looks unknown,
  // and a row that says 未登记 for half a second is a lie that flickers.
  const javaOptions = [
    ...runtimes.map((runtime) => ({
      value: runtime.javaPath,
      label: `Java ${runtime.major || '?'} · ${runtime.version || '版本未知'}`,
      note: runtime.valid
        ? runtime.origin === 'external'
          ? '本机路径'
          : `${runtime.imageType.toUpperCase()}（面板安装）`
        : '路径已失效',
    })),
  ]
  if (javaLoaded && form.java !== '' && !runtimes.some((r) => r.javaPath === form.java)) {
    javaOptions.unshift({ value: form.java, label: form.java, note: '未登记' })
  }

  return (
    <form className="stack stack--narrow" onSubmit={save}>
      <PageHead title="实例设置" lead="名称、目录、核心、Java 和内存，以及它怎么启动。" />

      <LaunchCheckPanel
        check={check}
        legacyCommand={instance.legacyCommand ?? []}
        onRecheck={() => setCheckRev((rev) => rev + 1)}
      />

      <Section form title="基本信息" note="这台服务器叫什么、文件放在哪、是什么服务端。">
      <label className="field field--md">
        <span>实例名称</span>
        <input
          value={form.name}
          onChange={(e) => update('name', e.target.value)}
          required
        />
      </label>

      <DirectoryField
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
        <label className="field field--md">
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
        <label className="field field--sm">
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
        <FieldHelp summary="哪些认不出来？">
          <strong>Forge 这类认不出来</strong> —— 没有 jar 名可读，
          <code>version_history.json</code> 也只有 Paper 系才写。认不出来的后果很具体：
          mod 会被装进 <code>plugins/</code> 而不是 <code>mods/</code>，插件市场里每一条也都标成「未知」。
        </FieldHelp>
      </p>
      </Section>

      <Section form title="启动方式" note="面板拼出来的那条命令行：用哪个 Java、跑哪个 jar、给多少内存。">
      <div className="segmented" role="group" aria-label="启动方式">
        {[
          {
            value: false,
            label: '核心 jar',
            note: 'java -Xmx… -jar server.jar',
          },
          {
            value: true,
            label: '参数文件',
            note: 'Forge / NeoForge 的 @user_jvm_args.txt',
          },
        ].map((entry) => (
          <button
            key={String(entry.value)}
            type="button"
            className={`segmented__option${
              argFileMode === entry.value ? ' segmented__option--active' : ''
            }`}
            aria-pressed={argFileMode === entry.value}
            onClick={() => setArgFileMode(entry.value)}
          >
            <strong>{entry.label}</strong>
            <small>{entry.note}</small>
          </button>
        ))}
      </div>

      {/* The Java choice is argv[0] in both modes. It is also exported into
          the environment, which is what a server that shells out to a java
          of its own picks up.

          Only what the panel has been told about: there is no free-text path
          here any more, and 「资源库 → Java 环境」 is the one way in. The
          server enforces the same rule on save, so this select is a
          convenience, not the guard. */}
      <label className="field field--md">
        <span>Java 环境</span>
        <Select
          ariaLabel="Java 环境"
          value={form.java}
          options={javaOptions}
          onChange={(next) => update('java', next)}
        />
        <small>
          {runtimes.length > 0
            ? 'Java 只能在「资源库 → Java 环境」里添加——装一个，或者登记本机已有的；添加过的在这里选。'
            : '还没有可选的 Java。到「资源库 → Java 环境」装一个，或者登记本机已有的，之后这里就能选。'}
        </small>
      </label>

      {argFileMode ? (
        <>
          <label className="field">
            <span>参数文件</span>
            <textarea
              rows={3}
              value={argFileText}
              onChange={(e) => setArgFileText(e.target.value)}
              placeholder={'user_jvm_args.txt\nlibraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt'}
              spellCheck={false}
            />
            <small>
              一行一个，路径从实例目录算起，面板会按顺序拼成
              <code> java @第一个 @第二个 …</code>。
            </small>
            <FieldHelp summary="为什么 Forge 没有 jar？">
              Forge 和 NeoForge 从 1.17 起就没有可以
              直接跑的 jar 了，安装器留下的就是这两个文件 —— 照 <code>run.sh</code> 里那行抄过来即可。
            </FieldHelp>
          </label>

          <div className="actions">
            <Button type="button" onClick={() => setImporting(true)}>
              从启动脚本读参数…
            </Button>
          </div>

          <ArgFileMemory
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
          {/* One row, because they are one sentence: 跑哪个 jar、给多少内存 —
              which is what this section's own note says it is about. Stacked,
              a 380px select and two 120px boxes left three ragged right edges
              in as many rows, and the eye had to walk down a staircase to read
              a single decision. */}
          <div className="field-row">
            <label className="field field--md">
              <span>服务端 jar</span>
              <Select
                allowCustom
                ariaLabel="服务端 jar"
                value={form.jar}
                placeholder="server.jar"
                options={jars.map((jar) => ({
                  value: jar.name,
                  label: jar.name,
                  note: formatBytes(jar.size),
                }))}
                onChange={(next) => update('jar', next)}
              />
              <small>
                {jars.length > 0
                  ? `上面这个目录下找到 ${jars.length} 个 jar 文件，点输入框可以直接选`
                  : '目录下暂时没有 jar 文件，从上面装一个核心，或自己传一个'}
              </small>
            </label>

            <label className="field field--num">
              <span>最小内存 (MB)</span>
              <input
                type="number"
                min={0}
                step={256}
                value={form.minMemoryMB}
                onChange={(e) => update('minMemoryMB', Number(e.target.value))}
              />
            </label>
            <label className="field field--num">
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

          <div className="field">
            <span>JVM 参数</span>
            <JVMPresets
              activeNote={presetNote}
              rows={jvmRows}
              onPick={(id) => void applyPreset(id)}
              onImport={() => setImporting(true)}
              onView={setJvmView}
            />
            {jvmRows ? (
              <JVMArgsEditor value={jvmText} onChange={setJvmText} />
            ) : (
              <textarea
                rows={4}
                value={jvmText}
                onChange={(e) => setJvmText(e.target.value)}
                placeholder={'-XX:+UseG1GC\n-XX:MaxGCPauseMillis=200'}
                aria-label="JVM 参数"
              />
            )}
            {aikarNeedsEqualHeap && (
              <div className="alert alert--warn">
                这套参数的前提是最小内存和最大内存一样大，现在填的是 {form.minMemoryMB} /{' '}
                {form.maxMemoryMB} MB。把上面的最小内存也改成 {form.maxMemoryMB} 再保存。
              </div>
            )}
            {!jvmRows && <small>一行一个参数，会放在 -jar 之前。</small>}
          </div>

          <div className="field">
            <span>服务端参数</span>
            {!proxy && (
              <div className="presets">
                <div className="presets__row">
                  <button
                    className={`chip${hasNogui ? ' chip--on' : ''}`}
                    type="button"
                    aria-pressed={hasNogui}
                    onClick={toggleNogui}
                  >
                    --nogui
                  </button>
                </div>
              </div>
            )}
            <textarea
              rows={2}
              value={serverText}
              onChange={(e) => setServerText(e.target.value)}
              placeholder={proxy ? '' : '--nogui'}
              aria-label="服务端参数"
            />
            <small>
              一行一个参数，会放在 jar 之后。
              {proxy
                ? ' Velocity 遇到不认识的参数会直接退出，一般这里留空。'
                : ' --nogui 关掉服务端自带的那个 Swing 窗口，无头机器上基本都要。'}
            </small>
          </div>
        </>
      )}

      <InstanceCorePicker
        instance={instance}
        cores={cores}
        onApplied={onCoreApplied}
        onOpenLibrary={onOpenLibrary}
        jarIgnored={argFileMode}
      />

      {importing && (
        <ScriptImportDialog
          instanceId={instance.id}
          onApply={applyDraft}
          onClose={() => setImporting(false)}
        />
      )}
      </Section>

      <Section form title="控制台" note="网页控制台怎么读服务端的输出、怎么把命令送回去。">
      <label className="field field--md">
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
        <small>控制台按这个编码解读服务器输出、并按同样的编码发送命令。</small>
        <FieldHelp summary="乱码了怎么办？">
          「自动」会让 JVM 用
          UTF-8 输出，同时对不是 UTF-8 的行按系统编码兜底。用自己的脚本启动时，
          「让 JVM 用 UTF-8」这半件事要靠上面那个 <code>JAVA_TOOL_OPTIONS</code> 开关；
          那个关着、中文 Windows 上又乱码的话，这里改成 GBK 通常就好了。
        </FieldHelp>
      </label>

      <label className="checkbox">
        <input
          type="checkbox"
          checked={form.tty && ttySupported}
          disabled={!ttySupported}
          onChange={(e) => update('tty', e.target.checked)}
        />
        <div className="checkbox__text">
          <span>使用终端模式（推荐）</span>
          {ttySupported ? (
            <>
              <small>Tab 补全由正在运行的服务端回答，进度条不用等换行就能看到。</small>
              <FieldHelp>
                把服务器跑在伪终端上，就像你自己在 SSH 里开着它一样。这样 Tab 补全由
                <strong>正在运行的服务端</strong>回答（插件命令、真实玩家名都算数），
                进度条不用等换行就能看到，颜色也不需要强制。代价是终端只有一条流，
                stderr 不再单独标红。关掉则回到管道模式。
              </FieldHelp>
            </>
          ) : (
            <small>本系统没有可用的伪终端（Windows 需要 ConPTY），所有实例都以管道模式运行。</small>
          )}
        </div>
      </label>

      <label className="checkbox">
        <input
          type="checkbox"
          checked={form.forceColor}
          disabled={form.tty && ttySupported}
          onChange={(e) => update('forceColor', e.target.checked)}
        />
        <div className="checkbox__text">
          <span>强制彩色输出（推荐）</span>
          <small>仅在管道模式下有意义，终端模式下这两个参数不会被加上。</small>
          <FieldHelp>
            服务端只在检测到终端时才上色，所以管道模式会加上
            <code> -Dterminal.jline=false -Dterminal.ansi=true</code>，让网页控制台和
            cmd 里一样有颜色。终端模式下服务端本来就看得到终端，这两个参数不会被加上
            —— <code>terminal.jline=false</code> 恰好会关掉终端模式想要的那个补全。
            用自己的脚本启动时，这两个参数走
            <code> JAVA_TOOL_OPTIONS</code> 送进去，要在上面把那个开关留着。
          </FieldHelp>
        </div>
      </label>
      </Section>

      <Section form title="进程管理" note="面板什么时候替你开服、什么时候替你重启、怎么停。">
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
        <label className="field field--md">
          <span>停服命令</span>
          <input
            value={form.stopCommand}
            onChange={(e) => update('stopCommand', e.target.value)}
            placeholder={proxy ? 'end' : 'stop'}
          />
        </label>
        <label className="field field--num">
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
      </Section>

      {error && <div className="alert alert--error">{error}</div>}
      {status && <div className="alert alert--ok">{status}</div>}

      {dirty && (
        <div className="formbar">
          <span className="formbar__note">有未保存的改动</span>
          <Button size="row" type="button" onClick={revert} disabled={busy}>
            放弃
          </Button>
          <Button variant="primary" size="row" type="submit" disabled={busy}>
            {busy ? '保存中…' : '保存设置'}
          </Button>
        </div>
      )}

      <Section
        tone="danger"
        title="危险操作"
        note="这两个都不可撤销，面板没有为它们留回收站。服务器运行时都不可用。"
      >
        <div className="actions">
          <Button
            type="button"
            onClick={() => remove(false)}
            disabled={busy || isLive(instance.state)}
          >
            从面板移除
          </Button>
          <Button
            variant="danger"
            type="button"
            onClick={() => remove(true)}
            disabled={busy || isLive(instance.state)}
          >
            删除实例及所有文件
          </Button>
        </div>
      </Section>
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
 * It reports and repairs nothing. The failure it used to exist for — a start
 * script that backgrounds the JVM, leaving the panel holding a process that
 * exits a second later while the server everyone can see keeps running — is
 * gone with the script mode that allowed it: the panel builds the command line
 * itself now. What is left is worth saying before the first start all the
 * same, because a launch target that is not there produces an error at 开服 and
 * nowhere else.
 */
function LaunchCheckPanel({
  check,
  legacyCommand,
  onRecheck,
}: {
  check: LaunchCheck | null
  legacyCommand: string[]
  onRecheck: () => void
}) {
  if (check === null) return null

  // No findings is the normal case and does not deserve a card: a heading that
  // says "everything is fine" is one the eye has to process on every visit to
  // learn nothing. One line under the page head says it and gets out of the way.
  if (check.issues.length === 0) {
    return (
      <div className="launchstrip">
        <span className="launchstrip__dot" aria-hidden="true" />
        <span>
          没发现问题。
          {check.mode === 'argfile'
            ? '参数文件都在目录里。'
            : '核心和目录都对得上。'}
        </span>
        <button className="link" type="button" onClick={onRecheck}>
          重新检查
        </button>
      </div>
    )
  }

  return (
    <Section form title="开服前检查" note="按下「启动」之前，面板能先看出来的问题。">
      <ul className="launchcheck">
        {check.issues.map((issue) => (
          <li
            key={issue.code}
            className={`launchcheck__item launchcheck__item--${issue.level}`}
          >
            <strong className="launchcheck__level">{LEVEL_LABELS[issue.level]}</strong>
            <p className="launchcheck__text">{issue.message}</p>
            {/* The retired argv, shown only where it is the answer to the
                issue above: somebody has to retype it into the form, and
                this is the only place it still exists. */}
            {issue.code === 'needs-setup' &&
              legacyCommand.map((arg, at) => (
                <code className="launchcheck__line" key={`${at}-${arg}`}>
                  {arg}
                </code>
              ))}
          </li>
        ))}
      </ul>

      <div className="actions">
        <Button size="row" type="button" onClick={onRecheck}>
          重新检查
        </Button>
      </div>
    </Section>
  )
}

/**
 * The heap of an argfile-launched server, which is not in the instance config.
 *
 * The memory fields above build a -Xmx onto the command line; an @file is
 * expanded in place and the JVM lets the last -Xmx win, so the one inside
 * user_jvm_args.txt would override it. Rather than emit a flag that loses, the
 * panel emits none and the control moves into that file — and where there is
 * no such file it says so instead of showing a slider that changes nothing.
 */
/**
 * The ready-made argument sets, above the box they fill.
 *
 * Buttons and not a checkbox per flag. The sets here only work as sets — half
 * of Aikar's G1 tuning merged into half of a ZGC setup is not a third tuning —
 * and the flags worth offering at all are a handful out of hundreds, each with
 * a Java-version predicate that goes stale every release. See jvmPresets.ts.
 *
 * 从启动脚本读参数 sits in the same row because it answers the same question,
 * one step further back: what the arguments should be when you already have a
 * server that works and no idea what is in its run.sh.
 *
 * The 卡片 / 文本 pair at the far end switches how the same arguments are
 * shown. Both write one-per-line text and the card view can express nothing a
 * keyboard could not — so this is a view toggle, not two ways to configure a
 * JVM, and neither side has to be reachable from the other for a setting to be
 * settable.
 */
function JVMPresets({
  activeNote,
  rows,
  onPick,
  onImport,
  onView,
}: {
  activeNote: string | null
  rows: boolean
  onPick: (id: string) => void
  onImport: () => void
  onView: (rows: boolean) => void
}) {
  return (
    <div className="presets">
      <div className="presets__row">
        {JVM_PRESETS.map((entry) => (
          <button
            key={entry.id}
            className="chip"
            type="button"
            onClick={() => onPick(entry.id)}
          >
            {entry.label}
          </button>
        ))}
        <button className="chip chip--right" type="button" onClick={onImport}>
          从启动脚本读…
        </button>
        <div className="presets__view" role="group" aria-label="JVM 参数的显示方式">
          <button
            className={`chip${rows ? ' chip--on' : ''}`}
            type="button"
            aria-pressed={rows}
            onClick={() => onView(true)}
          >
            卡片
          </button>
          <button
            className={`chip${rows ? '' : ' chip--on'}`}
            type="button"
            aria-pressed={!rows}
            onClick={() => onView(false)}
          >
            文本
          </button>
        </div>
      </div>
      {activeNote && <small className="presets__note">{activeNote}</small>}
    </div>
  )
}

function ArgFileMemory({
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
      <p className="muted">
        这个服务端的内存写在参数文件里，面板不去猜 —— 上面那组内存设置只对「核心 jar」有效。
        Forge / NeoForge 把 <code>-Xmx</code> 放在
        <code> user_jvm_args.txt</code>，那个文件在时这里会直接变成可编辑的。
      </p>
    )
  }

  const others = jvm.args.filter((arg) => !/^-X(mx|ms)/.test(arg))

  return (
    <>
      <div className="field-row">
        <label className="field field--num">
          <span>最小内存 (MB)</span>
          <input
            type="number"
            min={0}
            step={256}
            value={min}
            onChange={(e) => onMin(Number(e.target.value))}
          />
        </label>
        <label className="field field--num">
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

      <p className="muted">
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
        <Button size="row" type="button" disabled={busy} onClick={onSave}>
          写入 {jvm.fileName}
        </Button>
        {status && <span className="muted">{status}</span>}
      </div>
    </>
  )
}
