import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, api, downloadURL, uploadFiles } from '../api'
import { formatBytes } from '../format'
import type { FileEntry, FileListing, InstanceStatus } from '../types'

interface EditorState {
  path: string
  content: string
  original: string
}

export function FileManager({ instance }: { instance: InstanceStatus }) {
  const [dir, setDir] = useState('')
  const [listing, setListing] = useState<FileListing | null>(null)
  const [editor, setEditor] = useState<EditorState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  const [dragging, setDragging] = useState(false)

  const fileInput = useRef<HTMLInputElement | null>(null)

  const load = useCallback(
    async (target: string) => {
      try {
        setListing(await api.listFiles(instance.id, target))
        setDir(target)
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : '读取目录失败')
      }
    },
    [instance.id],
  )

  useEffect(() => {
    setEditor(null)
    void load('')
  }, [instance.id, load])

  const refresh = () => void load(dir)

  const upload = async (files: File[]) => {
    if (files.length === 0) return
    setBusy(true)
    setError(null)
    setStatus(null)
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
        const names = files.map((f) => f.name).join('、')
        if (!window.confirm(`${names} 已存在，是否覆盖？`)) {
          setStatus('已取消上传')
          return
        }
        setProgress(0)
        await uploadFiles(instance.id, dir, files, setProgress, true)
      }
      setStatus(`已上传 ${files.length} 个文件`)
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
    setStatus(null)
    try {
      await action()
      setStatus(done)
      await load(dir)
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const openEntry = async (entry: FileEntry) => {
    if (entry.isDir) {
      void load(entry.path)
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
    if (!editor) return
    setBusy(true)
    setError(null)
    try {
      await api.writeFile(instance.id, editor.path, editor.content)
      setEditor({ ...editor, original: editor.content })
      setStatus(`已保存 ${editor.path}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const closeEditor = () => {
    if (editor && editor.content !== editor.original) {
      if (!window.confirm('有未保存的修改，确定要关闭吗？')) return
    }
    setEditor(null)
  }

  if (!listing) {
    return <div className="panel">{error ?? '加载中…'}</div>
  }

  if (editor) {
    const dirty = editor.content !== editor.original
    return (
      <div className="stack">
        <section className="panel">
          <div className="chart-head">
            <h3 className="panel__title">{editor.path}</h3>
            <p className="chart-head__meta">{dirty ? '有未保存的修改' : '已是最新'}</p>
          </div>
          <textarea
            className="file-editor"
            value={editor.content}
            onChange={(e) => setEditor({ ...editor, content: e.target.value })}
            spellCheck={false}
            rows={24}
          />
          {error && <div className="alert alert--error">{error}</div>}
          {status && <div className="alert alert--ok">{status}</div>}
          <div className="actions">
            <button className="btn btn--primary" onClick={() => void saveEditor()} disabled={busy || !dirty}>
              保存
            </button>
            <button className="btn" onClick={closeEditor}>
              返回文件列表
            </button>
          </div>
        </section>
      </div>
    )
  }

  return (
    <div
      className={`stack${dragging ? ' stack--dropping' : ''}`}
      onDragOver={(e) => {
        e.preventDefault()
        setDragging(true)
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault()
        setDragging(false)
        void upload(Array.from(e.dataTransfer.files))
      }}
    >
      <section className="panel">
        <Breadcrumb dir={dir} onNavigate={(next) => void load(next)} />

        <div className="file-toolbar">
          <button
            className="btn btn--primary"
            onClick={() => fileInput.current?.click()}
            disabled={busy}
          >
            上传文件
          </button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              void upload(Array.from(e.target.files ?? []))
              e.target.value = ''
            }}
          />
          <button
            className="btn"
            disabled={busy}
            onClick={() => {
              const name = window.prompt('新建文件夹名称')
              if (!name?.trim()) return
              void guard(
                () => api.mkdir(instance.id, joinPath(dir, name.trim())),
                `已创建 ${name.trim()}`,
              )
            }}
          >
            新建文件夹
          </button>
          <button className="btn" onClick={refresh} disabled={busy}>
            刷新
          </button>
          <span className="file-toolbar__hint">
            拖拽文件到这里也能上传，单文件上限 {formatBytes(listing.maxUploadBytes)}
          </span>
        </div>

        {progress != null && (
          <div className="progress">
            <div className="progress__bar" style={{ width: `${Math.round(progress * 100)}%` }} />
            <span className="progress__label">{Math.round(progress * 100)}%</span>
          </div>
        )}

        {error && <div className="alert alert--error">{error}</div>}
        {status && <div className="alert alert--ok">{status}</div>}

        <div className="table-scroll">
          <table className="data-table data-table--files">
            <thead>
              <tr>
                <th>名称</th>
                <th className="num">大小</th>
                <th>修改时间</th>
                <th className="actions">操作</th>
              </tr>
            </thead>
            <tbody>
              {dir !== '' && (
                <tr>
                  <td colSpan={4}>
                    <button className="file-link" onClick={() => void load(parentOf(dir))}>
                      ../ 上一级
                    </button>
                  </td>
                </tr>
              )}
              {listing.entries.length === 0 && (
                <tr>
                  <td colSpan={4} className="muted">
                    这个目录是空的。把服务端 jar 拖进来就能开始。
                  </td>
                </tr>
              )}
              {listing.entries.map((entry) => (
                <FileRow
                  key={entry.path}
                  entry={entry}
                  instanceId={instance.id}
                  busy={busy}
                  onOpen={() => void openEntry(entry)}
                  onRename={(next) =>
                    void guard(
                      () => api.renameFile(instance.id, entry.path, joinPath(dir, next)),
                      `已重命名为 ${next}`,
                    )
                  }
                  onDelete={() =>
                    void guard(() => api.deleteFile(instance.id, entry.path), `已删除 ${entry.name}`)
                  }
                />
              ))}
            </tbody>
          </table>
        </div>

        <p className="chart-note">
          目录：<code>{listing.root}</code>
          {' · '}所有操作都被限制在这个目录内。
        </p>
      </section>
    </div>
  )
}

function FileRow({
  entry,
  instanceId,
  busy,
  onOpen,
  onRename,
  onDelete,
}: {
  entry: FileEntry
  instanceId: string
  busy: boolean
  onOpen: () => void
  onRename: (next: string) => void
  onDelete: () => void
}) {
  return (
    <tr>
      <td>
        <button
          className={`file-link${entry.isDir || entry.editable ? '' : ' file-link--plain'}`}
          onClick={onOpen}
          disabled={!entry.isDir && !entry.editable}
          title={entry.isDir ? '打开目录' : entry.editable ? '编辑' : '此文件不支持在线编辑'}
        >
          <span className="file-icon">{entry.isDir ? '📁' : entry.symlink ? '🔗' : '📄'}</span>
          {entry.name}
        </button>
        {entry.symlink && <span className="badge">符号链接</span>}
      </td>
      <td className="num">{entry.isDir ? '—' : formatBytes(entry.size)}</td>
      <td>{new Date(entry.modified).toLocaleString('zh-CN')}</td>
      <td className="actions">
        {!entry.isDir && (
          <a className="link" href={downloadURL(instanceId, entry.path)} download>
            下载
          </a>
        )}
        <button
          className="link"
          disabled={busy}
          onClick={() => {
            const next = window.prompt('新名称', entry.name)
            if (next?.trim() && next.trim() !== entry.name) onRename(next.trim())
          }}
        >
          重命名
        </button>
        <button
          className="link link--danger"
          disabled={busy}
          onClick={() => {
            const warning = entry.isDir
              ? `确定要删除目录「${entry.name}」及其中所有内容吗？此操作不可撤销。`
              : `确定要删除「${entry.name}」吗？`
            if (window.confirm(warning)) onDelete()
          }}
        >
          删除
        </button>
      </td>
    </tr>
  )
}

function Breadcrumb({ dir, onNavigate }: { dir: string; onNavigate: (next: string) => void }) {
  const parts = dir === '' ? [] : dir.split('/')

  return (
    <nav className="breadcrumb">
      <button className="breadcrumb__crumb" onClick={() => onNavigate('')}>
        实例根目录
      </button>
      {parts.map((part, index) => (
        <span key={`${part}-${index}`}>
          <span className="breadcrumb__sep">/</span>
          <button
            className="breadcrumb__crumb"
            onClick={() => onNavigate(parts.slice(0, index + 1).join('/'))}
          >
            {part}
          </button>
        </span>
      ))}
    </nav>
  )
}

function joinPath(dir: string, name: string): string {
  return dir === '' ? name : `${dir}/${name}`
}

function parentOf(dir: string): string {
  const index = dir.lastIndexOf('/')
  return index < 0 ? '' : dir.slice(0, index)
}
