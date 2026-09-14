import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { ApiError, api, downloadURL, uploadFiles } from '../api'
import { ask } from '../confirm'
import { sideBySide } from '../filediff'
import { readPref, writePref } from '../localPrefs'
import { toast, toastError, toastWarn } from '../toast'
import { useMediaQuery } from '../useMediaQuery'
import { formatBytes } from '../format'
import type { FileEntry, FileListing, FileUsage, InstanceStatus } from '../types'
import { Button } from './Button'
import { extensionOf } from './FileIcon'
import { FileCrumbs, FileTools } from './FileBar'
import { FileEditor } from './FileEditor'
import type { EditorPane, FileKind, Layout, OpenFile } from './FileEditor'
import { FileList } from './FileList'
import type { SelectMode, Sort, SortKey } from './FileList'
import { FileRecentPanel } from './FileRecentPanel'
import { FileSearchPanel } from './FileSearchPanel'
import { FileSidebar } from './FileSidebar'
import type { SidePanel } from './FileSidebar'
import { FileTree } from './FileTree'
import { GoToFile } from './GoToFile'
import type { MenuItem } from './Menu'
import { Modal } from './Modal'
import { Note } from './Note'
import { SchematicPreview } from './SchematicPreview'
import { Skeleton, SkeletonPanel, SkeletonRows, SkeletonScreen } from './Skeleton'
import { StatusSlot } from './StatusBar'

/**
 * The file pane: a sidebar that says where you are, and a main area that shows
 * what you are looking at. No mode switch.
 *
 * There used to be one, called 编辑模式, and it did three unrelated things at
 * once — folded the shell to its rail, swapped the tree from folders to
 * folders-and-files, and gave the editor the listing's width. Reading code
 * therefore began by pressing a button named after a state rather than after a
 * job. What that button was really being asked for is here instead, as a fact
 * rather than a setting: with nothing open the main area is the directory, and
 * opening a file makes it the editor, because that is what the screen is now
 * for. Closing the last tab puts the directory back.
 *
 * The arrangement before this one had the tree and the listing stacked in one
 * rail. They were fighting over its height — the deeper the tree the less room
 * the listing had, at exactly the point the listing was longest — and the rail
 * was too narrow for the listing's four columns either way, which is where the
 * 紧凑 / 详情 switch came from. Moving the listing into the main area settles
 * all three: the sidebar is only ever about location, the listing has room for
 * its columns, and there is no density to choose.
 *
 * The editor's floor is not a round number. Paper's own bukkit.yml opens with
 * an 85-character comment, and at 12.5px monospace anything under about 620px
 * starts that file on a horizontal scrollbar.
 */

/** `jump` is a directory another page wants opened here.
 *
 * A token rather than a bare path because the pane stays mounted: the plugin
 * list's 配置 link has to work the second time it is pressed on the same
 * plugin, and a path that has not changed would not re-trigger anything. */
export interface FileJump {
  path: string
  token: number
  /** A file inside `path` to open on arrival. 配置历史 sends one: landing in
   *  the right directory is not the answer to "let me edit the file I was
   *  just looking at the diff of". */
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

/** A delete that has to be typed out, and what it is about. */
interface TypedDelete {
  entry: FileEntry
  resolve: (ok: boolean) => void
}

/** A save that found the file changed underneath it. */
interface Conflict {
  path: string
  mine: string
  theirs: string
}

/**
 * Drops any group that has run out of tabs.
 *
 * The first one stays whatever happens — it is the one the empty state belongs
 * to, and with nothing open at all the main area is the directory listing
 * rather than an editor. Shared by closing a tab and by dragging one into the
 * other group, because two code paths that each decide what an empty group
 * means will eventually decide differently.
 */
function prunePanes(panes: EditorPane[]): EditorPane[] {
  return panes.filter((pane, index) => index === 0 || pane.tabs.length > 0)
}

/** Long enough for the browser to start one download before the next click. */
const DOWNLOAD_GAP = 400

/** How wide the sidebar may be dragged, and where it starts. */
const SIDE_MIN = 240
const SIDE_DEFAULT = 300
const SIDE_MAX = 480

/**
 * The floor under one editor group: 82 monospace columns, which is the longest
 * comment line in Paper's and Spigot's own default configs. It is enforced
 * while dragging rather than in CSS, because CSS cannot say "take it out of
 * whichever rail is being dragged" — a minmax floor on the editor's own track
 * overflows the grid and pushes the pane sideways instead of stopping the drag.
 */
const GROUP_MIN = 620

/* The grid has no gap: the handle is its own 14px gutter track. See .fm. */
const GRIP = 14

/**
 * The four widths the pane changes shape at, from the design note.
 *
 * Viewport widths rather than column widths, and deliberately: what matters is
 * how much room there is in total. A column somebody has dragged narrow is a
 * choice, and a layout that reacted to it would be arguing with them.
 */
/**
 * Where two side-by-side groups can both clear their 620px floor *and* leave
 * the sidebar standing.
 *
 * Derived rather than picked: 2×620 of code, two 46px gutters, the 12px
 * between the groups, the 300px sidebar with its 14px handle, the main area's
 * own padding, and the shell's 56px rail. The design note's table says the
 * sidebar survives a split from 1600px up, and its hard floor says a group is
 * never under 620px — at 1600 those two cannot both be true, and the floor is
 * the one written down twice. So the fold happens by room rather than by the
 * round number.
 */
const ROOMY = '(min-width: 1860px)'
const SNUG = '(max-width: 1360px)'
const NO_COL_SPLIT = '(max-width: 1280px)'
/** Kept in step with App.tsx's DRAWER_QUERY and the media query in
 *  styles.css. All three move together. */
const DRAWER = '(max-width: 1024px)'

/** How many files the 最近 panel and the empty ⌘P palette remember. */
const MAX_RECENT = 20

/**
 * Twelve is where a tab strip stops being a strip and becomes a list you
 * scroll to search. Past it the oldest *saved* tab goes — never one with
 * unsaved work in it, which is the one thing a cap must not be allowed to
 * throw away.
 */
const MAX_TABS = 12

/** Folders whose contents are not replaceable from a download page. Deleting
 *  one is still allowed; it just does not get to look like deleting `cache`. */
const PRECIOUS = new Set(['world', 'world_nether', 'world_the_end', 'plugins', 'libraries'])

/** The configs a running server has already read, which therefore keep serving
 *  the old values until it restarts. Paper's are matched by prefix because the
 *  set of paper-*.yml files changes between versions. */
function needsRestart(path: string): boolean {
  const name = path.slice(path.lastIndexOf('/') + 1)
  if (name === 'server.properties' || name === 'bukkit.yml' || name === 'spigot.yml') return true
  if (name.startsWith('paper-') && name.endsWith('.yml')) return true
  return /^plugins\/[^/]+\/config\.ya?ml$/.test(path)
}

export function FileManager({
  instance,
  active,
  jump,
  onOpenHistory,
}: {
  instance: InstanceStatus
  /** Whether this section is the one on screen. Sections stay mounted behind
   *  whatever replaced them (see InstanceView), so "no longer visible" is not
   *  the same event as unmounting — and the keyboard and the poll both have to
   *  stand down on the first of the two. */
  active: boolean
  jump?: FileJump
  /** Sends the open file to 配置历史. Absent when nothing upstream can switch
   *  sections, and when the panel has no config history at all. */
  onOpenHistory?: (path: string) => void
}) {
  const [dir, setDir] = useState('')
  const [listing, setListing] = useState<FileListing | null>(null)
  // One thing only: this directory would not load. Everything else that can
  // fail here — a save, an upload, a delete — is something somebody just did,
  // and that goes to the corner rather than to a slot at the top of a page
  // they may already have scrolled past.
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  const [pending, setPending] = useState(false)
  const [treeKey, setTreeKey] = useState(0)

  // What the list is showing, as opposed to what the directory holds.
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<Sort>({ key: 'name', asc: true })
  // Two different things, and keeping them apart is what stops the bulk bar
  // from appearing every time somebody opens a file. `cursor` is where the
  // keyboard is and what a plain click moved; `selected` is what has actually
  // been ticked, and it is what 批量操作 acts on.
  const [cursor, setCursor] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  // Where a Shift-click measures from: the last row the cursor landed on.
  const anchor = useRef<string | null>(null)

  // One buffer per path; the panes hold paths. The same file in both halves of
  // a split is therefore one piece of text rather than two that drift.
  const [files, setFiles] = useState<Map<string, OpenFile>>(() => new Map())
  const [panes, setPanes] = useState<EditorPane[]>([{ tabs: [], active: null }])
  const [focusedPane, setFocusedPane] = useState(0)

  const [naming, setNaming] = useState<NameState | null>(null)
  const [typed, setTyped] = useState<TypedDelete | null>(null)
  const [moving, setMoving] = useState<FileEntry[] | null>(null)
  const [conflict, setConflict] = useState<Conflict | null>(null)
  const [keys, setKeys] = useState(false)
  const [preview, setPreview] = useState<FileEntry | null>(null)
  // Bumped by the shortcut, to ask the editor to open its find box.
  const [findTick, setFindTick] = useState(0)

  const fileInput = useRef<HTMLInputElement | null>(null)
  const searchBox = useRef<HTMLInputElement | null>(null)
  const listBody = useRef<HTMLDivElement | null>(null)
  const grid = useRef<HTMLDivElement | null>(null)
  const dirRef = useRef('')

  // Whether anything is open is the whole of the A/B decision. No switch, no
  // preference, nothing to remember: the layout is a fact about the work.
  const open = panes.some((pane) => pane.tabs.length > 0)

  const roomy = useMediaQuery(ROOMY)
  const snug = useMediaQuery(SNUG)
  const noColumnSplit = useMediaQuery(NO_COL_SPLIT)
  const narrow = useMediaQuery(DRAWER)

  // How wide the sidebar is, whether it is folded, and which of its three
  // panels is showing — one record because they are one control. A new key
  // rather than a migration of the old {width,split,tree,list} pair, which
  // described a layout that no longer exists.
  const sideKey = `hc.files.side.${instance.id}`
  const [side, setSide] = useState(() =>
    readPref<{ width: number; collapsed: boolean; panel: SidePanel }>(sideKey, {
      width: SIDE_DEFAULT,
      collapsed: false,
      panel: 'tree',
    }),
  )
  useEffect(() => {
    writePref(sideKey, side)
  }, [sideKey, side])

  /**
   * Whether the reader has had an opinion about the sidebar this session.
   *
   * The layout folds the sidebar when a split needs the room and puts it back
   * afterwards, which is right until somebody disagrees — and then it has to
   * stop, or every ⌘B is undone by the next thing that happens. Deliberately
   * not persisted: it is an answer to "not now", not a setting. Cleared when
   * the split closes, which is when the argument is over.
   */
  const [pinned, setPinned] = useState(false)

  // How the editor groups are arranged, and how the room is divided between
  // them. Persisted with the sidebar because they are one answer to "how is
  // this instance's file page laid out".
  const layoutKey = `hc.files.layout.${instance.id}`
  const [layout, setLayout] = useState<Layout>(() =>
    readPref<Layout>(layoutKey, { dir: 'col', ratio: 0.5 }),
  )
  useEffect(() => {
    writePref(layoutKey, layout)
  }, [layoutKey, layout])

  // Which files this browser was last in, newest first. Per instance and per
  // browser: which files *you* were in is not a fact about the server.
  const recentKey = `hc.files.recent.${instance.id}`
  const [recent, setRecent] = useState<string[]>(() => readPref<string[]>(recentKey, []))
  useEffect(() => {
    writePref(recentKey, recent)
  }, [recentKey, recent])

  const [usage, setUsage] = useState<FileUsage | null>(null)
  const [goto, setGoto] = useState(false)
  /** A line the editor should put the caret on once the file is in front. A
   *  token rather than a bare number: clicking the same search hit twice has
   *  to work, and a line that has not changed would not re-trigger anything. */
  const [reveal, setReveal] = useState<{ path: string; line: number; token: number } | null>(null)
  /** Where the caret is in the focused group, for the shell's status bar. The
   *  groups report it upward rather than printing it: with two of them on
   *  screen there are two status lines and one caret. */
  const [caret, setCaret] = useState<{ line: number; column: number } | null>(null)
  const searchPanel = useRef<HTMLInputElement | null>(null)

  const sideWidth = Math.max(SIDE_MIN, Math.min(SIDE_MAX, side.width))

  /**
   * Whether the sidebar is floating over the main area rather than beside it.
   *
   * One rule: an open sidebar floats whenever the main area cannot spare its
   * width. That is the case below 1360px, where a 300px column and one editor
   * group at its 620px floor do not both fit, and it is also the case during a
   * side-by-side split under 1860px, where two groups need everything there is.
   *
   * Stated this way rather than as two separate behaviours because the second
   * one is what the floor is for: the sidebar can always be opened — by ⌘B, by
   * ⌘⇧F reaching for the search panel — and opening it must never be what
   * pushes a group under 620px. Picking something is what puts it away again;
   * see dismiss.
   */
  const overlay =
    !side.collapsed &&
    (snug || narrow || (panes.length > 1 && layout.dir === 'col' && !roomy))
  const overlayNow = useRef(overlay)
  overlayNow.current = overlay

  /** Puts a floating sidebar away. A no-op when it is a real column, which is
   *  what stops a click in the tree from folding the sidebar on a wide
   *  screen. */
  const dismiss = useCallback(() => {
    if (overlayNow.current) setSide((current) => ({ ...current, collapsed: true }))
  }, [])

  /** Remembers a file as recently opened. Newest first, deduplicated, capped. */
  const remember = useCallback((path: string) => {
    setRecent((current) => [path, ...current.filter((one) => one !== path)].slice(0, MAX_RECENT))
  }, [])

  const load = useCallback(
    async (target: string) => {
      setPending(true)
      try {
        setListing(await api.listFiles(instance.id, target))
        setDir(target)
        // The slot holds one thing — "this directory would not load" — so a
        // directory that did load is the only thing that clears it.
        setError(null)
        // A tick against a row that is no longer on screen is a delete waiting
        // to happen in a directory nobody is looking at.
        setSelected(new Set())
        setCursor(null)
        anchor.current = null
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

  /** Walks somewhere, saying whether it was there. The bar's path box needs
   *  the answer — a directory that does not exist has to leave what was typed
   *  on screen rather than clearing it and going quiet. */
  const navigate = useCallback(
    async (target: string): Promise<boolean> => {
      setPending(true)
      try {
        const next = await api.listFiles(instance.id, target)
        setListing(next)
        setDir(target)
        setError(null)
        setSelected(new Set())
        setCursor(null)
        anchor.current = null
        if (target !== dirRef.current) setQuery('')
        dirRef.current = target
        return true
      } catch {
        return false
      } finally {
        setPending(false)
      }
    },
    [instance.id],
  )

  const patchFile = useCallback((path: string, patch: Partial<OpenFile>) => {
    setFiles((current) => {
      const file = current.get(path)
      if (!file) return current
      const next = new Map(current)
      next.set(path, { ...file, ...patch })
      return next
    })
  }, [])

  const dirtyPaths = useMemo(() => {
    const out = new Set<string>()
    for (const file of files.values()) {
      if (file.content !== file.original) out.add(file.path)
    }
    return out
  }, [files])

  const activePath = panes[focusedPane]?.active ?? null

  /** Every path with a tab open anywhere, so the tree can mark them. */
  const openPaths = useMemo(() => new Set(panes.flatMap((pane) => pane.tabs)), [panes])

  // Read inside the callbacks rather than depended on: rebuilding them on
  // every keystroke would re-run the effects that are keyed to them.
  const filesNow = useRef(files)
  filesNow.current = files

  /* ---------------------------------------------------------- opening */

  const kindOf = useCallback(
    (entry: FileEntry): FileKind => {
      if (isImage(entry.name)) return 'image'
      if (entry.editable) return 'text'
      // The daemon says "not editable" for two different reasons and does not
      // say which, so the size decides: a .yml the editor refuses is one that
      // is too big, and a .jar is one it was never going to open.
      return entry.size > (listing?.maxEditableBytes ?? 0) && isTextName(entry.name)
        ? 'oversize'
        : 'binary'
    },
    [listing?.maxEditableBytes],
  )

  /**
   * Drops the oldest saved tab once the strip is full.
   *
   * Nothing with unsaved work in it is ever a candidate. A cap that can throw
   * away typing is a cap that eventually will, and being able to leave a file
   * half-edited while you go and check another one is most of what the strip
   * is for.
   */
  const capTabs = useCallback((tabs: string[]): string[] => {
    if (tabs.length <= MAX_TABS) return tabs
    const clean = tabs.find((path) => {
      const file = filesNow.current.get(path)
      return file !== undefined && file.content === file.original
    })
    return clean === undefined ? tabs : tabs.filter((path) => path !== clean)
  }, [])

  /** Puts a path in front, opening a tab for it if it has none. A file already
   *  open in one of the panes is brought forward there rather than duplicated
   *  into the other. */
  const showTab = useCallback(
    (path: string, background: boolean) => {
      setPanes((current) => {
        const held = current.findIndex((pane) => pane.tabs.includes(path))
        const into = held === -1 ? 0 : held
        return current.map((pane, index) => {
          if (index !== into) return pane
          const tabs = pane.tabs.includes(path) ? pane.tabs : capTabs([...pane.tabs, path])
          return { tabs, active: background && pane.active !== null ? pane.active : path }
        })
      })
    },
    [capTabs],
  )

  /**
   * The file's mtime, read out of its directory listing.
   *
   * The read endpoint returns content and nothing else, and the write endpoint
   * takes no precondition, so this is the only mtime the panel can get and the
   * comparison happens on this side rather than on the server's. That makes it
   * a check with a race in it: somebody can write the file between this
   * listing and the PUT that follows. It is strictly better than not looking —
   * the window is milliseconds rather than however long the tab was open — but
   * it is not a guarantee, and it should be replaced the day the API carries
   * an mtime or an ETag through read and write. See §9 of the design note in
   * docs/superpowers/specs.
   */
  const mtimeOf = useCallback(
    async (path: string): Promise<string | null> => {
      try {
        const listed = await api.listFiles(instance.id, parentOf(path))
        return listed.entries.find((entry) => entry.name === baseName(path))?.modified ?? null
      } catch {
        return null
      }
    },
    [instance.id],
  )

  const openPath = useCallback(
    async (path: string, opts: { background?: boolean; hint?: FileEntry; line?: number } = {}) => {
      const background = opts.background === true
      remember(path)
      if (opts.line !== undefined) {
        const line = opts.line
        setReveal((current) => ({ path, line, token: (current?.token ?? 0) + 1 }))
      }
      if (filesNow.current.has(path)) {
        showTab(path, background)
        dismiss()
        return
      }

      const hint = opts.hint
      const kind = hint ? kindOf(hint) : 'text'
      const modified = hint?.modified ?? (await mtimeOf(path)) ?? ''
      const size = hint?.size ?? 0

      let content = ''
      if (kind === 'text') {
        try {
          content = (await api.readFile(instance.id, path)).content
        } catch (err) {
          toastError(err instanceof Error ? err.message : '打开文件失败')
          return
        }
      }

      setFiles((current) => {
        const next = new Map(current)
        next.set(path, { path, content, original: content, modified, kind, size })
        return next
      })
      showTab(path, background)
      dismiss()
    },
    [instance.id, kindOf, mtimeOf, showTab, remember, dismiss],
  )

  const openEntry = useCallback(
    (entry: FileEntry, background = false) => {
      if (entry.isDir) {
        void load(entry.path)
        return
      }
      // A .schem is a thing you look at once rather than a thing you keep a
      // tab of, and the daemon renders it for us. The modal is the right shape
      // for that; a tab would not be.
      if (isSchematic(entry.name)) {
        setPreview(entry)
        return
      }
      void openPath(entry.path, { background, hint: entry })
    },
    [load, openPath],
  )

  /* ----------------------------------------------------------- saving */

  /** Restarting kicks everybody who is on the server, so it asks — even though
   *  the offer arrived on a toast the reader went looking for. */
  const restart = useCallback(async () => {
    const ok = await ask({
      title: '现在重启服务器？',
      lead: '在线的玩家会被断开，重启期间服务器无法进入。',
      confirmLabel: '重启',
    })
    if (!ok) return
    try {
      await api.power(instance.id, 'restart')
      toast('已请求重启')
    } catch (err) {
      toastError(err instanceof Error ? err.message : '重启失败')
    }
  }, [instance.id])

  const save = useCallback(
    async (path: string) => {
      const file = filesNow.current.get(path)
      if (!file || file.content === file.original || file.readOnly) return
      setBusy(true)
      try {
        const now = await mtimeOf(path)
        if (now !== null && file.modified !== '' && now !== file.modified) {
          const theirs = (await api.readFile(instance.id, path)).content
          setConflict({ path, mine: file.content, theirs })
          return
        }
        await api.writeFile(instance.id, path, file.content)
        patchFile(path, {
          original: file.content,
          modified: (await mtimeOf(path)) ?? file.modified,
          stale: false,
        })
        if (instance.state === 'running' && needsRestart(path)) {
          toast('已保存 · 需重启服务器后生效', {
            // Keyed so saving four configs in a row is one reminder rather
            // than four identical ones stacked in the corner.
            key: 'files-needs-restart',
            action: { label: '重启', onSelect: () => void restart() },
          })
        } else {
          toast(`已保存 ${baseName(path)}`)
        }
        // The row beside the editor is now showing the old size and the old
        // time for a file that was just written. Only when it is the directory
        // on screen: a save in plugins/Foo does not need the root re-read.
        if (parentOf(path) === dirRef.current) void load(dirRef.current)
      } catch (err) {
        toastError(err instanceof Error ? err.message : '保存失败')
      } finally {
        setBusy(false)
      }
    },
    [instance.id, instance.state, mtimeOf, patchFile, restart, load],
  )

  const reload = useCallback(
    async (path: string) => {
      try {
        const fresh = await api.readFile(instance.id, path)
        patchFile(path, {
          content: fresh.content,
          original: fresh.content,
          modified: (await mtimeOf(path)) ?? '',
          stale: false,
        })
      } catch (err) {
        toastError(err instanceof Error ? err.message : '重新读取失败')
      }
    },
    [instance.id, mtimeOf, patchFile],
  )

  const revert = async (path: string) => {
    const file = files.get(path)
    if (!file || file.content === file.original) return
    const ok = await ask({
      title: `丢弃 ${baseName(path)} 的未保存改动？`,
      lead: '编辑器会回到磁盘上的那一份，改动无法找回。',
      confirmLabel: '还原',
      danger: true,
    })
    if (!ok) return
    patchFile(path, { content: file.original })
  }

  /* ------------------------------------------------------------- tabs */

  const panesNow = useRef(panes)
  panesNow.current = panes

  /**
   * Rewrites one pane's tabs, keeps the front one sensible, and forgets any
   * buffer no pane is holding any more.
   *
   * The new arrangement is worked out here rather than inside a setPanes
   * updater. An updater has to be pure — React runs it twice in development —
   * and this has to move three pieces of state at once, so doing it in there
   * would mean the other two being set as a side effect of a function that is
   * allowed to be called again for no reason.
   */
  const dropTabs = useCallback((pane: number, rewrite: (tabs: string[]) => string[]) => {
    const next = panesNow.current.map((one, index) => {
      if (index !== pane) return one
      const was = one.tabs.indexOf(one.active ?? '')
      const tabs = rewrite(one.tabs)
      if (one.active !== null && tabs.includes(one.active)) return { tabs, active: one.active }
      // The neighbour on the left, or the new first one: closing the tab you
      // were reading should leave you next to where you were, not nowhere.
      return { tabs, active: tabs[Math.max(0, Math.min(was - 1, tabs.length - 1))] ?? null }
    })
    const kept = prunePanes(next)
    const live = new Set(kept.flatMap((one) => one.tabs))

    setPanes(kept)
    setFocusedPane((at) => Math.min(at, kept.length - 1))
    setFiles((held) => {
      if (held.size === live.size) return held
      const out = new Map<string, OpenFile>()
      for (const [path, file] of held) if (live.has(path)) out.set(path, file)
      return out
    })
  }, [])

  /**
   * Opens a second group, in the direction asked for.
   *
   * Two groups is the ceiling the UI offers; the shape is a list so that
   * raising it later is a number rather than a rewrite. Splitting an empty
   * group would produce two empty states side by side, so it needs a file.
   */
  const splitInto = useCallback((dir: 'col' | 'row') => {
    setLayout((current) => ({ ...current, dir }))
    setPanes((current) => {
      const front = current[0]?.active ?? null
      if (current.length >= 2 || front === null) return current
      return [...current, { tabs: [front], active: front }]
    })
    setFocusedPane(1)
  }, [])
  const splitNow = useRef(splitInto)
  splitNow.current = splitInto

  /**
   * Moves a tab from one group to another.
   *
   * It ends by running the same tidy-up dropTabs does — a group that has run
   * out of tabs stops being a group — through prunePanes, because two code
   * paths that each decide what an empty group means will eventually decide
   * differently.
   */
  const moveTab = useCallback((from: number, to: number, path: string) => {
    if (from === to) return
    const next = panesNow.current.map((pane, index) => {
      if (index === from) {
        const tabs = pane.tabs.filter((one) => one !== path)
        return {
          tabs,
          active: pane.active === path ? (tabs[0] ?? null) : pane.active,
        }
      }
      if (index === to) {
        const tabs = pane.tabs.includes(path) ? pane.tabs : [...pane.tabs, path]
        return { tabs, active: path }
      }
      return pane
    })
    const kept = prunePanes(next)
    setPanes(kept)
    setFocusedPane(Math.min(kept.length - 1, kept.findIndex((pane) => pane.tabs.includes(path))))
  }, [])

  const closeTab = useCallback(
    async (pane: number, path: string) => {
      const file = filesNow.current.get(path)
      if (file && file.content !== file.original) {
        const ok = await ask({
          title: '放弃未保存的修改？',
          lead: `${baseName(path)} 有改动还没有保存，关掉这个标签会丢掉它们。`,
          confirmLabel: '放弃修改',
          danger: true,
        })
        if (!ok) return
      }
      dropTabs(pane, (tabs) => tabs.filter((one) => one !== path))
    },
    [dropTabs],
  )

  const closeAll = useCallback(() => {
    setPanes([{ tabs: [], active: null }])
    setFocusedPane(0)
    setFiles(new Map())
  }, [])

  /**
   * Follows a path that moved or went away through the open tabs.
   *
   * A tab left pointing at a file that is no longer there fails at save time,
   * which is the worst possible moment to find out — the text is in the box and
   * the file it belongs to is gone. `to` of null closes them instead.
   */
  const retab = useCallback((from: string, to: string | null) => {
    const moved = (path: string): string | null => {
      if (path === from) return to
      if (!path.startsWith(`${from}/`)) return path
      return to === null ? null : to + path.slice(from.length)
    }
    setFiles((current) => {
      const out = new Map<string, OpenFile>()
      for (const [path, file] of current) {
        const next = moved(path)
        if (next !== null) out.set(next, { ...file, path: next })
      }
      return out
    })
    setPanes((current) =>
      current.map((pane) => {
        const tabs = pane.tabs.map(moved).filter((path): path is string => path !== null)
        const active = pane.active === null ? null : moved(pane.active)
        return {
          tabs,
          active: active !== null && tabs.includes(active) ? active : (tabs[0] ?? null),
        }
      }),
    )
  }, [])

  /* -------------------------------------------------------- lifecycle */

  useEffect(() => {
    closeAll()
    void load('')
  }, [instance.id, load, closeAll])

  /**
   * What the instance weighs, for the foot of the sidebar.
   *
   * Read once when the page opens and again after anything that writes,
   * because it is a full directory walk on the daemon's side — cached there
   * for half a minute, so the refreshes below mostly cost a map lookup. Not
   * polled: a size that is thirty seconds old is a size.
   */
  useEffect(() => {
    if (!active) return
    let alive = true
    api
      .fileUsage(instance.id)
      .then((next) => {
        if (alive) setUsage(next)
      })
      .catch(() => {
        // A figure the panel could not read is a figure it does not draw. The
        // page has nothing else riding on it.
      })
    return () => {
      alive = false
    }
  }, [active, instance.id, treeKey])

  /**
   * The sidebar gets out of the way when a split needs the room, and comes
   * back when the split closes — unless the reader has had an opinion this
   * session, in which case the layout stops having one.
   *
   * `pinned` is cleared when the split closes because that is when the
   * argument is over: the next split starts from the automatic behaviour
   * again, rather than from a decision made about a different screen.
   */
  useEffect(() => {
    if (panes.length > 1) {
      // Only a side-by-side split takes width from the sidebar. Stacked groups
      // take height, which the sidebar was not using.
      const wantsWidth = layout.dir === 'col'
      if (!pinned && wantsWidth && !roomy) setSide((current) => ({ ...current, collapsed: true }))
      return
    }
    setPinned(false)
    // Only the fold this effect made is undone. Below 1360 the column is an
    // overlay and folded is its resting state, so putting it back there would
    // be covering the file somebody just opened.
    if (!pinned && !snug) setSide((current) => ({ ...current, collapsed: false }))
  }, [panes.length, layout.dir, roomy, snug, pinned])

  /** Below the drawer width the sidebar starts folded: 300px out of 390 is not
   *  a column beside the main area, it is the main area. */
  useEffect(() => {
    if (narrow) setSide((current) => ({ ...current, collapsed: true }))
  }, [narrow])

  // Keyed on the token alone: the path is read when it fires, and adding it to
  // the dependencies would re-navigate on an unrelated render that happened to
  // recreate the object.
  const jumpTo = useRef(jump)
  jumpTo.current = jump
  useEffect(() => {
    if (jump?.token === undefined) return
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

  /**
   * Whether anything open has changed on disk behind the panel's back.
   *
   * A poll, and deliberately a lazy one. There is no file-change stream to
   * subscribe to — the WebSocket carries the console and nothing else — so the
   * alternatives were this or nothing at all. Ten seconds, only while this
   * section is the one on screen and the window has focus, and one listing per
   * directory rather than one request per open file.
   */
  useEffect(() => {
    if (!active || files.size === 0) return
    const tick = async () => {
      const dirs = new Set([...files.values()].map((file) => parentOf(file.path)))
      const seen = new Map<string, string>()
      for (const parent of dirs) {
        try {
          const listed = await api.listFiles(instance.id, parent)
          for (const entry of listed.entries) seen.set(entry.path, entry.modified)
        } catch {
          // A directory that has gone away is not this poll's news to break:
          // the next thing the operator does in it will say so, with a button
          // to press about it.
        }
      }
      for (const file of filesNow.current.values()) {
        const now = seen.get(file.path)
        if (now === undefined || now === file.modified) continue
        if (file.content === file.original) {
          // Nothing of theirs to lose, so take the new bytes without asking.
          // Somebody watching latest.log should see it grow.
          void reload(file.path)
        } else {
          patchFile(file.path, { stale: true })
        }
      }
    }
    const timer = window.setInterval(() => {
      if (document.hasFocus()) void tick()
    }, 10_000)
    return () => window.clearInterval(timer)
  }, [active, files, instance.id, patchFile, reload])

  /* --------------------------------------------------------- the list */

  const entries = useMemo(() => listing?.entries ?? [], [listing])
  const takenNames = useMemo(() => entries.map((entry) => entry.name), [entries])

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

  const pick = useCallback(
    (path: string, mode: SelectMode) => {
      setCursor(path)
      if (mode === 'replace') {
        // A plain click moves the cursor and lets go of whatever was ticked.
        // It does not tick this row: opening a file is not an instruction to
        // delete it, and the head above the list should still be the head.
        anchor.current = path
        setSelected((current) => (current.size === 0 ? current : new Set()))
        return
      }
      if (mode === 'toggle') {
        anchor.current = path
        setSelected((current) => {
          const next = new Set(current)
          if (!next.delete(path)) next.add(path)
          return next
        })
        return
      }
      setSelected((current) => {
        const from = rows.findIndex((entry) => entry.path === (anchor.current ?? path))
        const to = rows.findIndex((entry) => entry.path === path)
        if (from === -1 || to === -1) return new Set([path])
        const next = new Set(current)
        for (let i = Math.min(from, to); i <= Math.max(from, to); i++) next.add(rows[i].path)
        return next
      })
    },
    [rows],
  )

  const toggleSort = (key: SortKey) =>
    // Name reads best A→Z, but "which is the biggest" and "what changed last"
    // are the questions the other two are clicked to answer, so they open on
    // the descending end.
    setSort((prev) => (prev.key === key ? { key, asc: !prev.asc } : { key, asc: key === 'name' }))

  /* ------------------------------------------------------ file actions */

  const askName = useCallback(
    (request: Omit<NameState, 'resolve'>) =>
      new Promise<string | null>((resolve) => setNaming({ ...request, resolve })),
    [],
  )

  const guard = async (action: () => Promise<void>, done: string) => {
    setBusy(true)
    try {
      await action()
      toast(done)
      await load(dirRef.current)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const upload = async (picked: File[]) => {
    if (picked.length === 0) return
    setBusy(true)
    setProgress(0)
    try {
      try {
        await uploadFiles(instance.id, dir, picked, setProgress)
      } catch (err) {
        // 409 means a file of that name is already there. Replacing a server
        // jar with a newer build is the common case, so offer it rather than
        // making the operator delete the old one first — but never do it
        // without asking, since the same name could be a world file.
        if (!(err instanceof ApiError) || err.status !== 409) throw err
        const replace = await ask({
          title: '目录里已经有同名文件',
          lead: `${nameList(picked.map((file) => file.name))}会覆盖同名的旧文件。`,
          detail: '旧文件不会进回收站。',
          confirmLabel: '覆盖',
          danger: true,
        })
        if (!replace) {
          toast('已取消上传')
          return
        }
        setProgress(0)
        await uploadFiles(instance.id, dir, picked, setProgress, true)
      }
      toast(picked.length === 1 ? `已上传 ${picked[0].name}` : `已上传 ${picked.length} 个文件`)
      await load(dir)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '上传失败')
    } finally {
      setBusy(false)
      setProgress(null)
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
    setTreeKey((key) => key + 1)
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
    try {
      await api.writeFile(instance.id, path, '')
      await load(dir)
      setFiles((current) => {
        const next = new Map(current)
        next.set(path, { path, content: '', original: '', modified: '', kind: 'text', size: 0 })
        return next
      })
      showTab(path, false)
      toast(`已创建 ${name}`)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '创建失败')
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
    const to = joinPath(dir, next)
    await guard(() => api.renameFile(instance.id, entry.path, to), `已重命名为 ${next}`)
    retab(entry.path, to)
    setTreeKey((key) => key + 1)
  }

  const remove = async (entry: FileEntry) => {
    // A folder takes its whole tree with it and there is no undo anywhere in
    // this panel, so it is the one delete a mis-aimed click does not get to
    // do: the name has to be typed. A file gets the ordinary question.
    const ok = entry.isDir
      ? await new Promise<boolean>((resolve) => setTyped({ entry, resolve }))
      : await ask({
          title: `删除文件「${entry.name}」？`,
          lead: '删除后无法撤销，请确认这不是存档或配置。',
          confirmLabel: '删除',
          danger: true,
        })
    if (!ok) return
    await guard(() => api.deleteFile(instance.id, entry.path), `已删除 ${entry.name}`)
    retab(entry.path, null)
    setTreeKey((key) => key + 1)
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
      lead: nameList(targets.map((entry) => entry.name)),
      detail:
        (folders > 0 ? `其中 ${folders} 个是文件夹，里面的内容会一起删除。` : '') +
        '删除后无法撤销。',
      confirmLabel: `删除 ${targets.length} 项`,
      danger: true,
    })
    if (!ok) return

    setBusy(true)
    const failed: string[] = []
    for (const entry of targets) {
      try {
        await api.deleteFile(instance.id, entry.path)
        retab(entry.path, null)
      } catch {
        failed.push(entry.name)
      }
    }
    const done = targets.length - failed.length
    if (failed.length > 0) toastError(`${failed.length} 项删除失败：${failed.join('、')}`)
    if (done > 0) toast(`已删除 ${done} 项`)
    setBusy(false)
    setTreeKey((key) => key + 1)
    await load(dirRef.current)
  }

  /** Moving is a rename across directories — the endpoint takes two paths
   *  relative to the instance root, so it already is one. Sequential for the
   *  same reason removeMany is. */
  const moveTo = async (targets: FileEntry[], into: string) => {
    setBusy(true)
    const failed: string[] = []
    for (const entry of targets) {
      const to = joinPath(into, entry.name)
      if (to === entry.path) continue
      try {
        await api.renameFile(instance.id, entry.path, to)
        retab(entry.path, to)
      } catch {
        failed.push(entry.name)
      }
    }
    const done = targets.length - failed.length
    if (failed.length > 0) toastError(`${failed.length} 项移动失败：${failed.join('、')}`)
    if (done > 0) toast(`已移动 ${done} 项到 ${into === '' ? '实例根目录' : into}`)
    setBusy(false)
    setTreeKey((key) => key + 1)
    await load(dirRef.current)
  }

  /** Downloads a selection by clicking one link after another. Spaced out
   *  because a browser handed six navigations in the same tick treats the last
   *  five as a popup. Folders are skipped rather than faked: the daemon has no
   *  endpoint that packs one, and pulling a world folder through the browser
   *  file by file is not the same offer. */
  const downloadMany = async (targets: FileEntry[]) => {
    const picked = targets.filter((entry) => !entry.isDir)
    if (picked.length === 0) {
      toastWarn('选中的都是文件夹。面板没有打包下载，文件夹要进去逐个下载。')
      return
    }
    for (const [index, entry] of picked.entries()) {
      const link = document.createElement('a')
      link.href = downloadURL(instance.id, entry.path)
      link.download = entry.name
      document.body.appendChild(link)
      link.click()
      link.remove()
      if (index < picked.length - 1) {
        await new Promise((resolve) => window.setTimeout(resolve, DOWNLOAD_GAP))
      }
    }
    toast(`已开始下载 ${picked.length} 个文件`)
  }

  /* ---------------------------------------------------- dragging rails */

  /**
   * Drags one divider.
   *
   * The editor's floor is enforced here rather than in CSS, because CSS cannot
   * say "take it out of whichever rail is being dragged": a minmax floor on
   * the editor's own track would simply overflow the grid and push the pane
   * sideways instead of stopping the drag.
   */
  const drag = (event: React.PointerEvent<HTMLDivElement>) => {
    const frame = grid.current
    if (!frame) return
    event.preventDefault()
    const startX = event.clientX
    const from = sideWidth
    const room = frame.clientWidth

    const move = (at: PointerEvent) => {
      const ceiling = Math.max(SIDE_MIN, Math.min(SIDE_MAX, room - GRIP - GROUP_MIN))
      const next = Math.min(ceiling, Math.max(SIDE_MIN, from + (at.clientX - startX)))
      setSide((current) => ({ ...current, width: next }))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
  }

  /* -------------------------------------------------------- shortcuts */

  const activePathNow = useRef(activePath)
  activePathNow.current = activePath
  const focusedPaneNow = useRef(focusedPane)
  focusedPaneNow.current = focusedPane
  const selectedNow = useRef(selected)
  selectedNow.current = selected
  const cursorNow = useRef(cursor)
  cursorNow.current = cursor
  const rowsNow = useRef(rows)
  rowsNow.current = rows
  const noColumnSplitNow = useRef(noColumnSplit)
  noColumnSplitNow.current = noColumnSplit

  /** Moves the cursor one row, and scrolls it back into view. Deliberately not
   *  a selection: walking a directory with the arrow keys is reading it, and
   *  it should not end with twenty rows ticked. */
  const step = useCallback((by: number) => {
    const all = rowsNow.current
    if (all.length === 0) return
    const at = all.findIndex((entry) => entry.path === cursorNow.current)
    const to = Math.max(0, Math.min(all.length - 1, at === -1 ? 0 : at + by))
    setCursor(all[to].path)
    anchor.current = all[to].path
    const row = listBody.current?.children[to] as HTMLElement | undefined
    row?.scrollIntoView({ block: 'nearest' })
  }, [])

  useEffect(() => {
    if (!active) return
    const onKey = (event: KeyboardEvent) => {
      const mod = event.metaKey || event.ctrlKey

      if (mod && event.key.toLowerCase() === 's') {
        event.preventDefault()
        if (activePathNow.current) void save(activePathNow.current)
        return
      }
      if (mod && event.key.toLowerCase() === 'w') {
        if (!activePathNow.current) return
        event.preventDefault()
        void closeTab(focusedPaneNow.current, activePathNow.current)
        return
      }
      // Before the plain ⌘F below, or the shifted one is eaten by it.
      if (mod && event.shiftKey && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        setSide((current) => ({ ...current, collapsed: false, panel: 'search' }))
        // Next tick: the panel is being un-hidden by the state change above,
        // and a hidden input takes no caret.
        window.setTimeout(() => searchPanel.current?.focus(), 0)
        return
      }
      if (mod && event.key.toLowerCase() === 'f') {
        // Inside the editor the browser's own find is the wrong instrument: it
        // searches the rendered mirror rather than the buffer, and it cannot
        // replace. Everywhere else this means the directory filter.
        event.preventDefault()
        if (isInEditor(event.target)) setFindTick((n) => n + 1)
        else searchBox.current?.focus()
        return
      }
      if (mod && event.key.toLowerCase() === 'b') {
        event.preventDefault()
        setPinned(true)
        setSide((current) => ({ ...current, collapsed: !current.collapsed }))
        return
      }
      if (mod && event.key.toLowerCase() === 'p') {
        // preventDefault, or this is the browser's print dialog. Works while
        // typing for the same reason ⌘K does: you press it from wherever you
        // are when you realise you want a different file.
        event.preventDefault()
        setGoto((on) => !on)
        return
      }
      if (mod && event.key === '\\') {
        event.preventDefault()
        if (!noColumnSplitNow.current) splitNow.current('col')
        return
      }
      if (mod && (event.key === '1' || event.key === '2')) {
        const want = Number(event.key) - 1
        if (want >= panesNow.current.length) return
        event.preventDefault()
        setFocusedPane(want)
        return
      }
      if (event.key === 'Escape') {
        // The path box and the find box handle their own Escape and stop it
        // before it reaches here, so by this point the innermost thing left to
        // back out of is the selection.
        if (selectedNow.current.size > 0) setSelected(new Set())
        return
      }
      // Everything below is a bare key, so it belongs to whatever is taking
      // text if anything is.
      if (isTyping(event.target)) return

      if (event.key === 'Backspace') {
        event.preventDefault()
        void load(parentOf(dirRef.current))
        return
      }
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        step(event.key === 'ArrowDown' ? 1 : -1)
        return
      }
      if (event.key === 'Enter') {
        const entry = rowsNow.current.find((one) => one.path === cursorNow.current)
        if (entry) {
          event.preventDefault()
          openEntry(entry)
        }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [active, save, closeTab, load, openEntry, step])

  /* ------------------------------------------------------------ render */

  // The two sections collapse from their own headers, so there is nothing here
  // about folding them: a control that exists in two places is two places to
  // learn it and one of them to keep in step.
  const more: MenuItem[] = [
    { label: '复制当前路径', onSelect: () => void navigator.clipboard?.writeText(dir || '/') },
    { label: '转到文件（⌘/Ctrl + P）', onSelect: () => setGoto(true) },
    { label: '快捷键', onSelect: () => setKeys(true) },
  ]

  /**
   * A row's actions, built once and used twice: by the listing's ⋯ and by the
   * tree's right-click. The design note asks for the same items in the same
   * order in both places, and one list is the only way to be sure of it.
   */
  const entryMenu = (entry: FileEntry): MenuItem[] => [
    { label: '重命名', disabled: busy || !entry.writable, onSelect: () => void rename(entry) },
    {
      label: '复制路径',
      onSelect: () => void navigator.clipboard?.writeText(entry.path),
    },
    { label: '移动到…', disabled: busy || !entry.writable, onSelect: () => setMoving([entry]) },
    // Only for something the editor can actually hold. A jar opened "in a new
    // tab" would be a tab showing a download card nobody asked for.
    ...(entry.isDir || !entry.editable
      ? []
      : [{ label: '在新标签打开', onSelect: () => openEntry(entry, true) }]),
    {
      label: '删除',
      danger: true,
      disabled: busy || !entry.writable,
      onSelect: () => void remove(entry),
    },
  ]

  const dialogs = (
    <>
      {naming && (
        <NameDialog
          request={naming}
          onAnswer={(name) => {
            naming.resolve(name)
            setNaming(null)
          }}
        />
      )}
      {typed && (
        <TypedDeleteDialog
          entry={typed.entry}
          onAnswer={(ok) => {
            typed.resolve(ok)
            setTyped(null)
          }}
        />
      )}
      {moving && (
        <MoveDialog
          instanceId={instance.id}
          entries={moving}
          from={dir}
          onClose={() => setMoving(null)}
          onMove={(into) => {
            const targets = moving
            setMoving(null)
            void moveTo(targets, into)
          }}
        />
      )}
      {conflict && (
        <ConflictDialog
          conflict={conflict}
          onClose={() => setConflict(null)}
          onOverwrite={() => {
            const { path, mine } = conflict
            setConflict(null)
            void (async () => {
              setBusy(true)
              try {
                await api.writeFile(instance.id, path, mine)
                patchFile(path, {
                  original: mine,
                  modified: (await mtimeOf(path)) ?? '',
                  stale: false,
                })
                toast(`已保存 ${baseName(path)}`)
              } catch (err) {
                toastError(err instanceof Error ? err.message : '保存失败')
              } finally {
                setBusy(false)
              }
            })()
          }}
          onTakeTheirs={() => {
            const { path } = conflict
            setConflict(null)
            void reload(path)
          }}
        />
      )}
      {keys && <KeysDialog onClose={() => setKeys(false)} />}
      {preview && (
        <SchematicPreview
          instanceId={instance.id}
          entry={preview}
          onClose={() => setPreview(null)}
        />
      )}
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
    </>
  )

  if (!listing) {
    if (error) return <div className="alert">{error}</div>
    return (
      <SkeletonScreen label="正在读取目录…">
        <SkeletonPanel title={false}>
          <Skeleton w="88px" h={15} />
          <SkeletonRows rows={8} />
        </SkeletonPanel>
      </SkeletonScreen>
    )
  }

  /**
   * The two tracks: the sidebar and the main area.
   *
   * The sidebar takes the width it was dragged to, but never more than the
   * room left once one editor group has its floor. Expressed in CSS rather
   * than measured here because the pane's width is the grid's own `100%`, and
   * reading it in JS would mean a resize observer to say the same thing.
   *
   * Folded it is a 40px strip; floating it leaves the grid entirely and the
   * strip's width is what the main area starts after, so the two cases share
   * one template.
   */
  const template =
    side.collapsed || overlay
      ? `40px minmax(0, 1fr)`
      : `min(${sideWidth}px, max(${SIDE_MIN}px, 100% - ${GRIP}px - ${GROUP_MIN}px)) ` +
        `${GRIP}px minmax(0, 1fr)`

  const stats = {
    count: entries.length,
    bytes: entries.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0),
  }

  const picked = rows.filter((entry) => selected.has(entry.path))
  const pickedBytes = picked.reduce((sum, entry) => sum + (entry.isDir ? 0 : entry.size), 0)

  const newItems: MenuItem[] = [
    { label: '新建文件', disabled: busy || !listing.writable, onSelect: () => void createFile() },
    { label: '新建文件夹', disabled: busy || !listing.writable, onSelect: () => void createFolder() },
  ]

  const refresh = () => {
    // 刷新 means "read the disk again", and a tree still showing a folder
    // deleted somewhere else is exactly what the button gets pressed about.
    setTreeKey((key) => key + 1)
    void load(dir)
  }

  /* The toolbar is in both of the main area's forms and is the same component
     in both: the design note asks for 上传 / 新建 / 刷新 in the same group, the
     same order and the same shape whatever else is on screen. While rows are
     ticked it becomes the bulk bar, at the same height, so the list under it
     does not jump when a selection starts. */
  const tools = (
    <FileTools
      dir={dir}
      stats={stats}
      query={query}
      onQuery={setQuery}
      searchRef={searchBox}
      onUpload={() => fileInput.current?.click()}
      onNewFile={() => void createFile()}
      onNewFolder={() => void createFolder()}
      onRefresh={refresh}
      more={more}
      busy={busy}
      pending={pending}
      writable={listing.writable}
      bulk={
        picked.length === 0 ? undefined : (
          <>
            <span className="ftools__where">
              已选 {picked.length} 项 · {formatBytes(pickedBytes)}
            </span>
            <div className="ftools__acts">
              <Button size="small" disabled={busy} onClick={() => void downloadMany(picked)}>
                下载
              </Button>
              <Button
                size="small"
                disabled={busy || !listing.writable}
                onClick={() => setMoving(picked)}
              >
                移动到…
              </Button>
              <Button
                size="small"
                variant="danger"
                disabled={busy || !listing.writable}
                onClick={() => void removeMany(picked)}
              >
                删除
              </Button>
              <button
                type="button"
                className="link"
                onClick={() => setSelected(new Set())}
              >
                取消选择
              </button>
            </div>
          </>
        )
      }
    />
  )

  return (
    <div className="stack stack--full fmpage">
      {progress != null && (
        <div className="progress fmpage__progress">
          <div className="progress__bar" style={{ width: `${Math.round(progress * 100)}%` }} />
          <span className="progress__label">{Math.round(progress * 100)}%</span>
        </div>
      )}

      <div
        className="fm"
        ref={grid}
        style={{ gridTemplateColumns: template }}
        data-overlay={overlay || undefined}
      >
        <FileSidebar
          collapsed={side.collapsed}
          overlay={overlay}
          onToggle={() => {
            setPinned(true)
            setSide((current) => ({ ...current, collapsed: !current.collapsed }))
          }}
          panel={side.panel}
          onPanel={(panel) => setSide((current) => ({ ...current, panel }))}
          newItems={newItems}
          onRefresh={refresh}
          more={more}
          pending={pending}
          usage={usage}
          tree={
            <FileTree
              instanceId={instance.id}
              path={dir}
              reloadKey={treeKey}
              onOpen={(next) => {
                void load(next)
              }}
              onOpenFile={(entry) => openEntry(entry)}
              openPaths={openPaths}
              activePath={activePath}
              menuFor={entryMenu}
            />
          }
          search={
            <FileSearchPanel
              instanceId={instance.id}
              boxRef={searchPanel}
              onOpen={(path, line) => void openPath(path, { line })}
            />
          }
          recent={
            <FileRecentPanel
              paths={recent}
              activePath={activePath}
              onOpen={(path) => void openPath(path)}
              onForget={(path) => setRecent((current) => current.filter((one) => one !== path))}
              onClear={() => setRecent([])}
            />
          }
        />

        {/* Only between two real columns. A handle beside a folded strip, or
            beside a sidebar that is floating over what it would resize, moves
            a boundary that is not there. */}
        {!side.collapsed && !overlay && (
          <div
            className="fm__grip"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整侧边栏宽度"
            onPointerDown={drag}
            onDoubleClick={() => setSide((current) => ({ ...current, width: SIDE_DEFAULT }))}
          />
        )}

        <div className="fm__main">
          {/* The trail describes what is in the main area, so it is only here
              in the form that has a directory in it. In the editor form each
              group carries a trail of its own, for the file it is showing. */}
          {!open && <FileCrumbs dir={dir} onNavigate={navigate} pending={pending} />}
          {tools}
          {open ? (
            <FileEditor
              findTick={findTick}
              reveal={reveal}
              instanceId={instance.id}
              instanceName={instance.name}
              files={files}
              panes={panes}
              focusedPane={focusedPane}
              layout={layout}
              canSplitColumns={!noColumnSplit && !narrow}
              canSplit={!narrow}
              maxEditableBytes={listing.maxEditableBytes}
              onFocusPane={setFocusedPane}
              onCaret={setCaret}
              onSelectTab={(pane, path) =>
                setPanes((current) =>
                  current.map((one, index) => (index === pane ? { ...one, active: path } : one)),
                )
              }
              onCloseTab={(pane, path) => void closeTab(pane, path)}
              onCloseOthers={(pane, path) => dropTabs(pane, () => [path])}
              onCloseRight={(pane, path) =>
                dropTabs(pane, (tabs) => tabs.slice(0, tabs.indexOf(path) + 1))
              }
              onSplit={splitInto}
              onRatio={(ratio) => setLayout((current) => ({ ...current, ratio }))}
              onMoveTab={moveTab}
              onWalk={(next) => void load(next)}
              onChange={(path, content) => patchFile(path, { content })}
              onSave={(path) => void save(path)}
              onRevert={(path) => void revert(path)}
              onReload={(path) => void reload(path)}
              onKeepMine={(path) => patchFile(path, { stale: false })}
              onPatch={patchFile}
              onOpenHistory={onOpenHistory}
              onLocate={(path) => {
                // After the walk, not before it: load clears the selection
                // when it lands, so ticking the row first is ticking it for as
                // long as the request takes.
                void (async () => {
                  await load(parentOf(path))
                  setCursor(path)
                })()
                setSide((current) => ({ ...current, collapsed: false, panel: 'tree' }))
              }}
              onShowKeys={() => setKeys(true)}
              busy={busy}
            />
          ) : (
            <div className="fbrowse">
              <FileList
                instanceId={instance.id}
                dir={dir}
                entries={rows}
                total={entries.length}
                sort={sort}
                onSort={toggleSort}
                selected={selected}
                cursor={cursor}
                onSelect={pick}
                activePath={activePath}
                dirtyPaths={dirtyPaths}
                onOpen={(entry) => openEntry(entry)}
                menuFor={entryMenu}
                onUpload={() => fileInput.current?.click()}
                onDropFiles={(files) => void upload(files)}
                onRetry={() => void load(dir)}
                onClearQuery={() => setQuery('')}
                query={query}
                error={error}
                pending={pending}
                busy={busy}
                writable={listing.writable}
                bodyRef={listBody}
              />
            </div>
          )}
        </div>
      </div>

      {goto && (
        <GoToFile
          instanceId={instance.id}
          recent={recent}
          onClose={() => setGoto(false)}
          onOpen={(path, split) => {
            setGoto(false)
            void (async () => {
              await openPath(path)
              if (split && panesNow.current.length < 2) splitInto(noColumnSplit ? 'row' : 'col')
            })()
          }}
        />
      )}

      {/* What the shell's status bar says while this page is the one on
          screen. It is the caret's position and nothing else that could not be
          read off the page itself — see components/StatusBar. */}
      {active && (
        <StatusSlot>
          {caret !== null && (
            <span className="statusbar__fact">
              行 {caret.line}，列 {caret.column}
            </span>
          )}
          {panes.length > 1 && (
            <span className="statusbar__fact">
              {layout.dir === 'col' ? '左右分屏' : '上下分屏'} · {panes.length} 组
            </span>
          )}
          <span className="statusbar__fact">{dir === '' ? '/' : `/${dir}`}</span>
        </StatusSlot>
      )}
      {active && dirtyPaths.size > 0 && (
        <StatusSlot side="right">
          <span className="statusbar__fact statusbar__fact--warn">
            {dirtyPaths.size} 个文件未保存
          </span>
        </StatusSlot>
      )}

      {dialogs}
    </div>
  )
}

/* ------------------------------------------------------------- pieces */

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
          <Button type="button" onClick={() => onAnswer(null)}>
            取消
          </Button>
          <Button variant="primary" type="submit" disabled={problem != null}>
            {request.confirmLabel}
          </Button>
        </div>
      </form>
    </Modal>
  )
}

/**
 * Deleting a folder, with the name typed out.
 *
 * A folder takes everything under it and nothing in this panel has an undo, so
 * this is the one delete that must not be reachable by a click landing in the
 * wrong row. The typing is not ceremony: it is the step that makes the reader
 * say which folder, out loud, before it goes.
 */
function TypedDeleteDialog({
  entry,
  onAnswer,
}: {
  entry: FileEntry
  onAnswer: (ok: boolean) => void
}) {
  const [value, setValue] = useState('')
  const input = useRef<HTMLInputElement | null>(null)
  useEffect(() => input.current?.focus(), [])

  const matched = value === entry.name

  return (
    <Modal onClose={() => onAnswer(false)} label={`删除文件夹 ${entry.name}`}>
      <form
        className="modal__card"
        onSubmit={(event) => {
          event.preventDefault()
          if (matched) onAnswer(true)
        }}
      >
        <h2 className="modal__title">删除文件夹「{entry.name}」？</h2>
        <p className="modal__lead">里面的所有内容会一起删除，而且无法撤销。</p>
        {PRECIOUS.has(entry.name) && (
          <Note tone="error">
            <b>{entry.name}</b> 是服务器跑起来要用的目录。删掉它，这台服务器很可能起不来，
            而且里面的东西不是从下载页能装回来的。
          </Note>
        )}
        <label className="field">
          <span>
            输入 <code>{entry.name}</code> 以确认
          </span>
          <input
            ref={input}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            spellCheck={false}
            autoComplete="off"
          />
        </label>
        <div className="modal__actions">
          <Button type="button" onClick={() => onAnswer(false)}>
            取消
          </Button>
          <Button variant="danger" type="submit" disabled={!matched}>
            删除
          </Button>
        </div>
      </form>
    </Modal>
  )
}

/** Where to move the selection. The tree does the picking rather than a path
 *  box: a typo in a box is a file in a directory nobody meant to create. */
function MoveDialog({
  instanceId,
  entries,
  from,
  onClose,
  onMove,
}: {
  instanceId: string
  entries: FileEntry[]
  from: string
  onClose: () => void
  onMove: (into: string) => void
}) {
  const [into, setInto] = useState(from)

  return (
    <Modal onClose={onClose} label="移动到">
      <div className="modal__card modal__card--wide">
        <h2 className="modal__title">移动 {entries.length} 项</h2>
        <p className="modal__lead">{nameList(entries.map((entry) => entry.name))}</p>
        <div className="fmove">
          <FileTree instanceId={instanceId} path={into} onOpen={setInto} compact />
        </div>
        <p className="modal__lead">
          目标：<code>{into === '' ? '实例根目录' : into}</code>
        </p>
        <div className="modal__actions">
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" disabled={into === from} onClick={() => onMove(into)}>
            移动到这里
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/**
 * The file changed under a save.
 *
 * Three answers rather than two, because the third is the only one that is not
 * a guess: somebody who can see both versions can decide, and somebody who
 * cannot is being asked to gamble with whichever half they did not write.
 */
function ConflictDialog({
  conflict,
  onClose,
  onOverwrite,
  onTakeTheirs,
}: {
  conflict: Conflict
  onClose: () => void
  onOverwrite: () => void
  onTakeTheirs: () => void
}) {
  const [showing, setShowing] = useState(false)
  const rows = useMemo(
    () => (showing ? sideBySide(conflict.theirs, conflict.mine) : []),
    [showing, conflict],
  )

  return (
    <Modal onClose={onClose} label="文件已被改动">
      <div className="modal__card modal__card--wide">
        <h2 className="modal__title">{baseName(conflict.path)} 在别处被改过了</h2>
        <p className="modal__lead">
          从你打开它到现在，磁盘上的这个文件变了。用你的版本覆盖会把那一次改动抹掉。
        </p>

        {showing && (
          <div className="ediff">
            <div className="ediff__head">
              <span>磁盘上的版本</span>
              <span>我的版本</span>
            </div>
            <div className="ediff__body">
              {rows.map((row, index) => (
                <div
                  key={index}
                  className={row.same ? 'ediff__row' : 'ediff__row ediff__row--differs'}
                >
                  <span className="ediff__side">{row.left ?? ''}</span>
                  <span className="ediff__side">{row.right ?? ''}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        <div className="modal__actions">
          {!showing && <Button onClick={() => setShowing(true)}>并排查看差异</Button>}
          <Button onClick={onTakeTheirs}>放弃我的改动，重新加载</Button>
          <Button variant="danger" onClick={onOverwrite}>
            用我的覆盖
          </Button>
        </div>
      </div>
    </Modal>
  )
}

function KeysDialog({ onClose }: { onClose: () => void }) {
  const rows: Array<[string, string]> = [
    ['⌘/Ctrl + F', '焦点在列表时聚焦搜索框；在编辑器里则打开查找替换'],
    ['⌘/Ctrl + S', '保存当前标签'],
    ['⌘/Ctrl + W', '关闭当前标签'],
    ['⌘/Ctrl + Z / ⇧Z', '编辑器撤销 / 重做'],
    ['↑ / ↓', '在文件列表里上下移动选择'],
    ['Enter', '打开选中项'],
    ['Backspace', '返回上一级目录'],
    ['Shift / ⌘Ctrl + 点击', '区间选 / 加选'],
    ['Esc', '退出路径编辑、关闭查找框、取消多选'],
  ]

  return (
    <Modal onClose={onClose} label="快捷键">
      <div className="modal__card">
        <h2 className="modal__title">快捷键</h2>
        <dl className="fkeys">
          {rows.map(([key, what]) => (
            <div className="fkeys__row" key={key}>
              <dt>{key}</dt>
              <dd>{what}</dd>
            </div>
          ))}
        </dl>
        <div className="modal__actions">
          <Button variant="primary" onClick={onClose}>
            知道了
          </Button>
        </div>
      </div>
    </Modal>
  )
}

/* -------------------------------------------------------------- helpers */

/** The first few names of a batch, so a confirmation says what it is about. */
function nameList(names: string[]): string {
  const shown = names.slice(0, 3)
  return names.length > shown.length ? `${shown.join('、')} 等 ${names.length} 项` : shown.join('、')
}

/** Only the raster types the daemon agrees to serve inline; see previewTypes
 *  in handlers_fs.go. SVG is deliberately not among them — it is a document
 *  that carries script — so it is a download here too. */
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

/** Extensions the daemon would have opened had the file been small enough —
 *  see editableExtensions in serverfiles/browser.go. Kept only to tell "too
 *  big to edit" apart from "never was text", which the listing does not say. */
const TEXTUAL = new Set([
  '.yml',
  '.yaml',
  '.json',
  '.properties',
  '.toml',
  '.conf',
  '.cfg',
  '.ini',
  '.xml',
  '.txt',
  '.md',
  '.log',
  '.csv',
  '.lang',
  '.snbt',
  '.sh',
  '.bat',
  '.cmd',
  '.ps1',
  '.env',
  '.mcmeta',
  '.kts',
])

function isTextName(name: string): boolean {
  return TEXTUAL.has(extensionOf(name))
}

/**
 * Whether a key event landed somewhere that is taking text.
 *
 * Not a tagName check alone: `contenteditable` and `role="textbox"` are both
 * places where a keystroke belongs to something other than the page's
 * shortcuts, and neither of them is an <input>.
 */
function isTyping(target: EventTarget | null): boolean {
  const node = target as HTMLElement | null
  if (node === null) return false
  const tag = node.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return node.isContentEditable === true || node.getAttribute?.('role') === 'textbox'
}

function isInEditor(target: EventTarget | null): boolean {
  const node = target as HTMLElement | null
  return node?.classList?.contains('editor__text') === true
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
