import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'

import { ask } from '../confirm'
import { formatBytes, formatDate } from '../format'
import type { DownloadJob, JavaDistribution, JavaRuntime, SystemJava } from '../types'
import { jobMeta } from '../types'
import type { JavaController } from '../useJava'
import { Badge } from './Badge'
import { Button } from './Button'
import { EmptyState } from './EmptyState'
import { Note } from './Note'
import { Page } from './Page'
import { Section } from './Section'
import { Select } from './Select'
import { Shelf } from './Shelf'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'

/** Named because the page renders it before its data arrives as well as after,
 *  and the two have to be the same string or the page moves when it loads. */
const JAVA_LEAD =
  '不同版本的服务端要不同的 Java：1.16 要 8，1.17 要 17，1.20.5 起要 21，Paper 26 要 25。这里装的 Java 归面板所有，不动系统里的 Java；装好之后在实例的「启动设置」里选一个即可。'

/** Which Java a Minecraft version needs, shown on the version being picked. */
const VERSION_HINTS: Record<number, string> = {
  8: '1.8 – 1.16.5',
  17: '1.17 – 1.20.4',
  21: '1.20.5 及以上',
  25: 'Paper 26 及以上',
}

/** What to say about a version nothing in the table covers. */
function majorNote(major: number, lts: boolean): string {
  return VERSION_HINTS[major] ?? (lts ? '长期支持版本' : '过渡版本，一般用不到')
}

const IMAGE_TYPES: { value: 'jre' | 'jdk'; label: string; note: string }[] = [
  { value: 'jre', label: 'JRE', note: '跑服够用，体积更小' },
  { value: 'jdk', label: 'JDK', note: '带编译器和调试工具' },
]

/**
 * Panel-wide Java management: what is installed, what the system has, and a
 * one-click install of a build from whichever distribution is selected.
 *
 * It is its own page rather than part of an instance because a runtime is
 * shared — one download serves every server that needs that version, and
 * deleting one is a decision about all of them. Instances only pick from what
 * is here, in their 「启动设置」.
 *
 * One page, and it was three: what is installed, what can be installed, and
 * where it is fetched from. They were split because the second and third were
 * each a screen tall; they are a line-per-build grid and two selects in a card
 * head now, so all three fit above the fold and the split was costing a
 * navigation step for nothing. The order still carries what the split was
 * protecting: what you have first, never a form.
 */
export function JavaPage({
  java,
  want,
  onOpenCores,
}: {
  java: JavaController
  /** The major to arrive with already picked, when something sent the operator
   *  here to install a specific one — the 添加核心 dialog's warning does. A
   *  button that drops you on this page and leaves you to find Java 25 in the
   *  list is the half-measure that warning used to be. */
  want?: number
  onOpenCores: () => void
}) {
  const { overview, majors, distributions, job, installing, busy } = java
  // Shut by default: adding a Java by hand is a thing you do once, and
  // this page is mostly opened to install one or to read what is there.
  const [adding, setAdding] = useState(false)
  const [newPath, setNewPath] = useState('')
  const [major, setMajor] = useState<number | null>(null)
  const [imageType, setImageType] = useState<'jre' | 'jdk'>('jre')
  const [showAllMajors, setShowAllMajors] = useState(false)
  const [source, setSource] = useState<string | null>(null)
  const [distribution, setDistribution] = useState<string | null>(null)

  // Default to the newest LTS: it is what current Minecraft wants and what
  // upstream supports longest. Only until the operator picks something.
  useEffect(() => {
    if (majors.length === 0) return
    setMajor((current) => current ?? (majors.find((m) => m.lts) ?? majors[0]).major)
  }, [majors])

  // A major asked for in the URL wins over that default, and over whatever was
  // picked on a previous visit: it is the whole content of the link that got
  // them here. Shown even when upstream does not offer it — the list has a
  // switch for the majors it hides, and silently picking something else would
  // be worse than an empty selection.
  useEffect(() => {
    if (!want) return
    setMajor(want)
    setShowAllMajors(true)
  }, [want])

  // The source the last install used, until this page picks another. It comes
  // from the panel rather than this browser: it describes the server's route
  // out, so it should be the same on a phone as on the laptop that set it.
  const remembered = overview?.source
  useEffect(() => {
    if (!remembered) return
    setSource((current) => current ?? remembered)
  }, [remembered])

  const rememberedDistribution = overview?.distribution
  useEffect(() => {
    if (!rememberedDistribution) return
    setDistribution((current) => current ?? rememberedDistribution)
  }, [rememberedDistribution])

  const remove = async (runtime: JavaRuntime) => {
    // Two different deletes behind one button. Removing a managed runtime
    // erases files; removing a registered path only forgets it, and says so —
    // promising to free bytes it will not free is worse than saying nothing.
    const external = runtime.origin === 'external'
    const ok = await ask({
      title: external
        ? `不再登记这个 Java？`
        : `删除 Java ${runtime.version}？`,
      lead: external
        ? '面板只是忘掉这条路径，磁盘上的 Java 一个字节都不动。'
        : `会从面板的运行时目录里删掉它，释放 ${formatBytes(runtime.size)}。`,
      detail:
        runtime.usedBy.length > 0
          ? `实例「${runtime.usedBy.join('、')}」还在用它。它们照常启动——启动读的是实例自己的配置，不是这张表——但下次在设置里改动 Java 之前，得先重新选一个。`
          : external
            ? '没有实例在用它。'
            : '没有实例在用它，系统自带的 Java 也不受影响。',
      confirmLabel: external ? '不再登记' : '删除',
      danger: true,
    })
    if (!ok) return
    if (external) {
      await java.unregister(runtime.id)
      return
    }
    await java.remove(runtime.id)
  }

  const submitPath = (event: FormEvent) => {
    event.preventDefault()
    const path = newPath.trim()
    if (path) void java.register(path)
  }

  // The controller reports a refusal through java.error rather than throwing,
  // so the submit handler cannot tell whether the path was taken. The row
  // turning up in the list can — and it is the only proof worth acting on.
  // A rejected path stays in the box, which is where it has to be to be fixed.
  useEffect(() => {
    const path = newPath.trim()
    if (!path || !overview) return
    if (overview.runtimes.some((entry) => entry.javaPath === path)) {
      setNewPath('')
      setAdding(false)
    }
  }, [overview, newPath])

  if (!overview) {
    // The heading and the lead are constants, not data — showing them for real
    // straight away means the page opens with its own name on it, and the only
    // thing that arrives later is what was actually being fetched. Replacing
    // the lead with 正在读取… and then swapping in three lines of copy moved
    // everything below it down the moment the request came back.
    return (
      <Page wide title="Java 环境" lead={JAVA_LEAD}>
        <SkeletonScreen inPage label="正在读取已装的 Java…">
          <SkeletonPanel head>
            {/* 已安装 is a list of runtime rows, and how many there are is
                exactly what is being fetched — so this is the system Java plus
                one install, the commonest case on a machine that has been set
                up. */}
            <SkeletonRows rows={2} />
          </SkeletonPanel>
          <SkeletonPanel head>
            <Skeleton w="100%" h={34} />
            <Skeleton w="60%" h={34} />
          </SkeletonPanel>
        </SkeletonScreen>
      </Page>
    )
  }

  const runtimes = overview.runtimes
  const totalSize = runtimes.reduce((sum, runtime) => sum + runtime.size, 0)
  // The prompt to register what is on PATH exists to answer "I have a Java and
  // the panel will not let me pick it". A bare "java" entry — what an instance
  // configured before the registry says — answers that too, so either one
  // silences it.
  const systemUsable =
    overview.system != null &&
    runtimes.some(
      (entry) => entry.javaPath === overview.system?.path || entry.javaPath === 'java',
    )
  const detected = systemUsable ? null : overview.system
  // Both distributions ship every major, but only the LTS ones (and whatever
  // is already on disk, or picked) are worth putting in front of someone
  // running a Minecraft server. The rest are one click away.
  const visibleMajors = majors.filter(
    (entry) => showAllMajors || entry.lts || entry.installed || entry.major === major,
  )
  const hiddenMajors = majors.length - visibleMajors.length

  // The source list follows the distribution picked on this page, not the one
  // the panel remembers: picking Temurin has to show the Adoptium mirrors
  // straight away, before anything is installed.
  const chosen = distributions.find((entry) => entry.id === distribution)
  const sources = chosen?.sources ?? []
  const distributionName = chosen?.name ?? 'Java'
  // A source that is a URL rather than one of the offered mirrors is the
  // hand-typed Zulu prefix, and the box below the grid is where it shows.
  const customMirror = source != null && /^https?:\/\//.test(source) ? source : ''

  return (
    <Page
      wide
      title="Java 环境"
      lead={JAVA_LEAD}
      facts={
        <>
          {/* One fact, not four. The head used to carry os/arch, a count, a
              total and the distribution's name as four separate chips, none of
              which is the thing you came to read. What is worth a glance is
              how much of the disk this shelf is holding. */}
          <span>
            {runtimes.length > 0
              ? `${runtimes.length} 个运行时 · 占用 ${formatBytes(totalSize)}`
              : '还没装 Java'}
          </span>
          {overview.platform.os && (
            <span>
              {overview.platform.os}/{overview.platform.arch}
            </span>
          )}
        </>
      }
    >
      {overview.platform.warning && (
        <Note tone="warn">{overview.platform.warning}</Note>
      )}
      {java.error && <div className="alert">{java.error}</div>}

      {/* An install keeps running after you navigate away, so it is reported
          at the top of the page rather than inside the card that started it. */}
      {job && <InstallStatus job={job} distributions={distributions} />}

      <Section
        title="可用的 Java"
        meta={
          runtimes.length > 0
            ? `${runtimes.length} 个可选，面板自己装的占用 ${formatBytes(totalSize)}`
            : '还没有可选的 Java'
        }
        tools={
          <button className="link" type="button" onClick={() => setAdding((on) => !on)}>
            {adding ? '取消' : '添加本机 Java'}
          </button>
        }
      >
        {adding && (
          <form className="java-add" onSubmit={submitPath}>
            <input
              type="text"
              className="input-slim java-add__path"
              value={newPath}
              spellCheck={false}
              autoFocus
              aria-label="Java 可执行文件的路径"
              placeholder="/usr/lib/jvm/java-21-openjdk/bin/java"
              onChange={(e) => setNewPath(e.target.value)}
            />
            <Button type="submit" disabled={busy || newPath.trim() === ''}>
              登记
            </Button>
            <p className="java-add__note muted">
              填 java 可执行文件本身，不是它所在的目录。面板会跑一次{' '}
              <code>java -version</code> 问出版本再记下来——问不出来的不会被登记。
            </p>
          </form>
        )}

        {runtimes.length === 0 && !detected ? (
          <EmptyState title="还没有可选的 Java，实例的启动设置里会是空的。">
            下面挑一个版本装上，几十秒的事，全程不动系统环境；已经有 Java 的话，上面「添加本机
            Java」填路径登记进来。
          </EmptyState>
        ) : (
          <Shelf head={['Java', '完整版本', '体积', '装入 / 登记于', '使用中的实例', '']}>
            {detected && (
              <DetectedRow
                system={detected}
                busy={busy}
                onRegister={() => void java.register(detected.path, true)}
              />
            )}
            {runtimes.map((runtime) => (
              <RuntimeRow
                key={runtime.id}
                runtime={runtime}
                busy={busy}
                onRemove={() => void remove(runtime)}
                onReprobe={() => void java.reprobe(runtime.id)}
              />
            ))}
          </Shelf>
        )}
      </Section>

      {/* 可安装 used to be a page of its own, and 下载设置 another. Both are on
          this one now: the catalogue is a line per build rather than a screen
          of chooser tiles, and where those builds come from is two selects in
          this card's head — which is where a property of the download belongs,
          rather than behind a second navigation step. */}
      <Section
        title="可安装"
        tools={
          majors.length > 0 && (
            <>
              {distributions.length > 1 && (
                <Select
                  value={distribution ?? ''}
                  onChange={(id) => {
                    setDistribution(id)
                    // The two source lists share nothing but auto and
                    // official, so a mirror picked for the other
                    // distribution cannot carry over.
                    setSource(null)
                  }}
                  disabled={installing}
                  ariaLabel="发行版"
                  className="input-slim"
                  options={distributions.map((entry) => ({
                    value: entry.id,
                    label: entry.name,
                    note: entry.note,
                  }))}
                />
              )}
              {sources.length > 0 && (
                <Select
                  value={source ?? ''}
                  onChange={setSource}
                  disabled={installing}
                  ariaLabel="下载源"
                  className="input-slim"
                  placeholder="下载源"
                  options={sources.map((entry) => ({
                    value: entry.id,
                    label: entry.name,
                    note: entry.note,
                  }))}
                />
              )}
              <div className="segmented segmented--inline" role="group" aria-label="镜像类型">
                {IMAGE_TYPES.map((entry) => (
                  <button
                    key={entry.value}
                    type="button"
                    className={`segmented__option${
                      imageType === entry.value ? ' segmented__option--active' : ''
                    }`}
                    aria-pressed={imageType === entry.value}
                    title={entry.note}
                    disabled={installing}
                    onClick={() => setImageType(entry.value)}
                  >
                    <strong>{entry.label}</strong>
                  </button>
                ))}
              </div>
              {(hiddenMajors > 0 || showAllMajors) && (
                <button
                  className="link"
                  type="button"
                  onClick={() => setShowAllMajors((on) => !on)}
                >
                  {showAllMajors ? '只看 LTS' : `全部 ${majors.length} 个`}
                </button>
              )}
            </>
          )
        }
      >
        {majors.length === 0 ? (
          <>
            <p className="muted">
              没能从 {distributionName} 取到可安装的版本列表 —— 通常是这台机器连不上外网。
              已装的 Java 不受影响，仍然可以正常启动服务器。
            </p>
          </>
        ) : (
          <>
            <div className="pick-grid">
              {visibleMajors.map((entry) => {
                const running = installing && job !== null && Number(jobMeta(job, 'major')) === entry.major
                return (
                  <div
                    key={entry.major}
                    className={`pick${entry.major === major ? ' pick--on' : ''}`}
                  >
                    <span className="pick__tile">{entry.major}</span>
                    <div className="pick__body">
                      <span className="pick__name">
                        <strong>Java {entry.major}</strong>
                        {entry.lts && <Badge>LTS</Badge>}
                        {entry.installed && <Badge tone="ok">已安装</Badge>}
                      </span>
                      <span className="pick__meta">
                        {majorNote(entry.major, entry.lts)} · {imageType.toUpperCase()}
                      </span>
                    </div>
                    {running ? (
                      <Button
                        size="small"
                        variant="danger"
                        type="button"
                        disabled={busy}
                        onClick={() => void java.cancel()}
                      >
                        取消
                      </Button>
                    ) : (
                      <Button
                        size="small"
                        type="button"
                        disabled={busy || installing}
                        onClick={() => {
                          setMajor(entry.major)
                          void java.install(
                            distribution ?? '',
                            entry.major,
                            imageType,
                            source ?? '',
                          )
                        }}
                      >
                        {entry.installed ? '重装' : '安装'}
                      </Button>
                    )}
                  </div>
                )
              })}
            </div>

            {/* Only Zulu. Temurin's mirrors copy a nested tree, so a bare
                prefix there would 404 — which is why this is not a field the
                other distribution simply leaves empty. */}
            {distribution === 'zulu' && (
              <div className="field">
                <span>或者自己填一个镜像地址（可选）</span>
                <input
                  value={customMirror}
                  onChange={(event) => setSource(event.target.value.trim())}
                  placeholder="https://mirror.example/zulu/bin/"
                  spellCheck={false}
                  disabled={installing}
                />
                <small>
                  Zulu 的包在 cdn.azul.com，国内没有已知的镜像，所以上面不预设。知道能用的加速地址就填在这儿
                  —— 面板会把文件名接在后面下载，校验和照样卡 Azul 官方的，下不到会自动退回官方 CDN。
                </small>
              </div>
            )}

            <p className="chart-note">
              标注的是这个大版本对应的服务端版本区间，拿不准就选 LTS。装的都是 {distributionName}{' '}
              的官方构建，校验和始终来自它自己的接口，下载源只负责传压缩包，对不上的一律不装；装到{' '}
              <code>{overview.root}</code>，不会碰系统里的 Java。
            </p>
          </>
        )}
      </Section>

      <p className="chart-note">
        服务端 jar 本身不在这里 —— 那在
        <button className="link" onClick={onOpenCores}>
          服务端核心
        </button>
        。每个核心版本对 Java 的最低要求也标在那一页上。
      </p>
    </Page>
  )
}

/** The machine's own Java. Listed because an instance can launch with it, but
 *  it is not the panel's to delete. */
/**
 * The Java the panel found on this machine but has not been told it may use.
 *
 * It is an offer, not an entry: detection is the panel guessing, and a list
 * where every row was decided by a person is the whole point of the registry.
 * Accepting it registers the absolute path rather than the bare "java" — the
 * operator is confirming this Java, not a promise to follow PATH wherever it
 * goes later.
 *
 * Once accepted the row disappears, because the same Java is then in the list
 * below like any other.
 */
function DetectedRow({
  system,
  busy,
  onRegister,
}: {
  system: SystemJava
  busy: boolean
  onRegister: () => void
}) {
  return (
    <article className="asset asset--muted">
      <div className="asset__head">
        <span className="asset__tile">{system.major || '?'}</span>
        <div className="asset__title">
          <span className="asset__label">
            <strong>系统 Java {system.major || '?'}</strong>
            <Badge>来自 {system.source}</Badge>
            <Badge tone="muted">未登记</Badge>
          </span>
          <span className="asset__sub">
            <span>{system.vendor || '未知发行方'}</span>
            <code title={system.path}>{system.path}</code>
          </span>
        </div>
      </div>

      <dl className="asset__facts asset__facts--split">
        <div>
          <dt>完整版本</dt>
          <dd>{system.version}</dd>
        </div>
        {/* The panel did not install this one, so it knows neither its size nor
            when it arrived. The two cells stay to hold their tracks — the
            columns below have to line up under the ones above. */}
        <div className="asset__hole" aria-hidden="true" />
        <div className="asset__hole" aria-hidden="true" />
      </dl>

      <footer className="asset__actions asset__actions--split">
        <span className="muted">面板发现了它，但实例里还选不到。</span>
        <button className="link" type="button" disabled={busy} onClick={onRegister}>
          登记进来
        </button>
      </footer>
    </article>
  )
}

function RuntimeRow({
  runtime,
  busy,
  onRemove,
  onReprobe,
}: {
  runtime: JavaRuntime
  busy: boolean
  onRemove: () => void
  onReprobe: () => void
}) {
  const external = runtime.origin === 'external'
  return (
    <article className="asset">
      <div className="asset__head">
        <span className={`asset__tile${external ? '' : ' asset__tile--accent'}`}>
          {runtime.major || '?'}
        </span>
        <div className="asset__title">
          <span className="asset__label">
            <strong>Java {runtime.major || '?'}</strong>
            {external ? <Badge tone="muted">本机路径</Badge> : <Badge>{runtime.imageType.toUpperCase()}</Badge>}
            {runtime.live && <Badge tone="live">运行中</Badge>}
            {/* The only colour in this list. A path that is gone still shows,
                because an instance points at it — but it has to say so, or the
                first anyone hears of it is a server that will not start. */}
            {!runtime.valid && <Badge tone="warn">路径已失效</Badge>}
          </span>
          <span className="asset__sub">
            <span>{runtime.vendor || '未知发行方'}</span>
            <code title={runtime.javaPath}>{runtime.javaPath}</code>
          </span>
        </div>
      </div>

      <dl className="asset__facts asset__facts--split">
        <div>
          <dt>完整版本</dt>
          <dd>{runtime.version || '未知'}</dd>
        </div>
        {/* A registered path is somebody else's directory: the panel neither
            measured it nor put it there. The cell stays to hold its track. */}
        {external ? (
          <div className="asset__hole" aria-hidden="true" />
        ) : (
          <div>
            <dt>体积</dt>
            <dd>{formatBytes(runtime.size)}</dd>
          </div>
        )}
        <div>
          <dt>{external ? '登记于' : '安装于'}</dt>
          <dd>{formatDate(runtime.installedAt)}</dd>
        </div>
      </dl>

      <footer className="asset__actions asset__actions--split">
        {runtime.usedBy.length > 0 ? (
          <span className="asset__users">
            使用中：
            {runtime.usedBy.map((name) => (
              <Badge key={name}>
                {name}
              </Badge>
            ))}
          </span>
        ) : (
          <span className="muted">暂时没有实例用它</span>
        )}
        <span className="asset__buttons">
          {/* Only for a registered path: a managed runtime's version comes off
              the release file the panel unpacked, and nothing upgrades it in
              place. Somebody else's JDK does get upgraded under the panel. */}
          {external && (
            <button className="link" type="button" disabled={busy} onClick={onReprobe}>
              重新检测
            </button>
          )}
          <button
            className="link link--danger"
            type="button"
            disabled={busy || runtime.live}
            title={runtime.live ? '有实例正在用它运行，先停服' : undefined}
            onClick={onRemove}
          >
            {external ? '不再登记' : '删除'}
          </button>
        </span>
      </footer>
    </article>
  )
}

/** Which source is serving it: the one that answered, or — until one has —
 *  the one that was asked for. */
function sourceOf(job: DownloadJob): string {
  return job.route || jobMeta(job, 'source')
}

function InstallStatus({
  job,
  distributions,
}: {
  job: DownloadJob
  distributions: JavaDistribution[]
}) {
  // The job carries the source that is actually serving it, which is not
  // always the one that was picked — a mirror that has not synced this build
  // yet hands over to the next one. Saying so is the difference between "why
  // is this slow" and "ah, it fell back to GitHub".
  //
  // Looked up under the job's own distribution rather than the page's: a
  // running install keeps its source name even if the picker has moved on.
  const from =
    distributions
      .find((entry) => entry.id === jobMeta(job, 'distribution'))
      ?.sources.find((entry) => entry.id === sourceOf(job))?.name ?? sourceOf(job)

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
          正在下载 {job.title}
          {job.subtitle ? ` ${job.subtitle.toUpperCase()}` : ''}
          {from && ` · 下载源：${from}`}
        </p>
      </div>
    )
  }

  if (job.state === 'extracting') {
    return (
      <Note tone="ok">
        正在解压 {job.title}…
      </Note>
    )
  }

  if (job.state === 'done') {
    return (
      <Note tone="ok">
        {job.title} 已安装，去实例的「启动设置」里选它。
      </Note>
    )
  }

  if (job.state === 'cancelled') {
    return <Note tone="ok">已取消安装 Java {Number(jobMeta(job, 'major'))}，没有留下任何文件。</Note>
  }

  return (
    <Note tone="error">
      安装失败：{job.error ?? '未知错误'}
      {from && <> —— 可以换个下载源再试一次（这次用的是{from}）。</>}
    </Note>
  )
}
