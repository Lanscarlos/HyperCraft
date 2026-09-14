import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import { changedKeys, fromLines, toInput, toLines } from '../instanceForm'
import { JVM_PRESETS } from '../jvmPresets'
import type { InstanceSection } from '../routes'
import type {
  InstanceInput,
  InstanceStatus,
  JavaRuntime,
  JVMArgs,
  LaunchDraft,
} from '../types'
import { isLive } from '../types'
import type { CoreController } from '../useCores'
import { useHostJars } from '../useHostJars'
import { useLaunchPreview } from '../useLaunchPreview'
import { Button } from './Button'
import { JavaCoreCard } from './JavaCoreCard'
import { JvmArgsCard } from './JvmArgsCard'
import { LaunchConsole } from './LaunchConsole'
import { MemoryCard } from './MemoryCard'
import { InstanceCorePicker } from './InstanceCorePicker'
import { PageHead } from './Page'
import { ScriptImportDialog } from './ScriptImportDialog'
import { Section } from './Section'
import { Select } from './Select'

interface Props {
  instance: InstanceStatus
  /** The machine's physical memory in bytes, 0 before the first read.
   *
   *  From the host's own numbers rather than from this instance's metrics:
   *  those are only polled while the server is running, and this is the page
   *  you are on precisely when it is not. */
  hostMemoryTotal: number
  /** Every server on the machine: what the rest of them already claim is the
   *  missing half of "is this heap reasonable". */
  instances: InstanceStatus[]
  cores: CoreController
  onSaved: (updated: InstanceStatus) => void
  onOpenLibrary: () => void
  onOpenSection: (section: InstanceSection) => void
}

/** Where the JVM 参数 view preference is kept. */
const JVM_VIEW_KEY = 'hc.jvmargs.view'

/**
 * How this server is started: which Java, which jar, how much heap.
 *
 * Its own page rather than a section of 实例设置 because it is the only part
 * of that form anybody opens twice — once to pick a jar, then again on every
 * heap and GC change after that.
 *
 * `.stack` and not `.stack--narrow`: an instance pane's plain stack is the
 * tile measure, which is what the second column added on top of this needs.
 * 实例设置 keeps the narrow one, being a form from top to bottom.
 */
export function StartupSettings({
  instance,
  hostMemoryTotal,
  instances,
  cores,
  onSaved,
  onOpenLibrary,
  onOpenSection,
}: Props) {
  const [form, setForm] = useState<InstanceInput>(() => toInput(instance))
  const [jvmText, setJvmText] = useState(() => toLines(instance.jvmArgs ?? []))
  const [serverText, setServerText] = useState(() => toLines(instance.serverArgs ?? []))
  const [argFileText, setArgFileText] = useState(() => toLines(instance.argFiles ?? []))
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
  const [jvm, setJvm] = useState<JVMArgs | null>(null)
  const [jvmMin, setJvmMin] = useState(0)
  const [jvmMax, setJvmMax] = useState(0)
  const [jvmBusy, setJvmBusy] = useState(false)
  const [jvmStatus, setJvmStatus] = useState<string | null>(null)
  const [argFileRev, setArgFileRev] = useState(0)
  // Rows or the raw text. Remembered because it is a preference about how you
  // read arguments, not about this instance — someone who thinks in text wants
  // text on every instance, and having to say so on each one is the annoying
  // half of offering the choice at all.
  const [jvmRows, setJvmRows] = useState(
    () => window.localStorage.getItem(JVM_VIEW_KEY) !== 'text',
  )
  // Seeded from the stored pair rather than from a preference: an instance
  // already running Xms = Xmx is one somebody set that way on purpose, and
  // starting it unlocked invites the next edit to break it.
  const [heapLocked, setHeapLocked] = useState(
    () => instance.minMemoryMB > 0 && instance.minMemoryMB === instance.maxMemoryMB,
  )

  const setJvmView = (rows: boolean) => {
    setJvmRows(rows)
    window.localStorage.setItem(JVM_VIEW_KEY, rows ? 'rows' : 'text')
  }

  // A proxy launches differently enough to be worth saying so: it exits on the
  // --nogui every Minecraft server wants.
  const proxy = instance.kind === 'proxy'

  // The jar list follows the directory field rather than the saved config, so
  // retargeting an instance at an existing server directory offers that
  // directory's jars before the change is even saved.
  const { jars } = useHostJars(form.directory, jarsRev)

  // What 保存 would send, against what is stored.
  const stored = toInput(instance)
  const pending: InstanceInput = {
    ...form,
    jvmArgs: fromLines(jvmText),
    serverArgs: fromLines(serverText),
    argFiles: argFileMode ? fromLines(argFileText) : [],
  }
  const changed = changedKeys(stored, pending)
  const dirty = changed.length > 0

  // The command and the checks, for the draft as it stands rather than for
  // what is saved. Both come off one endpoint: built separately they would
  // disagree on screen, one describing the form and the other the disk.
  const { preview, stale, failed } = useLaunchPreview(instance.id, {
    java: pending.java,
    jar: pending.jar,
    argFiles: pending.argFiles,
    minMemoryMB: pending.minMemoryMB,
    maxMemoryMB: pending.maxMemoryMB,
    jvmArgs: pending.jvmArgs,
    serverArgs: pending.serverArgs,
    loader: pending.loader,
  })

  const reseed = useCallback((from: InstanceStatus) => {
    setForm(toInput(from))
    setJvmText(toLines(from.jvmArgs ?? []))
    setServerText(toLines(from.serverArgs ?? []))
    setArgFileText(toLines(from.argFiles ?? []))
    setArgFileMode((from.argFiles?.length ?? 0) > 0)
  }, [])

  // Both settings pages hold the whole config and PUT the whole thing back, so
  // a page sitting on a stale copy would undo whatever the other one just
  // saved. And they sit on one: the panes stay mounted behind each other, so
  // "the other page saved while this one was off screen" is the normal case,
  // not an exotic one. Re-seed whenever the stored config moved and this form
  // has nothing of its own to lose.
  //
  // Keyed on the stored values rather than on instance.id: the id never
  // changes within a mount — App keys the whole view on it — so an id-keyed
  // effect would never fire at all.
  const storedKey = JSON.stringify(stored)
  const seeded = useRef(storedKey)
  useEffect(() => {
    if (seeded.current === storedKey) return
    seeded.current = storedKey
    if (dirty) return
    reseed(instance)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [storedKey])

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
  }, [instance.id, argFileRev])

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
    // One-way. A script with argfiles has to switch the form over or the value
    // it just filled in is on a tab nobody is looking at; a script without them
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
    setStatus(`已填上「${chosen.label}」，确认无误再点保存`)
  }


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


  // Back to what is stored.
  const revert = () => {
    reseed(instance)
    setError(null)
    setStatus(null)
  }

  const update = <K extends keyof InstanceInput>(key: K, value: InstanceInput[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }))

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
      // The whole config, not just this page's half: an absent field is a
      // zero value to the daemon, so sending only the launch fields would
      // blank the instance name and every process setting. See instanceForm.
      const payload: InstanceInput = {
        ...form,
        jvmArgs: fromLines(jvmText),
        serverArgs: fromLines(serverText),
        // An empty list is how the daemon is told this is a jar launch, so
        // switching back to 核心 jar has to send one.
        argFiles,
      }
      onSaved(await api.updateInstance(instance.id, payload))
      setStatus(isLive(instance.state) ? '已保存，将在下次启动时生效' : '已保存')
      setArgFileRev((rev) => rev + 1)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  // The heap of an argfile-launched server lives in a file, not in the
  // instance config, so it saves to its own endpoint rather than riding along
  // with the form — a half-applied save across two writes would be worse than
  // two buttons.
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

  /* The Java choice is argv[0] in both modes. It is also exported into the
     environment, which is what a server that shells out to a java of its own
     picks up.

     Only what the panel has been told about: there is no free-text path here
     any more, and 「资源库 → Java 环境」 is the one way in. The server
     enforces the same rule on save, so this select is a convenience, not the
     guard. */
  const javaField = (
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
  )

  return (
    <form className="stack" onSubmit={save}>
      <PageHead
        title="启动方式"
        lead="用哪个 Java、跑哪个 jar、给多少内存，以及面板据此拼出的那条命令。"
      />

      {error && <div className="alert alert--error">{error}</div>}

      <div className="startup">
        <div className="startup__form">
      <JavaCoreCard
          argFileMode={argFileMode}
          onMode={setArgFileMode}
          javaField={javaField}
          jar={form.jar}
          jars={jars}
          onJar={(next) => update('jar', next)}
          argFileText={argFileText}
          onArgFileText={setArgFileText}
          onImportScript={() => setImporting(true)}
          corePicker={
            <InstanceCorePicker
              instance={instance}
              cores={cores}
              onApplied={onCoreApplied}
              onOpenLibrary={onOpenLibrary}
              jarIgnored={argFileMode}
            />
          }
        />

        {argFileMode ? (
          <Section
            form
            title={
              <>
                <span className="originmark originmark--memory" aria-hidden="true" />
                内存
              </>
            }
            note="这个服务端的内存写在参数文件里 —— 面板的 -Xmx 到不了 JVM，所以它改的是那个文件。"
          >
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
          </Section>
        ) : (
          <MemoryCard
            min={form.minMemoryMB}
            max={form.maxMemoryMB}
            onMin={(value) => update('minMemoryMB', value)}
            onMax={(value) => update('maxMemoryMB', value)}
            locked={heapLocked}
            onLocked={setHeapLocked}
            hostTotalBytes={hostMemoryTotal}
            others={instances
              .filter((one) => one.id !== instance.id)
              .map((one) => ({ name: one.name, mb: one.effectiveMaxMemoryMB }))}
          />
        )}

        <JvmArgsCard
          text={jvmText}
          onText={setJvmText}
          count={pending.jvmArgs.length}
          rows={jvmRows}
          onView={setJvmView}
          onPick={(id) => void applyPreset(id)}
          onImport={() => setImporting(true)}
        />

        <Section
          form
          title={
            <>
              <span className="originmark originmark--server" aria-hidden="true" />
              服务端参数
            </>
          }
          meta="跟在 jar 之后"
          note="传给服务端自己的参数，不是给 JVM 的。"
        >
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
        </Section>

        {importing && (
          <ScriptImportDialog
            instanceId={instance.id}
            onApply={applyDraft}
            onClose={() => setImporting(false)}
          />
        )}
        </div>

        <aside className="startup__rail">
          <LaunchConsole
            preview={preview}
            stale={stale}
            failed={failed}
            onApplyFix={(patch) => {
              // Merged as it arrives. No branch on the issue code lives here —
              // that is what lets the backend add a check without a frontend
              // change. Only the two array fields have to be routed, because
              // this form edits them as text.
              const { jvmArgs, serverArgs, ...rest } = patch
              setForm((prev) => ({ ...prev, ...rest }))
              if (jvmArgs) setJvmText(toLines(jvmArgs))
              if (serverArgs) setServerText(toLines(serverArgs))
            }}
          />
        </aside>
      </div>

      {status && <div className="alert alert--ok">{status}</div>}

      {dirty && (
        <div className="formbar">
          <span className="formbar__note">
            {changed.length} 项改动待保存
            {isLive(instance.state) ? '，服务器正在运行，下次启动生效' : '，下次启动即生效'}
          </span>
          {/* 查看差异 is 配置历史's job — it is already the page that answers
              "what did this look like before", and a second diff view would be
              a second answer to maintain. */}
          <Button size="row" type="button" onClick={() => onOpenSection('config-history')}>
            查看历史
          </Button>
          <Button size="row" type="button" onClick={revert} disabled={busy}>
            放弃
          </Button>
          <Button variant="primary" size="row" type="submit" disabled={busy}>
            {busy ? '保存中…' : '保存设置'}
          </Button>
        </div>
      )}
    </form>
  )
}

/**
 * The heap of an argfile-launched server, which is not in the instance config.
 *
 * The memory fields above build a -Xmx onto the command line; an @file is
 * expanded in place and the last -Xmx wins, so the one inside
 * user_jvm_args.txt would beat anything the panel put in front of it. Editing
 * that file is therefore the only way to change this server's heap.
 */
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
