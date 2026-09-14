import {
  Fragment,
  useCallback,
  useDeferredValue,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { createPortal } from 'react-dom'

import { downloadURL, previewURL } from '../api'
import { diffLines } from '../filediff'
import type { LineChange } from '../filediff'
import { formatBytes } from '../format'
import { highlight, highlightLog, langOf } from '../highlight'
import { toast } from '../toast'
import { Badge } from './Badge'
import { Button } from './Button'
import { FileIcon } from './FileIcon'
import { Glyph } from './Glyph'
import { Menu } from './Menu'
import { Note } from './Note'
import type { MenuItem } from './Menu'

/**
 * The editor half of the file pane.
 *
 * One buffer per path, and panes that hold paths rather than copies: the same
 * file open in both halves of a split is one piece of text, so typing in the
 * left pane is not something the right one finds out about later.
 */

/** What kind of thing is behind a tab. Only `text` gets an editor; the rest
 *  get a card, because an editor showing a jar is a way to corrupt one. */
export type FileKind = 'text' | 'binary' | 'image' | 'oversize'

export interface OpenFile {
  path: string
  content: string
  /** What is on disk as far as this tab knows. Saving sets it, and the change
   *  marks in the gutter are content-against-this rather than against the
   *  file as it was first opened. */
  original: string
  /** The directory listing's mtime when this was read. The conflict check has
   *  nothing else to compare against — see mtimeOf in FileManager. */
  modified: string
  kind: FileKind
  size: number
  readOnly?: boolean
  /** Soft wrap. Off by default, and it costs the gutter — see the note on the
   *  body below. */
  wrap?: boolean
  /** The disk moved under an edited buffer, and the banner is up. */
  stale?: boolean
}

export interface EditorPane {
  tabs: string[]
  active: string | null
}

/**
 * How the groups are arranged, and how the room is divided between them.
 *
 * 'col' is two columns side by side, 'row' is one above the other. Both are
 * first-class: comparing two YAMLs stacked lets their line numbers and their
 * indentation line up, which reads better than side by side and costs no
 * width — so 上下 is not the narrow-screen fallback, it is the other choice.
 */
export interface Layout {
  dir: 'col' | 'row'
  /** The first group's share, 0..1. */
  ratio: number
}

export interface FileEditorProps {
  instanceId: string
  /** The first step of a group's breadcrumb: the server the file is in. */
  instanceName: string
  files: Map<string, OpenFile>
  panes: EditorPane[]
  focusedPane: number
  layout: Layout
  /** Whether a second group fits at all, and whether it fits side by side.
   *  Below 1280px two columns cannot both hold their 620px floor, so 左右 is
   *  refused there while 上下 stays — it takes height, not width. */
  canSplit: boolean
  canSplitColumns: boolean
  maxEditableBytes: number
  onFocusPane: (index: number) => void
  /** Where the caret is in the focused group, for the shell's status bar. Null
   *  when this group is not the focused one, or has no text in front. */
  onCaret: (at: { line: number; column: number } | null) => void
  onSelectTab: (pane: number, path: string) => void
  onCloseTab: (pane: number, path: string) => void
  onCloseOthers: (pane: number, path: string) => void
  onCloseRight: (pane: number, path: string) => void
  onSplit: (dir: 'col' | 'row') => void
  onRatio: (next: number) => void
  /** Dragging a tab from one group into the other. */
  onMoveTab: (from: number, to: number, path: string) => void
  /** A step of the group's breadcrumb: walk the sidebar to that directory. */
  onWalk: (dir: string) => void
  onChange: (path: string, content: string) => void
  onSave: (path: string) => void
  onRevert: (path: string) => void
  onReload: (path: string) => void
  onKeepMine: (path: string) => void
  onPatch: (path: string, patch: Partial<OpenFile>) => void
  onOpenHistory?: (path: string) => void
  /** Walks the listing to the file's own directory and ticks it. */
  onLocate: (path: string) => void
  onShowKeys: () => void
  /** Bumped by the pane's ⌘F handler. A counter rather than a boolean because
   *  the request is "open the box now", and pressing the shortcut again while
   *  it is already open should still put the caret back in it. */
  findTick: number
  /** A line to put the caret on, from the sidebar's search panel. Carries a
   *  token for the same reason findTick is a counter: clicking the same hit
   *  twice has to work. */
  reveal: { path: string; line: number; token: number } | null
  busy: boolean
}

/** How a dragged tab is carried between groups. A custom type rather than
 *  text/plain so a path dragged out of some other application cannot look like
 *  one of ours. */
const TAB_MIME = 'application/x-hypercraft-tab'

/** Every directory on the way to a file, for the group's breadcrumb. */
function crumbsOf(path: string): { name: string; path: string; last: boolean }[] {
  const parts = path.split('/').filter(Boolean)
  return parts.map((name, index) => ({
    name,
    path: parts.slice(0, index + 1).join('/'),
    last: index === parts.length - 1,
  }))
}

/**
 * Where the caret and the scroll were, per instance and path.
 *
 * Module-level rather than component state on purpose: it has to outlive the
 * tab being closed. Coming back to a 4000-line paper-global.yml and landing at
 * the top of it is the whole of the annoyance this removes.
 */
const spots = new Map<string, { caret: number; top: number; left: number }>()

export function FileEditor(props: FileEditorProps) {
  const { panes, layout, onFocusPane, onRatio } = props
  const frame = useRef<HTMLElement | null>(null)

  const split = panes.length > 1
  // The ratio is a grid template rather than two widths, so dragging moves one
  // number and the browser does the arithmetic. Inline because it is a value
  // only JS knows; everything else about the arrangement is in styles.css.
  const style = !split
    ? undefined
    : layout.dir === 'col'
      ? { gridTemplateColumns: `${layout.ratio}fr 12px ${1 - layout.ratio}fr` }
      : { gridTemplateRows: `${layout.ratio}fr 12px ${1 - layout.ratio}fr` }

  /**
   * Drags the boundary between the two groups.
   *
   * The ratio is what is stored rather than a pixel size: the pane's size
   * changes with the window, and a stored 640px is a different split on every
   * screen. The clamp keeps both groups above a fifth of the room, which is
   * where a group stops being one.
   */
  const drag = (event: React.PointerEvent<HTMLDivElement>) => {
    const box = frame.current
    if (!box) return
    event.preventDefault()

    const move = (at: PointerEvent) => {
      const rect = box.getBoundingClientRect()
      const along =
        layout.dir === 'col'
          ? (at.clientX - rect.left) / rect.width
          : (at.clientY - rect.top) / rect.height
      onRatio(Math.min(0.8, Math.max(0.2, along)))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
  }

  return (
    <section className="fedit" data-panes={panes.length} data-dir={layout.dir} style={style} ref={frame}>
      {panes.map((pane, index) => (
        <Fragment key={index}>
          {index > 0 && (
            <div
              className="fedit__grip"
              role="separator"
              aria-orientation={layout.dir === 'col' ? 'vertical' : 'horizontal'}
              aria-label="调整两组编辑器的比例"
              onPointerDown={drag}
              onDoubleClick={() => onRatio(0.5)}
            />
          )}
          <Pane index={index} pane={pane} {...props} onFocus={() => onFocusPane(index)} />
        </Fragment>
      ))}
    </section>
  )
}

interface PaneProps extends FileEditorProps {
  index: number
  pane: EditorPane
  onFocus: () => void
}

function Pane({
  index,
  pane,
  instanceId,
  instanceName,
  files,
  panes,
  focusedPane,
  canSplit,
  canSplitColumns,
  maxEditableBytes,
  onSelectTab,
  onCloseTab,
  onCloseOthers,
  onCloseRight,
  onSplit,
  onMoveTab,
  onWalk,
  onCaret,
  onChange,
  onSave,
  onRevert,
  onReload,
  onKeepMine,
  onPatch,
  onOpenHistory,
  onLocate,
  onShowKeys,
  onFocus,
  findTick,
  reveal,
  busy,
}: PaneProps) {
  const file = pane.active === null ? null : (files.get(pane.active) ?? null)
  const [context, setContext] = useState<{ x: number; y: number; path: string } | null>(null)
  const [finding, setFinding] = useState(false)
  const [dropping, setDropping] = useState(false)

  // ⌘F, from the pane-level handler. Only the half the keyboard is in answers
  // it — opening both boxes at once would leave two carets and one keyboard.
  useEffect(() => {
    if (findTick > 0 && index === focusedPane) setFinding(true)
  }, [findTick, index, focusedPane])

  // The strip scrolls, so the tab in front can be somewhere off the end of it
  // — which is what splitting five tabs into a 172px strip does, and it looks
  // exactly like the wrong file being open.
  //
  // `panes.length` is in here and it is not decoration: splitting changes
  // neither the front tab nor the tab count of the pane that was already
  // there, it only makes its strip less than a third as wide, and the scroll
  // offset it was left at then points past the end.
  const strip = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    strip.current
      ?.querySelector('.etab--on')
      ?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [pane.active, pane.tabs.length, panes.length])

  useEffect(() => {
    if (context === null) return
    const dismiss = () => setContext(null)
    window.addEventListener('pointerdown', dismiss)
    window.addEventListener('keydown', dismiss)
    return () => {
      window.removeEventListener('pointerdown', dismiss)
      window.removeEventListener('keydown', dismiss)
    }
  }, [context])

  const more: MenuItem[] = file
    ? [
        {
          label: isJSON(file.path) ? '格式化' : '格式化（只支持 JSON）',
          disabled: !isJSON(file.path) || file.readOnly || file.kind !== 'text',
          onSelect: () => {
            try {
              onChange(file.path, `${JSON.stringify(JSON.parse(file.content), null, 2)}\n`)
            } catch {
              // A config that does not parse is the usual reason somebody is
              // in here, so this is an ordinary outcome rather than an error
              // the page has to hold on screen.
              toast('这个 JSON 还有语法错误，没有格式化')
            }
          },
        },
        {
          label: file.wrap ? '关闭自动换行' : '开启自动换行',
          onSelect: () => onPatch(file.path, { wrap: !file.wrap }),
        },
        {
          label: file.readOnly ? '取消只读' : '只读模式',
          onSelect: () => onPatch(file.path, { readOnly: !file.readOnly }),
        },
        { label: '在文件列表中定位', onSelect: () => onLocate(file.path) },
        {
          label: '关闭其他标签',
          disabled: pane.tabs.length < 2,
          onSelect: () => onCloseOthers(index, file.path),
        },
        {
          label: '关闭右侧标签',
          disabled: pane.tabs.indexOf(file.path) === pane.tabs.length - 1,
          onSelect: () => onCloseRight(index, file.path),
        },
        { label: '快捷键', onSelect: onShowKeys },
      ]
    : []

  return (
    <div
      className="fedit__pane"
      data-focused={panes.length > 1 && index === focusedPane ? '' : undefined}
      data-dropping={dropping || undefined}
      onFocusCapture={onFocus}
      onPointerDownCapture={onFocus}
      // Dropping a tab here moves the file into this group. The payload is the
      // group it came from and the path, as JSON: dataTransfer carries strings
      // and nothing else, and a bare path would leave the other group unable
      // to say which one is giving the tab up.
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes(TAB_MIME)) return
        event.preventDefault()
        setDropping(true)
      }}
      onDragLeave={() => setDropping(false)}
      onDrop={(event) => {
        setDropping(false)
        const raw = event.dataTransfer.getData(TAB_MIME)
        if (raw === '') return
        event.preventDefault()
        try {
          const moved = JSON.parse(raw) as { pane: number; path: string }
          onMoveTab(moved.pane, index, moved.path)
        } catch {
          // A payload this component did not write. Nothing to move.
        }
      }}
    >
      <div className="fedit__bar">
        <div className="fedit__tabs" ref={strip} role="tablist" aria-label="打开的文件">
          {pane.tabs.map((path) => {
            const tab = files.get(path)
            const dirty = tab !== undefined && tab.content !== tab.original
            return (
              <div
                key={path}
                className={`etab${path === pane.active ? ' etab--on' : ''}`}
                draggable
                onDragStart={(event) => {
                  event.dataTransfer.setData(TAB_MIME, JSON.stringify({ pane: index, path }))
                  event.dataTransfer.effectAllowed = 'move'
                }}
                onContextMenu={(event) => {
                  event.preventDefault()
                  setContext({ x: event.clientX, y: event.clientY, path })
                }}
                onAuxClick={(event) => {
                  // Middle click closes, the way it does in every other tab
                  // strip anybody has used. preventDefault because the browser
                  // would otherwise start an autoscroll.
                  if (event.button !== 1) return
                  event.preventDefault()
                  onCloseTab(index, path)
                }}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={path === pane.active}
                  className="etab__pick"
                  onClick={() => onSelectTab(index, path)}
                  title={path}
                >
                  <FileIcon name={baseName(path)} />
                  <span className="etab__name">{baseName(path)}</span>
                  {dirty && <span className="etab__dot" aria-label="有未保存的修改" />}
                </button>
                <button
                  type="button"
                  className="etab__close"
                  onClick={() => onCloseTab(index, path)}
                  aria-label={`关闭 ${baseName(path)}`}
                >
                  ×
                </button>
              </div>
            )
          })}
        </div>

        <div className="fedit__tools">
          {/* Up here rather than down among 保存 and 还原, where it used to sit.
              It is a question about this file — what did it look like before,
              and who moved it — not an edit, and in a row of edit buttons it
              read as one more thing you were about to commit. */}
          {onOpenHistory && file && (
            <button
              type="button"
              className="link fedit__history"
              onClick={() => onOpenHistory(file.path)}
              title="在配置历史里比较这个文件"
            >
              <Glyph name="clock" />
              <span className="fedit__history-text">配置历史</span>
            </button>
          )}
          <span className="fedit__sep" aria-hidden="true" />
          {/* Two items rather than one switch. 上下 is not the narrow-screen
              fallback: stacked, two YAMLs line their indentation and their line
              numbers up, which is easier to read than side by side and costs no
              width. 左右 is the one that has a floor to meet, so it is the one
              that goes away below 1280px. */}
          <Menu
            className="btn btn--icon btn--small"
            title={panes.length >= 2 ? '已经是两组了' : '分屏对照'}
            ariaLabel="分屏对照"
            items={[
              {
                label: '左右分屏',
                disabled: panes.length >= 2 || file === null || !canSplitColumns,
                onSelect: () => onSplit('col'),
              },
              {
                label: '上下分屏',
                disabled: panes.length >= 2 || file === null || !canSplit,
                onSelect: () => onSplit('row'),
              },
            ]}
          >
            <Glyph name="split" />
          </Menu>
          <Button
            icon
            size="small"
            aria-label="查找替换"
            title="查找替换（⌘/Ctrl+F）"
            disabled={file === null || file.kind !== 'text'}
            onClick={() => setFinding((on) => !on)}
          >
            <Glyph name="search" />
          </Button>
          <Menu
            className="btn btn--icon btn--small"
            items={more}
            title="更多"
            ariaLabel="更多"
          >
            <Glyph name="ellipsis" />
          </Menu>
        </div>
      </div>

      {/* A context menu has to open where the pointer is, which is the one
          thing Menu cannot do — it anchors to its own trigger. The sheet's
          classes are shared with it so the two look like one thing. */}
      {context &&
        createPortal(
          <div
            className="menu__sheet"
            role="menu"
            data-state="in"
            data-dir="down"
            style={{ left: context.x, top: context.y }}
          >
            <button
              type="button"
              role="menuitem"
              className="menu__item"
              onClick={() => onCloseTab(index, context.path)}
            >
              关闭
            </button>
            <button
              type="button"
              role="menuitem"
              className="menu__item"
              onClick={() => onCloseOthers(index, context.path)}
            >
              关闭其他
            </button>
            <button
              type="button"
              role="menuitem"
              className="menu__item"
              onClick={() => onCloseRight(index, context.path)}
            >
              关闭右侧
            </button>
            <button
              type="button"
              role="menuitem"
              className="menu__item"
              onClick={() => void navigator.clipboard?.writeText(context.path)}
            >
              复制路径
            </button>
          </div>,
          document.body,
        )}

      {file === null ? (
        <div className="fedit__blank">
          <Glyph name="doc" />
          <p>把一个标签拖到这里，或者关掉这一组。</p>
        </div>
      ) : (
        <>
          {/* Where this tab's file is, which is a different question from where
              the sidebar is standing: switching directories in the tree must
              not rewrite the trail above the file you are editing. */}
          <nav className="fedit__crumbs" aria-label="当前文件的位置">
            <button type="button" className="fedit__crumb" onClick={() => onWalk('')}>
              {instanceName}
            </button>
            {crumbsOf(file.path).map((step) => (
              <Fragment key={step.path}>
                <span className="fedit__crumb-sep" aria-hidden="true">
                  /
                </span>
                {step.last ? (
                  <span className="fedit__crumb fedit__crumb--here" aria-current="page">
                    {step.name}
                  </span>
                ) : (
                  <button
                    type="button"
                    className="fedit__crumb"
                    onClick={() => onWalk(step.path)}
                  >
                    {step.name}
                  </button>
                )}
              </Fragment>
            ))}
          </nav>
          {file.stale && (
            <Note tone="warn" className="fedit__stale">
              <span>磁盘上的这个文件已经变了，而你这里还有没保存的改动。</span>
              <Button size="small" onClick={() => onReload(file.path)}>
                重新加载
              </Button>
              <Button size="small" onClick={() => onKeepMine(file.path)}>
                保留我的版本
              </Button>
            </Note>
          )}
          {file.kind === 'text' ? (
            <Body
              key={file.path}
              instanceId={instanceId}
              file={file}
              finding={finding}
              focused={index === focusedPane}
              reveal={reveal !== null && reveal.path === file.path ? reveal : null}
              onCaret={onCaret}
              onCloseFind={() => setFinding(false)}
              onChange={onChange}
              onSave={onSave}
              onRevert={onRevert}
              busy={busy}
            />
          ) : (
            <Card instanceId={instanceId} file={file} maxEditableBytes={maxEditableBytes} />
          )}
        </>
      )}
    </div>
  )
}

/**
 * The text editor, with a gutter.
 *
 * Line numbers are not decoration here: what gets opened in this box is
 * server.properties and a plugin's config.yml, and what sends someone to it is
 * a console line that ends in "at line 42".
 *
 * Four layers, all with identical type and line-height: the gutter, a marks
 * layer that tints the changed rows, the highlighted mirror, and the textarea
 * the caret actually lives in. They are kept in step by translating them, not
 * by scrolling them. The mirrors used to be scrolled to the textarea's own
 * offset, which is only the same number while both boxes can reach it:
 * `wrap="off"` puts a horizontal scrollbar inside the textarea, so its client
 * box is ~13px shorter than the mirrors' and it scrolls ~13px further.
 * Assigning that offset to a mirror clamped it, and at the bottom of a file the
 * colours — and the line numbers — sat a scrollbar's height below the caret. A
 * translate is not clamped by anything, so every layer agrees at every offset,
 * including the last one.
 *
 * Soft wrap is off for the same reason the gutter exists: a wrapped line takes
 * two rows on screen and one number in the margin, and there is no honest way
 * to draw the second row's number. Turning wrap on therefore turns the gutter
 * and the change marks off, and the status line says so.
 */
function Body({
  instanceId,
  file,
  finding,
  focused,
  reveal,
  onCaret,
  onCloseFind,
  onChange,
  onSave,
  onRevert,
  busy,
}: {
  instanceId: string
  file: OpenFile
  finding: boolean
  /** Whether this group has the keyboard. Only the focused one reports its
   *  caret upward; two groups reporting would fight over one status line. */
  focused: boolean
  reveal: { line: number; token: number } | null
  onCaret: (at: { line: number; column: number } | null) => void
  onCloseFind: () => void
  onChange: (path: string, content: string) => void
  onSave: (path: string) => void
  onRevert: (path: string) => void
  busy: boolean
}) {
  const dirty = file.content !== file.original
  const gutter = useRef<HTMLDivElement | null>(null)
  const marks = useRef<HTMLDivElement | null>(null)
  const hl = useRef<HTMLPreElement | null>(null)
  const box = useRef<HTMLTextAreaElement | null>(null)
  const [caret, setCaret] = useState(0)

  // Past this the count is recomputed on every keystroke over a string big
  // enough to feel it, and the numbers have stopped being useful anyway. The
  // gutter, the marks and the colouring all switch off together at it: one
  // threshold, so a 20 000-line log is never half-decorated.
  const lines = useMemo(
    () => (file.content.length > 400_000 ? 0 : file.content.split('\n').length),
    [file.content],
  )
  const huge = lines === 0
  const gutterOff = huge || file.wrap === true

  // Bytes, not characters: a config full of Chinese is three times the length
  // it looks. Memoised because the Blob is an allocation per keystroke.
  const bytes = useMemo(() => new Blob([file.content]).size, [file.content])

  const lang = useMemo(() => langOf(file.path), [file.path])
  // A frame behind the textarea on purpose: typing must never wait on a
  // tokeniser or on a diff, and either landing one frame late is invisible.
  const deferred = useDeferredValue(file.content)

  const painted = useMemo(() => {
    if (huge) return null
    if (lang.prism) return highlight(deferred, lang.prism)
    if (isLog(file.path)) return highlightLog(deferred)
    return null
  }, [deferred, huge, lang.prism, file.path])

  const changes = useMemo(
    () => (gutterOff ? new Map<number, LineChange>() : diffLines(file.original, deferred)),
    [gutterOff, file.original, deferred],
  )

  /** Puts every mirror where the textarea is. See the note above for why this
   *  is a transform and not a scroll offset. */
  const mirror = useCallback(() => {
    const text = box.current
    if (!text) return
    const { scrollTop, scrollLeft } = text
    // The gutter never moves sideways: its numbers are right-aligned against a
    // rail that stays put, and scrolling to column 300 must not take them off
    // the screen with it.
    if (gutter.current) gutter.current.style.transform = `translateY(${-scrollTop}px)`
    if (marks.current) marks.current.style.transform = `translateY(${-scrollTop}px)`
    if (hl.current) hl.current.style.transform = `translate(${-scrollLeft}px, ${-scrollTop}px)`
  }, [])

  // The colours landing a frame late changes the layers without a scroll
  // event: the browser clamps the textarea's offset to the new height, and the
  // mirrors have to be told about it.
  useLayoutEffect(mirror, [mirror, painted, lines, changes])

  // Put the caret and the scroll back where this file was left.
  const spotKey = `${instanceId}:${file.path}`
  useLayoutEffect(() => {
    const text = box.current
    const spot = spots.get(spotKey)
    if (!text || !spot) return
    text.selectionStart = spot.caret
    text.selectionEnd = spot.caret
    text.scrollTop = spot.top
    text.scrollLeft = spot.left
    setCaret(spot.caret)
    mirror()
  }, [spotKey, mirror])

  useEffect(
    () => () => {
      const text = box.current
      if (!text) return
      spots.set(spotKey, {
        caret: text.selectionStart,
        top: text.scrollTop,
        left: text.scrollLeft,
      })
    },
    [spotKey],
  )

  // A tab away with unsaved changes is a browser-level event; the panel's own
  // close already asks.
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => event.preventDefault()
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  /** Puts one line back the way the disk has it. `add` means the line is not
   *  on disk at all, so putting it back is taking it out. */
  const revertLine = (line: number, change: LineChange) => {
    const rows = file.content.split('\n')
    if (change.kind === 'add') rows.splice(line - 1, 1)
    else rows[line - 1] = change.was
    onChange(file.path, rows.join('\n'))
  }

  const at = position(file.content, caret)

  // Reported upward rather than printed here; see the note on the status row
  // below. Cleared on the way out so the bar does not keep showing a position
  // in a file that has been closed.
  useEffect(() => {
    if (!focused) return
    onCaret(at)
    return () => onCaret(null)
  }, [focused, at.line, at.column, onCaret])

  /**
   * Puts the caret on a line somebody clicked in the search panel.
   *
   * Keyed on the token, not the line: clicking the same hit twice has to work,
   * and a line number that has not changed would not re-trigger anything. The
   * offset is computed from the buffer rather than stored with the hit, because
   * the buffer is what the textarea indexes into and it may have been edited
   * since the search ran.
   */
  useEffect(() => {
    if (reveal === null) return
    const text = box.current
    if (!text) return
    const rows = file.content.split('\n')
    const line = Math.min(rows.length, Math.max(1, reveal.line))
    let start = 0
    for (let i = 0; i < line - 1; i++) start += rows[i].length + 1
    text.focus()
    text.setSelectionRange(start, start + rows[line - 1].length)
    setCaret(start)
    // Roughly a third of the way down rather than at the very top: a line at
    // the top edge has no context above it, which is most of what somebody
    // following a search hit is about to read.
    text.scrollTop = Math.max(0, (line - 1) * lineHeight(text) - text.clientHeight / 3)
    mirror()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reveal?.token])

  return (
    <>
      {finding && (
        <Find
          content={file.content}
          readOnly={file.readOnly === true}
          onClose={onCloseFind}
          onReplace={(next) => onChange(file.path, next)}
          onReveal={(start, end) => {
            const text = box.current
            if (!text) return
            text.focus()
            text.setSelectionRange(start, end)
            setCaret(start)
            // Bring it into view: setSelectionRange alone does not scroll a
            // textarea that is already focused.
            const line = file.content.slice(0, start).split('\n').length
            text.scrollTop = Math.max(0, (line - 6) * lineHeight(text))
            mirror()
          }}
        />
      )}

      <div className="editor" data-wrap={file.wrap ? '' : undefined}>
        {!gutterOff && (
          <div className="editor__gutter" aria-hidden="true">
            <div className="editor__lines" ref={gutter}>
              {Array.from({ length: lines }, (_, i) => {
                const change = changes.get(i + 1)
                return (
                  <div
                    key={i}
                    className={change ? 'editor__ln editor__ln--changed' : 'editor__ln'}
                  >
                    {i + 1}
                    {change && !file.readOnly && (
                      <button
                        type="button"
                        className="editor__undo"
                        title="还原此行"
                        aria-label={`还原第 ${i + 1} 行`}
                        onClick={() => revertLine(i + 1, change)}
                      >
                        ↺
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        )}

        <div className="editor__wrap">
          {!gutterOff && (
            <div className="editor__marks" aria-hidden="true">
              <div ref={marks}>
                {Array.from({ length: lines }, (_, i) => (
                  <div
                    key={i}
                    className={changes.has(i + 1) ? 'editor__mark editor__mark--on' : 'editor__mark'}
                  />
                ))}
              </div>
            </div>
          )}

          {/* Under the textarea, never in front of it: it must not take a
              click, a selection, or a screen reader's attention. Safe as
              innerHTML — see highlight(), which escapes every character Prism
              does not wrap itself. */}
          {painted !== null && (
            <pre
              className="editor__hl"
              ref={hl}
              aria-hidden="true"
              dangerouslySetInnerHTML={{ __html: painted }}
            />
          )}

          <textarea
            ref={box}
            className={painted !== null ? 'editor__text editor__text--lit' : 'editor__text'}
            value={file.content}
            readOnly={file.readOnly}
            onChange={(event) => onChange(file.path, event.target.value)}
            // Every mirror, from the one event: layers that scroll apart are
            // layers that say different things about the same line.
            onScroll={mirror}
            onSelect={(event) => setCaret(event.currentTarget.selectionStart)}
            onClick={(event) => setCaret(event.currentTarget.selectionStart)}
            onKeyUp={(event) => setCaret(event.currentTarget.selectionStart)}
            spellCheck={false}
            wrap={file.wrap ? 'soft' : 'off'}
            aria-label={`编辑 ${file.path}`}
          />
        </div>
      </div>

      {/* What an editor's foot is for: the facts you check before saving, and
          the two buttons that are actually edits. 关闭 used to be a third one —
          the × on the tab is already the way out, and two of them is two
          places to learn.

          Where the caret is used to be here too. It went to the shell's status
          bar when splitting arrived: two groups each printing a caret position
          means a reader has to work out which of the two is theirs before
          reading either, and there is only ever one caret. */}
      <div className="editor__status">
        <span className="editor__facts">
          {lang.label} · UTF-8 · {file.content.includes('\r\n') ? 'CRLF' : 'LF'}
        </span>
        {changes.size > 0 && <Badge tone="warn">{changes.size} 行已改</Badge>}
        {file.readOnly && <Badge>只读</Badge>}
        {huge && <Badge tone="muted">文件过大，已关闭高亮与改动标记</Badge>}
        {!huge && file.wrap && <Badge tone="muted">自动换行时没有行号</Badge>}
        <span className="editor__facts editor__facts--end">
          {formatBytes(bytes)}
          {lines > 0 && ` · ${lines} 行`}
        </span>
        <Button size="small" disabled={busy || !dirty} onClick={() => onRevert(file.path)}>
          还原
        </Button>
        <Button
          size="small"
          variant="primary"
          disabled={busy || !dirty || file.readOnly}
          title="保存（⌘/Ctrl + S）"
          onClick={() => onSave(file.path)}
        >
          保存
        </Button>
      </div>
    </>
  )
}

/**
 * Find and replace, over the buffer rather than over the DOM.
 *
 * Plain string search with a case switch, and nothing else. A regular
 * expression box here would be a second language to get right in a panel whose
 * job is to change one value in a config, and a bad pattern silently matching
 * nothing is worse than no box at all.
 */
function Find({
  content,
  readOnly,
  onClose,
  onReplace,
  onReveal,
}: {
  content: string
  readOnly: boolean
  onClose: () => void
  onReplace: (next: string) => void
  onReveal: (start: number, end: number) => void
}) {
  const [needle, setNeedle] = useState('')
  const [swap, setSwap] = useState('')
  const [cased, setCased] = useState(false)
  const cursor = useRef(0)
  const field = useRef<HTMLInputElement | null>(null)

  useEffect(() => {
    field.current?.focus()
    field.current?.select()
  }, [])

  const hay = cased ? content : content.toLowerCase()
  const pin = cased ? needle : needle.toLowerCase()
  const hits = useMemo(() => {
    if (pin === '') return 0
    let count = 0
    let from = hay.indexOf(pin)
    while (from !== -1) {
      count++
      from = hay.indexOf(pin, from + pin.length)
    }
    return count
  }, [hay, pin])

  const step = (back: boolean) => {
    if (pin === '') return
    const at = back
      ? hay.lastIndexOf(pin, Math.max(0, cursor.current - pin.length - 1))
      : hay.indexOf(pin, cursor.current)
    // Wrap rather than stop. A search that goes quiet at the end of the file
    // reads as "not found", which is a different and wrong answer.
    const found = at === -1 ? (back ? hay.lastIndexOf(pin) : hay.indexOf(pin)) : at
    if (found === -1) return
    cursor.current = back ? found : found + pin.length
    onReveal(found, found + pin.length)
  }

  return (
    <div className="efind" role="search">
      <input
        ref={field}
        className="efind__box"
        value={needle}
        placeholder="查找"
        aria-label="查找"
        onChange={(event) => {
          setNeedle(event.target.value)
          cursor.current = 0
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault()
            step(event.shiftKey)
          }
          if (event.key === 'Escape') {
            event.stopPropagation()
            onClose()
          }
        }}
      />
      {!readOnly && (
        <input
          className="efind__box"
          value={swap}
          placeholder="替换为"
          aria-label="替换为"
          onChange={(event) => setSwap(event.target.value)}
        />
      )}
      <label className="efind__case">
        <input type="checkbox" className="tick" checked={cased} onChange={() => setCased(!cased)} />
        区分大小写
      </label>
      <span className="efind__count">{needle === '' ? '' : `${hits} 处`}</span>
      <Button size="small" disabled={hits === 0} onClick={() => step(true)}>
        上一个
      </Button>
      <Button size="small" disabled={hits === 0} onClick={() => step(false)}>
        下一个
      </Button>
      {!readOnly && (
        <Button
          size="small"
          disabled={hits === 0}
          onClick={() => {
            // Case-insensitive replace has to walk the haystack rather than
            // use split/join: the matches are not all spelled the same way.
            let out = ''
            let from = 0
            for (;;) {
              const at = hay.indexOf(pin, from)
              if (at === -1) break
              out += content.slice(from, at) + swap
              from = at + pin.length
            }
            onReplace(out + content.slice(from))
          }}
        >
          全部替换
        </Button>
      )}
      <Button icon size="small" aria-label="关闭查找" title="关闭查找（Esc）" onClick={onClose}>
        ×
      </Button>
    </div>
  )
}

/** A file the editor will not open, and what can be done with it instead. */
function Card({
  instanceId,
  file,
  maxEditableBytes,
}: {
  instanceId: string
  file: OpenFile
  maxEditableBytes: number
}) {
  return (
    <div className="fcard">
      <FileIcon name={baseName(file.path)} />
      <b className="fcard__name">{baseName(file.path)}</b>
      <p className="fcard__note">
        {file.kind === 'oversize'
          ? `文件有 ${formatBytes(file.size)}，超过编辑器能打开的上限 ${formatBytes(
              maxEditableBytes,
            )}。`
          : file.kind === 'image'
            ? `图片 · ${formatBytes(file.size)}`
            : `这是一个二进制文件（${formatBytes(file.size)}），面板不提供在线编辑。`}
      </p>
      {file.kind === 'image' && (
        <img className="fcard__shot" src={previewURL(instanceId, file.path)} alt={baseName(file.path)} />
      )}
      <a className="btn" href={downloadURL(instanceId, file.path)} download>
        <Glyph name="download" />
        下载
      </a>
    </div>
  )
}

/** Line and column of an offset, both 1-based, the way an editor counts. */
function position(text: string, offset: number): { line: number; column: number } {
  const before = text.slice(0, Math.min(offset, text.length))
  const rows = before.split('\n')
  return { line: rows.length, column: rows[rows.length - 1].length + 1 }
}

/** One row's height in the box, read off it rather than assumed: the editor's
 *  type is set in the stylesheet and a theme could move it. */
function lineHeight(text: HTMLTextAreaElement): number {
  const px = Number.parseFloat(window.getComputedStyle(text).lineHeight)
  return Number.isFinite(px) && px > 0 ? px : 20
}

function baseName(path: string): string {
  const index = path.lastIndexOf('/')
  return index < 0 ? path : path.slice(index + 1)
}

function isJSON(path: string): boolean {
  return path.toLowerCase().endsWith('.json') || path.toLowerCase().endsWith('.mcmeta')
}

function isLog(path: string): boolean {
  return path.toLowerCase().endsWith('.log')
}
