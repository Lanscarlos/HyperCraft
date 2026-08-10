import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactElement, ReactNode } from 'react'

import { ApiError, api, downloadURL, previewURL, uploadFiles } from '../api'
import { formatBytes, formatDate, formatSince } from '../format'
import type { FileEntry, FileListing, InstanceStatus } from '../types'
import { Modal } from './Modal'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'
import { Toast } from './Toast'

interface EditorState {
  path: string
  content: string
  original: string
}

/** Which column the list is ordered by, and which way. */
type SortKey = 'name' | 'size' | 'modified'
interface Sort {
  key: SortKey
  asc: boolean
}

/**
 * `jump` is a directory another page wants opened here.
 *
 * A token rather than a bare path because the pane stays mounted: the plugin
 * list's 配置 link has to work the second time it is pressed on the same
 * plugin, and a path that has not changed would not re-trigger anything.
 */
export interface FileJump {
  path: string
  token: number
}

/** A pending yes/no, waiting on the dialog that will answer it. */
interface ConfirmState {
  title: string
  lead?: ReactNode
  confirmLabel: string
  danger?: boolean
  resolve: (ok: boolean) => void
}

/** A pending "type a name", same idea. */
interface NameState {
  title: string
  lead?: string
  label: string
  initial: string
  confirmLabel: string
  /** Names already used in the directory, so a clash is caught before the
   *  request rather than as a 409 afterwards. */
  taken: string[]
  resolve: (name: string | null) => void
}

/** Long enough for the browser to start one download before the next click. */
const DOWNLOAD_GAP = 400

export function FileManager({ instance, jump }: { instance: InstanceStatus; jump?: FileJump }) {
  const [dir, setDir] = useState('')
  const [listing, setListing] = useState<FileListing | null>(null)
  const [editor, setEditor] = useState<EditorState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<{ text: string; id: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  const [dragging, setDragging] = useState(false)

  // What the list is showing, as opposed to what the directory holds: the
  // filter box and the column ordering. Both are view state, so they survive a
  // refresh but not a change of directory (see load).
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<Sort>({ key: 'name', asc: true })
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const [confirm, setConfirm] = useState<ConfirmState | null>(null)
  const [naming, setNaming] = useState<NameState | null>(null)
  const [preview, setPreview] = useState<FileEntry | null>(null)

  const fileInput = useRef<HTMLInputElement | null>(null)
  const dirRef = useRef('')
  // Drag events fire on every element the pointer crosses, so a single drag
  // over the table is a stream of enter/leave pairs. Counting them is what
  // keeps the drop hint from flickering all the way down the page.
  const dragDepth = useRef(0)

  // Only ever true while a *different* directory is being fetched. Stepping
  // into a folder used to be silent for as long as the listing took — nothing
  // moved, nothing spun — so a slow disk read was indistinguishable from a
  // click that missed, and the answer was to click again. The listing on
  // screen is still correct until the new one lands, so it stays where it is
  // and only says it is on its way out.
  const [pending, setPending] = useState(false)

  const say = useCallback((text: string) => setStatus({ text, id: Date.now() }), [])

  const load = useCallback(
    async (target: string) => {
      setPending(true)
      try {
        setListing(await api.listFiles(instance.id, target))
        setDir(target)
        setError(null)
        // A tick against a row that is no longer on screen is a delete waiting
        // to happen in a directory nobody is looking at.
        setSelected(new Set())
        if (target !== dirRef.current) setQuery('')
        dirRef.current = target
      } catch (err) {
        setError(err instanceof Error ? err.message : '读取目录失败')
      } finally {
        setPending(false)
      }
    },
    [instance.id],
  )

  useEffect(() => {
    setEditor(null)
    void load('')
  }, [instance.id, load])

  // Keyed on the token alone: the path is read when it fires, and adding it to
  // the dependencies would re-navigate on an unrelated render that happened to
  // recreate the object.
  const jumpPath = useRef(jump?.path ?? '')
  jumpPath.current = jump?.path ?? ''
  useEffect(() => {
    if (jump?.token === undefined) return
    setEditor(null)
    void load(jumpPath.current)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jump?.token, load])

  const refresh = () => void load(dir)

  /**
   * The two questions this pane has to ask, as promises.
   *
   * They replace window.confirm and window.prompt, which is not a cosmetic
   * change: the native boxes cannot say *which* five files are about to be
   * deleted, cannot tell a name that is already taken from one that is free
   * until the request comes back a 409, and on a phone they arrive as a bare
   * browser chrome dialog with the panel's own name at the top of it. What is
   * kept from them is the shape — call it, await the answer — so the flows
   * below still read top to bottom.
   */
  const ask = useCallback(
    (request: Omit<ConfirmState, 'resolve'>) =>
      new Promise<boolean>((resolve) => setConfirm({ ...request, resolve })),
    [],
  )
  const askName = useCallback(
    (request: Omit<NameState, 'resolve'>) =>
      new Promise<string | null>((resolve) => setNaming({ ...request, resolve })),
    [],
  )

  const settleConfirm = (ok: boolean) => {
    confirm?.resolve(ok)
    setConfirm(null)
  }
  const settleName = (name: string | null) => {
    naming?.resolve(name)
    setNaming(null)
  }

  const entries = listing?.entries ?? []
  const takenNames = useMemo(() => entries.map((entry) => entry.name), [entries])

  const upload = async (files: File[]) => {
    if (files.length === 0) return
    setBusy(true)
    setError(null)
    setProgress(0)
    try {
      try {
        await uploadFiles(instance.id, dir, files, setProgress)
      } catch (err) {
        // 409 means a file of that name is already there. Replacing a server
        // jar with a newer build is the common case, so offer it rather than
        // making the operator delete the old one first — but never do it
        // without asking, since the same name could be a world file.
        if (!(err instanceof ApiError) || err.status !== 409) throw err
        const replace = await ask({
          title: '目录里已经有同名文件',
          lead: (
            <>
              <NameList names={files.map((file) => file.name)} />
              覆盖会用新文件替换旧的，旧文件不会进回收站。
            </>
          ),
          confirmLabel: '覆盖',
          danger: true,
        })
        if (!replace) {
          say('已取消上传')
          return
        }
        setProgress(0)
        await uploadFiles(instance.id, dir, files, setProgress, true)
      }
      say(files.length === 1 ? `已上传 ${files[0].name}` : `已上传 ${files.length} 个文件`)
      await load(dir)
    } catch (err) {
      setError(err instanceof Error ? err.message : '上传失败')
    } finally {
      setBusy(false)
      setProgress(null)
    }
  }

  const guard = async (action: () => Promise<void>, done: string) => {
    setBusy(true)
    setError(null)
    try {
      await action()
      say(done)
      await load(dir)
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const createFolder = async () => {
    const name = await askName({
      title: '新建文件夹',
      lead: `新文件夹会建在「${dir === '' ? '实例根目录' : dir}」`,
      label: '文件夹名称',
      initial: '',
      confirmLabel: '创建',
      taken: takenNames,
    })
    if (!name) return
    await guard(() => api.mkdir(instance.id, joinPath(dir, name)), `已创建 ${name}`)
  }

  // A new config is nearly always created in order to be typed into, so this
  // writes the empty file and goes straight to the editor rather than leaving
  // an empty row behind for the operator to find and click.
  const createFile = async () => {
    const name = await askName({
      title: '新建文件',
      lead: `新文件会建在「${dir === '' ? '实例根目录' : dir}」，创建后直接打开编辑器`,
      label: '文件名称',
      initial: '',
      confirmLabel: '创建并编辑',
      taken: takenNames,
    })
    if (!name) return
    const path = joinPath(dir, name)
    setBusy(true)
    setError(null)
    try {
      await api.writeFile(instance.id, path, '')
      await load(dir)
      setEditor({ path, content: '', original: '' })
      say(`已创建 ${name}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  const rename = async (entry: FileEntry) => {
    const next = await askName({
      title: `重命名${entry.isDir ? '文件夹' : '文件'}`,
      label: '新名称',
      initial: entry.name,
      confirmLabel: '重命名',
      taken: takenNames,
    })
    if (!next || next === entry.name) return
    await guard(
      () => api.renameFile(instance.id, entry.path, joinPath(dir, next)),
      `已重命名为 ${next}`,
    )
  }

  const remove = async (entry: FileEntry) => {
    const ok = await ask({
      title: `删除${entry.isDir ? '文件夹' : '文件'}「${entry.name}」？`,
      lead: entry.isDir
        ? '文件夹里的所有内容会一起删除，无法撤销。'
        : '删除后无法撤销，请确认这不是存档或配置。',
      confirmLabel: '删除',
      danger: true,
    })
    if (!ok) return
    await guard(() => api.deleteFile(instance.id, entry.path), `已删除 ${entry.name}`)
  }

  /**
   * Deleting a selection, one request at a time.
   *
   * Sequential rather than parallel: these are directory operations on one
   * disk, and twenty concurrent RemoveAll calls on a world folder buy nothing
   * but a less useful error if one of them fails. Failures are collected
   * instead of aborting the run — stopping half way through would leave the
   * operator to work out which half.
   */
  const removeMany = async (targets: FileEntry[]) => {
    if (targets.length === 0) return
    const folders = targets.filter((entry) => entry.isDir).length
    const ok = await ask({
      title: `删除选中的 ${targets.length} 项？`,
      lead: (
        <>
          <NameList names={targets.map((entry) => entry.name)} />
          {folders > 0 && `其中 ${folders} 个是文件夹，里面的内容会一起删除。`}
          删除后无法撤销。
        </>
      ),
      confirmLabel: `删除 ${targets.length} 项`,
      danger: true,
    })
    if (!ok) return

    setBusy(true)
    setError(null)
    const failed: string[] = []
    for (const entry of targets) {
      try {
        await api.deleteFile(instance.id, entry.path)
      } catch {
        failed.push(entry.name)
      }
    }
    const done = targets.length - failed.length
    if (failed.length > 0) setError(`${failed.length} 项删除失败：${failed.join('、')}`)
    if (done > 0) say(`已删除 ${done} 项`)
    setBusy(false)
    await load(dir)
  }

  /** Downloads a selection by clicking one link after another. Spaced out
   *  because a browser handed six navigations in the same tick treats the
   *  last five as a popup. */
  const downloadMany = async (targets: FileEntry[]) => {
    const files = targets.filter((entry) => !entry.isDir)
    if (files.length === 0) {
      setError('选中的都是文件夹，文件夹需要逐个进入下载。')
      return
    }
    for (const [index, entry] of files.entries()) {
      const link = document.createElement('a')
      link.href = downloadURL(instance.id, entry.path)
      link.download = entry.name
      document.body.appendChild(link)
      link.click()
      link.remove()
      if (index < files.length - 1) {
        await new Promise((resolve) => window.setTimeout(resolve, DOWNLOAD_GAP))
      }
    }
    say(`已开始下载 ${files.length} 个文件`)
  }

  const openEntry = async (entry: FileEntry) => {
    if (entry.isDir) {
      void load(entry.path)
      return
    }
    if (isImage(entry.name)) {
      setPreview(entry)
      return
    }
    if (!entry.editable) return

    try {
      const file = await api.readFile(instance.id, entry.path)
      setEditor({ path: entry.path, content: file.content, original: file.content })
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '打开文件失败')
    }
  }

  const saveEditor = async () => {
    if (!editor || editor.content === editor.original) return
    setBusy(true)
    setError(null)
    try {
      await api.writeFile(instance.id, editor.path, editor.content)
      setEditor({ ...editor, original: editor.content })
      say(`已保存 ${baseName(editor.path)}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const closeEditor = async () => {
    if (editor && editor.content !== editor.original) {
      const ok = await ask({
        title: '放弃未保存的修改？',
        lead: `${baseName(editor.path)} 有改动还没有保存，返回列表会丢掉它们。`,
        confirmLabel: '放弃修改',
        danger: true,
      })
      if (!ok) return
    }
    setEditor(null)
  }

  /** The rows actually on screen: the directory, filtered and ordered. */
  const rows = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const visible = needle
      ? entries.filter((entry) => entry.name.toLowerCase().includes(needle))
      : entries.slice()

    const direction = sort.asc ? 1 : -1
    visible.sort((a, b) => {
      // Folders stay above files in both directions. They are the navigation,
      // not the smallest or the oldest thing in the list.
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      switch (sort.key) {
        case 'size':
          return (a.size - b.size) * direction
        case 'modified':
          return (Date.parse(a.modified) - Date.parse(b.modified)) * direction
        default:
          return a.name.localeCompare(b.name, 'zh-CN') * direction
      }
    })
    return visible
  }, [entries, query, sort])

  const selectedEntries = useMemo(
    () => rows.filter((entry) => selected.has(entry.path)),
    [rows, selected],
  )

  const toggleSort = (key: SortKey) =>
    // Name reads best A→Z, but "which is the biggest" and "what changed last"
    // are the questions the other two columns are clicked to answer, so they
    // open on the descending end.
    setSort((prev) => (prev.key === key ? { key, asc: !prev.asc } : { key, asc: key === 'name' }))

  const toggleOne = (path: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (!next.delete(path)) next.add(path)
      return next
    })

  const allTicked = rows.length > 0 && rows.every((entry) => selected.has(entry.path))
  const someTicked = selected.size > 0 && !allTicked
  const tickAllRef = useRef<HTMLInputElement | null>(null)
  useEffect(() => {
    if (tickAllRef.current) tickAllRef.current.indeterminate = someTicked
  }, [someTicked])

  const dialogs = (
    <>
      {status && (
        <Toast key={status.id} message={status.text} onDone={() => setStatus(null)} />
      )}
      {confirm && (
        <ConfirmDialog request={confirm} onAnswer={settleConfirm} />
      )}
      {naming && <NameDialog request={naming} onAnswer={settleName} />}
      {preview && (
        <ImagePreview
          instanceId={instance.id}
          entry={preview}
          onClose={() => setPreview(null)}
        />
      )}
    </>
  )

  if (!listing) {
    if (error) return <div className="alert alert--error">{error}</div>
    return (
      <SkeletonScreen label="正在读取目录…">
        <SkeletonPanel title={false}>
          {/* 实例根目录, the toolbar, then the listing — the same three bands
              the real panel is, in the same order and at the same heights. */}
          <Skeleton w="88px" h={15} />
          <div className="file-toolbar">
            <Skeleton w="82px" h={30} />
            <Skeleton w="96px" h={30} />
            <Skeleton w="60px" h={30} />
          </div>
          <SkeletonRows rows={8} />
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  if (editor) {
    return (
      <>
        <FileEditor
          editor={editor}
          busy={busy}
          error={error}
          onChange={(content) => setEditor({ ...editor, content })}
          onSave={() => void saveEditor()}
          onRevert={() => setEditor({ ...editor, content: editor.original })}
          onClose={() => void closeEditor()}
        />
        {dialogs}
      </>
    )
  }

  const folders = entries.filter((entry) => entry.isDir).length
  const files = entries.length - folders
  const totalBytes = entries.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0)

  return (
    <div
      className="stack"
      onDragEnter={(event) => {
        if (!hasFiles(event.dataTransfer)) return
        dragDepth.current += 1
        setDragging(true)
      }}
      onDragOver={(event) => {
        if (!hasFiles(event.dataTransfer)) return
        event.preventDefault()
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1)
        if (dragDepth.current === 0) setDragging(false)
      }}
      onDrop={(event) => {
        if (!hasFiles(event.dataTransfer)) return
        event.preventDefault()
        dragDepth.current = 0
        setDragging(false)
        void upload(Array.from(event.dataTransfer.files))
      }}
    >
      <section className={`panel files${dragging ? ' files--dropping' : ''}`}>
        <div className="files__head">
          <button
            className="files__up"
            onClick={() => void load(parentOf(dir))}
            disabled={dir === '' || pending}
            title="返回上一级"
            aria-label="返回上一级"
          >
            <Glyph name="up" />
          </button>
          <Breadcrumb dir={dir} onNavigate={(next) => void load(next)} />
        </div>

        <div className="file-toolbar">
          <button
            className="btn btn--primary"
            onClick={() => fileInput.current?.click()}
            disabled={busy}
          >
            <Glyph name="upload" />
            上传文件
          </button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              void upload(Array.from(event.target.files ?? []))
              event.target.value = ''
            }}
          />
          <button className="btn" disabled={busy} onClick={() => void createFolder()}>
            <Glyph name="new-folder" />
            新建文件夹
          </button>
          <button className="btn" disabled={busy} onClick={() => void createFile()}>
            <Glyph name="new-file" />
            新建文件
          </button>
          <button
            className="btn btn--icon"
            onClick={refresh}
            disabled={busy || pending}
            title="刷新"
            aria-label="刷新"
          >
            <Glyph name="refresh" className={pending ? 'spin' : undefined} />
          </button>

          <div className="file-toolbar__find">
            <Glyph name="search" />
            <input
              className="file-toolbar__search"
              type="search"
              value={query}
              placeholder="在当前目录中查找"
              aria-label="在当前目录中查找"
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Escape') setQuery('')
              }}
            />
          </div>
        </div>

        {selectedEntries.length > 0 && (
          <div className="file-bulk">
            <span className="file-bulk__count">已选择 {selectedEntries.length} 项</span>
            <button
              className="btn"
              disabled={busy}
              onClick={() => void downloadMany(selectedEntries)}
            >
              <Glyph name="download" />
              下载
            </button>
            <button
              className="btn btn--danger"
              disabled={busy}
              onClick={() => void removeMany(selectedEntries)}
            >
              <Glyph name="trash" />
              删除
            </button>
            <button className="link file-bulk__clear" onClick={() => setSelected(new Set())}>
              取消选择
            </button>
          </div>
        )}

        {progress != null && (
          <div className="progress">
            <div className="progress__bar" style={{ width: `${Math.round(progress * 100)}%` }} />
            <span className="progress__label">{Math.round(progress * 100)}%</span>
          </div>
        )}

        {error && (
          <div className="alert alert--error">
            {error}
            <button className="link" onClick={() => setError(null)}>
              知道了
            </button>
          </div>
        )}

        <div className="table-scroll" data-pending={pending || undefined}>
          <table className="data-table data-table--files">
            <colgroup>
              <col className="col--tick" />
              <col />
              <col className="col--size" />
              <col className="col--time" />
              <col className="col--ops" />
            </colgroup>
            <thead>
              <tr>
                <th className="col--tick">
                  <input
                    ref={tickAllRef}
                    type="checkbox"
                    className="tick"
                    checked={allTicked}
                    disabled={rows.length === 0}
                    aria-label="全选"
                    onChange={() =>
                      setSelected(
                        allTicked ? new Set() : new Set(rows.map((entry) => entry.path)),
                      )
                    }
                  />
                </th>
                <SortHeader label="名称" column="name" sort={sort} onSort={toggleSort} />
                <SortHeader label="大小" column="size" sort={sort} onSort={toggleSort} align="num" />
                <SortHeader label="修改时间" column="modified" sort={sort} onSort={toggleSort} />
                <th className="col--ops">操作</th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && (
                <tr className="file-empty__row">
                  <td colSpan={5}>
                    {query ? (
                      <div className="file-empty">
                        <p>没有匹配「{query}」的文件。</p>
                        <button className="btn" onClick={() => setQuery('')}>
                          清除筛选
                        </button>
                      </div>
                    ) : (
                      <div className="file-empty">
                        <Glyph name="folder-open" className="file-empty__glyph" />
                        <p>这个目录是空的。把服务端 jar 或插件拖进来就能开始。</p>
                        <button className="btn" onClick={() => fileInput.current?.click()}>
                          上传文件
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              )}
              {rows.map((entry) => (
                <FileRow
                  key={entry.path}
                  entry={entry}
                  instanceId={instance.id}
                  busy={busy}
                  ticked={selected.has(entry.path)}
                  onTick={() => toggleOne(entry.path)}
                  onOpen={() => void openEntry(entry)}
                  onRename={() => void rename(entry)}
                  onDelete={() => void remove(entry)}
                />
              ))}
            </tbody>
          </table>
        </div>

        <div className="files__foot">
          <span>
            {folders} 个文件夹 · {files} 个文件
            {files > 0 && ` · 共 ${formatBytes(totalBytes)}`}
            {query && rows.length !== entries.length && ` · 已筛选出 ${rows.length} 项`}
          </span>
          <span className="files__root" title={listing.root}>
            <code>{listing.root}</code> · 所有操作都被限制在这个目录内
          </span>
        </div>

        {dragging && (
          <div className="file-drop" aria-hidden="true">
            <div className="file-drop__card">
              <Glyph name="upload" className="file-drop__glyph" />
              松开即可上传到 <b>{dir === '' ? '实例根目录' : dir}</b>
              <small>单文件上限 {formatBytes(listing.maxUploadBytes)}</small>
            </div>
          </div>
        )}
      </section>

      {dialogs}
    </div>
  )
}

/* ------------------------------------------------------------------ rows */

function FileRow({
  entry,
  instanceId,
  busy,
  ticked,
  onTick,
  onOpen,
  onRename,
  onDelete,
}: {
  entry: FileEntry
  instanceId: string
  busy: boolean
  ticked: boolean
  onTick: () => void
  onOpen: () => void
  onRename: () => void
  onDelete: () => void
}) {
  const kind = kindOf(entry)
  const openable = entry.isDir || entry.editable || isImage(entry.name)

  return (
    <tr data-ticked={ticked || undefined}>
      <td className="col--tick">
        <input
          type="checkbox"
          className="tick"
          checked={ticked}
          onChange={onTick}
          aria-label={`选择 ${entry.name}`}
        />
      </td>
      <td>
        <div className="filecell">
          <span className={`fileicon fileicon--${TONE[kind]}`}>
            <Glyph name={GLYPH[kind]} />
          </span>
          <button
            className={`file-link${openable ? '' : ' file-link--plain'}`}
            onClick={onOpen}
            disabled={!openable}
            title={
              entry.isDir
                ? '打开目录'
                : entry.editable
                  ? '编辑'
                  : isImage(entry.name)
                    ? '预览'
                    : '此文件不支持在线打开，可以下载后查看'
            }
          >
            {entry.name}
          </button>
          {entry.symlink && <span className="badge">符号链接</span>}
        </div>
      </td>
      <td className="num">{entry.isDir ? '—' : formatBytes(entry.size)}</td>
      <td>
        <time className="file-time" dateTime={entry.modified} title={formatDate(entry.modified)}>
          {formatSince(entry.modified)}
        </time>
      </td>
      <td className="col--ops">
        <div className="file-actions">
          {!entry.isDir && (
            <a
              className="iconbtn"
              href={downloadURL(instanceId, entry.path)}
              download
              title="下载"
              aria-label={`下载 ${entry.name}`}
            >
              <Glyph name="download" />
            </a>
          )}
          <button
            className="iconbtn"
            disabled={busy}
            onClick={onRename}
            title="重命名"
            aria-label={`重命名 ${entry.name}`}
          >
            <Glyph name="rename" />
          </button>
          <button
            className="iconbtn iconbtn--danger"
            disabled={busy}
            onClick={onDelete}
            title="删除"
            aria-label={`删除 ${entry.name}`}
          >
            <Glyph name="trash" />
          </button>
        </div>
      </td>
    </tr>
  )
}

function SortHeader({
  label,
  column,
  sort,
  onSort,
  align,
}: {
  label: string
  column: SortKey
  sort: Sort
  onSort: (key: SortKey) => void
  align?: 'num'
}) {
  const active = sort.key === column
  return (
    <th
      className={align === 'num' ? 'num' : undefined}
      aria-sort={active ? (sort.asc ? 'ascending' : 'descending') : 'none'}
    >
      <button className="th-sort" onClick={() => onSort(column)}>
        {label}
        <span className="th-sort__mark" aria-hidden="true">
          {active ? (sort.asc ? '▲' : '▼') : '▲'}
        </span>
      </button>
    </th>
  )
}

function Breadcrumb({ dir, onNavigate }: { dir: string; onNavigate: (next: string) => void }) {
  const parts = dir === '' ? [] : dir.split('/')

  return (
    <nav className="breadcrumb" aria-label="目录路径">
      <button
        className={`breadcrumb__crumb${parts.length === 0 ? ' breadcrumb__crumb--here' : ''}`}
        onClick={() => onNavigate('')}
        aria-current={parts.length === 0 ? 'page' : undefined}
      >
        <Glyph name="home" />
        实例根目录
      </button>
      {parts.map((part, index) => {
        const here = index === parts.length - 1
        return (
          <span key={`${part}-${index}`} className="breadcrumb__step">
            <span className="breadcrumb__sep">/</span>
            <button
              className={`breadcrumb__crumb${here ? ' breadcrumb__crumb--here' : ''}`}
              onClick={() => onNavigate(parts.slice(0, index + 1).join('/'))}
              aria-current={here ? 'page' : undefined}
            >
              {part}
            </button>
          </span>
        )
      })}
    </nav>
  )
}

/* ---------------------------------------------------------------- editor */

/**
 * The text editor, with a gutter.
 *
 * Line numbers are not decoration here: what gets opened in this box is
 * server.properties and a plugin's config.yml, and what sends someone to it is
 * a console line that ends in "at line 42". The gutter is one text node rather
 * than one element per line — a 20 000-line log is a plausible thing to open,
 * and 20 000 spans is not — and it is kept in step with the textarea by
 * mirroring its scroll offset. Both need identical type and line-height for
 * that to hold, which is why the two rules in the stylesheet share a font
 * declaration; wrapping is off for the same reason, since a soft-wrapped line
 * takes two rows on screen and one number in the margin.
 */
function FileEditor({
  editor,
  busy,
  error,
  onChange,
  onSave,
  onRevert,
  onClose,
}: {
  editor: EditorState
  busy: boolean
  error: string | null
  onChange: (content: string) => void
  onSave: () => void
  onRevert: () => void
  onClose: () => void
}) {
  const dirty = editor.content !== editor.original
  const gutter = useRef<HTMLDivElement | null>(null)

  // Past this the count is recomputed on every keystroke over a string big
  // enough to feel it, and the numbers have stopped being useful anyway.
  const lines = useMemo(
    () => (editor.content.length > 400_000 ? 0 : editor.content.split('\n').length),
    [editor.content],
  )
  const gutterText = useMemo(
    () => (lines === 0 ? '' : Array.from({ length: lines }, (_, index) => index + 1).join('\n')),
    [lines],
  )
  // Bytes, not characters: a config full of Chinese is three times the length
  // it looks. Memoised because the Blob is an allocation per keystroke.
  const bytes = useMemo(() => new Blob([editor.content]).size, [editor.content])

  // A tab away with unsaved changes is a browser-level event; the panel's own
  // 返回 already asks.
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => event.preventDefault()
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  return (
    <div className="stack">
      <section className="panel">
        <div className="editor__head">
          <div className="editor__title">
            <span className={`fileicon fileicon--${TONE[kindOfName(editor.path)]}`}>
              <Glyph name={GLYPH[kindOfName(editor.path)]} />
            </span>
            <div>
              <h3 className="panel__title">{baseName(editor.path)}</h3>
              {/* Only when it says something the title does not: a file in the
                  root would otherwise print its own name twice. */}
              {editor.path !== baseName(editor.path) && (
                <p className="editor__path">{editor.path}</p>
              )}
            </div>
          </div>
          <div className="editor__facts">
            <span>{formatBytes(bytes)}</span>
            {lines > 0 && <span>{lines} 行</span>}
            <span className={dirty ? 'editor__dot editor__dot--dirty' : 'editor__dot'}>
              {dirty ? '有未保存的修改' : '已是最新'}
            </span>
          </div>
        </div>

        <div className="editor">
          {lines > 0 && (
            <div className="editor__gutter" ref={gutter} aria-hidden="true">
              {gutterText}
            </div>
          )}
          <textarea
            className="editor__text"
            value={editor.content}
            onChange={(event) => onChange(event.target.value)}
            onScroll={(event) => {
              if (gutter.current) gutter.current.scrollTop = event.currentTarget.scrollTop
            }}
            onKeyDown={(event) => {
              // The shortcut everyone's fingers already know, and without it
              // the browser offers to save the whole page as HTML.
              if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
                event.preventDefault()
                onSave()
              }
            }}
            spellCheck={false}
            wrap="off"
            aria-label={`编辑 ${editor.path}`}
          />
        </div>

        {error && <div className="alert alert--error">{error}</div>}

        <div className="actions">
          <button className="btn btn--primary" onClick={onSave} disabled={busy || !dirty}>
            保存
          </button>
          <button className="btn" onClick={onRevert} disabled={busy || !dirty}>
            撤销修改
          </button>
          <span className="editor__hint">Ctrl / ⌘ + S 也能保存</span>
          <button className="btn actions__danger" onClick={onClose}>
            返回文件列表
          </button>
        </div>
      </section>
    </div>
  )
}

/* --------------------------------------------------------------- dialogs */

function ConfirmDialog({
  request,
  onAnswer,
}: {
  request: ConfirmState
  onAnswer: (ok: boolean) => void
}) {
  return (
    <Modal onClose={() => onAnswer(false)} label={request.title}>
      <div className="modal__card">
        <h2 className="modal__title">{request.title}</h2>
        {request.lead && <div className="modal__lead">{request.lead}</div>}
        <div className="modal__actions">
          <button className="btn" onClick={() => onAnswer(false)}>
            取消
          </button>
          <button
            className={request.danger ? 'btn btn--danger' : 'btn btn--primary'}
            onClick={() => onAnswer(true)}
            autoFocus
          >
            {request.confirmLabel}
          </button>
        </div>
      </div>
    </Modal>
  )
}

function NameDialog({
  request,
  onAnswer,
}: {
  request: NameState
  onAnswer: (name: string | null) => void
}) {
  const [value, setValue] = useState(request.initial)
  const input = useRef<HTMLInputElement | null>(null)

  // Renaming server-icon.png is nearly always about the "server-icon" half, so
  // the extension is left out of the selection: type and the suffix survives.
  useEffect(() => {
    const field = input.current
    if (!field) return
    field.focus()
    const dot = request.initial.lastIndexOf('.')
    field.setSelectionRange(0, dot > 0 ? dot : request.initial.length)
  }, [request.initial])

  const problem = nameProblem(value, request.taken, request.initial)
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (problem) return
    onAnswer(value.trim())
  }

  return (
    <Modal onClose={() => onAnswer(null)} label={request.title}>
      <form className="modal__card" onSubmit={submit}>
        <h2 className="modal__title">{request.title}</h2>
        {request.lead && <p className="modal__lead">{request.lead}</p>}

        <label className="field">
          <span>{request.label}</span>
          <input
            ref={input}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            spellCheck={false}
            autoComplete="off"
          />
          {/* Only once something has been typed: an empty box is not a mistake
              yet, and opening the dialog already shouting is not helpful. */}
          {problem && value.length > 0 && <small className="field__bad">{problem}</small>}
        </label>

        <div className="modal__actions">
          <button className="btn" type="button" onClick={() => onAnswer(null)}>
            取消
          </button>
          <button className="btn btn--primary" type="submit" disabled={problem != null}>
            {request.confirmLabel}
          </button>
        </div>
      </form>
    </Modal>
  )
}

function ImagePreview({
  instanceId,
  entry,
  onClose,
}: {
  instanceId: string
  entry: FileEntry
  onClose: () => void
}) {
  const [broken, setBroken] = useState(false)

  return (
    <Modal onClose={onClose} label={`预览 ${entry.name}`}>
      <div className="modal__card modal__card--wide">
        <h2 className="modal__title">{entry.name}</h2>
        <p className="modal__lead">
          {formatBytes(entry.size)} · {formatDate(entry.modified)}
        </p>
        <div className="preview">
          {broken ? (
            <p className="muted">这张图片无法显示，可能已经损坏或格式不受支持。</p>
          ) : (
            <img
              src={previewURL(instanceId, entry.path)}
              alt={entry.name}
              onError={() => setBroken(true)}
            />
          )}
        </div>
        <div className="modal__actions">
          <a className="btn" href={downloadURL(instanceId, entry.path)} download>
            下载
          </a>
          <button className="btn btn--primary" onClick={onClose}>
            关闭
          </button>
        </div>
      </div>
    </Modal>
  )
}

/** The first few names of a batch, so a confirmation says what it is about. */
function NameList({ names }: { names: string[] }) {
  const shown = names.slice(0, 6)
  return (
    <ul className="namelist">
      {shown.map((name) => (
        <li key={name}>{name}</li>
      ))}
      {names.length > shown.length && <li className="muted">…等共 {names.length} 项</li>}
    </ul>
  )
}

/* --------------------------------------------------------------- glyphs */

type GlyphName =
  | 'up'
  | 'home'
  | 'upload'
  | 'download'
  | 'refresh'
  | 'search'
  | 'rename'
  | 'trash'
  | 'new-folder'
  | 'new-file'
  | 'folder'
  | 'folder-open'
  | 'doc'
  | 'config'
  | 'image'
  | 'archive'
  | 'jar'
  | 'script'
  | 'data'

const GLYPHS: Record<GlyphName, ReactElement> = {
  up: <path d="M12 20V5m0 0-6 6m6-6 6 6" />,
  home: <path d="M4 10.5 12 4l8 6.5V19a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 19v-8.5Z" />,
  upload: (
    <>
      <path d="M12 16V4m0 0-4.5 4.5M12 4l4.5 4.5" />
      <path d="M4.5 15v3.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V15" />
    </>
  ),
  download: (
    <>
      <path d="M12 4v12m0 0-4.5-4.5M12 16l4.5-4.5" />
      <path d="M4.5 16v2.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V16" />
    </>
  ),
  refresh: (
    <>
      <path d="M20 12a8 8 0 1 1-2.6-5.9" />
      <path d="M20 4v4.5h-4.5" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6" />
      <path d="m15 15 4.5 4.5" />
    </>
  ),
  // A pencil: renaming is writing on the thing, not moving it.
  rename: (
    <>
      <path d="M4.5 19.5h4L20 8a2.1 2.1 0 0 0-3-3L5.5 16.5l-1 3Z" />
      <path d="m14.5 6.5 3 3" />
    </>
  ),
  trash: (
    <>
      <path d="M4.5 6.5h15M9.5 6.5V5a1.5 1.5 0 0 1 1.5-1.5h2A1.5 1.5 0 0 1 14.5 5v1.5" />
      <path d="M6.5 6.5 7.5 20a1.5 1.5 0 0 0 1.5 1.4h6a1.5 1.5 0 0 0 1.5-1.4l1-13.5" />
      <path d="M10.5 10.5v7M13.5 10.5v7" />
    </>
  ),
  'new-folder': (
    <>
      <path d="M4 6.5a2 2 0 0 1 2-2h3.4l1.8 2.2H18a2 2 0 0 1 2 2v3" />
      <path d="M4 6.5v11a2 2 0 0 0 2 2h7" />
      <path d="M17.5 15v6M14.5 18h6" />
    </>
  ),
  'new-file': (
    <>
      <path d="M13.5 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h4" />
      <path d="M13.5 3.5 19 9v3" />
      <path d="M18 15v6M15 18h6" />
    </>
  ),
  folder: <path d="M4 6.5A2 2 0 0 1 6 4.5h3.4l1.8 2.2H18a2 2 0 0 1 2 2v8.8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6.5Z" />,
  'folder-open': (
    <>
      <path d="M4 18.5V6.5a2 2 0 0 1 2-2h3.4l1.8 2.2H18a2 2 0 0 1 2 2v1.8" />
      <path d="M4 18.5 6.4 11h15.1l-2.4 7.5H4Z" />
    </>
  ),
  doc: (
    <>
      <path d="M13.5 3.5H7a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9l-5.5-5.5Z" />
      <path d="M13.5 3.5V9H19" />
      <path d="M8.5 13.5h7M8.5 17h4.5" />
    </>
  ),
  // Sliders, the same shape the settings page uses: a file of knobs.
  config: (
    <>
      <path d="M3.5 8h8M16 8h4.5M3.5 16h4.5M12.5 16h8" />
      <circle cx="13.75" cy="8" r="2.25" />
      <circle cx="10.25" cy="16" r="2.25" />
    </>
  ),
  image: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <circle cx="9" cy="9.75" r="1.6" />
      <path d="m4.5 17.5 4.75-4.75 3.25 3.25 2.75-2.5 4.75 4" />
    </>
  ),
  archive: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M3.5 9.5h17M10.25 13h3.5" />
    </>
  ),
  // The cube the core library uses: a jar is a packaged build.
  jar: (
    <>
      <path d="M12 3 20 7.5v9L12 21l-8-4.5v-9L12 3Z" />
      <path d="m4 7.5 8 4.5 8-4.5M12 12v9" />
    </>
  ),
  script: (
    <>
      <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
      <path d="m7.5 10 2.5 2.5-2.5 2.5M13 15h3.5" />
    </>
  ),
  data: (
    <>
      <ellipse cx="12" cy="6.5" rx="7.5" ry="3" />
      <path d="M4.5 6.5v11c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3v-11" />
      <path d="M4.5 12c0 1.7 3.4 3 7.5 3s7.5-1.3 7.5-3" />
    </>
  ),
}

/**
 * The file manager's own icon set.
 *
 * Separate from components/Icon because these are about file types rather than
 * navigation, and drawn on the same 24px grid with the same stroke so a row of
 * them lines up with the rest of the panel.
 */
function Glyph({ name, className }: { name: GlyphName; className?: string }) {
  return (
    <svg
      className={className ? `icon ${className}` : 'icon'}
      viewBox="0 0 24 24"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {GLYPHS[name]}
    </svg>
  )
}

/* ----------------------------------------------------------- file kinds */

type Kind = 'dir' | 'jar' | 'archive' | 'image' | 'config' | 'text' | 'script' | 'data' | 'plain'

const KIND_BY_EXT: Record<string, Kind> = {
  '.jar': 'jar',
  '.zip': 'archive',
  '.tar': 'archive',
  '.gz': 'archive',
  '.tgz': 'archive',
  '.rar': 'archive',
  '.7z': 'archive',
  '.xz': 'archive',
  '.zst': 'archive',
  '.png': 'image',
  '.jpg': 'image',
  '.jpeg': 'image',
  '.gif': 'image',
  '.webp': 'image',
  '.bmp': 'image',
  '.ico': 'image',
  '.svg': 'image',
  '.yml': 'config',
  '.yaml': 'config',
  '.json': 'config',
  '.properties': 'config',
  '.toml': 'config',
  '.conf': 'config',
  '.cfg': 'config',
  '.ini': 'config',
  '.xml': 'config',
  '.env': 'config',
  '.mcmeta': 'config',
  '.txt': 'text',
  '.md': 'text',
  '.log': 'text',
  '.csv': 'text',
  '.lang': 'text',
  '.snbt': 'text',
  '.sh': 'script',
  '.bat': 'script',
  '.cmd': 'script',
  '.ps1': 'script',
  '.dat': 'data',
  '.dat_old': 'data',
  '.mca': 'data',
  '.mcr': 'data',
  '.nbt': 'data',
  '.db': 'data',
  '.lock': 'data',
}

const GLYPH: Record<Kind, GlyphName> = {
  dir: 'folder',
  jar: 'jar',
  archive: 'archive',
  image: 'image',
  config: 'config',
  text: 'doc',
  script: 'script',
  data: 'data',
  plain: 'doc',
}

/** Four tints rather than nine. The point of the colour is to let the eye
 *  find the folders and then the configs; a rainbow would be a legend to
 *  learn. */
const TONE: Record<Kind, string> = {
  dir: 'dir',
  jar: 'pkg',
  archive: 'pkg',
  image: 'media',
  config: 'conf',
  script: 'conf',
  text: 'plain',
  data: 'plain',
  plain: 'plain',
}

function extensionOf(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot).toLowerCase() : ''
}

function kindOf(entry: FileEntry): Kind {
  return entry.isDir ? 'dir' : kindOfName(entry.name)
}

function kindOfName(name: string): Kind {
  return KIND_BY_EXT[extensionOf(name)] ?? 'plain'
}

/** Only the types the panel serves inline; see previewTypes in handlers_fs.go.
 *  SVG is deliberately not among them, so it is a download here too. */
const PREVIEWABLE = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.ico'])

function isImage(name: string): boolean {
  return PREVIEWABLE.has(extensionOf(name))
}

/* -------------------------------------------------------------- helpers */

function hasFiles(transfer: DataTransfer | null): boolean {
  // Dragging selected text across the page is not an upload.
  return transfer != null && Array.from(transfer.types).includes('Files')
}

function nameProblem(value: string, taken: string[], initial: string): string | null {
  const name = value.trim()
  if (name === '') return '请输入名称'
  if (name === '.' || name === '..') return '这个名称不能用'
  if (/[/\\]/.test(name)) return '名称里不能有斜杠：请先进入目标目录再新建'
  if (/[\u0000-\u001f\u007f]/.test(name)) return '名称里有不可见字符'
  if (name !== initial && taken.some((used) => used.toLowerCase() === name.toLowerCase())) {
    return '这个目录里已经有同名的文件了'
  }
  return null
}

function joinPath(dir: string, name: string): string {
  return dir === '' ? name : `${dir}/${name}`
}

function parentOf(dir: string): string {
  const index = dir.lastIndexOf('/')
  return index < 0 ? '' : dir.slice(0, index)
}

function baseName(path: string): string {
  const index = path.lastIndexOf('/')
  return index < 0 ? path : path.slice(index + 1)
}
