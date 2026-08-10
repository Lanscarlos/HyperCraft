import { useEffect, useMemo, useRef, useState } from 'react'

import { api } from '../api'
import { formatBytes } from '../format'
import type { InstanceStatus, JavaRuntime, ServerCore, SystemInfo } from '../types'
import type { CoreController } from '../useCores'
import { useHostJars } from '../useHostJars'
import type { JavaController } from '../useJava'
import { CoreCatalogue, useCoreCatalogue } from './CoreCatalogue'
import { Page } from './Page'
import { DirectoryField } from './PathPicker'
import { Select } from './Select'

interface Props {
  cores: CoreController
  java: JavaController
  system: SystemInfo | null
  /** Puts the finished instance into the app's list before anything navigates
   *  to it — the five-second poll is too late for a page opening right now. */
  onCreated: (instance: InstanceStatus) => void
  onOpenInstance: (id: string) => void
  onCancel: () => void
}

type StepId = 'core' | 'java' | 'basics' | 'server' | 'confirm'

/** How the core for this instance is being obtained. */
type CoreMode = 'library' | 'download' | 'none'

/** Sentinel for "type a java path yourself", matching LaunchSettings. */
const CUSTOM_JAVA = '__custom__'

const EULA_URL = 'https://aka.ms/MinecraftEULA'

/**
 * The server.properties keys the wizard asks about, with Minecraft's own
 * defaults.
 *
 * Only what is worth deciding *before* the first start is here. Two of them —
 * the level name and the seed — are the whole reason this step exists rather
 * than living in 服务器配置: once the world has generated, changing them makes
 * a second world instead of changing this one.
 */
const PROPERTY_DEFAULTS: Record<string, string> = {
  'server-port': '25565',
  'max-players': '20',
  motd: 'A Minecraft Server',
  difficulty: 'easy',
  gamemode: 'survival',
  'online-mode': 'true',
  'level-name': 'world',
  'level-seed': '',
}

const DIFFICULTIES = [
  { value: 'peaceful', label: '和平' },
  { value: 'easy', label: '简单' },
  { value: 'normal', label: '普通' },
  { value: 'hard', label: '困难' },
]

const GAMEMODES = [
  { value: 'survival', label: '生存' },
  { value: 'creative', label: '创造' },
  { value: 'adventure', label: '冒险' },
  { value: 'spectator', label: '旁观' },
]

/** Quick answers to "how much memory", in MB. The field stays the real one. */
const MEMORY_PRESETS: { mb: number; note: string }[] = [
  { mb: 2048, note: '小服 / 测试' },
  { mb: 4096, note: '十来个人' },
  { mb: 6144, note: '二三十人' },
  { mb: 8192, note: '大服 / 整合包' },
]

/**
 * Which Java major a Minecraft version needs.
 *
 * The same table the Java page shows, applied the other way round: there it
 * annotates a Java version with the servers it runs, here it reads a server
 * version and says which Java to install. A version string that is not a
 * Minecraft one — Velocity numbers its own releases — returns 0, which means
 * "no opinion" rather than "any Java will do", and nothing is warned about.
 */
function requiredJavaFor(version: string): number {
  const match = /^1\.(\d+)(?:\.(\d+))?/.exec(version.trim())
  if (!match) return 0
  const minor = Number(match[1])
  const patch = Number(match[2] ?? '0')
  if (minor <= 16) return 8
  if (minor < 20) return 17
  if (minor === 20 && patch < 5) return 17
  return 21
}

function coreLabel(core: ServerCore): string {
  return core.imported ? core.fileName : `${core.projectName} ${core.version}`
}

function coreNote(core: ServerCore): string {
  return [core.imported ? '自行放入' : `构建 #${core.build}`, formatBytes(core.size)].join(' · ')
}

/** What one of the four creation calls is doing, for the checklist. */
type TaskState = 'pending' | 'active' | 'done' | 'failed' | 'skipped'

interface Task {
  id: string
  label: string
  state: TaskState
  note?: string
}

const TASK_MARKS: Record<TaskState, string> = {
  pending: '·',
  active: '…',
  done: '✓',
  failed: '!',
  skipped: '–',
}

/**
 * Creating a server, one decision per screen.
 *
 * The old dialog asked for all five answers at once and assumed every one of
 * them already existed: it offered the cores that had been downloaded, the jar
 * that was already in the directory, and said nothing at all about Java. That
 * works on the tenth server and not on the first, where the honest answer to
 * "服务端核心" is an empty dropdown and a link to another page — which throws
 * away everything typed so far.
 *
 * So the order here is the order the pieces depend on each other, and every
 * step can *produce* what it asks for rather than only pick from what exists:
 * the core step downloads one, the Java step installs one. What the core is
 * decides the rest — the Minecraft version names the Java major, a proxy has
 * no world and no EULA, so its step is dropped entirely.
 *
 * Nothing is written until 创建 on the last step, and then it is four calls in
 * order (instance, core, EULA, properties) with a line each, because the first
 * of them is the one that cannot be undone by pressing 返回 — after it the
 * instance exists, and a failure further down has to say so instead of looking
 * like nothing happened.
 */
export function NewInstanceWizard({
  cores,
  java,
  system,
  onCreated,
  onOpenInstance,
  onCancel,
}: Props) {
  const stored = cores.cores

  const [step, setStep] = useState<StepId>('core')
  const [coreMode, setCoreMode] = useState<CoreMode>('library')
  const [coreId, setCoreId] = useState('')
  const [javaPath, setJavaPath] = useState('java')
  const [customJava, setCustomJava] = useState(false)
  const [installMajor, setInstallMajor] = useState<number | null>(null)
  const [name, setName] = useState('')
  const [directory, setDirectory] = useState('')
  const [jar, setJar] = useState('')
  const [maxMemoryMB, setMaxMemoryMB] = useState(4096)
  const [autoStart, setAutoStart] = useState(false)
  const [autoRestart, setAutoRestart] = useState(true)
  const [props, setProps] = useState<Record<string, string>>(PROPERTY_DEFAULTS)
  const [eula, setEula] = useState(false)

  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [created, setCreated] = useState<InstanceStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // True only for a job this wizard started: the panel keeps the last job
  // around after it finishes, and a download from an hour ago must not be read
  // as "your core is ready".
  const [awaitingCore, setAwaitingCore] = useState(false)
  const [awaitingJava, setAwaitingJava] = useState(false)
  // Set once the operator edits the name, so deriving it from the core stops.
  const namedByHand = useRef(false)
  // Same for the three-way switch above the core step.
  const modeByHand = useRef(false)

  const catalogue = useCoreCatalogue(coreMode === 'download')
  const { jars, exists: directoryExists } = useHostJars(directory)

  // An empty library means the first thing to do is fill it, so that is the
  // page the step lands on rather than an empty grid with a hint under it.
  // Decided here rather than in useState because the library is fetched at the
  // app level and arrives a moment *after* this page does — reading its length
  // at mount says "empty" for every wizard, including the ones opened on a
  // panel with a shelf full of cores.
  const libraryLoaded = cores.library !== null
  useEffect(() => {
    if (modeByHand.current || !libraryLoaded) return
    if (stored.length === 0) setCoreMode('download')
  }, [libraryLoaded, stored.length])

  const core = stored.find((entry) => entry.id === coreId)
  const proxy = core?.kind === 'proxy'
  const required = core && !proxy ? requiredJavaFor(core.version) : 0

  const runtimes = java.overview?.runtimes ?? []
  const systemJava = java.overview?.system ?? null

  // A finished download is a core in the library, which is what this step was
  // asking for — so it is selected rather than merely announced.
  useEffect(() => {
    const job = cores.job
    if (!awaitingCore || job?.state !== 'done') return
    setAwaitingCore(false)
    void cores.refresh()
    if (job.coreId) {
      setCoreId(job.coreId)
      setCoreMode('library')
    }
  }, [awaitingCore, cores])

  useEffect(() => {
    const job = java.job
    if (!awaitingJava || job?.state !== 'done') return
    setAwaitingJava(false)
    if (job.runtimeId) {
      const installed = runtimes.find((runtime) => runtime.id === job.runtimeId)
      if (installed) {
        setJavaPath(installed.javaPath)
        setCustomJava(false)
      }
    }
  }, [awaitingJava, java.job, runtimes])

  // The core names the server, until the operator says otherwise. "Paper
  // 1.21.4" is a better first guess than an empty box, and it is the string
  // they would have typed on their own for the first server.
  useEffect(() => {
    if (namedByHand.current || !core) return
    setName(core.imported ? core.fileName.replace(/\.jar$/i, '') : `${core.projectName} ${core.version}`)
  }, [core])

  /**
   * Java follows the core: the *lowest* version that clears the requirement.
   *
   * Not the newest — that is the trap. Every Minecraft version has a floor and
   * most of them have a ceiling too: 1.16.5 needs at least Java 8 and does not
   * run on 17, so "newest available" hands an old server a Java that refuses
   * it. The lowest one that qualifies is the one upstream tested against.
   *
   * With no requirement to go on (a proxy, whose version numbers are its own)
   * the reasoning is inverted — nothing here can say what it needs, and the
   * things that number themselves that way are the modern ones.
   */
  const bestJava = useMemo(() => {
    const candidates = [
      ...runtimes.map((runtime) => ({
        value: runtime.javaPath,
        major: runtime.major,
        managed: true,
      })),
      ...(systemJava ? [{ value: 'java', major: systemJava.major, managed: false }] : []),
    ].filter((entry) => entry.major > 0)

    const qualifying = required > 0 ? candidates.filter((e) => e.major >= required) : []
    // A tie goes to the panel's own runtime: it is the one the panel can
    // promise is still there next month.
    const pool = qualifying.length > 0 ? qualifying : candidates
    const ranked = [...pool].sort(
      (a, b) =>
        (qualifying.length > 0 ? a.major - b.major : b.major - a.major) ||
        Number(b.managed) - Number(a.managed),
    )
    return ranked[0]?.value ?? 'java'
  }, [runtimes, systemJava, required])

  const javaTouched = useRef(false)
  useEffect(() => {
    if (javaTouched.current) return
    setJavaPath(bestJava)
  }, [bestJava])

  // The version the core wants, pre-picked in the install grid.
  useEffect(() => {
    if (required === 0) return
    setInstallMajor((current) => current ?? required)
  }, [required])

  const chosenMajor =
    javaPath === 'java'
      ? (systemJava?.major ?? 0)
      : (runtimes.find((runtime) => runtime.javaPath === javaPath)?.major ?? 0)
  // A custom path is a path the panel has never run; it cannot say which Java
  // is at the end of it, so it does not pretend to.
  const javaTooOld = required > 0 && chosenMajor > 0 && chosenMajor < required
  // The other direction, and the one nobody expects: 1.16 and older were built
  // before Java 17 removed what they call into, so a *newer* Java is just as
  // broken as an older one. Only said for the case that is not a judgement
  // call — the versions whose floor is Java 8.
  const javaTooNew = required === 8 && chosenMajor >= 17

  const steps = useMemo(() => {
    const list: { id: StepId; label: string }[] = [
      { id: 'core', label: '服务端核心' },
      { id: 'java', label: 'Java 环境' },
      { id: 'basics', label: '名称与位置' },
    ]
    // A proxy has no world, no EULA and no server.properties — Velocity reads
    // its own toml. Asking about MOTD and 难度 would be asking about a file
    // that will never exist.
    if (!proxy) list.push({ id: 'server', label: '服务器设置' })
    list.push({ id: 'confirm', label: '确认创建' })
    return list
  }, [proxy])

  // Standing on a step that just disappeared (a proxy was picked while it was
  // open) lands on the one that replaced it rather than on a blank page.
  useEffect(() => {
    if (!steps.some((entry) => entry.id === step)) setStep('confirm')
  }, [steps, step])

  const port = Number(props['server-port'])
  const valid: Record<StepId, boolean> = {
    core: coreMode === 'none' || core !== undefined,
    java: !customJava || javaPath.trim() !== '',
    basics: name.trim() !== '',
    server: Number.isFinite(port) && port > 0 && port < 65536,
    confirm: true,
  }

  const index = steps.findIndex((entry) => entry.id === step)
  const current = steps[index]
  // A step is reachable when everything before it has an answer; the rail is
  // the way back as much as it is the way forward.
  const reachable = (target: number) =>
    steps.slice(0, target).every((entry) => valid[entry.id])

  const setProp = (key: string, value: string) =>
    setProps((prev) => ({ ...prev, [key]: value }))

  const changedProps = Object.entries(props).filter(
    ([key, value]) => value.trim() !== (PROPERTY_DEFAULTS[key] ?? ''),
  )
  // A proxy has neither of these files, and answering the server step before
  // going back and picking Velocity is a real path through here — without this
  // it would write a server.properties and an eula.txt into a Velocity
  // directory, where they mean nothing and look like someone's mistake.
  const writeProps = !proxy && changedProps.length > 0
  const writeEula = !proxy && eula

  /**
   * Creates the instance and everything that has to happen inside its
   * directory, in the only order that works: the directory does not exist
   * until the instance does, so the core, the EULA and the properties all
   * come after it.
   *
   * A failure after the first call leaves a real instance behind. It is
   * reported and kept — deleting a server the panel just made, because the
   * copy of a jar failed, is a much worse surprise than an instance missing
   * its core.
   */
  const create = async () => {
    setBusy(true)
    setError(null)

    const plan: Task[] = [
      { id: 'instance', label: '创建实例', state: 'active' },
      {
        id: 'core',
        label: core ? `复制核心 ${core.fileName}` : '复制核心',
        state: core ? 'pending' : 'skipped',
        note: core ? undefined : '这一步跳过：没有选核心',
      },
      {
        id: 'eula',
        label: '同意 EULA',
        state: writeEula ? 'pending' : 'skipped',
        note: writeEula ? undefined : proxy ? '代理端不需要' : '这一步跳过：还没有同意',
      },
      {
        id: 'props',
        label: '写入 server.properties',
        state: writeProps ? 'pending' : 'skipped',
        note: writeProps ? undefined : proxy ? '代理端不需要' : '这一步跳过：全部保持默认',
      },
    ]
    setTasks(plan)

    const mark = (id: string, state: TaskState, note?: string) =>
      setTasks((prev) =>
        (prev ?? []).map((task) => (task.id === id ? { ...task, state, note } : task)),
      )

    let instance: InstanceStatus
    try {
      instance = await api.createInstance({
        name: name.trim(),
        directory: directory.trim(),
        java: customJava ? javaPath.trim() : javaPath,
        jar: core?.fileName ?? jar.trim(),
        maxMemoryMB,
        minMemoryMB: Math.min(1024, maxMemoryMB),
        // A proxy takes no --nogui; anything else is a Minecraft server.
        serverArgs: proxy ? [] : ['--nogui'],
        autoStart,
        autoRestart,
      })
      mark('instance', 'done')
    } catch (err) {
      mark('instance', 'failed', err instanceof Error ? err.message : '创建失败')
      setError(err instanceof Error ? err.message : '创建失败')
      setBusy(false)
      return
    }

    // From here the instance exists. Every later failure is reported on its
    // own line and none of them stops the ones after it: a server with no
    // EULA is still a server, and it is better handed over half-finished with
    // a list of what is missing than rolled back.
    if (core) {
      mark('core', 'active')
      try {
        const applied = await api.applyCore(instance.id, { coreId: core.id, setAsJar: true })
        instance = applied.instance
        mark('core', 'done')
      } catch (err) {
        mark('core', 'failed', err instanceof Error ? err.message : '复制失败')
      }
    }

    if (writeEula) {
      mark('eula', 'active')
      try {
        await api.acceptEula(instance.id)
        mark('eula', 'done')
      } catch (err) {
        mark('eula', 'failed', err instanceof Error ? err.message : '写入失败')
      }
    }

    if (writeProps) {
      mark('props', 'active')
      try {
        await api.saveProperties(
          instance.id,
          changedProps.map(([key, value]) => ({ key, value })),
        )
        mark('props', 'done', `${changedProps.length} 项`)
      } catch (err) {
        mark('props', 'failed', err instanceof Error ? err.message : '写入失败')
      }
    }

    onCreated(instance)
    setCreated(instance)
    setBusy(false)
  }

  const start = async (instance: InstanceStatus) => {
    setBusy(true)
    try {
      onCreated(await api.power(instance.id, 'start'))
    } catch (err) {
      setError(err instanceof Error ? err.message : '启动失败')
    } finally {
      setBusy(false)
      onOpenInstance(instance.id)
    }
  }

  return (
    <Page
      title="新建实例"
      lead="一步一步来：先决定这台服务器跑什么，再决定用什么跑，最后才是它叫什么、放在哪。每一步都可以往回改，直到最后一步按下创建为止，磁盘上什么都不会变。"
    >
      <ol className="wizard-steps">
        {steps.map((entry, position) => {
          const done = position < index && valid[entry.id]
          const active = entry.id === step
          const open = position <= index || reachable(position)
          return (
            <li key={entry.id}>
              <button
                type="button"
                className={`wizard-step${active ? ' wizard-step--active' : ''}${
                  done ? ' wizard-step--done' : ''
                }`}
                aria-current={active ? 'step' : undefined}
                disabled={!open || created !== null}
                onClick={() => setStep(entry.id)}
              >
                <span className="wizard-step__index" aria-hidden="true">
                  {done ? '✓' : position + 1}
                </span>
                <span className="wizard-step__label">{entry.label}</span>
              </button>
            </li>
          )
        })}
      </ol>

      {created ? (
        <Finished
          instance={created}
          tasks={tasks ?? []}
          busy={busy}
          eula={writeEula}
          proxy={proxy}
          hasCore={core !== undefined || jar.trim() !== ''}
          onStart={() => void start(created)}
          onOpen={() => onOpenInstance(created.id)}
        />
      ) : (
        <>
          {step === 'core' && (
            <CoreStep
              mode={coreMode}
              loaded={libraryLoaded}
              onMode={(next) => {
                modeByHand.current = true
                setCoreMode(next)
              }}
              stored={stored}
              coreId={coreId}
              onPick={setCoreId}
              cores={cores}
              catalogue={catalogue}
              awaiting={awaitingCore}
              onDownload={() => {
                setAwaitingCore(true)
                void cores.download(catalogue.projectId, catalogue.versionId)
              }}
            />
          )}

          {step === 'java' && (
            <JavaStep
              java={java}
              runtimes={runtimes}
              systemJava={systemJava}
              value={javaPath}
              custom={customJava}
              required={required}
              tooOld={javaTooOld}
              tooNew={javaTooNew}
              core={core}
              installMajor={installMajor}
              awaiting={awaitingJava}
              onPick={(next) => {
                javaTouched.current = true
                if (next === CUSTOM_JAVA) {
                  setCustomJava(true)
                  return
                }
                setCustomJava(false)
                setJavaPath(next)
              }}
              onCustom={setJavaPath}
              onInstallMajor={setInstallMajor}
              onInstall={(major) => {
                setAwaitingJava(true)
                void java.install(major, 'jre', '')
              }}
            />
          )}

          {step === 'basics' && (
            <BasicsStep
              name={name}
              onName={(value) => {
                namedByHand.current = true
                setName(value)
              }}
              directory={directory}
              onDirectory={setDirectory}
              directoryExists={directoryExists}
              jars={jars}
              jar={jar}
              onJar={setJar}
              needsJar={core === undefined}
              maxMemoryMB={maxMemoryMB}
              onMemory={setMaxMemoryMB}
              system={system}
              autoStart={autoStart}
              onAutoStart={setAutoStart}
              autoRestart={autoRestart}
              onAutoRestart={setAutoRestart}
            />
          )}

          {step === 'server' && (
            <ServerStep props={props} onChange={setProp} eula={eula} onEula={setEula} />
          )}

          {step === 'confirm' && (
            <ConfirmStep
              core={core}
              jar={jar}
              javaPath={javaPath}
              javaLabel={
                javaPath === 'java'
                  ? `系统 java${systemJava?.major ? `（Java ${systemJava.major}）` : ''}`
                  : (() => {
                      const picked = runtimes.find((runtime) => runtime.javaPath === javaPath)
                      return picked ? `Java ${picked.major} · ${picked.version}` : javaPath
                    })()
              }
              javaTooOld={javaTooOld}
              required={required}
              name={name}
              directory={directory}
              maxMemoryMB={maxMemoryMB}
              autoStart={autoStart}
              autoRestart={autoRestart}
              proxy={proxy}
              eula={eula}
              changed={changedProps}
              tasks={tasks}
              error={error}
            />
          )}

          <div className="wizard__nav">
            <button
              className="btn"
              type="button"
              disabled={busy}
              onClick={() => (index === 0 ? onCancel() : setStep(steps[index - 1].id))}
            >
              {index === 0 ? '取消' : '上一步'}
            </button>

            {step === 'confirm' ? (
              <button
                className="btn btn--primary"
                type="button"
                disabled={busy || !valid.basics}
                onClick={() => void create()}
              >
                {busy ? '创建中…' : '创建实例'}
              </button>
            ) : (
              <button
                className="btn btn--primary"
                type="button"
                disabled={!valid[step]}
                onClick={() => setStep(steps[index + 1].id)}
              >
                下一步：{steps[index + 1]?.label}
              </button>
            )}
          </div>

          {current && !valid[current.id] && <BlockedHint step={current.id} mode={coreMode} />}
        </>
      )}
    </Page>
  )
}

/** Why 下一步 is greyed out. A disabled button with no explanation is the
 *  worst thing a wizard can do. */
function BlockedHint({ step, mode }: { step: StepId; mode: CoreMode }) {
  const text =
    step === 'core'
      ? mode === 'download'
        ? '等这个核心下载完就能继续，或者切到「核心库」用一个已经有的。'
        : '先挑一个核心，或者切到「先不放核心」——之后再自己指定 jar 也行。'
      : step === 'basics'
        ? '给它起个名字就能继续。'
        : step === 'java'
          ? '自定义路径不能是空的。'
          : '端口要是 1 到 65535 之间的数字。'
  return <p className="wizard__blocked">{text}</p>
}

function CoreStep({
  mode,
  loaded,
  onMode,
  stored,
  coreId,
  onPick,
  cores,
  catalogue,
  awaiting,
  onDownload,
}: {
  mode: CoreMode
  loaded: boolean
  onMode: (mode: CoreMode) => void
  stored: ServerCore[]
  coreId: string
  onPick: (id: string) => void
  cores: CoreController
  catalogue: ReturnType<typeof useCoreCatalogue>
  awaiting: boolean
  onDownload: () => void
}) {
  const { job, downloading, busy } = cores

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">这台服务器跑什么</h2>
        <p className="chart-head__meta">核心决定版本，版本决定后面要哪个 Java</p>
      </div>

      <div className="segmented" role="group" aria-label="核心来源">
        {[
          { value: 'library' as const, label: '从核心库选', note: `已有 ${stored.length} 个，秒装` },
          { value: 'download' as const, label: '下载新核心', note: '从上游拉一个构建' },
          { value: 'none' as const, label: '先不放核心', note: '自己往目录里传 jar' },
        ].map((entry) => (
          <button
            key={entry.value}
            type="button"
            className={`segmented__option${
              mode === entry.value ? ' segmented__option--active' : ''
            }`}
            aria-pressed={mode === entry.value}
            disabled={downloading}
            onClick={() => onMode(entry.value)}
          >
            <strong>{entry.label}</strong>
            <small>{entry.note}</small>
          </button>
        ))}
      </div>

      {mode === 'library' &&
        (!loaded ? (
          <p className="muted">正在读取核心库…</p>
        ) : stored.length === 0 ? (
          <div className="welcome__empty">
            <p>核心库还是空的。</p>
            <p className="muted">
              <button className="link" type="button" onClick={() => onMode('download')}>
                下载一个
              </button>
              ，几十兆的事；下完就留在核心库里，以后再开服直接用。
            </p>
          </div>
        ) : (
          <div className="field">
            <span>核心库里的核心</span>
            <div className="choice-grid choice-grid--wide">
              {stored.map((entry) => (
                <button
                  key={entry.id}
                  type="button"
                  className={`choice${entry.id === coreId ? ' choice--active' : ''}`}
                  aria-pressed={entry.id === coreId}
                  onClick={() => onPick(entry.id)}
                >
                  <span className="choice__label">
                    {coreLabel(entry)}
                    {entry.kind === 'proxy' && <span className="badge">代理端</span>}
                    {entry.imported && <span className="badge">自行放入</span>}
                  </span>
                  <span className="choice__note">{coreNote(entry)}</span>
                </button>
              ))}
            </div>
            <small>选中的这一份会复制进新实例的目录，核心库里的原件不动。</small>
          </div>
        ))}

      {mode === 'download' && (
        <>
          {catalogue.loading ? (
            <p className="muted">正在读取可下载的核心…</p>
          ) : catalogue.projects.length === 0 ? (
            <div className="alert alert--error">
              没能取到可下载的核心列表 —— 通常是这台机器连不上外网。可以切到「核心库」用已经下好的，
              或者「先不放核心」，自己把 jar 传进目录。
            </div>
          ) : (
            <>
              <CoreCatalogue
                catalogue={catalogue}
                disabled={downloading}
                javaNote={(major) => `该版本至少需要 Java ${major}，下一步就装它。`}
              />
              {cores.error && <div className="alert alert--error">{cores.error}</div>}
              {awaiting && job && <DownloadStatus job={job} />}
              <div className="actions">
                {downloading ? (
                  <button
                    className="btn btn--danger"
                    type="button"
                    disabled={busy}
                    onClick={() => void cores.cancel()}
                  >
                    取消下载
                  </button>
                ) : (
                  <button
                    className="btn btn--primary"
                    type="button"
                    disabled={busy || !catalogue.versionId}
                    onClick={onDownload}
                  >
                    下载 {catalogue.project?.name ?? ''} {catalogue.versionId}
                  </button>
                )}
                <span className="file-toolbar__hint">
                  下载走服务器自己的网络，关掉网页也会继续。
                </span>
              </div>
            </>
          )}
        </>
      )}

      {mode === 'none' && (
        <p className="chart-note">
          实例先建起来，jar 之后再说 —— 用文件管理器传进目录，或者在「实例设置 → 从核心库安装」
          里装一个。下一步会让你填目录里那个 jar 的文件名，现在留空也行。
          Forge、Fabric、整合包服务端走的都是这条路。
        </p>
      )}
    </section>
  )
}

function DownloadStatus({ job }: { job: NonNullable<CoreController['job']> }) {
  if (job.state === 'downloading') {
    const fraction = job.total > 0 ? job.downloaded / job.total : 0
    return (
      <div className="download-status">
        <div className="progress">
          <div className="progress__bar" style={{ width: `${Math.round(fraction * 100)}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${Math.round(fraction * 100)}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : formatBytes(job.downloaded)}
          </span>
        </div>
        <p className="chart-note">
          正在下载 {job.fileName}（{job.projectName} {job.version} 构建 #{job.build}）
        </p>
      </div>
    )
  }
  if (job.state === 'failed') {
    return <div className="alert alert--error">下载失败：{job.error ?? '未知错误'}</div>
  }
  if (job.state === 'cancelled') {
    return <div className="alert">已取消下载，没有写入任何文件。</div>
  }
  return null
}

function JavaStep({
  java,
  runtimes,
  systemJava,
  value,
  custom,
  required,
  tooOld,
  tooNew,
  core,
  installMajor,
  awaiting,
  onPick,
  onCustom,
  onInstallMajor,
  onInstall,
}: {
  java: JavaController
  runtimes: JavaRuntime[]
  systemJava: { path: string; version: string; major: number; source: string } | null
  value: string
  custom: boolean
  required: number
  tooOld: boolean
  tooNew: boolean
  core: ServerCore | undefined
  installMajor: number | null
  awaiting: boolean
  onPick: (value: string) => void
  onCustom: (value: string) => void
  onInstallMajor: (major: number) => void
  onInstall: (major: number) => void
}) {
  const { job, installing, majors } = java

  /**
   * What this Java is worth to the core that was picked, as a badge.
   *
   * Both directions matter: a Java older than the floor cannot load the jar,
   * and one newer than 1.16's ceiling cannot either. A picker that only warned
   * about the first would put 可用 on the Java that breaks the second.
   */
  const verdict = (major: number): { label: string; warn: boolean } | null => {
    if (required === 0 || major === 0) return null
    if (major < required) return { label: '版本过低', warn: true }
    if (required === 8 && major >= 17) return { label: '可能太新', warn: true }
    return { label: '可用', warn: false }
  }

  const covered =
    required === 0 ||
    runtimes.some((runtime) => runtime.major >= required) ||
    (systemJava?.major ?? 0) >= required

  // LTS only, plus whatever is installed or picked. The rest of Adoptium's
  // list is not something a Minecraft server has any use for.
  const visibleMajors = majors.filter(
    (entry) => entry.lts || entry.installed || entry.major === installMajor,
  )

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">用哪个 Java 跑</h2>
        <p className="chart-head__meta">
          {required > 0
            ? `${core ? coreLabel(core) : '这个版本'} 需要 Java ${required} 或更高`
            : '服务端是个 jar，得有 Java 才能跑起来'}
        </p>
      </div>

      {required > 0 && !covered && (
        <div className="alert alert--warn">
          机器上还没有能跑这个版本的 Java。下面装一个 Java {required}，几十秒的事，
          装的是面板自己的一份，不动系统环境。
        </div>
      )}

      <div className="field">
        <span>启动用的 Java</span>
        <div className="choice-grid choice-grid--wide">
          {systemJava && (
            <button
              type="button"
              className={`choice${value === 'java' && !custom ? ' choice--active' : ''}`}
              aria-pressed={value === 'java' && !custom}
              onClick={() => onPick('java')}
            >
              <span className="choice__label">
                系统 java
                {systemJava.major > 0 && <span className="badge">Java {systemJava.major}</span>}
                <Verdict of={verdict(systemJava.major)} />
              </span>
              <span className="choice__note">{systemJava.path}</span>
            </button>
          )}

          {runtimes.map((runtime) => (
            <button
              key={runtime.id}
              type="button"
              className={`choice${value === runtime.javaPath && !custom ? ' choice--active' : ''}`}
              aria-pressed={value === runtime.javaPath && !custom}
              onClick={() => onPick(runtime.javaPath)}
            >
              <span className="choice__label">
                Java {runtime.major}
                <span className="badge">{runtime.imageType.toUpperCase()}</span>
                <Verdict of={verdict(runtime.major)} />
              </span>
              <span className="choice__note">
                {runtime.version} · 面板安装
              </span>
            </button>
          ))}

          <button
            type="button"
            className={`choice${custom ? ' choice--active' : ''}`}
            aria-pressed={custom}
            onClick={() => onPick(CUSTOM_JAVA)}
          >
            <span className="choice__label">自定义路径</span>
            <span className="choice__note">机器上别处装的 Java，自己填 java 可执行文件</span>
          </button>
        </div>

        {custom && (
          <input
            value={value}
            onChange={(e) => onCustom(e.target.value)}
            placeholder="/usr/lib/jvm/java-21-openjdk/bin/java"
            spellCheck={false}
          />
        )}
        {!systemJava && runtimes.length === 0 && (
          <small>这台机器上还没有任何 Java —— 下面装一个，不然服务端起不来。</small>
        )}
      </div>

      {tooOld && (
        <div className="alert alert--warn">
          选中的这个 Java 比 {core ? coreLabel(core) : '这个核心'} 要求的低，
          服务端启动时会直接报 UnsupportedClassVersionError。可以继续，但建议先装一个新的。
        </div>
      )}

      {tooNew && (
        <div className="alert alert--warn">
          {core ? coreLabel(core) : '1.16 及以下的服务端'} 是 Java 17 之前的东西，
          在新版 Java 上通常直接起不来。装一个 Java 8 给它，别的实例照样用新的。
        </div>
      )}

      <div className="panel__sub">
        <h3 className="panel__title">面板里再装一个</h3>
        {majors.length === 0 ? (
          <p className="muted">
            没能从 Adoptium 取到可安装的版本列表 —— 通常是这台机器连不上外网。
            已装的 Java 不受影响，上面照常可选。
          </p>
        ) : (
          <>
            <div className="choice-grid">
              {visibleMajors.map((entry) => (
                <button
                  key={entry.major}
                  type="button"
                  className={`choice${entry.major === installMajor ? ' choice--active' : ''}`}
                  aria-pressed={entry.major === installMajor}
                  disabled={installing}
                  onClick={() => onInstallMajor(entry.major)}
                >
                  <span className="choice__value">{entry.major}</span>
                  <span className="choice__label">
                    Java {entry.major}
                    {entry.lts && <span className="badge">LTS</span>}
                    {entry.installed && <span className="badge badge--ok">已安装</span>}
                    {entry.major === required && <span className="badge badge--ok">本核心需要</span>}
                  </span>
                </button>
              ))}
            </div>

            {java.error && <div className="alert alert--error">{java.error}</div>}
            {awaiting && job && <InstallStatus job={job} />}

            <div className="actions">
              {installing ? (
                <button
                  className="btn btn--danger"
                  type="button"
                  disabled={java.busy}
                  onClick={() => void java.cancel()}
                >
                  取消安装
                </button>
              ) : (
                <button
                  className="btn"
                  type="button"
                  disabled={java.busy || installMajor == null}
                  onClick={() => installMajor != null && onInstall(installMajor)}
                >
                  安装 Java {installMajor ?? ''} JRE
                </button>
              )}
              <span className="file-toolbar__hint">装好会自动选上，不用回头改。</span>
            </div>
          </>
        )}
      </div>
    </section>
  )
}

function Verdict({ of }: { of: { label: string; warn: boolean } | null }) {
  if (!of) return null
  return <span className={`badge ${of.warn ? 'badge--warn' : 'badge--ok'}`}>{of.label}</span>
}

function InstallStatus({ job }: { job: NonNullable<JavaController['job']> }) {
  if (job.state === 'downloading') {
    const fraction = job.total > 0 ? job.downloaded / job.total : 0
    return (
      <div className="download-status">
        <div className="progress">
          <div className="progress__bar" style={{ width: `${Math.round(fraction * 100)}%` }} />
          <span className="progress__label">
            {job.total > 0
              ? `${Math.round(fraction * 100)}% · ${formatBytes(job.downloaded)} / ${formatBytes(job.total)}`
              : formatBytes(job.downloaded)}
          </span>
        </div>
        <p className="chart-note">
          正在下载 Java {job.major} {job.imageType.toUpperCase()}（{job.version}）
        </p>
      </div>
    )
  }
  if (job.state === 'extracting') {
    return <div className="alert alert--ok">正在解压 Java {job.major}…</div>
  }
  if (job.state === 'failed') {
    return <div className="alert alert--error">安装失败：{job.error ?? '未知错误'}</div>
  }
  if (job.state === 'cancelled') {
    return <div className="alert">已取消安装，没有留下任何文件。</div>
  }
  return null
}

function BasicsStep({
  name,
  onName,
  directory,
  onDirectory,
  directoryExists,
  jars,
  jar,
  onJar,
  needsJar,
  maxMemoryMB,
  onMemory,
  system,
  autoStart,
  onAutoStart,
  autoRestart,
  onAutoRestart,
}: {
  name: string
  onName: (value: string) => void
  directory: string
  onDirectory: (value: string) => void
  directoryExists: boolean
  jars: { name: string; size: number }[]
  jar: string
  onJar: (value: string) => void
  needsJar: boolean
  maxMemoryMB: number
  onMemory: (value: number) => void
  system: SystemInfo | null
  autoStart: boolean
  onAutoStart: (value: boolean) => void
  autoRestart: boolean
  onAutoRestart: (value: boolean) => void
}) {
  const total = system?.host.memoryTotal ?? 0
  // Leaving the machine less than a fifth of its memory means the panel, the
  // OS and everything else are fighting the JVM for what is left.
  const tooMuch = total > 0 && maxMemoryMB * 1024 * 1024 > total * 0.8

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">叫什么，放在哪</h2>
        <p className="chart-head__meta">名字随时能改，目录建好之后就不方便动了</p>
      </div>

      <label className="field">
        <span>实例名称</span>
        <input
          value={name}
          onChange={(e) => onName(e.target.value)}
          placeholder="生存服"
          required
          autoFocus
        />
        <small>面板里怎么称呼它。留空的目录也会用这个名字。</small>
      </label>

      <DirectoryField
        value={directory}
        onChange={onDirectory}
        placeholder="留空则放在面板数据目录的 servers/ 下"
        hint={
          directory.trim() === ''
            ? '留空自动生成：面板数据目录的 servers/ 下，以实例名命名。也可以「浏览…」选本机任意位置 —— 外挂硬盘、NAS 挂载点，或者一个已经有服务端的目录。'
            : directoryExists
              ? `目录已存在${jars.length > 0 ? `，里面有 ${jars.length} 个 jar` : ''}。`
              : '这个目录还不存在，创建实例时会一并建好。'
        }
      />

      {needsJar && (
        <label className="field">
          <span>服务端 jar 文件名</span>
          <input
            value={jar}
            onChange={(e) => onJar(e.target.value)}
            placeholder="server.jar"
            list="wizard-jars"
            spellCheck={false}
          />
          <datalist id="wizard-jars">
            {jars.map((entry) => (
              <option key={entry.name} value={entry.name} />
            ))}
          </datalist>
          <small>
            {jars.length > 0
              ? `目录下找到 ${jars.length} 个 jar，点输入框可以直接选。`
              : '没选核心，所以这里填目录里那个 jar 的文件名。现在留空也行，之后在「实例设置」里补。'}
          </small>
        </label>
      )}

      <div className="field">
        <span>最大内存</span>
        <div className="segmented" role="group" aria-label="最大内存">
          {MEMORY_PRESETS.map((preset) => (
            <button
              key={preset.mb}
              type="button"
              className={`segmented__option${
                maxMemoryMB === preset.mb ? ' segmented__option--active' : ''
              }`}
              aria-pressed={maxMemoryMB === preset.mb}
              onClick={() => onMemory(preset.mb)}
            >
              <strong>{preset.mb / 1024} GB</strong>
              <small>{preset.note}</small>
            </button>
          ))}
        </div>
        <input
          type="number"
          min={512}
          step={512}
          value={maxMemoryMB}
          aria-label="最大内存 (MB)"
          onChange={(e) => onMemory(Number(e.target.value))}
        />
        <small>
          单位 MB。{total > 0 && `本机共 ${formatBytes(total)}。`}
          最小内存跟着设成 1 GB（或与这里相同，取小的那个）。
        </small>
      </div>

      {tooMuch && (
        <div className="alert alert--warn">
          这超过了本机内存的八成。系统、面板和别的实例也要吃内存，给到这么高的话，
          真用满时会被系统直接杀掉进程。
        </div>
      )}

      <label className="checkbox checkbox--stacked">
        <input
          type="checkbox"
          checked={autoStart}
          onChange={(e) => onAutoStart(e.target.checked)}
        />
        <span>面板启动时自动启动它</span>
        <small>机器重启后不用手动开服。</small>
      </label>

      <label className="checkbox checkbox--stacked">
        <input
          type="checkbox"
          checked={autoRestart}
          onChange={(e) => onAutoRestart(e.target.checked)}
        />
        <span>崩溃后自动重启</span>
        <small>连续失败 5 次后放弃，不会无限重启一个起不来的服务端。</small>
      </label>
    </section>
  )
}

function ServerStep({
  props,
  onChange,
  eula,
  onEula,
}: {
  props: Record<string, string>
  onChange: (key: string, value: string) => void
  eula: boolean
  onEula: (value: boolean) => void
}) {
  return (
    <>
      <section className="panel panel--form">
        <h2 className="panel__title">服务器设置</h2>
        <p className="chart-note">
          这些写进 <code>server.properties</code>，之后在「服务器配置」页随时能改 ——
          除了存档名和种子：世界一旦生成，改它们等于换一个世界。
        </p>

        <label className="field">
          <span>端口</span>
          <input
            type="number"
            min={1}
            max={65535}
            value={props['server-port']}
            onChange={(e) => onChange('server-port', e.target.value)}
          />
          <small>同一台机器上每个服务器要用不同的端口。</small>
        </label>

        <label className="field">
          <span>最大玩家数</span>
          <input
            type="number"
            min={1}
            value={props['max-players']}
            onChange={(e) => onChange('max-players', e.target.value)}
          />
        </label>

        <label className="field field--full">
          <span>服务器标语 (MOTD)</span>
          <input value={props.motd} onChange={(e) => onChange('motd', e.target.value)} />
          <small>多人游戏列表里显示的那行字。中文会自动转义，游戏内显示正常。</small>
        </label>

        <label className="field">
          <span>难度</span>
          <Select
            ariaLabel="难度"
            value={props.difficulty}
            options={DIFFICULTIES}
            onChange={(next) => onChange('difficulty', next)}
          />
        </label>

        <label className="field">
          <span>默认游戏模式</span>
          <Select
            ariaLabel="默认游戏模式"
            value={props.gamemode}
            options={GAMEMODES}
            onChange={(next) => onChange('gamemode', next)}
          />
        </label>

        <label className="field">
          <span>存档名称</span>
          <input
            value={props['level-name']}
            onChange={(e) => onChange('level-name', e.target.value)}
            spellCheck={false}
          />
          <small>世界文件夹的名字。</small>
        </label>

        <label className="field">
          <span>世界种子</span>
          <input
            value={props['level-seed']}
            onChange={(e) => onChange('level-seed', e.target.value)}
            placeholder="留空随机"
            spellCheck={false}
          />
          <small>只在第一次生成世界时有用，之后改它不会改变已经生成的地形。</small>
        </label>

        <label className="checkbox checkbox--stacked">
          <input
            type="checkbox"
            checked={props['online-mode'] === 'true'}
            onChange={(e) => onChange('online-mode', e.target.checked ? 'true' : 'false')}
          />
          <span>正版验证</span>
          <small>关掉才能让离线账号进服，同时也意味着任何人都能顶着别人的名字进来。</small>
        </label>
      </section>

      <section className={eula ? 'panel' : 'panel panel--warn'}>
        <h2 className="panel__title">Minecraft EULA</h2>
        <label className="checkbox checkbox--stacked">
          <input type="checkbox" checked={eula} onChange={(e) => onEula(e.target.checked)} />
          <span>
            我已阅读并同意{' '}
            <a href={EULA_URL} target="_blank" rel="noreferrer">
              Minecraft 最终用户许可协议
            </a>
          </span>
          <small>
            勾上之后面板会在实例目录里写一个 <code>eula=true</code>，记录的是你的决定。
            不同意也能建实例，但服务端启动后会立刻退出 —— 这是 Mojang 定的，不是面板。
          </small>
        </label>
      </section>
    </>
  )
}

function ConfirmStep({
  core,
  jar,
  javaPath,
  javaLabel,
  javaTooOld,
  required,
  name,
  directory,
  maxMemoryMB,
  autoStart,
  autoRestart,
  proxy,
  eula,
  changed,
  tasks,
  error,
}: {
  core: ServerCore | undefined
  jar: string
  javaPath: string
  javaLabel: string
  javaTooOld: boolean
  required: number
  name: string
  directory: string
  maxMemoryMB: number
  autoStart: boolean
  autoRestart: boolean
  proxy: boolean
  eula: boolean
  changed: [string, string][]
  tasks: Task[] | null
  error: string | null
}) {
  const habits = [autoStart ? '开机自启' : '', autoRestart ? '崩溃自动重启' : '']
    .filter(Boolean)
    .join(' · ')

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">确认一下</h2>
        <p className="chart-head__meta">按下创建之前，磁盘上什么都还没变</p>
      </div>

      <dl className="wizard-summary">
        <div>
          <dt>名称</dt>
          <dd>{name.trim() || '（还没填）'}</dd>
        </div>
        <div>
          <dt>目录</dt>
          <dd>
            {directory.trim() === '' ? (
              <span className="muted">自动生成：面板数据目录的 servers/{name.trim()}</span>
            ) : (
              <code>{directory.trim()}</code>
            )}
          </dd>
        </div>
        <div>
          <dt>核心</dt>
          <dd>
            {core ? (
              <>
                {coreLabel(core)} <code>{core.fileName}</code>
              </>
            ) : jar.trim() !== '' ? (
              <>
                不复制核心，启动 <code>{jar.trim()}</code>
              </>
            ) : (
              <span className="muted">不放核心，之后自己指定 jar</span>
            )}
          </dd>
        </div>
        <div>
          <dt>Java</dt>
          <dd>
            {javaLabel}
            {javaTooOld && (
              <span className="badge badge--warn">低于该核心要求的 Java {required}</span>
            )}
            <br />
            <code>{javaPath}</code>
          </dd>
        </div>
        <div>
          <dt>内存</dt>
          <dd>最大 {maxMemoryMB} MB{habits && ` · ${habits}`}</dd>
        </div>
        {!proxy && (
          <div>
            <dt>服务器设置</dt>
            <dd>
              {changed.length > 0 ? (
                changed.map(([key, value]) => (
                  <span className="badge" key={key}>
                    {key}={value || '（空）'}
                  </span>
                ))
              ) : (
                <span className="muted">全部保持默认，服务端第一次启动时自己生成</span>
              )}
            </dd>
          </div>
        )}
        {!proxy && (
          <div>
            <dt>EULA</dt>
            <dd>{eula ? '已同意，创建时写入' : <span className="muted">还没同意</span>}</dd>
          </div>
        )}
      </dl>

      {!proxy && !eula && (
        <div className="alert alert--warn">
          EULA 还没同意，这个服务端启动后会立刻退出。可以现在回上一步勾一下，
          也可以之后在「服务器配置」页里勾。
        </div>
      )}

      {tasks && (
        <ul className="wizard-tasks">
          {tasks.map((task) => (
            <li key={task.id} className={`wizard-task wizard-task--${task.state}`}>
              <span className="wizard-task__mark" aria-hidden="true">
                {TASK_MARKS[task.state]}
              </span>
              <span className="wizard-task__label">{task.label}</span>
              {task.note && <span className="wizard-task__note">{task.note}</span>}
            </li>
          ))}
        </ul>
      )}

      {error && <div className="alert alert--error">{error}</div>}
    </section>
  )
}

function Finished({
  instance,
  tasks,
  busy,
  eula,
  proxy,
  hasCore,
  onStart,
  onOpen,
}: {
  instance: InstanceStatus
  tasks: Task[]
  busy: boolean
  eula: boolean
  proxy: boolean
  hasCore: boolean
  onStart: () => void
  onOpen: () => void
}) {
  const failed = tasks.filter((task) => task.state === 'failed')
  // Starting a server with no jar, or one that will quit on the EULA line, is
  // not a helpful default — so the button that does it says why it is not the
  // primary one.
  const ready = hasCore && (proxy || eula) && failed.length === 0

  return (
    <section className="panel">
      <h2 className="panel__title">「{instance.name}」建好了</h2>
      {/* The directory is the one fact worth repeating here: it is where the
          world will be, and for an auto-generated path this is the first time
          anyone sees it. */}
      <p className="panel__path">{instance.directory}</p>

      <ul className="wizard-tasks">
        {tasks.map((task) => (
          <li key={task.id} className={`wizard-task wizard-task--${task.state}`}>
            <span className="wizard-task__mark" aria-hidden="true">
              {TASK_MARKS[task.state]}
            </span>
            <span className="wizard-task__label">{task.label}</span>
            {task.note && <span className="wizard-task__note">{task.note}</span>}
          </li>
        ))}
      </ul>

      {failed.length > 0 && (
        <div className="alert alert--error">
          实例本身建好了，但有 {failed.length} 步没做成。进去之后在「实例设置」和「服务器配置」里
          可以把它们补上。
        </div>
      )}

      {!hasCore && (
        <div className="alert">
          目录里还没有服务端 jar。用「文件」页传一个进去，或者在「实例设置 → 从核心库安装」里装一个，
          然后才能开服。
        </div>
      )}

      {!proxy && !eula && (
        <div className="alert alert--warn">
          EULA 还没同意，现在启动的话服务端会立刻退出。去「服务器配置」页勾一下就行。
        </div>
      )}

      <div className="actions">
        <button className="btn btn--primary" type="button" disabled={busy} onClick={onOpen}>
          进入控制台
        </button>
        <button className="btn" type="button" disabled={busy || !ready} onClick={onStart}>
          {busy ? '启动中…' : '立即开服'}
        </button>
      </div>
    </section>
  )
}
