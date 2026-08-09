import { useEffect, useState } from 'react'

import { formatBytes, formatDate } from '../format'
import type { LibraryView } from '../routes'
import type { JavaInstallJob, JavaRuntime, JavaSource, SystemJava } from '../types'
import type { JavaController } from '../useJava'
import { Page } from './Page'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/** Named because the page renders it before its data arrives as well as after,
 *  and the two have to be the same string or the page moves when it loads. */
const JAVA_LEAD =
  '不同版本的服务端要不同的 Java：1.16 要 8，1.17 要 17，1.20.5 起要 21，Paper 26 要 25。这里装的 Java 归面板所有，不动系统里的 Java；装好之后在实例的「启动设置」里选一个即可。'

/** The heading each of the three pages carries. */
const TITLES: Partial<Record<LibraryView, string>> = {
  installed: 'Java 环境',
  install: '安装新版本',
  source: '下载源',
}

const LEADS: Partial<Record<LibraryView, string>> = {
  installed: JAVA_LEAD,
  install: '从 Eclipse Temurin 装一个新的大版本。下载走服务器自己的网络，关掉网页也会继续。',
  source:
    '装的都是同一个 Eclipse Temurin 构建 —— 版本信息和校验和始终来自 Adoptium 官方，镜像只负责传那几十兆的压缩包，对不上的一律不装。',
}

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
 * one-click install of a Temurin build.
 *
 * It is its own page rather than part of an instance because a runtime is
 * shared — one download serves every server that needs that version, and
 * deleting one is a decision about all of them. Instances only pick from what
 * is here, in their 「启动设置」.
 *
 * Three pages under one entry — what is installed, what can be installed, and
 * where it is fetched from — but one component: the version list and the
 * chosen mirror are the same decision seen from two sides, and the source page
 * would have nothing to hand the install page if they were split apart.
 */
export function JavaPage({
  java,
  view,
  onOpenView,
  onOpenCores,
}: {
  java: JavaController
  view: LibraryView
  onOpenView: (view: LibraryView) => void
  onOpenCores: () => void
}) {
  const { overview, majors, sources, job, installing, busy } = java
  const [major, setMajor] = useState<number | null>(null)
  const [imageType, setImageType] = useState<'jre' | 'jdk'>('jre')
  const [showAllMajors, setShowAllMajors] = useState(false)
  const [source, setSource] = useState<string | null>(null)

  // Default to the newest LTS: it is what current Minecraft wants and what
  // upstream supports longest. Only until the operator picks something.
  useEffect(() => {
    if (majors.length === 0) return
    setMajor((current) => current ?? (majors.find((m) => m.lts) ?? majors[0]).major)
  }, [majors])

  // The source the last install used, until this page picks another. It comes
  // from the panel rather than this browser: it describes the server's route
  // out, so it should be the same on a phone as on the laptop that set it.
  const remembered = overview?.source
  useEffect(() => {
    if (!remembered) return
    setSource((current) => current ?? remembered)
  }, [remembered])

  const remove = async (runtime: JavaRuntime) => {
    const warning =
      runtime.usedBy.length > 0
        ? `实例「${runtime.usedBy.join('、')}」还在用 ${runtime.version}，删掉后它们下次启动会失败。确定删除吗？`
        : `确定要删除 Java ${runtime.version}（${formatBytes(runtime.size)}）吗？`
    if (!window.confirm(warning)) return
    await java.remove(runtime.id)
  }

  if (!overview) {
    // The heading and the lead are constants, not data — showing them for real
    // straight away means the page opens with its own name on it, and the only
    // thing that arrives later is what was actually being fetched. Replacing
    // the lead with 正在读取… and then swapping in three lines of copy moved
    // everything below it down the moment the request came back.
    return (
      <Page wide title={TITLES[view] ?? 'Java 环境'} lead={JAVA_LEAD}>
        <SkeletonScreen inPage label="正在读取已装的 Java…">
          <SkeletonPanel title={false}>
            <div className="chart-head">
              <Skeleton w="64px" h={15} />
              <Skeleton w="180px" h={12} />
            </div>
            {/* 已安装 is a grid of runtime cards, and how many there are is
                exactly what is being fetched — so this is one card's worth,
                the commonest case on a machine that has been set up. */}
            <div className="asset-grid">
              <Skeleton w="100%" h={196} />
            </div>
          </SkeletonPanel>
          <SkeletonPanel title={false}>
            <div className="chart-head">
              <Skeleton w="96px" h={15} />
              <Skeleton w="220px" h={12} />
            </div>
            <Skeleton w="100%" h={34} />
            <Skeleton w="60%" h={34} />
          </SkeletonPanel>
        </SkeletonScreen>
      </Page>
    )
  }

  const selected = majors.find((entry) => entry.major === major)
  const runtimes = overview.runtimes
  const totalSize = runtimes.reduce((sum, runtime) => sum + runtime.size, 0)
  // Adoptium ships every major, but only the LTS ones (and whatever is already
  // on disk, or picked) are worth putting in front of someone running a
  // Minecraft server. The rest are one click away.
  const visibleMajors = majors.filter(
    (entry) => showAllMajors || entry.lts || entry.installed || entry.major === major,
  )
  const hiddenMajors = majors.length - visibleMajors.length

  const sourceName = sources.find((entry) => entry.id === source)?.name ?? '自动选择'

  return (
    <Page
      wide
      title={TITLES[view] ?? 'Java 环境'}
      lead={LEADS[view] ?? JAVA_LEAD}
      aside={
        <p className="meta-chips">
          {overview.platform.os && (
            <span>
              {overview.platform.os}/{overview.platform.arch}
            </span>
          )}
          <span>面板已装 {runtimes.length} 个</span>
          {runtimes.length > 0 && <span>共 {formatBytes(totalSize)}</span>}
          <span>由 Eclipse Temurin 提供</span>
        </p>
      }
    >

      {overview.platform.warning && (
        <div className="alert alert--error">{overview.platform.warning}</div>
      )}

      {/* An install keeps running after you navigate away, so it is reported
          on whichever of these pages you happen to be looking at. */}
      {view !== 'install' && job && (job.state === 'downloading' || job.state === 'extracting') && (
        <InstallStatus job={job} sources={sources} />
      )}

      {view === 'installed' && (
      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">已安装</h2>
          <p className="chart-head__meta">
            {runtimes.length > 0
              ? `面板管理 ${runtimes.length} 个，共 ${formatBytes(totalSize)}`
              : '面板还没有装过 Java'}
          </p>
        </div>

        {runtimes.length === 0 && !overview.system ? (
          <div className="welcome__empty">
            <p>这台机器上还没有任何 Java，服务端起不来。</p>
            <p className="muted">
              <button className="link" type="button" onClick={() => onOpenView('install')}>
                挑一个版本装上
              </button>
              ，几十秒的事，全程不动系统环境。
            </p>
          </div>
        ) : (
          <div className="asset-grid">
            {overview.system && <SystemCard system={overview.system} />}
            {runtimes.map((runtime) => (
              <RuntimeCard
                key={runtime.id}
                runtime={runtime}
                busy={busy}
                onRemove={() => void remove(runtime)}
              />
            ))}
          </div>
        )}
      </section>
      )}

      {view === 'install' && (
      <section className="panel">
        {job && <InstallStatus job={job} sources={sources} />}
        {java.error && <div className="alert alert--error">{java.error}</div>}

        {majors.length === 0 ? (
          <p className="muted">
            没能从 Adoptium 取到可安装的版本列表 —— 通常是这台机器连不上外网。
            已装的 Java 不受影响，仍然可以正常启动服务器。
          </p>
        ) : (
          <>
            <div className="field">
              <div className="field__head">
                <span>选择版本</span>
                {(hiddenMajors > 0 || showAllMajors) && (
                  <button
                    className="link"
                    type="button"
                    onClick={() => setShowAllMajors((on) => !on)}
                  >
                    {showAllMajors ? '只看 LTS 版本' : `显示全部 ${majors.length} 个版本`}
                  </button>
                )}
              </div>
              <div className="choice-grid">
                {visibleMajors.map((entry) => (
                  <button
                    key={entry.major}
                    type="button"
                    className={`choice${entry.major === major ? ' choice--active' : ''}`}
                    aria-pressed={entry.major === major}
                    disabled={installing}
                    onClick={() => setMajor(entry.major)}
                  >
                    <span className="choice__value">{entry.major}</span>
                    <span className="choice__label">
                      Java {entry.major}
                      {entry.lts && <span className="badge">LTS</span>}
                      {entry.installed && <span className="badge badge--ok">已安装</span>}
                    </span>
                    <span className="choice__note">{majorNote(entry.major, entry.lts)}</span>
                  </button>
                ))}
              </div>
              <small>标注的是这个大版本对应的服务端版本区间，拿不准就选 LTS。</small>
            </div>

            <div className="field">
              <span>镜像类型</span>
              <div className="segmented" role="group" aria-label="镜像类型">
                {IMAGE_TYPES.map((entry) => (
                  <button
                    key={entry.value}
                    type="button"
                    className={`segmented__option${
                      imageType === entry.value ? ' segmented__option--active' : ''
                    }`}
                    aria-pressed={imageType === entry.value}
                    disabled={installing}
                    onClick={() => setImageType(entry.value)}
                  >
                    <strong>{entry.label}</strong>
                    <small>{entry.note}</small>
                  </button>
                ))}
              </div>
            </div>

            {sources.length > 0 && (
              <p className="chart-note">
                下载源：{sourceName} ——{' '}
                <button className="link" type="button" onClick={() => onOpenView('source')}>
                  换一个
                </button>
                。国内机器直连 Adoptium 慢的话，换个教育网镜像通常快得多。
              </p>
            )}

            <div className="actions">
              {installing ? (
                <button
                  className="btn btn--danger"
                  onClick={() => void java.cancel()}
                  disabled={busy}
                >
                  取消安装
                </button>
              ) : (
                <button
                  className="btn btn--primary"
                  onClick={() => major != null && void java.install(major, imageType, source ?? '')}
                  disabled={busy || major == null}
                >
                  {selected?.installed ? '重新安装' : '安装'} Java {major ?? ''}{' '}
                  {imageType.toUpperCase()}
                </button>
              )}
              <span className="file-toolbar__hint">
                装到 <code>{overview.root}</code>，不会碰系统里的 Java。
              </span>
            </div>
          </>
        )}
      </section>
      )}

      {view === 'source' && (
        <SourcePicker
          sources={sources}
          current={source}
          busy={installing}
          onPick={setSource}
          onDone={() => onOpenView('install')}
        />
      )}

      {view === 'installed' && (
        <p className="chart-note">
          服务端 jar 本身不在这里 —— 那在
          <button className="link" onClick={onOpenCores}>
            服务端核心
          </button>
          。每个核心版本对 Java 的最低要求也标在那一页上。
        </p>
      )}
    </Page>
  )
}

/**
 * Where the archive is fetched from.
 *
 * Its own page rather than a field in the install form: it is chosen once, on
 * the day the panel is set up or the day a mirror stops working, and the
 * install form is where you go weekly. The choice is remembered by the panel
 * itself — it describes the server's route out, not this browser's — so it is
 * the same on a phone as on the laptop that set it.
 */
function SourcePicker({
  sources,
  current,
  busy,
  onPick,
  onDone,
}: {
  sources: JavaSource[]
  current: string | null
  busy: boolean
  onPick: (id: string) => void
  onDone: () => void
}) {
  if (sources.length === 0) {
    return (
      <div className="alert">
        没能取到可用的下载源列表 —— 通常是这台机器连不上外网。已装的 Java 不受影响。
      </div>
    )
  }

  return (
    <section className="panel">
      <p className="chart-note">只影响下载速度：装的是同一个构建，校验和始终来自 Adoptium 官方。</p>

      <div className="choice-grid choice-grid--wide">
        {sources.map((entry) => (
          <button
            key={entry.id}
            type="button"
            className={`choice${entry.id === current ? ' choice--active' : ''}`}
            aria-pressed={entry.id === current}
            disabled={busy}
            onClick={() => onPick(entry.id)}
          >
            <span className="choice__label">
              {entry.name}
              {entry.default && <span className="badge">推荐</span>}
            </span>
            <span className="choice__note">{entry.note}</span>
          </button>
        ))}
      </div>

      <p className="chart-note">
        选的源没有某个版本（镜像同步有延迟）会自动换下一个，下次安装默认还用这次选的。
        安装任务条上会写明这一次实际是从哪里下的。
      </p>

      <div className="actions">
        <button className="btn btn--primary" type="button" onClick={onDone}>
          去安装
        </button>
      </div>
    </section>
  )
}

/** The machine's own Java. Listed because an instance can launch with it, but
 *  it is not the panel's to delete. */
function SystemCard({ system }: { system: SystemJava }) {
  return (
    <article className="asset asset--muted">
      <div className="asset__head">
        <span className="asset__tile">{system.major || '?'}</span>
        <div className="asset__title">
          <strong>系统 Java {system.major || '?'}</strong>
          <span className="asset__sub">{system.vendor || '未知发行方'}</span>
        </div>
        <span className="badge">来自 {system.source}</span>
      </div>

      <dl className="asset__facts">
        <div>
          <dt>完整版本</dt>
          <dd>{system.version}</dd>
        </div>
        <div>
          <dt>归属</dt>
          <dd>系统自带</dd>
        </div>
      </dl>

      <p className="asset__path" title={system.path}>
        <code>{system.path}</code>
      </p>

      <footer className="asset__actions">
        <span className="muted">面板不管理它，也不会删除它。</span>
      </footer>
    </article>
  )
}

function RuntimeCard({
  runtime,
  busy,
  onRemove,
}: {
  runtime: JavaRuntime
  busy: boolean
  onRemove: () => void
}) {
  return (
    <article className="asset">
      <div className="asset__head">
        <span className="asset__tile asset__tile--accent">{runtime.major}</span>
        <div className="asset__title">
          <strong>Java {runtime.major}</strong>
          <span className="asset__sub">{runtime.vendor || '未知发行方'}</span>
        </div>
        <span className="badge">{runtime.imageType.toUpperCase()}</span>
        {runtime.live && <span className="badge badge--live">运行中</span>}
      </div>

      <dl className="asset__facts">
        <div>
          <dt>完整版本</dt>
          <dd>{runtime.version}</dd>
        </div>
        <div>
          <dt>体积</dt>
          <dd>{formatBytes(runtime.size)}</dd>
        </div>
        <div>
          <dt>安装于</dt>
          <dd>{formatDate(runtime.installedAt)}</dd>
        </div>
      </dl>

      <p className="asset__path" title={runtime.javaPath}>
        <code>{runtime.javaPath}</code>
      </p>

      <footer className="asset__actions">
        {runtime.usedBy.length > 0 ? (
          <span className="asset__users">
            使用中：
            {runtime.usedBy.map((name) => (
              <span className="badge" key={name}>
                {name}
              </span>
            ))}
          </span>
        ) : (
          <span className="muted">暂时没有实例用它</span>
        )}
        <button
          className="link link--danger"
          disabled={busy || runtime.live}
          title={runtime.live ? '有实例正在用它运行，先停服' : undefined}
          onClick={onRemove}
        >
          删除
        </button>
      </footer>
    </article>
  )
}

function InstallStatus({ job, sources }: { job: JavaInstallJob; sources: JavaSource[] }) {
  // The job carries the source that is actually serving it, which is not
  // always the one that was picked — a mirror that has not synced this build
  // yet hands over to the next one. Saying so is the difference between "why
  // is this slow" and "ah, it fell back to GitHub".
  const from = sources.find((entry) => entry.id === job.source)?.name ?? job.source

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
          {from && ` · 下载源：${from}`}
        </p>
      </div>
    )
  }

  if (job.state === 'extracting') {
    return (
      <div className="alert alert--ok">
        正在解压 Java {job.major}（{job.version}）…
      </div>
    )
  }

  if (job.state === 'done') {
    return (
      <div className="alert alert--ok">
        Java {job.major}（{job.version}）已安装，去实例的「启动设置」里选它。
      </div>
    )
  }

  if (job.state === 'cancelled') {
    return <div className="alert alert--ok">已取消安装 Java {job.major}，没有留下任何文件。</div>
  }

  return (
    <div className="alert alert--error">
      安装失败：{job.error ?? '未知错误'}
      {from && <> —— 可以换个下载源再试一次（这次用的是{from}）。</>}
    </div>
  )
}
