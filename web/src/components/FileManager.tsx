import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { ApiError, api, downloadURL, previewURL, uploadFiles } from '../api'
import { ask } from '../confirm'
import { formatBytes, formatDate, formatSince } from '../format'
import { toast } from '../toast'
import type { FileEntry, FileListing, InstanceStatus } from '../types'
import { FileIcon, extensionOf } from './FileIcon'
import { FileTree } from './FileTree'
import type { TreeNode } from './FileTree'
import { Glyph } from './Glyph'
import type { MenuItem } from './Menu'
import { Modal } from './Modal'
import { PageHead } from './Page'
import { SchematicPreview } from './SchematicPreview'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'

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
  /** A file inside `path` to open in the editor on arrival. 配置历史 sends one:
   *  landing in the right directory is not the answer to "let me edit the file
   *  I was just looking at the diff of". */
  file?: string
}

/** A pending "type a name", waiting on the dialog that will answer it. */
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

export function FileManager({
  instance,
  active,
  jump,
  onOpenHistory,
  onWorkspaceChange,
}: {
  instance: InstanceStatus
  /** Whether this section is the one on screen. Sections stay mounted behind
   *  whatever replaced them (see InstanceView), so "no longer visible" is not
   *  the same event as unmounting — and edit mode has to end on both. */
  active: boolean
  /** Asks the shell to fold to the rail for as long as edit mode is on. */
  onWorkspaceChange?: (full: boolean) => void
  jump?: FileJump
  /** Sends the open file to 配置历史. Absent when nothing upstream can switch
   *  sections, and when the panel has no config history at all. */
  onOpenHistory?: (path: string) => void
}) {
  const [dir, setDir] = useState('')
  const [listing, setListing] = useState<FileListing | null>(null)
  // Open files, in the order they were opened, and which one is in front.
  //
  // It used to be one file at a time, and the editor replaced the listing
  // while it was open: comparing two configs meant closing one, finding the
  // other, and remembering what the first one said. Tabs are the whole reason
  // the pane beside the list is worth having.
  const [tabs, setTabs] = useState<EditorState[]>([])
  const [activeTab, setActiveTab] = useState<string | null>(null)
  // Which of the two the narrow layout is showing. Only that layout reads it —
  // wide shows all three panes at once — but it has to live here because
  // opening a file is what flips it, and closing the last tab is not the only
  // way back: with two files open there would otherwise be no way to reach the
  // listing without closing both.
  const [narrowPane, setNarrowPane] = useState<'list' | 'editor'>('list')
  // The file pane has two jobs — managing files, and reading or writing one —
  // and they want opposite layouts. Editing mode is the second one: the listing
  // steps aside, the tree takes over answering "what is in here", and the
  // editor gets the width that was being spent on a column of file sizes.
  //
  // Deliberately not persisted. Landing on 文件 in a mode set last week, with
  // no listing and no toolbar, is a page that looks broken.
  const [editing, setEditing] = useState(false)
  // Edit mode's own filter, separate from the listing's 在当前目录中查找: that
  // one filters rows of one directory, this one filters the tree — and only
  // what the tree has already read.
  const [treeQuery, setTreeQuery] = useState('')

  // Leaving the section leaves the mode. It could be remembered instead, but
  // then coming back to 文件 would land on a page with no listing and no
  // toolbar — the same thing persisting it would do.
  useEffect(() => {
    if (!active) setEditing(false)
  }, [active])

  // The shell follows the mode, and gets it back on the way out. Unmounting is
  // the path a route change takes, and it has to hand the rail back too.
  useEffect(() => {
    onWorkspaceChange?.(editing)
  }, [editing, onWorkspaceChange])

  useEffect(() => () => onWorkspaceChange?.(false), [onWorkspaceChange])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  // Said on the buttons rather than as a banner: the answer people want is
  // "why is this greyed out", asked with the pointer already on it.
  const readOnlyHere = '这个目录不在你的角色允许的范围内'
  const [dragging, setDragging] = useState(false)

  // What the list is showing, as opposed to what the directory holds: the
  // filter box and the column ordering. Both are view state, so they survive a
  // refresh but not a change of directory (see load).
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<Sort>({ key: 'name', asc: true })
  const [selected, setSelected] = useState<Set<string>>(new Set())

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

  const editor = useMemo(
    () => tabs.find((tab) => tab.path === activeTab) ?? null,
    [tabs, activeTab],
  )

  // The tabs already show this; the tree shows it too because in edit mode the
  // tree is what gets scanned, and an unsaved file you cannot see is one you
  // lose by walking away from the page.
  const dirtyPaths = useMemo(
    () => new Set(tabs.filter((tab) => tab.content !== tab.original).map((tab) => tab.path)),
    [tabs],
  )

  /** Brings a file to the front, opening a tab for it if it has none. A file
   *  already open is never re-read: it may have unsaved edits in it. */
  const openEditor = useCallback((next: EditorState) => {
    setTabs((current) =>
      current.some((tab) => tab.path === next.path) ? current : [...current, next],
    )
    setActiveTab(next.path)
    setNarrowPane('editor')
  }, [])

  /** Edits the tab in front. */
  const patchActive = useCallback(
    (patch: Partial<EditorState>) => {
      setTabs((current) =>
        current.map((tab) => (tab.path === activeTab ? { ...tab, ...patch } : tab)),
      )
    },
    [activeTab],
  )

  const closeAll = useCallback(() => {
    setTabs([])
    setActiveTab(null)
    setNarrowPane('list')
  }, [])

  /** Opens a file in the editor by path, for callers that never had a row to
   *  click — the jump from 配置历史 arrives with a path and nothing else. */
  const openPath = useCallback(
    async (path: string) => {
      try {
        const file = await api.readFile(instance.id, path)
        openEditor({ path, content: file.content, original: file.content })
        setError(null)
      } catch (err) {
        setError(err instanceof Error ? err.message : '打开文件失败')
      }
    },
    [instance.id],
  )

  useEffect(() => {
    closeAll()
    void load('')
  }, [instance.id, load, closeAll])

  // Keyed on the token alone: the path is read when it fires, and adding it to
  // the dependencies would re-navigate on an unrelated render that happened to
  // recreate the object.
  const jumpTo = useRef(jump)
  jumpTo.current = jump
  useEffect(() => {
    if (jump?.token === undefined) return
    closeAll()
    const target = jumpTo.current
    void (async () => {
      await load(target?.path ?? '')
      // The directory first, always: if the file turns out to be unreadable —
      // binary, or deleted between the click and the request — the operator is
      // at least standing where it should be.
      if (target?.file) await openPath(target.file)
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jump?.token, load])

  // Bumped so the tree drops what it cached: 刷新 means "read the disk again",
  // and a tree still showing a folder that was deleted elsewhere is exactly
  // what the button is pressed about.
  const [treeKey, setTreeKey] = useState(0)
  const refresh = () => {
    setTreeKey((key) => key + 1)
    void load(dir)
  }

  // Escape leaves the mode. Not while typing: Escape in the editor is how the
  // browser's own find bar is dismissed, and in a filter box it clears the box
  // — neither should throw the whole layout away.
  useEffect(() => {
    if (!editing) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      const target = event.target as HTMLElement | null
      if (
        target?.isContentEditable ||
        (target != null && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
      ) {
        return
      }
      setEditing(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [editing])

  /**
   * "Type a name", as a promise — the same shape as `ask` in confirm.ts, and
   * for the same reason. window.prompt cannot tell a name that is already
   * taken from one that is free until the request comes back a 409, and on a
   * phone it arrives as browser chrome with the panel's own address above it.
   *
   * It stays local while the yes/no half moved out: a confirmation is the same
   * card everywhere, whereas this one is the file pane's, down to the list of
   * names already in the directory and the way it selects around a file
   * extension.
   */
  const askName = useCallback(
    (request: Omit<NameState, 'resolve'>) =>
      new Promise<string | null>((resolve) => setNaming({ ...request, resolve })),
    [],
  )

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
          toast('已取消上传')
          return
        }
        setProgress(0)
        await uploadFiles(instance.id, dir, files, setProgress, true)
      }
      toast(files.length === 1 ? `已上传 ${files[0].name}` : `已上传 ${files.length} 个文件`)
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
      toast(done)
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
      openEditor({ path, content: '', original: '' })
      toast(`已创建 ${name}`)
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
   * The row actions the listing keeps in its 操作 column, for a tree that is
   * standing in for the listing.
   *
   * Deliberately not reusing `rename`/`remove`: those two are written for a row
   * of the *current* directory and join new names onto `dir`. A tree row can be
   * three levels away from where the listing is standing, and renaming
   * plugins/Foo/bar.yml would have moved it to the root.
   */
  const renameInTree = async (node: TreeNode) => {
    const parent = parentOf(node.path)
    // Names already in that directory, so a clash is caught in the dialog
    // rather than as a 409 afterwards. One extra listing on a rename is
    // cheaper than the round trip it saves.
    let taken: string[] = takenNames
    if (parent !== dir) {
      try {
        taken = (await api.listFiles(instance.id, parent)).entries.map((entry) => entry.name)
      } catch {
        // Unreadable from here: let the server be the one to refuse.
        taken = []
      }
    }
    const next = await askName({
      title: `重命名${node.isDir ? '文件夹' : '文件'}`,
      label: '新名称',
      initial: node.name,
      confirmLabel: '重命名',
      taken,
    })
    if (!next || next === node.name) return
    await guard(
      () => api.renameFile(instance.id, node.path, joinPath(parent, next)),
      `已重命名为 ${next}`,
    )
    retab(node.path, joinPath(parent, next))
    setTreeKey((key) => key + 1)
  }

  const removeInTree = async (node: TreeNode) => {
    const ok = await ask({
      title: `删除${node.isDir ? '文件夹' : '文件'}「${node.name}」？`,
      lead: node.isDir
        ? '文件夹里的所有内容会一起删除，无法撤销。'
        : '删除后无法撤销，请确认这不是存档或配置。',
      confirmLabel: '删除',
      danger: true,
    })
    if (!ok) return
    await guard(() => api.deleteFile(instance.id, node.path), `已删除 ${node.name}`)
    retab(node.path, null)
    setTreeKey((key) => key + 1)
  }

  /**
   * Follows a path that moved or went away through the open tabs.
   *
   * A tab left pointing at a file that is no longer there fails at save time,
   * which is the worst possible moment to find out — the text is in the box and
   * the file it belongs to is gone. `to` of null closes them instead.
   */
  const retab = (from: string, to: string | null) => {
    const moved = (path: string) =>
      path === from ? to : path.startsWith(`${from}/`) && to !== null
        ? to + path.slice(from.length)
        : path.startsWith(`${from}/`)
          ? null
          : path
    setTabs((current) =>
      current
        .map((tab) => {
          const next = moved(tab.path)
          return next === null ? null : { ...tab, path: next }
        })
        .filter((tab): tab is EditorState => tab !== null),
    )
    setActiveTab((current) => (current === null ? null : moved(current)))
  }

  const treeMenu = useCallback(
    (node: TreeNode): MenuItem[] => [
      {
        label: '重命名',
        disabled: busy || !listing?.writable,
        onSelect: () => void renameInTree(node),
      },
      {
        label: '复制路径',
        onSelect: () => {
          void navigator.clipboard?.writeText(node.path)
          toast(`已复制 ${node.path}`)
        },
      },
      ...(node.isDir
        ? []
        : [
            {
              label: '下载',
              onSelect: () => {
                const link = document.createElement('a')
                link.href = downloadURL(instance.id, node.path)
                link.download = node.name
                link.click()
              },
            },
          ]),
      {
        label: '删除',
        danger: true,
        disabled: busy || !listing?.writable,
        onSelect: () => void removeInTree(node),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [busy, listing?.writable, instance.id, dir, takenNames],
  )

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
    if (done > 0) toast(`已删除 ${done} 项`)
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
    toast(`已开始下载 ${files.length} 个文件`)
  }

  const openEntry = async (entry: FileEntry) => {
    if (entry.isDir) {
      void load(entry.path)
      return
    }
    if (isImage(entry.name) || isSchematic(entry.name)) {
      setPreview(entry)
      return
    }
    if (!entry.editable) return
    await openPath(entry.path)
  }

  const saveEditor = async () => {
    if (!editor || editor.content === editor.original) return
    setBusy(true)
    setError(null)
    try {
      await api.writeFile(instance.id, editor.path, editor.content)
      patchActive({ original: editor.content })
      toast(`已保存 ${baseName(editor.path)}`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  /** Closes one tab, asking first if it still holds unsaved edits. */
  const closeTab = async (path: string) => {
    const target = tabs.find((tab) => tab.path === path)
    if (target && target.content !== target.original) {
      const ok = await ask({
        title: '放弃未保存的修改？',
        lead: `${baseName(path)} 有改动还没有保存，关掉这个标签会丢掉它们。`,
        confirmLabel: '放弃修改',
        danger: true,
      })
      if (!ok) return
    }
    const index = tabs.findIndex((tab) => tab.path === path)
    const rest = tabs.filter((tab) => tab.path !== path)
    setTabs(rest)
    if (activeTab === path) {
      // The neighbour on the left, or the new first one: closing the tab you
      // were reading should leave you next to where you were, not nowhere.
      setActiveTab(rest[Math.max(0, index - 1)]?.path ?? null)
    }
  }

  const closeEditor = () => void closeTab(activeTab ?? '')

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
      {naming && <NameDialog request={naming} onAnswer={settleName} />}
      {preview &&
        (isSchematic(preview.name) ? (
          <SchematicPreview
            instanceId={instance.id}
            entry={preview}
            onClose={() => setPreview(null)}
          />
        ) : (
          <ImagePreview
            instanceId={instance.id}
            entry={preview}
            onClose={() => setPreview(null)}
          />
        ))}
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

  const folders = entries.filter((entry) => entry.isDir).length
  const files = entries.length - folders
  const totalBytes = entries.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0)

  return (
    <div
      className={editing ? 'stack stack--full' : 'stack'}
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
      {/* The mode's whole point is vertical room, and the title plus the
          sentence under it are the first eight lines it buys back. */}
      {!editing && (
        <PageHead
          title="文件"
          lead="服务器目录里的东西：jar、存档、配置和日志。点一个文件直接打开，可以同时开着几个对照。"
        />
      )}

      {/* Tree, listing, editor. The editor used to replace the listing — one
          file at a time, and comparing two configs meant closing the first and
          remembering what it said. `data-pane` is what the narrow layout reads:
          below 1024 there is only room for one of these, and which one depends
          on whether anything is open. */}
      <div
        className={editing ? 'fm fm--editing' : 'fm'}
        data-pane={editor ? narrowPane : 'list'}
      >
        <aside className="fm__tree">
          {/* In edit mode the listing's toolbar is off screen, so the four
              buttons that are pressed daily come here. They act on the
              directory the tree is standing in, which is why it is named just
              below them: a 上传 that writes into an unnamed directory is a
              button nobody presses twice. */}
          {editing && (
            <>
              <div className="ftree__bar">
                <button
                  className="btn btn--icon"
                  onClick={() => fileInput.current?.click()}
                  disabled={busy || !listing.writable}
                  title={listing.writable ? '上传到当前目录' : readOnlyHere}
                  aria-label="上传文件"
                >
                  <Glyph name="upload" />
                </button>
                <button
                  className="btn btn--icon"
                  onClick={() => void createFile()}
                  disabled={busy || !listing.writable}
                  title={listing.writable ? '新建文件' : readOnlyHere}
                  aria-label="新建文件"
                >
                  <Glyph name="new-file" />
                </button>
                <button
                  className="btn btn--icon"
                  onClick={() => void createFolder()}
                  disabled={busy || !listing.writable}
                  title={listing.writable ? '新建文件夹' : readOnlyHere}
                  aria-label="新建文件夹"
                >
                  <Glyph name="new-folder" />
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
                <button
                  className="btn btn--icon ftree__leave"
                  onClick={() => setEditing(false)}
                  title="退出编辑模式（Esc）"
                  aria-label="退出编辑模式"
                >
                  <Glyph name="up" />
                </button>
              </div>
              <p className="ftree__where" title={dir || '实例根目录'}>
                {dir === '' ? '实例根目录' : dir}
              </p>
              <input
                className="ftree__find"
                type="search"
                value={treeQuery}
                placeholder="筛选已展开的目录"
                aria-label="筛选已展开的目录"
                onChange={(event) => setTreeQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Escape') setTreeQuery('')
                }}
              />
            </>
          )}
          <FileTree
            instanceId={instance.id}
            path={dir}
            reloadKey={treeKey}
            showFiles={editing}
            openPath={activeTab}
            dirtyPaths={dirtyPaths}
            filter={editing ? treeQuery : ''}
            menuFor={editing ? treeMenu : undefined}
            onOpen={(next) => void load(next)}
            onOpenFile={(next) => void openPath(next)}
          />
        </aside>

      {/* Gone rather than narrowed in edit mode: a column of file sizes
          beside an open config is the width that was making the config
          scroll sideways. */}
      {!editing && (
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
            {/* A confined role can walk through the folders on the way to the one
                it may edit, but not write in them. Offering the buttons there
                would be offering a request the panel refuses. */}
            <button
              className="btn btn--primary"
              onClick={() => fileInput.current?.click()}
              disabled={busy || !listing.writable}
              title={listing.writable ? undefined : readOnlyHere}
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
            <button
              className="btn"
              disabled={busy || !listing.writable}
              title={listing.writable ? undefined : readOnlyHere}
              onClick={() => void createFolder()}
            >
              <Glyph name="new-folder" />
              新建文件夹
            </button>
            <button
              className="btn"
              disabled={busy || !listing.writable}
              title={listing.writable ? undefined : readOnlyHere}
              onClick={() => void createFile()}
            >
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

            <button
              className="btn"
              onClick={() => setEditing(true)}
              title="把这一屏交给编辑器：列表让位，目录树带上文件"
            >
              <Glyph name="doc" />
              编辑模式
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
            {/* The path and the promise about it are two elements rather than one
                line, because one line means the path's ellipsis eats the promise:
                on a phone the whole "所有操作都被限制在这个目录内" disappeared and
                only a truncated path was left. */}
            <span className="files__root" title={listing.root}>
              <code>{listing.root}</code>
              {/* Two different promises. Without a role rule the honest sentence
                  is the instance directory; with one it is narrower, and saying
                  the wider thing would be telling somebody they can reach files
                  the panel will refuse them. */}
              <span className="files__root-note">
                {listing.scope.length > 0
                  ? `· 你的角色只能操作 ${listing.scope.join('、')}`
                  : '· 所有操作都被限制在这个目录内'}
              </span>
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
      )}

        <div className="fm__editor">
          {editor ? (
            <FileEditor
              editor={editor}
              tabs={tabs}
              activeTab={activeTab}
              onBackToList={() => setNarrowPane('list')}
              onSelectTab={(path: string) => setActiveTab(path)}
              onCloseTab={(path: string) => void closeTab(path)}
              busy={busy}
              error={error}
              onChange={(content) => patchActive({ content })}
              onSave={() => void saveEditor()}
              onRevert={() => patchActive({ content: editor.original })}
              onClose={() => void closeEditor()}
              onOpenHistory={onOpenHistory && (() => onOpenHistory(editor.path))}
            />
          ) : (
            // A placeholder rather than a collapsed column: the listing beside
            // it would otherwise jump a few hundred pixels wider the moment
            // anything is opened, on every open and every close.
            <div className="fm__blank">
              <Glyph name="doc" />
              <p>
                {editing
                  ? '从左边的目录树里点一个文件，会在这里打开。'
                  : '从中间的列表里点一个文件，会在这里打开。'}
              </p>
              <p className="muted">可以同时开着几个，用上面的标签切换。</p>
            </div>
          )}
        </div>
      </div>

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
  const openable = entry.isDir || entry.editable || isImage(entry.name) || isSchematic(entry.name)

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
          <FileIcon name={entry.name} dir={entry.isDir} />
          <button
            className={`file-link${openable ? '' : ' file-link--plain'}`}
            onClick={onOpen}
            disabled={!openable}
            title={
              entry.isDir
                ? '打开目录'
                : entry.editable
                  ? '编辑'
                  : isSchematic(entry.name)
                    ? '预览建筑'
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
          {/* A folder a confined role can only see because it leads to the one
              it may edit is not renamable or deletable — the panel refuses
              both, so the buttons say so before the click. */}
          <button
            className="iconbtn"
            disabled={busy || !entry.writable}
            onClick={onRename}
            title={entry.writable ? '重命名' : '这一项不在你的角色允许的范围内'}
            aria-label={`重命名 ${entry.name}`}
          >
            <Glyph name="rename" />
          </button>
          <button
            className="iconbtn iconbtn--danger"
            disabled={busy || !entry.writable}
            onClick={onDelete}
            title={entry.writable ? '删除' : '这一项不在你的角色允许的范围内'}
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
  tabs,
  activeTab,
  onBackToList,
  onSelectTab,
  onCloseTab,
  busy,
  error,
  onChange,
  onSave,
  onRevert,
  onClose,
  onOpenHistory,
}: {
  editor: EditorState
  tabs: EditorState[]
  activeTab: string | null
  /** Narrow layouts only: the listing is off screen there, and closing every
   *  tab must not be the only way back to it. */
  onBackToList: () => void
  onSelectTab: (path: string) => void
  onCloseTab: (path: string) => void
  busy: boolean
  error: string | null
  onChange: (content: string) => void
  onSave: () => void
  onRevert: () => void
  onClose: () => void
  onOpenHistory?: () => void
}) {
  const dirty = editor.content !== editor.original
  const gutter = useRef<HTMLDivElement | null>(null)
  // Where the caret is, for the status line. Read off the textarea on every
  // event that can move it rather than derived from the content: a click and
  // an arrow key both move it without changing a character.
  const [caret, setCaret] = useState(0)

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
      <section className="panel editor-pane">
        {/* One row per open file. The dot is the only unsaved indicator that
            survives switching away — the 有未保存的修改 line below only ever
            describes the file in front. */}
        {/* Only on the layout that hides the listing. */}
        <button type="button" className="editor__back" onClick={onBackToList}>
          ← 文件列表
        </button>

        <div className="etabs" role="tablist" aria-label="打开的文件">
          {tabs.map((tab) => (
            <div
              className={`etabs__tab${tab.path === activeTab ? ' etabs__tab--on' : ''}`}
              key={tab.path}
            >
              <button
                type="button"
                role="tab"
                aria-selected={tab.path === activeTab}
                className="etabs__pick"
                onClick={() => onSelectTab(tab.path)}
                title={tab.path}
              >
                <FileIcon name={baseName(tab.path)} />
                {baseName(tab.path)}
                {tab.content !== tab.original && (
                  <span className="etabs__dot" aria-label="有未保存的修改" />
                )}
              </button>
              <button
                type="button"
                className="etabs__close"
                onClick={() => onCloseTab(tab.path)}
                aria-label={`关闭 ${baseName(tab.path)}`}
              >
                ×
              </button>
            </div>
          ))}
        </div>

        {/* Only when it says something the tab does not: a file in the root
            would otherwise print its own name twice. */}
        {editor.path !== baseName(editor.path) && (
          <p className="editor__path">{editor.path}</p>
        )}

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
            onSelect={(event) => setCaret(event.currentTarget.selectionStart)}
            onClick={(event) => setCaret(event.currentTarget.selectionStart)}
            onKeyUp={(event) => setCaret(event.currentTarget.selectionStart)}
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

        {/* What an editor's foot is for: the facts you check before saving,
            none of which are worth a line of prose. 行/列 is here because the
            thing that sends someone to this box is usually a console line
            ending in "at line 42". */}
        <div className="editor__status">
          <span>{languageOf(editor.path)}</span>
          <span>UTF-8</span>
          <span>{editor.content.includes('\r\n') ? 'CRLF' : 'LF'}</span>
          <span>
            行 {position(editor.content, caret).line}，列 {position(editor.content, caret).column}
          </span>
          <span className="editor__status-right">
            {formatBytes(bytes)}
            {lines > 0 && ` · ${lines} 行`}
          </span>
          <span className={dirty ? 'editor__dot editor__dot--dirty' : 'editor__dot'}>
            {dirty ? '有未保存的修改' : '已是最新'}
          </span>
        </div>

        {error && <div className="alert alert--error">{error}</div>}

        <div className="actions">
          <button className="btn btn--primary" onClick={onSave} disabled={busy || !dirty}>
            保存
          </button>
          <button className="btn" onClick={onRevert} disabled={busy || !dirty}>
            撤销修改
          </button>
          {/* The question you ask right before editing a config you did not
              write: what did this look like before, and who moved it. */}
          {onOpenHistory && (
            <button className="btn" onClick={onOpenHistory} title="在配置历史里比较这个文件">
              配置历史
            </button>
          )}
          <span className="editor__hint">Ctrl / ⌘ + S 也能保存</span>
          <button className="btn" onClick={onClose}>
            关闭
          </button>
        </div>
      </section>
    </div>
  )
}

/* --------------------------------------------------------------- dialogs */

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

/** Only the types the panel serves inline; see previewTypes in handlers_fs.go.
 *  SVG is deliberately not among them, so it is a download here too. */
const PREVIEWABLE = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.bmp', '.ico'])

function isImage(name: string): boolean {
  return PREVIEWABLE.has(extensionOf(name))
}

/** The two WorldEdit formats the daemon can read; see IsSchematic in
 *  internal/api/handlers_schem.go, which this has to agree with. */
const SCHEMATICS = new Set(['.schem', '.schematic'])

function isSchematic(name: string): boolean {
  return SCHEMATICS.has(extensionOf(name))
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

/** Line and column of an offset, both 1-based, the way an editor counts. */
function position(text: string, offset: number): { line: number; column: number } {
  const before = text.slice(0, Math.min(offset, text.length))
  const lines = before.split('\n')
  return { line: lines.length, column: lines[lines.length - 1].length + 1 }
}

/** What the status line calls this file. Extension only — the panel does not
 *  parse these, and claiming to would be claiming a syntax check it has not
 *  got. */
function languageOf(path: string): string {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  const known: Record<string, string> = {
    yml: 'YAML',
    yaml: 'YAML',
    json: 'JSON',
    properties: 'Properties',
    toml: 'TOML',
    conf: 'Conf',
    cfg: 'Conf',
    txt: '纯文本',
    log: '日志',
    sh: 'Shell',
    md: 'Markdown',
    kts: 'Kotlin Script',
  }
  return known[ext] ?? (ext ? ext.toUpperCase() : '纯文本')
}
