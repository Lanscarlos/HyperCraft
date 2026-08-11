import { useEffect, useMemo, useState } from 'react'

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
export function ConfigHistory({ instance }: { instance: InstanceStatus }) {
  const [data, setData] = useState<ConfigHistoryOverview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<TimelineFilter>('notable')
  const [selected, setSelected] = useState<string | null>(null)
  const [changes, setChanges] = useState<ConfigFileChange[] | null>(null)
  const [openFile, setOpenFile] = useState<{ path: string; againstCurrent: boolean } | null>(null)
  const [diff, setDiff] = useState<ConfigFileDiff | null>(null)
  const [busy, setBusy] = useState(false)
  const [snapshotting, setSnapshotting] = useState(false)
  const [plan, setPlan] = useState<RestorePlan | null>(null)

  const load = async () => {
    try {
      const next = await api.configHistory(instance.id)
      setData(next)
      setError(null)
      // Keep the selection across a reload where it still exists; otherwise
      // fall to the newest row, which is what somebody opening the tab wants.
      const timeline = next.timeline ?? []
      setSelected((current) =>
        current && timeline.some((row) => row.ref === current) ? current : (timeline[0]?.ref ?? null),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取配置历史失败')
    }
  }

  useEffect(() => {
    void load()
  }, [instance.id])

  // The change list follows the selected row. Cleared first so a slow request
  // cannot leave the previous commit's files sitting under a new heading.
  useEffect(() => {
    setChanges(null)
    setOpenFile(null)
    setDiff(null)
    if (!selected) return
    let live = true
    void api
      .configHistoryChanges(instance.id, selected)
      .then((next) => {
        if (live) setChanges(next ?? [])
      })
      .catch((err) => {
        if (live) setError(err instanceof Error ? err.message : '读取变更列表失败')
      })
    return () => {
      live = false
    }
  }, [instance.id, selected])

  useEffect(() => {
    setDiff(null)
    if (!selected || !openFile) return
    let live = true
    void api
      .configHistoryDiff(instance.id, selected, openFile.path, openFile.againstCurrent)
      .then((next) => {
        if (live) setDiff(next)
      })
      .catch((err) => {
        if (live) setError(err instanceof Error ? err.message : '读取 diff 失败')
      })
    return () => {
      live = false
    }
  }, [instance.id, selected, openFile])

  const timeline = data?.timeline ?? []
  const rows = useMemo(() => timeline.filter((row) => matches(row, filter)), [timeline, filter])
  const current = timeline.find((row) => row.ref === selected) ?? null

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
  const pending = data.pending ?? []
  const initial = timeline.length > 0 ? timeline[timeline.length - 1] : null

  return (
    <div className="stack">
      <header className="chart-head">
        <h2 className="panel__title">
          配置历史 <span className="muted">{timeline.length}</span>
        </h2>
        <div className="chart-head__actions">
          <button className="btn btn--primary" onClick={() => setSnapshotting(true)} disabled={busy}>
            打快照
          </button>
          <button
            className="btn"
            onClick={() => initial && openFactory(setSelected, setOpenFile, initial.ref, changes)}
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
          也可以现在就 <button className="link" onClick={() => setSnapshotting(true)}>打快照</button>。
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
              {rows.map((row) => (
                <li key={row.ref}>
                  <button
                    className={`chist__row${row.ref === selected ? ' chist__row--active' : ''}`}
                    onClick={() => setSelected(row.ref)}
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
            {current === null ? (
              <p className="chist__note">选一个快照。</p>
            ) : (
              <>
                <header className="chist__detail-head">
                  <h2 className="panel__title">{current.message}</h2>
                  <p className="chist__note">
                    {formatDate(current.at)} · {current.author} · <code>{current.short}</code>
                    {current.core && <> · 核心 {current.core}</>}
                  </p>
                </header>

                {changes === null ? (
                  <Skeleton w="60%" h={12} />
                ) : changes.length === 0 ? (
                  <p className="chist__note">这个快照没有文件变更。</p>
                ) : (
                  <ul className="chist__files">
                    {changes.map((change) => (
                      <li key={change.path}>
                        <div className="chist__file">
                          <button
                            className="chist__file-open"
                            onClick={() =>
                              setOpenFile((prev) =>
                                prev?.path === change.path && !prev.againstCurrent
                                  ? null
                                  : { path: change.path, againstCurrent: false },
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
                            <button
                              className="btn btn--row"
                              onClick={() =>
                                setOpenFile({ path: change.path, againstCurrent: true })
                              }
                              disabled={change.status === 'deleted'}
                            >
                              与当前对比
                            </button>
                            <button
                              className="btn btn--row"
                              onClick={() => void previewRestore(current.ref, change.path)}
                              disabled={busy || change.status === 'deleted'}
                            >
                              还原此版本
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

/** 与出厂对比 selects the oldest row and opens its first changed file. */
function openFactory(
  setSelected: (ref: string) => void,
  setOpenFile: (file: { path: string; againstCurrent: boolean } | null) => void,
  ref: string,
  changes: ConfigFileChange[] | null,
): void {
  setSelected(ref)
  setOpenFile(changes && changes.length > 0 ? { path: changes[0].path, againstCurrent: true } : null)
}

type TimelineFilter = 'notable' | 'all' | 'transaction' | 'user' | 'restore'

/** 重要 is the default and hides the lifecycle rows. Two of those land on every
 *  start and stop, so a fleet that restarts nightly would bury every real edit
 *  under them within a week. */
const FILTERS: { id: TimelineFilter; label: string }[] = [
  { id: 'notable', label: '重要' },
  { id: 'all', label: '全部' },
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
  if (diff.hunks.length === 0) {
    return (
      <p className="chist__note">
        {againstCurrent ? '磁盘上的内容与这个版本一致。' : '这个快照没有改动这个文件。'}
      </p>
    )
  }

  const key = (hunk: number, index: number) => `${hunk}:${index}`
  const reveal = (id: string) => setShown((prev) => new Set(prev).add(id))

  return (
    <div className="chist__diff">
      {againstCurrent && <p className="chist__diff-head">左侧为快照内容，右侧为磁盘上的当前内容。</p>}
      {diff.truncated && (
        <p className="chist__diff-head">改动过大，按整文件替换显示。</p>
      )}
      {diff.hunks.map((hunk, h) => (
        <table className="chist__hunk" key={h}>
          <tbody>
            {hunk.lines.map((line, index) => (
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
