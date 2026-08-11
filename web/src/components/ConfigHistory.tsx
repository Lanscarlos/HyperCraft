import { useEffect, useMemo, useRef, useState } from 'react'

import { api, ApiError } from '../api'
import { ask } from '../confirm'
import { formatBytes, formatDate, formatSince } from '../format'
import { toast } from '../toast'
import type {
  ConfigFileChange,
  ConfigFileDiff,
  ConfigHistoryOverview,
  ConfigSnapshot,
  DiffLine,
  InstanceStatus,
  RestorePlan,
  SnapshotTrigger,
} from '../types'
import { Modal } from './Modal'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/**
 * The timeline of one server's configuration.
 *
 * Two panes: what happened, and what it changed. That split is the whole
 * information architecture — a timeline where each row already listed its files
 * would be unreadable at forty rows, and a file list without the timeline
 * cannot answer the question anybody arrives with, which is "what changed
 * between yesterday and today".
 *
 * The line the page must never stop saying: this is not a backup. Worlds,
 * player data and databases are outside what it records, deliberately, and the
 * feeling of having history is exactly the thing that would let somebody stop
 * taking real backups. So it is in the lead, and again in the footer.
 */
export function ConfigHistory({
  instance,
  focus,
  onOpenInFiles,
}: {
  instance: InstanceStatus
  /** A file 文件管理 wants examined here. Carries a token for the same reason
   *  FileJump does: this pane stays mounted, so asking twice for the same file
   *  has to work twice. */
  focus?: ConfigHistoryFocus
  /** Opens a recorded file where it actually lives, in 文件管理. Absent when
   *  nothing upstream can switch sections. */
  onOpenInFiles?: (path: string) => void
}) {
  const [data, setData] = useState<ConfigHistoryOverview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<TimelineFilter>('all')
  const [selected, setSelected] = useState<string | null>(null)
  const [changes, setChanges] = useState<ConfigFileChange[] | null>(null)
  const [openFile, setOpenFile] = useState<{ path: string; againstCurrent: boolean } | null>(null)
  const [diff, setDiff] = useState<ConfigFileDiff | null>(null)
  const [busy, setBusy] = useState(false)
  const [snapshotting, setSnapshotting] = useState(false)
  const [plan, setPlan] = useState<RestorePlan | null>(null)
  // Bumped by 刷新. Every fetch on this page hangs off it, so one click brings
  // the timeline, the open change list and the open diff back in step with a
  // directory that other people — and the server itself — keep writing to.
  const [reloads, setReloads] = useState(0)
  // 与出厂对比 wants to open a file that only exists once the oldest snapshot's
  // change list has come back, and setting openFile at click time does not
  // survive — the reset effect below clears it on every selection change. So
  // the click leaves the intent here and the fetch acts on it. A ref rather
  // than state: it must not be a dependency of the effect that reads it.
  const pendingFactory = useRef(false)
  // Same problem, known file: what to open once the selection lands.
  const opening = useRef<OpenFile | null>(null)
  const handledFocus = useRef<number | undefined>(undefined)

  const load = async () => {
    try {
      const next = await api.configHistory(instance.id)
      setData(next)
      setError(null)
      // Keep the selection across a reload where it still exists; otherwise
      // fall to the newest row, which is what somebody opening the tab wants.
      // 当前改动 is not in the timeline and outlives any reload: it is a view of
      // the directory, not of a commit.
      const timeline = next.timeline ?? []
      setSelected((current) =>
        current === PENDING_REF || (current && timeline.some((row) => row.ref === current))
          ? current
          : (timeline[0]?.ref ?? null),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取配置历史失败')
    }
  }

  useEffect(() => {
    void load()
  }, [instance.id])

  // What belonged to the row you just left goes as you leave it, so a slow
  // request cannot drop the previous commit's files under a new heading. Split
  // from the fetch below because 刷新 refetches without closing anything.
  useEffect(() => {
    setChanges(null)
    setDiff(null)
    setOpenFile(opening.current)
    opening.current = null
  }, [instance.id, selected])

  // The change list follows the selected row. 当前改动 has none to fetch: its
  // files come with the overview, which is the only thing that knows them.
  useEffect(() => {
    if (!selected || selected === PENDING_REF) return
    let live = true
    void api
      .configHistoryChanges(instance.id, selected)
      .then((next) => {
        if (!live) return
        const list = next ?? []
        setChanges(list)
        if (pendingFactory.current) {
          pendingFactory.current = false
          setOpenFile(list.length > 0 ? { path: list[0].path, againstCurrent: true } : null)
        }
      })
      .catch((err) => {
        if (live) setError(err instanceof Error ? err.message : '读取变更列表失败')
      })
    return () => {
      live = false
    }
  }, [instance.id, selected, reloads])

  const timeline = data?.timeline ?? []
  const pending = data?.pending ?? []
  const newest = timeline[0] ?? null
  const showingPending = selected === PENDING_REF
  // 当前改动 is the disk, so every one of its diffs is a comparison against the
  // newest snapshot — there is no commit of its own to diff.
  const diffRef = showingPending ? (newest?.ref ?? null) : selected

  useEffect(() => {
    setDiff(null)
    if (!diffRef || !openFile) return
    let live = true
    void api
      .configHistoryDiff(instance.id, diffRef, openFile.path, openFile.againstCurrent)
      .then((next) => {
        if (live) setDiff(next)
      })
      .catch((err) => {
        if (live) setError(err instanceof Error ? err.message : '读取 diff 失败')
      })
    return () => {
      live = false
    }
  }, [instance.id, diffRef, openFile, reloads])

  const rows = useMemo(() => timeline.filter((row) => matches(row, filter)), [timeline, filter])
  const current = timeline.find((row) => row.ref === selected) ?? null
  // The file list on the right, whichever row is selected.
  const files = showingPending ? pending : changes
  const pendingStats = pending.reduce(
    (sum, file) => ({
      insertions: sum.insertions + file.insertions,
      deletions: sum.deletions + file.deletions,
    }),
    { insertions: 0, deletions: 0 },
  )

  /** Selects a row and opens one of its files, whether or not the selection
   *  actually moves — see `opening`. */
  const openAt = (ref: string, file: OpenFile) => {
    if (selected === ref) {
      setOpenFile(file)
    } else {
      opening.current = file
      setSelected(ref)
    }
  }

  const selectRow = (ref: string) => {
    // Picking a row by hand cancels a pending 与出厂对比, so its change list
    // cannot arrive later and open a file under a snapshot nobody asked for.
    pendingFactory.current = false
    setSelected(ref)
  }

  // 文件管理 asking about one file: show it where it can be compared against
  // what is on disk — in 当前改动 when the file is one of the unrecorded ones,
  // against the newest snapshot otherwise.
  useEffect(() => {
    // Waits for the overview: which row can answer for this file is a question
    // only the timeline and the pending list can settle. Answered once per
    // token, so a later 刷新 does not re-open what was already looked at.
    if (focus?.token === undefined || !data || handledFocus.current === focus.token) return
    handledFocus.current = focus.token
    const path = focus.path
    const unrecorded = (data.pending ?? []).some((entry) => entry.path === path)
    const ref = unrecorded ? PENDING_REF : (data.timeline ?? [])[0]?.ref
    if (!ref) {
      setError('这个实例还没有任何快照，先打一个再来比较。')
      return
    }
    openAt(ref, { path, againstCurrent: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focus?.token, data])

  const refresh = async () => {
    setBusy(true)
    try {
      await load()
      setReloads((count) => count + 1)
    } finally {
      setBusy(false)
    }
  }

  const takeSnapshot = async (message: string) => {
    setBusy(true)
    try {
      const result = await api.configHistorySnapshot(instance.id, message)
      toast(result.skipped ? (result.reason ?? '没有变更，跳过') : '已记录快照')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '打快照失败')
    } finally {
      setBusy(false)
      setSnapshotting(false)
    }
  }

  /** Opens the restore preview. Nothing is written until the dialog is confirmed. */
  const previewRestore = async (ref: string, path?: string) => {
    setBusy(true)
    try {
      setPlan(await api.configHistoryRestorePlan(instance.id, ref, path))
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取还原预览失败')
    } finally {
      setBusy(false)
    }
  }

  const runRestore = async (target: RestorePlan, confirmed: boolean) => {
    setBusy(true)
    try {
      const result = await api.configHistoryRestore(
        instance.id,
        target.ref,
        target.path ?? '',
        confirmed,
      )
      setPlan(null)
      toast(result.result.skipped ? (result.result.reason ?? '无需还原') : '已还原，并记录为新的快照')
      await load()
    } catch (err) {
      // A version mismatch comes back as a 409 with the plan attached: it is
      // the second question rather than a failure, so the dialog stays up and
      // grows a confirmation instead of turning into an error.
      if (err instanceof ApiError && err.status === 409) {
        setPlan((prev) => prev && { ...prev, mismatch: prev.mismatch ?? { plugins: [] } })
        setError(err.message)
      } else {
        setError(err instanceof Error ? err.message : '还原失败')
      }
    } finally {
      setBusy(false)
    }
  }

  const compact = async () => {
    const confirmed = await ask({
      title: '压缩配置历史？',
      lead: '保留最近 100 个快照、每个月的第一个，以及所有插件事务快照，其余的从历史里移除。',
      detail:
        '这会重建仓库，被保留的快照内容和时间都不变。事务快照永不裁剪 —— 插件回滚依赖它们。',
      confirmLabel: '压缩历史',
    })
    if (!confirmed) return

    setBusy(true)
    try {
      const result = await api.configHistoryCompact(instance.id, 100)
      toast(
        `已压缩：${result.before} → ${result.after} 个快照，` +
          `${formatBytes(result.bytesBefore)} → ${formatBytes(result.bytesAfter)}`,
      )
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '压缩失败')
    } finally {
      setBusy(false)
    }
  }

  const setPathRule = async (path: string, rule: 'allow' | 'exclude') => {
    if (!data) return
    setBusy(true)
    try {
      const list = rule === 'allow' ? (data.settings.allow ?? []) : (data.settings.exclude ?? [])
      await api.configHistorySettings(instance.id, { [rule]: [...list, path] })
      toast(rule === 'allow' ? '已确认收录这个文件' : '已永久排除这个文件')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存设置失败')
    } finally {
      setBusy(false)
    }
  }

  if (!data) {
    if (error) return <div className="alert alert--error">{error}</div>
    return (
      <SkeletonScreen label="正在读取配置历史…">
        <SkeletonPanel>
          <Skeleton w="40%" h={14} />
          <Skeleton w="72%" h={12} />
          <Skeleton w="60%" h={12} />
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  if (!data.available) {
    return (
      <div className="stack">
        <div className="panel panel--warn">
          <h2 className="panel__title">这个实例没有启用配置历史</h2>
          <p className="chist__note">{data.reason ?? '面板没有启用这个模块。'}</p>
        </div>
      </div>
    )
  }

  const oversized = data.coverage.oversized ?? []
  const initial = timeline.length > 0 ? timeline[timeline.length - 1] : null

  /** 与出厂对比 selects the oldest snapshot and opens its first file against
   *  what is on disk now. */
  const compareWithFactory = () => {
    if (!initial) return
    // The oldest snapshot is usually a lifecycle row, which 重要 hides. Leaving
    // the filter alone would light up a detail pane whose row is nowhere in the
    // timeline beside it.
    if (!matches(initial, filter)) setFilter('all')
    if (selected === initial.ref) {
      // Already there, so the change list is loaded and there is nothing to
      // wait for.
      const first = changes?.[0]
      setOpenFile(first ? { path: first.path, againstCurrent: true } : null)
      return
    }
    pendingFactory.current = true
    setSelected(initial.ref)
  }

  return (
    <div className="stack">
      <header className="chart-head">
        <h2 className="panel__title">
          配置历史 <span className="muted">{timeline.length}</span>
        </h2>
        <div className="chart-head__actions">
          <button
            className="btn"
            onClick={() => void refresh()}
            disabled={busy}
            title="重新读取时间线、变更列表和当前打开的 diff"
          >
            刷新
          </button>
          <button className="btn btn--primary" onClick={() => setSnapshotting(true)} disabled={busy}>
            打快照
          </button>
          <button
            className="btn"
            onClick={compareWithFactory}
            disabled={busy || !initial}
            title="与最早记录的出厂状态比较"
          >
            与出厂对比
          </button>
          <button
            className="btn btn--danger"
            onClick={() => current && void previewRestore(current.ref)}
            disabled={busy || !current || data.running}
            title={data.running ? '整树还原要求服务器处于停止状态' : '把整棵配置树切回这个快照'}
          >
            整树还原（高级）
          </button>
        </div>
      </header>

      <p className="chart-note">
        这台服务器配置文件的时间线：谁改了什么，什么时候改的，改回去。
        <strong> 这不是备份</strong> —— 世界、玩家数据和数据库都不在收录范围内。
      </p>

      {error && <div className="alert alert--error">{error}</div>}

      {oversized.length > 0 && (
        <div className="panel panel--warn">
          <h2 className="panel__title">{oversized.length} 个文件超过单文件上限，快照已中止</h2>
          <p className="chist__note">
            上限是 {formatBytes(data.settings.limits.fileBytes)}。这通常说明收录规则碰到了一个
            数据文件；也可能是正当的大配置（例如 WorldGuard 的 regions.yml）。逐个决定后才会继续记录。
          </p>
          <ul className="chist__gate">
            {oversized.map((file) => (
              <li key={file.path}>
                <code>{file.path}</code>
                <span className="chist__gate-size">{formatBytes(file.size)}</span>
                <button className="btn btn--row" onClick={() => void setPathRule(file.path, 'allow')} disabled={busy}>
                  确认收录
                </button>
                <button
                  className="btn btn--row"
                  onClick={() => void setPathRule(file.path, 'exclude')}
                  disabled={busy}
                >
                  永久排除
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {pending.length > 0 && (
        <div className="alert alert--warn">
          有 {pending.length} 个文件的改动还没有记录。下次启服前会自动打一次快照，
          也可以现在就 <button className="link" onClick={() => setSnapshotting(true)}>打快照</button>
          {timeline.length > 0 && (
            <>
              ，或者先{' '}
              <button className="link" onClick={() => selectRow(PENDING_REF)}>
                看看改了什么
              </button>
            </>
          )}
          。
        </div>
      )}

      {timeline.length === 0 ? (
        <div className="panel">
          <h2 className="panel__title">还没有任何快照</h2>
          <p className="chist__note">
            按现在的收录规则会记录 {data.coverage.files} 个文件、共 {formatBytes(data.coverage.bytes)}。
            {(data.coverage.worlds?.length ?? 0) > 0 && (
              <>
                {' '}
                识别为世界目录并已跳过：{data.coverage.worlds?.join('、')}。
              </>
            )}
          </p>
          <p className="chist__note">启服、停服、插件升级和在面板里保存配置时都会自动记录。</p>
        </div>
      ) : (
        <div className="chist">
          <section className="chist__timeline panel" aria-label="快照时间线">
            <div className="chist__filters" role="group" aria-label="筛选快照">
              {FILTERS.map((entry) => (
                <button
                  key={entry.id}
                  className={`chip${filter === entry.id ? ' chip--active' : ''}`}
                  onClick={() => setFilter(entry.id)}
                  aria-pressed={filter === entry.id}
                >
                  {entry.label}
                </button>
              ))}
            </div>

            <ol className="chist__rows">
              {/* 当前改动 sits above the snapshots and outside the filters: it
                  is not one of them, it is the directory as it stands right
                  now, and it is the row somebody arriving mid-incident wants
                  first. Only when there is something to show — an empty
                  "nothing changed" row would be noise on every other visit. */}
              {pending.length > 0 && (
                <li>
                  <button
                    className={`chist__row${showingPending ? ' chist__row--active' : ''}`}
                    onClick={() => selectRow(PENDING_REF)}
                    aria-current={showingPending ? 'true' : undefined}
                  >
                    <span className="chist__badge chist__badge--pending">未记录</span>
                    <span className="chist__row-main">
                      <span className="chist__row-title">当前改动</span>
                      <span className="chist__row-meta">
                        <span>还在磁盘上</span>
                        <span>
                          {pending.length} 个文件
                          {pendingStats.insertions > 0 && (
                            <span className="chist__add"> +{pendingStats.insertions}</span>
                          )}
                          {pendingStats.deletions > 0 && (
                            <span className="chist__del"> −{pendingStats.deletions}</span>
                          )}
                        </span>
                      </span>
                    </span>
                  </button>
                </li>
              )}
              {rows.map((row) => (
                <li key={row.ref}>
                  <button
                    className={`chist__row${row.ref === selected ? ' chist__row--active' : ''}`}
                    onClick={() => selectRow(row.ref)}
                    aria-current={row.ref === selected ? 'true' : undefined}
                  >
                    <span className={`chist__badge chist__badge--${row.trigger}`}>
                      {TRIGGER_LABELS[row.trigger] ?? row.trigger}
                    </span>
                    <span className="chist__row-main">
                      <span className="chist__row-title">{row.message}</span>
                      <span className="chist__row-meta">
                        <span>{formatDate(row.at)}</span>
                        <span>
                          {row.stats.files} 个文件
                          {row.stats.insertions > 0 && (
                            <span className="chist__add"> +{row.stats.insertions}</span>
                          )}
                          {row.stats.deletions > 0 && (
                            <span className="chist__del"> −{row.stats.deletions}</span>
                          )}
                        </span>
                        <span className="chist__author">{row.author}</span>
                      </span>
                      {row.running && (
                        <span className="chist__warn">运行中快照，文件可能处于半写状态</span>
                      )}
                    </span>
                  </button>
                </li>
              ))}
              {rows.length === 0 && (
                <li className="chist__none">这个筛选下没有快照。切到「全部」看看自动快照。</li>
              )}
            </ol>
          </section>

          <section className="chist__detail panel" aria-label="快照详情">
            {!showingPending && current === null ? (
              <p className="chist__note">选一个快照。</p>
            ) : (
              <>
                <header className="chist__detail-head">
                  <h2 className="panel__title">{showingPending ? '当前改动' : current?.message}</h2>
                  <p className="chist__note">
                    {showingPending ? (
                      <>
                        磁盘上比最近一次快照
                        {newest && <> 「{newest.message}」</>}多出来的改动，还没有被记录。
                      </>
                    ) : (
                      current && (
                        <>
                          {formatDate(current.at)} · {current.author} · <code>{current.short}</code>
                          {current.core && <> · 核心 {current.core}</>}
                        </>
                      )
                    )}
                  </p>
                </header>

                {files === null ? (
                  <Skeleton w="60%" h={12} />
                ) : files.length === 0 ? (
                  <p className="chist__note">
                    {showingPending ? '磁盘和最近一次快照一致。' : '这个快照没有文件变更。'}
                  </p>
                ) : (
                  <ul className="chist__files">
                    {files.map((change) => (
                      <li key={change.path}>
                        <div className="chist__file">
                          <button
                            className="chist__file-open"
                            // 当前改动 only ever compares against the disk: the
                            // file *is* the disk, so a diff "inside the commit"
                            // does not exist for it.
                            onClick={() =>
                              setOpenFile((prev) =>
                                prev?.path === change.path &&
                                prev.againstCurrent === showingPending
                                  ? null
                                  : { path: change.path, againstCurrent: showingPending },
                              )
                            }
                            aria-expanded={openFile?.path === change.path}
                          >
                            <span className={`chist__status chist__status--${change.status}`}>
                              {STATUS_LABELS[change.status]}
                            </span>
                            <span className="chist__file-path" title={change.path}>
                              {change.path}
                            </span>
                            <span className="chist__file-stat">
                              {change.binary ? (
                                '二进制'
                              ) : (
                                <>
                                  <span className="chist__add">+{change.insertions}</span>{' '}
                                  <span className="chist__del">−{change.deletions}</span>
                                </>
                              )}
                            </span>
                          </button>
                          <div className="chist__file-actions">
                            {!showingPending && (
                              <button
                                className="btn btn--row"
                                onClick={() =>
                                  setOpenFile({ path: change.path, againstCurrent: true })
                                }
                                disabled={change.status === 'deleted'}
                              >
                                与当前对比
                              </button>
                            )}
                            {onOpenInFiles && (
                              <button
                                className="btn btn--row"
                                onClick={() => onOpenInFiles(change.path)}
                                disabled={change.status === 'deleted'}
                                title="在文件管理里打开这个文件"
                              >
                                在文件里打开
                              </button>
                            )}
                            <button
                              className="btn btn--row"
                              // Undoing an unrecorded change is a restore of the
                              // newest snapshot's copy — the same operation, and
                              // it goes through the same preview. What each side
                              // cannot restore differs: a snapshot that deleted
                              // a file holds no copy of it, and a file that
                              // snapshot never had cannot come back from it.
                              onClick={() =>
                                diffRef && void previewRestore(diffRef, change.path)
                              }
                              disabled={
                                busy ||
                                !diffRef ||
                                (showingPending
                                  ? change.status === 'added'
                                  : change.status === 'deleted')
                              }
                            >
                              {showingPending ? '撤销这次改动' : '还原此版本'}
                            </button>
                          </div>
                        </div>

                        {openFile?.path === change.path && (
                          <DiffView
                            diff={diff}
                            againstCurrent={openFile.againstCurrent}
                          />
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </>
            )}
          </section>
        </div>
      )}

      <footer className="chist__footer">
        <p className="chist__stats">
          {data.stats.commits} 次提交 · 收录 {data.stats.files} 个文件 · 仓库{' '}
          {formatBytes(data.stats.repoBytes)}
          {data.stats.compactedAt ? ` · 上次整理 ${formatSince(data.stats.compactedAt)}` : ' · 从未整理'}
        </p>
        <button className="btn" onClick={() => void compact()} disabled={busy || data.stats.commits === 0}>
          压缩历史
        </button>
        <p className="chist__disclaimer">
          配置历史不是备份，世界与玩家数据不在其中。全量备份策略要单独准备。
        </p>
      </footer>

      {snapshotting && (
        <SnapshotDialog
          running={data.running}
          busy={busy}
          onSubmit={takeSnapshot}
          onCancel={() => setSnapshotting(false)}
        />
      )}
      {plan && (
        <RestoreDialog
          plan={plan}
          busy={busy}
          onConfirm={(confirmed) => void runRestore(plan, confirmed)}
          onCancel={() => {
            setPlan(null)
            setError(null)
          }}
        />
      )}
    </div>
  )
}

type TimelineFilter = 'all' | 'notable' | 'transaction' | 'user' | 'restore'

/** A file opened in the detail pane, and which side it is compared against. */
interface OpenFile {
  path: string
  againstCurrent: boolean
}

/** What 文件管理 hands over when it asks about one file. */
export interface ConfigHistoryFocus {
  path: string
  token: number
}

/** The synthetic row for what is on disk but not yet recorded. Not a ref any
 *  repository would answer to, and deliberately shaped so it cannot be mistaken
 *  for one if it ever reaches a request. */
const PENDING_REF = '@pending'

/** 全部 is the default: a timeline that hides rows by default is a timeline
 *  somebody has to be told about before they can trust it. 重要 is still one
 *  click away, and it is worth having — start and stop each leave a lifecycle
 *  row, so a fleet that restarts nightly buries the real edits within a week. */
const FILTERS: { id: TimelineFilter; label: string }[] = [
  { id: 'all', label: '全部' },
  { id: 'notable', label: '重要' },
  { id: 'transaction', label: '事务' },
  { id: 'user', label: '用户' },
  { id: 'restore', label: '还原' },
]

const TRIGGER_LABELS: Record<SnapshotTrigger, string> = {
  lifecycle: '自动',
  transaction: '事务',
  user: '用户',
  restore: '还原',
}

const STATUS_LABELS = { added: '新增', modified: '修改', deleted: '删除' } as const

function matches(row: ConfigSnapshot, filter: TimelineFilter): boolean {
  if (filter === 'all') return true
  if (filter === 'notable') return row.trigger !== 'lifecycle'
  return row.trigger === filter
}

/**
 * One file's diff.
 *
 * Credentials arrive marked, and are drawn masked until clicked. The real value
 * is in the payload — the same session can open the file itself, so withholding
 * it would buy nothing — but it should not be on screen for anybody who happens
 * to be looking at the operator's monitor.
 */
function DiffView({ diff, againstCurrent }: { diff: ConfigFileDiff | null; againstCurrent: boolean }) {
  const [shown, setShown] = useState<Set<string>>(new Set())

  if (diff === null) {
    return (
      <div className="chist__diff">
        <Skeleton w="70%" h={12} />
      </div>
    )
  }
  if (diff.binary) {
    return <p className="chist__note">这是二进制内容，没有可读的逐行差异。</p>
  }
  // A diff with no lines arrives as null, not as an empty array: the differ
  // leaves the slice nil for equal sides and Go marshals that as null. Reading
  // .length off it threw, and one thrown render takes the whole panel down —
  // there is no error boundary above this tab.
  const hunks = diff.hunks ?? []
  if (hunks.length === 0) {
    return <p className="chist__note">{emptyDiffNote(diff, againstCurrent)}</p>
  }

  const key = (hunk: number, index: number) => `${hunk}:${index}`
  const reveal = (id: string) => setShown((prev) => new Set(prev).add(id))

  return (
    <div className="chist__diff">
      {againstCurrent && <p className="chist__diff-head">左侧为快照内容，右侧为磁盘上的当前内容。</p>}
      {diff.truncated && (
        <p className="chist__diff-head">改动过大，按整文件替换显示。</p>
      )}
      {hunks.map((hunk, h) => (
        <table className="chist__hunk" key={h}>
          <tbody>
            {(hunk.lines ?? []).map((line, index) => (
              <DiffRow
                key={key(h, index)}
                line={line}
                revealed={shown.has(key(h, index))}
                onReveal={() => reveal(key(h, index))}
              />
            ))}
          </tbody>
        </table>
      ))}
    </div>
  )
}

function DiffRow({
  line,
  revealed,
  onReveal,
}: {
  line: DiffLine
  revealed: boolean
  onReveal: () => void
}) {
  const hidden = line.sensitive && !revealed
  return (
    <tr className={`chist__line chist__line--${line.kind}`}>
      <td className="chist__gutter">{line.oldLine > 0 ? line.oldLine : ''}</td>
      <td className="chist__gutter">{line.newLine > 0 ? line.newLine : ''}</td>
      <td className="chist__sign">{SIGNS[line.kind]}</td>
      <td className="chist__code">
        {hidden ? (
          <button className="chist__secret" onClick={onReveal} title="点击显示">
            {line.masked}
          </button>
        ) : (
          <code>{line.text === '' ? ' ' : line.text}</code>
        )}
      </td>
    </tr>
  )
}

const SIGNS = { context: ' ', add: '+', delete: '−' } as const

/** Why a diff has nothing to show. "没有改动" would be wrong for a file that was
 *  genuinely added — it is just empty, and saying so is the difference between
 *  "the panel lost my file" and "the plugin wrote a zero-byte config". */
function emptyDiffNote(diff: ConfigFileDiff, againstCurrent: boolean): string {
  if (againstCurrent) return '磁盘上的内容与这个版本一致。'
  if (diff.status === 'added') return '这是一个空文件，没有内容可以显示。'
  if (diff.status === 'deleted') return '删除的是一个空文件。'
  return '这个快照没有改动这个文件。'
}

function SnapshotDialog({
  running,
  busy,
  onSubmit,
  onCancel,
}: {
  running: boolean
  busy: boolean
  onSubmit: (message: string) => void
  onCancel: () => void
}) {
  const [message, setMessage] = useState('')

  return (
    <Modal onClose={onCancel} busy={busy}>
      <form
        className="modal__card"
        onSubmit={(event) => {
          event.preventDefault()
          onSubmit(message)
        }}
      >
        <h2 className="modal__title">打一个快照</h2>
        <p className="modal__lead">
          记录当前的配置文件。没有变更就不会产生提交。
        </p>

        <label className="field">
          <span>说明</span>
          <input
            value={message}
            onChange={(event) => setMessage(event.target.value)}
            placeholder="例如：调高视距之前"
            autoFocus
          />
          <small>留空会记为「手动快照」。</small>
        </label>

        {running && (
          <div className="alert alert--warn">
            服务器正在运行，插件可能正在写自己的配置文件，这个快照可能记到半写状态。
            这条会在时间线上标注出来。
          </div>
        )}

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button className="btn btn--primary" type="submit" disabled={busy}>
            {busy ? '记录中…' : '打快照'}
          </button>
        </div>
      </form>
    </Modal>
  )
}

/**
 * The restore confirmation.
 *
 * It shows the full change list before anything is written, and it asks twice
 * when the plugin or core versions have moved: config from 5.5.70 under
 * 5.5.71's jar can stop a server booting, and nothing about clicking 还原
 * suggests that it might.
 */
function RestoreDialog({
  plan,
  busy,
  onConfirm,
  onCancel,
}: {
  plan: RestorePlan
  busy: boolean
  onConfirm: (confirmed: boolean) => void
  onCancel: () => void
}) {
  const [acknowledged, setAcknowledged] = useState(false)
  const changes = plan.changes ?? []
  const blocked = Boolean(plan.blockedBy)
  const needsAck = Boolean(plan.mismatch) && !acknowledged

  return (
    <Modal onClose={onCancel} busy={busy} label="还原确认">
      <div className="modal__card">
        <h2 className="modal__title">
          {plan.whole ? '整树还原' : `还原 ${plan.path}`}
        </h2>
        <p className="modal__lead">
          回到 {formatDate(plan.at)} 的「{plan.message}」。还原会写成一个新的提交，
          历史只会往前增加 —— 还原之后发现更糟，还能再还原回来。
        </p>

        {plan.blockedBy && <div className="alert alert--error">{plan.blockedBy}</div>}
        {plan.warning && <div className="alert alert--warn">{plan.warning}</div>}

        {plan.mismatch && (
          <div className="alert alert--warn">
            <strong>插件 / 核心版本和当时不一致。</strong>
            {plan.mismatch.coreThen && (
              <div>
                核心：当时 {plan.mismatch.coreThen}，现在 {plan.mismatch.coreNow}
              </div>
            )}
            {(plan.mismatch.plugins ?? []).map((entry) => (
              <div key={entry}>{entry}</div>
            ))}
            <label className="field field--inline">
              <input
                type="checkbox"
                checked={acknowledged}
                onChange={(event) => setAcknowledged(event.target.checked)}
              />
              <span>我知道旧配置配新版本可能起不来</span>
            </label>
          </div>
        )}

        <div className="chist__plan">
          <h3>会写回 {changes.length} 个文件</h3>
          <ul>
            {changes.map((change) => (
              <li key={change.path}>
                <code>{change.path}</code>
                <span className="chist__add">+{change.insertions}</span>
                <span className="chist__del">−{change.deletions}</span>
              </li>
            ))}
            {changes.length === 0 && <li>没有需要写回的内容。</li>}
          </ul>

          {(plan.removals?.length ?? 0) > 0 && (
            <>
              <h3>会删除 {plan.removals?.length} 个此后新增的文件</h3>
              <ul>
                {plan.removals?.map((path) => (
                  <li key={path}>
                    <code>{path}</code>
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>

        <div className="modal__actions">
          <button className="btn" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button
            className="btn btn--danger"
            onClick={() => onConfirm(acknowledged)}
            disabled={busy || blocked || needsAck || changes.length + (plan.removals?.length ?? 0) === 0}
          >
            {busy ? '还原中…' : '还原'}
          </button>
        </div>
      </div>
    </Modal>
  )
}
