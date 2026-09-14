import { useEffect, useMemo, useRef, useState } from 'react'

import { api, uploadCore } from '../api'
import { formatBytes, formatDate } from '../format'
import { toast, toastError } from '../toast'
import type { CoreBuild, CoreProject, CoreVersion } from '../types'
import type { JavaController } from '../useJava'
import { Badge } from './Badge'
import { Button } from './Button'
import { Icon } from './Icon'
import { Modal } from './Modal'
import { Note } from './Note'
import { Skeleton } from './Skeleton'
import { isRecommended } from './CoreCatalogue'

/**
 * Adding a core: which one, which version, which build — on one screen.
 *
 * This was a card that stood permanently on the library page, and it was the
 * wrong half of it: looking at the shelf is a daily thing, fetching a new core
 * is a monthly one, and the monthly one had two thirds of the page. So it is a
 * dialog now, and the page underneath is the list.
 *
 * Three things it is deliberately not:
 *
 * A wizard. Type, version and build are one decision made in three places, and
 * paging them hides the two you are comparing against. They are three panes of
 * one dialog and all three stay on screen.
 *
 * A flat list of versions. A build belongs *to* a version, and the old layout
 * put versions in a row of pills with "1 个构建" underneath, which reads as two
 * peers. Versions on the left, that version's builds on the right.
 *
 * Silent about Java. The compatibility check runs the moment a build is picked
 * rather than on the way out, and when it fails it says so where the eye
 * already is, with the way to fix it attached. Java that is too old is the
 * single most common reason a new core will not boot, and the panel knew it
 * all along and used to mention it in grey text at the bottom of the page.
 */

/** The id of the tile that is not a project. */
const UPLOAD = '\x00upload'

/** Roughly the tallest build list worth showing before it scrolls. */
const BUILD_ROWS = 3

export function AddCoreDialog({
  java,
  busy,
  upload,
  onClose,
  onDownload,
  onUploaded,
  onOpenJava,
}: {
  java: JavaController
  /** True while the shelf has a request of its own in flight. */
  busy: boolean
  /** Opens straight on the upload tile, for the 上传 jar button in the page
   *  head. It is the same dialog either way — a second one would be a second
   *  place for the metadata form to drift. */
  upload?: boolean
  onClose: () => void
  onDownload: (project: string, version: string, build: number) => Promise<void>
  onUploaded: () => void
  /** Goes to Java 运行时 with the major this build needs already picked. */
  onOpenJava: (major: number) => void
}) {
  const [projects, setProjects] = useState<CoreProject[]>([])
  const [picked, setPicked] = useState(upload ? UPLOAD : '')
  const [versions, setVersions] = useState<CoreVersion[]>([])
  const [versionId, setVersionId] = useState('')
  const [builds, setBuilds] = useState<CoreBuild[] | null>(null)
  const [buildId, setBuildId] = useState(0)
  const [filter, setFilter] = useState('')
  const [unstable, setUnstable] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)

  useEffect(() => {
    let live = true
    api
      .listCoreProjects()
      .then((list) => {
        if (!live) return
        setProjects(list)
        // Only when nothing is picked yet: arriving on the upload tile is a
        // choice the caller made, and the catalogue landing a moment later
        // must not take it back.
        setPicked((current) => current || list[0]?.id || UPLOAD)
      })
      .catch(() => live && setPicked(UPLOAD))
    return () => {
      live = false
    }
  }, [])

  useEffect(() => {
    if (picked === '' || picked === UPLOAD) return
    let live = true
    setVersions([])
    setVersionId('')
    setBuilds(null)
    setBuildId(0)
    setFilter('')
    api
      .listCoreVersions(picked)
      .then((list) => {
        if (!live) return
        setVersions(list)
        setVersionId(pickDefault(list))
        setError(null)
      })
      .catch((err) => live && setError(err instanceof Error ? err.message : '获取版本列表失败'))
    return () => {
      live = false
    }
  }, [picked])

  useEffect(() => {
    if (picked === '' || picked === UPLOAD || versionId === '') return
    let live = true
    setBuilds(null)
    setBuildId(0)
    api
      .listCoreBuilds(picked, versionId)
      .then((list) => {
        if (!live) return
        setBuilds(list)
        // The newest is what almost everybody wants, and having it picked is
        // what makes the confirmation bar say something on arrival.
        setBuildId(list[0]?.build ?? 0)
      })
      .catch(() => live && setBuilds([]))
    return () => {
      live = false
    }
  }, [picked, versionId])

  const project = projects.find((item) => item.id === picked)
  const version = versions.find((item) => item.id === versionId)
  const build = builds?.find((item) => item.build === buildId)

  // Two filters over one list, and whatever is selected survives both: the
  // confirmation bar names that version, so it has to stay on screen.
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const matched = versions.filter(
      (item) => (unstable || item.stable) && (!needle || item.id.toLowerCase().includes(needle)),
    )
    if (versionId && !matched.some((item) => item.id === versionId)) {
      const current = versions.find((item) => item.id === versionId)
      if (current) return [current, ...matched]
    }
    return matched
  }, [versions, unstable, filter, versionId])

  const shortfall = javaShortfall(java, version?.javaMinimum ?? 0)

  const start = async (close: () => void) => {
    if (!project || !version || !build) return
    setSending(true)
    try {
      await onDownload(project.id, version.id, build.build)
      // Straight out: the row the operator is waiting for is on the list
      // behind this dialog, with its own progress on it.
      close()
    } finally {
      setSending(false)
    }
  }

  return (
    <Modal onClose={onClose} label="添加核心" busy={sending}>
      {(close) => (
        <div className="modal__card modal__card--addcore">
          <header className="addcore__head">
            <div>
              <h2>添加核心</h2>
              <p className="addcore__lead">下载走服务器自己的网络，关掉网页也会继续。</p>
            </div>
            <Button icon type="button" aria-label="关闭" onClick={close} disabled={sending}>
              <Icon name="close" />
            </Button>
          </header>

          <div className="addcore__body">
            <section className="addcore__kinds" aria-label="核心类型">
              {projects.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className={`kindcard${item.id === picked ? ' kindcard--on' : ''}`}
                  aria-pressed={item.id === picked}
                  onClick={() => setPicked(item.id)}
                >
                  <span className="kindcard__tile" aria-hidden="true">
                    {item.name.slice(0, 1)}
                  </span>
                  <span className="kindcard__name">
                    {item.name}
                    {item.kind === 'proxy' && <Badge>代理端</Badge>}
                  </span>
                  <span className="kindcard__note">{item.description}</span>
                </button>
              ))}

              {/* The jar the catalogue cannot reach — Forge, Fabric, a modpack's
                  own server. The panel has always accepted one dropped into the
                  cores directory and has never had a way in from the page, so
                  the instruction to go and do it by hand was printed on the
                  page instead. */}
              <button
                type="button"
                className={`kindcard${picked === UPLOAD ? ' kindcard--on' : ''}`}
                aria-pressed={picked === UPLOAD}
                onClick={() => setPicked(UPLOAD)}
              >
                <span className="kindcard__tile kindcard__tile--quiet" aria-hidden="true">
                  +
                </span>
                <span className="kindcard__name">上传自定义 jar</span>
                <span className="kindcard__note">
                  Forge、Fabric、整合包自带的服务端，或者任何一个自己编译的 jar。
                </span>
              </button>
            </section>

            {picked === UPLOAD ? (
              <UploadPane busy={sending} onDone={onUploaded} onClose={close} />
            ) : (
              <div className="addcore__pick">
                <section className="addcore__versions" aria-label="版本">
                  <div className="addcore__tools">
                    <input
                      className="input-slim"
                      type="search"
                      value={filter}
                      placeholder="筛选版本"
                      aria-label="筛选版本"
                      onChange={(event) => setFilter(event.target.value)}
                      disabled={versions.length === 0}
                    />
                    <label className="checkbox checkbox--inline">
                      <input
                        type="checkbox"
                        checked={unstable}
                        onChange={(event) => {
                          setUnstable(event.target.checked)
                          if (!event.target.checked && version && !version.stable) {
                            setVersionId(pickDefault(versions))
                          }
                        }}
                      />
                      <span>显示预览版与快照</span>
                    </label>
                  </div>

                  <div className="addcore__vlist">
                    {versions.length === 0
                      ? Array.from({ length: 6 }, (_, i) => (
                          <Skeleton key={i} w="100%" h={30} />
                        ))
                      : groupVersions(visible, project?.kind === 'proxy').map((group) => (
                          <div key={group.label} className="addcore__group">
                            {group.label !== '' && (
                              <span className="addcore__grouphead">{group.label}</span>
                            )}
                            {group.items.map((item, index) => (
                              <button
                                key={item.id}
                                type="button"
                                className={`vrow${item.id === versionId ? ' vrow--on' : ''}`}
                                aria-pressed={item.id === versionId}
                                onClick={() => setVersionId(item.id)}
                              >
                                <span className="vrow__id">{item.id}</span>
                                {group.newest && index === 0 && <Badge tone="ok">最新</Badge>}
                                {item.support === 'UNSUPPORTED' && <Badge tone="muted">旧</Badge>}
                                {!item.stable && <Badge tone="warn">预览</Badge>}
                              </button>
                            ))}
                          </div>
                        ))}
                  </div>
                </section>

                <section className="addcore__builds" aria-label="构建">
                  <div className="addcore__bhead">
                    <span>构建号</span>
                    <span>变更摘要</span>
                    <span>发布时间</span>
                    <span className="addcore__num">体积</span>
                  </div>

                  <div className="addcore__blist">
                    {builds === null ? (
                      // Skeleton rows rather than a line of grey text: what is
                      // coming is a table, and a sentence in its place moves
                      // everything under it the moment the answer lands.
                      Array.from({ length: BUILD_ROWS }, (_, i) => (
                        <div className="brow brow--ghost" key={i}>
                          <Skeleton w="52px" h={12} />
                          <Skeleton w="70%" h={12} />
                          <Skeleton w="92px" h={12} />
                          <Skeleton w="44px" h={12} />
                        </div>
                      ))
                    ) : builds.length === 0 ? (
                      <p className="addcore__none">这个版本没有可下载的构建。</p>
                    ) : (
                      builds.map((item, index) => (
                        <button
                          key={item.build}
                          type="button"
                          className={`brow${item.build === buildId ? ' brow--on' : ''}`}
                          aria-pressed={item.build === buildId}
                          onClick={() => setBuildId(item.build)}
                        >
                          <span className="brow__id">
                            #{item.build}
                            {index === 0 && <Badge tone="ok">最新</Badge>}
                            {!isRecommended(item.channel) && (
                              <Badge tone="warn">{item.channel}</Badge>
                            )}
                          </span>
                          <span className="brow__log" title={item.changelog}>
                            {item.changelog || '没有变更摘要'}
                          </span>
                          <span className="brow__when">{formatDate(item.time)}</span>
                          <span className="addcore__num">{formatBytes(item.size)}</span>
                        </button>
                      ))
                    )}
                  </div>
                </section>
              </div>
            )}
          </div>

          {picked !== UPLOAD && (
            <>
              {error !== null && <Note tone="error">{error}</Note>}

              {/* Above the confirmation bar rather than under the page: this is
                  the one thing on screen that decides whether the jar will
                  start, and it carries the way to fix it. */}
              {shortfall !== null && (
                <Note tone="warn" className="addcore__warn">
                  <span>
                    这个核心需要 Java {shortfall.needs} 或更高，
                    {shortfall.have === 0
                      ? '本机一个 Java 都还没装'
                      : `本机目前最高是 Java ${shortfall.have}`}
                    —— 直接启动会报错退出。
                  </span>
                  <Button type="button" onClick={() => onOpenJava(shortfall.needs)}>
                    去装 Java {shortfall.needs}
                  </Button>
                </Note>
              )}

              <footer className="addcore__foot">
                <div className="addcore__target">
                  <span className="addcore__tile" aria-hidden="true">
                    {(project?.name ?? '?').slice(0, 1)}
                  </span>
                  <div className="addcore__what">
                    <strong>
                      {project?.name} {versionId}
                      {build ? ` #${build.build}` : ''}
                    </strong>
                    <code>
                      {build
                        ? [
                            build.fileName,
                            formatBytes(build.size),
                            (version?.javaMinimum ?? 0) > 0
                              ? `需要 Java ${version?.javaMinimum}+`
                              : 'Java 要求未知',
                          ].join(' · ')
                        : '正在确认构建'}
                    </code>
                  </div>
                </div>
                <div className="addcore__acts">
                  <Button type="button" onClick={close} disabled={sending}>
                    取消
                  </Button>
                  <Button
                    variant="primary"
                    type="button"
                    onClick={() => void start(close)}
                    disabled={busy || sending || !build}
                  >
                    {/* A failed check does not block the download: fetching the
                        jar now and installing the Java afterwards is a normal
                        order to do this in. The button only stops pretending
                        everything is fine. */}
                    {shortfall !== null ? '仍然下载' : '下载到核心库'}
                  </Button>
                </div>
              </footer>
            </>
          )}
        </div>
      )}
    </Modal>
  )
}

/**
 * The upload branch: the jar, and the four things about it only the operator
 * knows.
 *
 * Asking is the point. A jar dropped into the directory by hand shows up in
 * the list with no version, no Minecraft range and no Java requirement, and
 * those are the three columns that answer "will this start". The form is the
 * difference between a row that is informative and a row wearing 信息不全.
 */
function UploadPane({
  busy,
  onDone,
  onClose,
}: {
  busy: boolean
  onDone: () => void
  onClose: () => void
}) {
  const [file, setFile] = useState<File | null>(null)
  const [kind, setKind] = useState('server')
  const [version, setVersion] = useState('')
  const [javaMinimum, setJavaMinimum] = useState('')
  const [minecraft, setMinecraft] = useState('')
  const [over, setOver] = useState(false)
  const [progress, setProgress] = useState(0)
  const [sending, setSending] = useState(false)
  const input = useRef<HTMLInputElement | null>(null)

  const take = (list: FileList | null) => {
    const first = list?.[0]
    if (!first) return
    if (!first.name.toLowerCase().endsWith('.jar')) {
      toastError(`${first.name} 不是 jar 文件。`)
      return
    }
    setFile(first)
  }

  const send = async () => {
    if (!file) return
    setSending(true)
    try {
      await uploadCore(
        file,
        {
          kind,
          version: version.trim(),
          javaMinimum: Number(javaMinimum) || 0,
          minecraft: minecraft.trim(),
        },
        setProgress,
      )
      toast(`${file.name} 已存入核心库。`)
      onDone()
      onClose()
    } catch (err) {
      toastError(err instanceof Error ? err.message : '上传失败')
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="addcore__upload">
      <div
        className={`drop${over ? ' drop--over' : ''}`}
        onDragOver={(event) => {
          event.preventDefault()
          setOver(true)
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(event) => {
          event.preventDefault()
          setOver(false)
          take(event.dataTransfer.files)
        }}
      >
        <input
          ref={input}
          type="file"
          accept=".jar"
          hidden
          onChange={(event) => take(event.target.files)}
        />
        <strong>{file ? file.name : '把 jar 拖到这里'}</strong>
        <span className="drop__note">
          {file ? formatBytes(file.size) : '或者点下面的按钮挑一个。单个文件，最大 1 GB。'}
        </span>
        <Button type="button" onClick={() => input.current?.click()} disabled={sending}>
          {file ? '换一个文件' : '选择 jar'}
        </Button>
        {sending && (
          <span className="progress drop__progress">
            <span className="progress__bar" style={{ width: `${Math.round(progress * 100)}%` }} />
          </span>
        )}
      </div>

      <div className="addcore__form">
        <p className="addcore__formlead">
          这几项没人能替你填：jar 里没有一处可靠地写着它服务哪个 Minecraft 版本、要哪个
          Java。留空不会去猜，列表里那一行会标成「信息不全」。
        </p>

        <label className="field">
          <span>类型</span>
          <div className="addcore__kindpick">
            {[
              ['server', '服务端'],
              ['proxy', '代理端'],
            ].map(([id, label]) => (
              <button
                key={id}
                type="button"
                className={`chip${kind === id ? ' chip--on' : ''}`}
                aria-pressed={kind === id}
                onClick={() => setKind(id)}
              >
                {label}
              </button>
            ))}
          </div>
        </label>

        <label className="field">
          <span>版本</span>
          <input
            value={version}
            placeholder="47.2.0"
            onChange={(event) => setVersion(event.target.value)}
          />
        </label>

        <label className="field">
          <span>需要的 Java 版本</span>
          <input
            type="number"
            min={8}
            max={64}
            value={javaMinimum}
            placeholder="17"
            onChange={(event) => setJavaMinimum(event.target.value)}
          />
        </label>

        <label className="field">
          <span>支持的 MC 版本</span>
          <input
            value={minecraft}
            placeholder="1.20.1"
            onChange={(event) => setMinecraft(event.target.value)}
          />
        </label>

        <div className="actions">
          <Button type="button" onClick={onClose} disabled={sending}>
            取消
          </Button>
          <Button
            variant="primary"
            type="button"
            onClick={() => void send()}
            disabled={busy || sending || !file}
          >
            存入核心库
          </Button>
        </div>
      </div>
    </div>
  )
}

/** The version most people want: newest, still supported, not a pre-release. */
function pickDefault(versions: CoreVersion[]): string {
  const supported = versions.find((v) => v.stable && v.support === 'SUPPORTED')
  return (supported ?? versions.find((v) => v.stable) ?? versions[0])?.id ?? ''
}

/**
 * What the machine is short by, or null when it is not.
 *
 * Against the highest Java installed rather than against any of them: an
 * instance is pointed at one runtime, but the question here is whether this
 * core *can* run on this machine at all, and the answer to that is the best
 * Java there is.
 */
function javaShortfall(java: JavaController, needs: number): { needs: number; have: number } | null {
  if (needs <= 0) return null
  const runtimes = java.overview?.runtimes ?? []
  const have = runtimes.reduce((best, runtime) => Math.max(best, runtime.major), 0)
  // An empty list is not proof of nothing installed — it is also what an
  // unread shelf looks like for the first second of the page's life, and
  // warning on that would make the bar flash on every open.
  if (java.overview === null) return null
  return have >= needs ? null : { needs, have }
}

/**
 * Versions under the Minecraft line they belong to.
 *
 * A world server publishes one line per game version and there are two hundred
 * of them, so a flat list is a scroll with no landmarks. A proxy's versions are
 * its own — 3.4.0 is not a Minecraft version — and grouping those by their
 * first segment would invent a hierarchy that is not there.
 */
function groupVersions(
  versions: CoreVersion[],
  proxy: boolean,
): { label: string; items: CoreVersion[]; newest: boolean }[] {
  if (proxy) return [{ label: '', items: versions, newest: true }]

  const groups: { label: string; items: CoreVersion[]; newest: boolean }[] = []
  for (const version of versions) {
    const parts = version.id.split('.')
    const label = parts.length >= 2 ? `${parts[0]}.${parts[1]}.x` : version.id
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.items.push(version)
    else groups.push({ label, items: [version], newest: groups.length === 0 })
  }
  return groups
}
