import { useCallback, useMemo, useRef, useState } from 'react'

import { api, schematicDownloadURL, uploadSchematics } from '../api'
import { bareName, blockColor } from '../blockcolors'
import { ask } from '../confirm'
import { formatBytes, formatSince } from '../format'
import { toast } from '../toast'
import type {
  SchematicEntry,
  SchematicImportResult,
  SchematicTarget,
} from '../types'
import type { SchematicController } from '../useSchematics'
import { Modal } from './Modal'
import { Page } from './Page'
import { SchematicDialog } from './SchematicPreview'
import { Select } from './Select'
import { Skeleton, SkeletonPanel, SkeletonScreen } from './Skeleton'

/**
 * 建筑列表: the panel-wide shelf of .schem files.
 *
 * A build used to live in exactly one place — one server's schematics folder,
 * reachable through that server's file manager and invisible from anywhere
 * else. That is the right home for a build one server uses and the wrong one
 * for how builds are actually kept: the spawn gets pasted into next season's
 * world, the shop stalls go onto every survival server, and the warehouse of
 * downloads from the last three years belongs to the operator rather than to
 * whichever server happened to receive it. So it is a shelf, like plugins and
 * cores: held once, described once, copied into whichever server needs it.
 *
 * The tiles are a grid rather than the rows the other shelves use, and that is
 * the one place this page deliberately differs from them. A plugin row is read
 * for its name and its version; a build is read for its shape — 120×40×90, mostly
 * spruce, thirty thousand blocks — and those are numbers you compare across a
 * shelf rather than down a list.
 */
export function SchematicLibraryPage({
  schematics,
  openId,
  onOpen,
}: {
  schematics: SchematicController
  /** Which build's preview is open, from the URL, so a link to one opens it. */
  openId?: string
  onOpen: (id: string | null) => void
}) {
  const [query, setQuery] = useState('')
  const [editing, setEditing] = useState<SchematicEntry | null>(null)
  const [installing, setInstalling] = useState<SchematicEntry | null>(null)
  const [uploads, setUploads] = useState<SchematicImportResult[] | null>(null)
  const [progress, setProgress] = useState<number | null>(null)
  const picker = useRef<HTMLInputElement | null>(null)

  const { entries, targets, library, loading, busy, error } = schematics
  const opened = openId ? (entries.find((entry) => entry.id === openId) ?? null) : null

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (needle === '') return entries
    return entries.filter((entry) => matches(entry, needle))
  }, [entries, query])

  const send = async (files: File[]) => {
    if (files.length === 0) return
    setProgress(0)
    setUploads(null)
    try {
      const { results } = await uploadSchematics(files, setProgress)
      setUploads(results)
      const kept = results.filter((result) => result.entry).length
      if (kept > 0) toast(`已入库 ${kept} 个建筑`)
      await schematics.refresh()
    } catch (err) {
      toast(err instanceof Error ? err.message : '上传失败')
    } finally {
      setProgress(null)
    }
  }

  const remove = async (entry: SchematicEntry) => {
    const ok = await ask({
      title: '从建筑库删除这个建筑？',
      lead: `${entry.name}（${entry.fileName}，${formatBytes(entry.size)}）`,
      detail:
        '已经装到实例里的副本不受影响，它们是那台服自己的文件。库里的这一份删掉就没有了。',
      confirmLabel: '删除',
      danger: true,
    })
    if (!ok) return
    await schematics.remove(entry.id)
  }

  const rescan = async () => {
    const result = await schematics.rescan()
    if (!result) return
    toast(
      result.added === 0 && result.dropped === 0
        ? '目录里没有新的建筑'
        : `扫到 ${result.added} 个新建筑，清掉 ${result.dropped} 条失效记录`,
    )
  }

  if (loading && library === null) {
    return (
      <SkeletonScreen label="正在读取建筑库…">
        <SkeletonPanel lines={2} />
        <SkeletonPanel title={false}>
          <Skeleton w="100%" h={120} />
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  return (
    <Page
      wide
      title="建筑库"
      lead="面板存着的 .schem 全在这里。入库时就已经解析过，尺寸、方块用量、存档版本都能直接读，装到哪台服也是从这里点。"
      aside={
        <p className="meta-chips">
          <span>{entries.length > 0 ? `${entries.length} 个建筑` : '建筑库还是空的'}</span>
          {entries.length > 0 && <span>共 {formatBytes(library?.totalSize ?? 0)}</span>}
          {library?.root && <span title={library.root}>存放于 {library.root}</span>}
        </p>
      }
    >
      {error && <div className="alert alert--error">{error}</div>}

      <section className="panel">
        <div className="chart-head">
          <h2 className="panel__title">建筑列表</h2>
          <p className="chart-head__meta">把 .schem 丢进建筑库目录，扫描一下也会出现在这里</p>
        </div>

        <div className="schemlib__bar">
          <input
            className="filters__search"
            type="search"
            value={query}
            placeholder="搜名称、文件名、标签、作者"
            aria-label="搜索建筑"
            onChange={(event) => setQuery(event.target.value)}
          />
          <div className="schemlib__bar-actions">
            <button
              className="btn btn--primary"
              onClick={() => picker.current?.click()}
              disabled={progress !== null}
            >
              {progress === null ? '上传建筑' : `上传中 ${Math.round(progress * 100)}%`}
            </button>
            <button className="btn" onClick={() => void rescan()} disabled={busy}>
              扫描目录
            </button>
          </div>
          <input
            ref={picker}
            type="file"
            accept=".schem,.schematic"
            multiple
            hidden
            onChange={(event) => {
              void send(Array.from(event.target.files ?? []))
              // Cleared so picking the same file twice still fires a change.
              event.target.value = ''
            }}
          />
        </div>

        {uploads && <UploadReport results={uploads} onDismiss={() => setUploads(null)} />}

        {entries.length === 0 ? (
          <div className="welcome__empty">
            <p>建筑库还是空的。</p>
            <p className="muted">
              上传几个 .schem，从「建筑市场」下载，或者在实例的文件管理器里预览一个 schematic
              再点「加入建筑库」。
            </p>
          </div>
        ) : shown.length === 0 ? (
          <p className="muted">没有匹配「{query}」的建筑。</p>
        ) : (
          <div className="schemlib__grid" role="list">
            {shown.map((entry) => (
              <SchematicCard
                key={entry.id}
                entry={entry}
                busy={busy}
                onPreview={() => onOpen(entry.id)}
                onInstall={() => setInstalling(entry)}
                onEdit={() => setEditing(entry)}
                onRemove={() => void remove(entry)}
              />
            ))}
          </div>
        )}
      </section>

      {opened && <LibraryPreview entry={opened} onClose={() => onOpen(null)} />}

      {editing && (
        <EditDialog
          entry={editing}
          onCancel={() => setEditing(null)}
          onSave={async (patch) => {
            await schematics.edit(editing.id, patch)
            setEditing(null)
          }}
        />
      )}

      {installing && (
        <InstallDialog
          entry={installing}
          targets={targets}
          onClose={() => setInstalling(null)}
        />
      )}
    </Page>
  )
}

function matches(entry: SchematicEntry, needle: string): boolean {
  const haystack = [
    entry.name,
    entry.fileName,
    entry.note ?? '',
    entry.facts.author ?? '',
    entry.facts.savedName ?? '',
    ...(entry.tags ?? []),
  ]
  return haystack.some((value) => value.toLowerCase().includes(needle))
}

/* ------------------------------------------------------------------ tiles */

const ORIGIN_LABELS: Record<string, string> = {
  upload: '上传',
  instance: '实例导入',
  market: '建筑市场',
  found: '自行放入',
}

export function SchematicCard({
  entry,
  busy,
  onPreview,
  onInstall,
  onEdit,
  onRemove,
}: {
  entry: SchematicEntry
  busy: boolean
  onPreview: () => void
  onInstall: () => void
  onEdit: () => void
  onRemove: () => void
}) {
  const { facts } = entry
  const size = `${facts.width} × ${facts.height} × ${facts.length}`

  return (
    <article className="schemcard" role="listitem">
      {/* The whole head is the way in: the question a shelf of builds raises is
          "which one is this", and the answer is the preview. */}
      <button className="schemcard__open" onClick={onPreview} title={`预览「${entry.name}」`}>
        <span className="schemcard__title">
          <strong>{entry.name}</strong>
          {entry.origin.kind !== 'upload' && (
            <span className="badge">{ORIGIN_LABELS[entry.origin.kind] ?? entry.origin.kind}</span>
          )}
        </span>
        <span className="schemcard__file">
          <code title={entry.fileName}>{entry.fileName}</code>
        </span>
        <BlockBar blocks={facts.top ?? []} total={facts.nonAir} />
      </button>

      {facts.unreadable ? (
        <p className="schemcard__warn" title={facts.unreadable}>
          这个文件面板读不了，尺寸和方块清单都没有。装到实例还是可以的。
        </p>
      ) : (
        <dl className="schemcard__facts">
          <div>
            <dt>尺寸</dt>
            <dd title={`${facts.volume.toLocaleString('zh-CN')} 格选区`}>{size}</dd>
          </div>
          <div>
            <dt>方块</dt>
            <dd>{facts.nonAir.toLocaleString('zh-CN')}</dd>
          </div>
          <div>
            <dt>种类</dt>
            <dd>{facts.kinds}</dd>
          </div>
        </dl>
      )}

      {entry.note && <p className="schemcard__note">{entry.note}</p>}

      {(entry.tags?.length ?? 0) > 0 && (
        <p className="schemcard__tags">
          {entry.tags?.map((tag) => (
            <span className="badge" key={tag}>
              {tag}
            </span>
          ))}
        </p>
      )}

      <p className="schemcard__meta">
        {formatBytes(entry.size)} · {formatSince(entry.addedAt)}
        {entry.facts.author && ` · ${entry.facts.author}`}
      </p>

      <footer className="schemcard__actions">
        <button className="btn btn--row" onClick={onInstall} disabled={busy}>
          安装到实例
        </button>
        <button className="link" onClick={onEdit} disabled={busy}>
          编辑
        </button>
        <a className="link" href={schematicDownloadURL(entry.id)} download>
          下载
        </a>
        <button className="link link--danger" onClick={onRemove} disabled={busy}>
          删除
        </button>
      </footer>
    </article>
  )
}

/**
 * What a build is made of, as one bar.
 *
 * The block list in the preview answers "have I got enough spruce"; this
 * answers the question a shelf raises, which is "what *is* this" — a band of
 * greens and browns is a garden, one of greys is a castle. The colours are the
 * same ones the preview renders with, so a tile and its preview never disagree.
 */
export function BlockBar({
  blocks,
  total,
}: {
  blocks: { name: string; count: number }[]
  total: number
}) {
  if (blocks.length === 0 || total <= 0) {
    return <span className="schemcard__bar schemcard__bar--empty" aria-hidden="true" />
  }
  const covered = blocks.reduce((sum, block) => sum + block.count, 0)
  return (
    <span
      className="schemcard__bar"
      role="img"
      aria-label={`主要方块：${blocks.map((block) => bareName(block.name)).join('、')}`}
    >
      {blocks.map((block) => (
        <span
          key={block.name}
          style={{
            background: blockColor(block.name),
            // Against what the top blocks cover rather than against the whole
            // build: a bar that stops two thirds of the way across because the
            // tail was not stored reads as a bug.
            width: `${(block.count / covered) * 100}%`,
          }}
          title={`${bareName(block.name)} ${block.count.toLocaleString('zh-CN')}`}
        />
      ))}
    </span>
  )
}

function UploadReport({
  results,
  onDismiss,
}: {
  results: SchematicImportResult[]
  onDismiss: () => void
}) {
  const failed = results.filter((result) => result.error)
  if (failed.length === 0) {
    return (
      <div className="alert alert--ok">
        已入库 {results.length} 个建筑。
        <button className="link" onClick={onDismiss}>
          知道了
        </button>
      </div>
    )
  }
  return (
    <div className="alert alert--warn">
      <p>
        {results.length - failed.length} 个入库，{failed.length} 个没成：
      </p>
      <ul className="schemlib__failures">
        {failed.map((result) => (
          <li key={result.fileName}>
            <code>{result.fileName}</code> — {result.error}
          </li>
        ))}
      </ul>
      <button className="link" onClick={onDismiss}>
        知道了
      </button>
    </div>
  )
}

/* --------------------------------------------------------------- dialogs */

function LibraryPreview({ entry, onClose }: { entry: SchematicEntry; onClose: () => void }) {
  // Stable across renders: the dialog re-fetches whenever this identity
  // changes, and the page above it re-renders on every keystroke in the search
  // box.
  const load = useCallback(() => api.schematicPreview(entry.id), [entry.id])

  return (
    <SchematicDialog
      title={entry.name}
      lead={`${entry.fileName} · ${formatBytes(entry.size)}`}
      load={load}
      onClose={onClose}
      actions={
        <a className="btn" href={schematicDownloadURL(entry.id)} download>
          下载
        </a>
      }
    />
  )
}

function EditDialog({
  entry,
  onCancel,
  onSave,
}: {
  entry: SchematicEntry
  onCancel: () => void
  onSave: (patch: { name: string; note: string; tags: string[] }) => Promise<void>
}) {
  const [name, setName] = useState(entry.name)
  const [note, setNote] = useState(entry.note ?? '')
  const [tags, setTags] = useState((entry.tags ?? []).join('、'))
  const [saving, setSaving] = useState(false)

  const submit = async () => {
    setSaving(true)
    try {
      await onSave({
        name: name.trim(),
        note: note.trim(),
        // Both separators, because a Chinese keyboard produces 、 and an
        // English one produces a comma, and nobody should have to notice.
        tags: tags
          .split(/[、,，]/)
          .map((tag) => tag.trim())
          .filter((tag) => tag !== ''),
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal onClose={onCancel} busy={saving} label={`编辑 ${entry.name}`}>
      <div className="modal__card">
        <h2 className="modal__title">编辑建筑</h2>
        <p className="modal__lead">
          改的是面板怎么称呼它。磁盘上的文件名 <code>{entry.fileName}</code>{' '}
          不动——那是装到服上以后 //schem load 要打的名字。
        </p>

        <label className="field">
          <span>名称</span>
          <input value={name} onChange={(event) => setName(event.target.value)} autoFocus />
        </label>
        <label className="field">
          <span>备注</span>
          <textarea
            rows={3}
            value={note}
            placeholder="这是什么、谁做的、装在哪台服上过"
            onChange={(event) => setNote(event.target.value)}
          />
        </label>
        <label className="field">
          <span>标签</span>
          <input
            value={tags}
            placeholder="出生点、中世纪、商店"
            onChange={(event) => setTags(event.target.value)}
          />
          <small>用顿号或逗号分开，最多 12 个。</small>
        </label>

        <div className="modal__actions">
          <button className="btn" onClick={onCancel} disabled={saving}>
            取消
          </button>
          <button className="btn btn--primary" onClick={() => void submit()} disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * 安装到实例: copy one build into a server's schematics folder.
 *
 * The directory is the whole reason this is a dialog rather than a menu item.
 * //load reads one folder and which one it is depends on the editor the server
 * runs — WorldEdit, FastAsyncWorldEdit, or the mod version on Fabric — and a
 * file in the wrong one produces no error anywhere: the server starts, and
 * //schem load says the build does not exist. So the panel looks at what each
 * server has installed and says which folder it picked.
 */
function InstallDialog({
  entry,
  targets,
  onClose,
}: {
  entry: SchematicEntry
  targets: SchematicTarget[]
  onClose: () => void
}) {
  const [instanceId, setInstanceId] = useState(targets[0]?.id ?? '')
  const target = targets.find((item) => item.id === instanceId) ?? null
  const [dir, setDir] = useState('')
  const [overwrite, setOverwrite] = useState(false)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const dirs = target?.dirs ?? []
  const chosen = dir || dirs[0]?.dir || ''

  const install = async () => {
    setBusy(true)
    setError(null)
    try {
      const result = await api.installSchematic(entry.id, instanceId, chosen, overwrite)
      setDone(result.command)
      toast(`已装到 ${target?.name ?? '实例'}：${result.path}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '安装失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} busy={busy} label={`把 ${entry.name} 安装到实例`}>
      <div className="modal__card">
        <h2 className="modal__title">安装到实例</h2>
        <p className="modal__lead">
          把 <code>{entry.fileName}</code> 复制一份到服务器的 schematics
          目录。复制过去以后它就是那台服自己的文件，之后删库里的这份也不影响它。
        </p>

        {targets.length === 0 ? (
          <div className="alert">还没有实例可以装。</div>
        ) : (
          <>
            <label className="field">
              <span>装到哪台服</span>
              <Select
                className="select--block"
                ariaLabel="选择实例"
                value={instanceId}
                onChange={(value) => {
                  setInstanceId(value)
                  setDir('')
                  setDone(null)
                }}
                options={targets.map((item) => ({
                  value: item.id,
                  label: item.name,
                  note: item.dirs.find((entry) => entry.present)?.editor ?? '没检测到建筑编辑器',
                }))}
              />
            </label>

            <label className="field">
              <span>目标目录</span>
              <Select
                className="select--block"
                ariaLabel="选择目标目录"
                value={chosen}
                onChange={(value) => {
                  setDir(value)
                  setDone(null)
                }}
                options={dirs.map((item) => ({
                  value: item.dir,
                  label: item.dir,
                  note: item.present ? `${item.editor}（这台服装了）` : `${item.editor}（没装）`,
                }))}
              />
              <small>
                {dirs.some((item) => item.present)
                  ? '面板按这台服已经装的编辑器挑的目录。'
                  : '这台服上没找到 WorldEdit 或 FAWE，目录会新建出来——先放建筑再装插件是正常顺序。'}
              </small>
            </label>

            <label className="field field--inline">
              <input
                type="checkbox"
                checked={overwrite}
                onChange={(event) => setOverwrite(event.target.checked)}
              />
              <span>覆盖同名文件</span>
            </label>

            {error && <div className="alert alert--error">{error}</div>}
            {done && (
              <div className="alert alert--ok">
                装好了。进服打 <code>{done}</code> 就能贴出来。
              </div>
            )}
          </>
        )}

        <div className="modal__actions">
          <button className="btn" onClick={onClose} disabled={busy}>
            {done ? '关闭' : '取消'}
          </button>
          <button
            className="btn btn--primary"
            onClick={() => void install()}
            disabled={busy || targets.length === 0}
          >
            {busy ? '安装中…' : '安装'}
          </button>
        </div>
      </div>
    </Modal>
  )
}
