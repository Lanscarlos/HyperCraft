import {
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

export interface FileEditorProps {
  instanceId: string
  files: Map<string, OpenFile>
  panes: EditorPane[]
  focusedPane: number
  maxEditableBytes: number
  onFocusPane: (index: number) => void
  onSelectTab: (pane: number, path: string) => void
  onCloseTab: (pane: number, path: string) => void
  onCloseOthers: (pane: number, path: string) => void
  onCloseRight: (pane: number, path: string) => void
  onSplit: () => void
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
  busy: boolean
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
  const { panes, onFocusPane } = props

  return (
    <section className="fedit" data-panes={panes.length}>
      {panes.map((pane, index) => (
        <Pane
          key={index}
          index={index}
          pane={pane}
          {...props}
          onFocus={() => onFocusPane(index)}
        />
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
  files,
  panes,
  focusedPane,
  maxEditableBytes,
  onSelectTab,
  onCloseTab,
  onCloseOthers,
  onCloseRight,
  onSplit,
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
  busy,
}: PaneProps) {
  const file = pane.active === null ? null : (files.get(pane.active) ?? null)
  const [context, setContext] = useState<{ x: number; y: number; path: string } | null>(null)
  const [finding, setFinding] = useState(false)

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
      onFocusCapture={onFocus}
      onPointerDownCapture={onFocus}
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
          <Button
            icon
            size="small"
            aria-label="分屏对照"
            title={panes.length >= 2 ? '已经是两栏了' : '分屏对照'}
            disabled={panes.length >= 2}
            onClick={onSplit}
          >
            <Glyph name="split" />
          </Button>
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
          <p>从中间的列表里点一个文件，会在这里打开。</p>
        </div>
      ) : (
        <>
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
  onCloseFind,
  onChange,
  onSave,
  onRevert,
  busy,
}: {
  instanceId: string
  file: OpenFile
  finding: boolean
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
          places to learn. */}
      <div className="editor__status">
        <span className="editor__facts">
          {lang.label} · UTF-8 · {file.content.includes('\r\n') ? 'CRLF' : 'LF'} · 行 {at.line}，列{' '}
          {at.column}
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
