# 文件管理页重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 本仓库默认 Inline Execution——直接用
> `executing-plans` 在当前会话里逐条实现，不要停下来问执行方式（见 CLAUDE.md）。
> 步骤用 `- [ ]` 复选框跟踪。

**Goal:** 删掉「编辑模式」开关，让文件页的布局跟随「是否有文件打开」自动变化，并把顶栏、
目录树、文件列表、编辑器四块按新的信息结构重排。

**Architecture:** 现在的 `FileManager.tsx`（1931 行）同时是编排器、列表、编辑器和一堆对话框。
按职责拆成五个文件：`FileManager`（状态与布局编排）、`FileBar`（顶栏与面包屑）、
`FileList`（列表）、`FileEditor`（编辑器与标签）、`FileTree`（只剩导航）。编辑器状态从
「一个 tabs 数组」改成「`files: Map<path, OpenFile>` + `panes: EditorPane[]`」，分屏才有地方放。
布局是一个 `grid`，列宽由 JS 算（可拖拽、按实例持久化），所以 `grid-template-columns` 走内联
style——这是 frontend-design skill 允许的「必须由 JS 计算的动态值」。

**Tech Stack:** React 18 + TypeScript + Vite，无新依赖。样式全部进 `web/src/styles.css`，
只用 token。检查手段：`npm --prefix web run build`（= `check-ui.mjs` + `tsc -b` + `vite build`）。

**Spec:** `docs/superpowers/specs/2026-09-14-file-manager-rework-design.md`

## Global Constraints

- **不新增依赖**，不引 CSS 框架 / 组件库 / CSS-in-JS，不拆分 `styles.css`。
- **只用令牌**：颜色、圆角、阴影、时长、缓动都从 `styles.css` 开头的令牌区取，不写裸 hex。
  新增令牌必须 light / dark 两个块都加（`:root,[data-theme='light'][data-palette='sakura']`
  与对应的 dark 块，另外三套 palette 在 palettes 区块里各自继承，不需要单独加）。
- **不改配色主题与字体**（spec §1、§11）。
- **代码注释用英文，文档用中文**（CLAUDE.md）。注释解释「为什么」。
- **`min-width: 0`**（列方向 `min-height: 0`）加在每一个可能装长文本的 flex/grid 子项上。
- **1024px 断点在两处**：`styles.css` 的媒体查询与 `App.tsx:59` 的 `DRAWER_QUERY`。本次不改
  这个数值，只是新代码里要复用它。
- **一屏一个实心按钮**：`variant="primary"` 的数量由 `web/scripts/check-ui.mjs` 的
  `PRIMARY_ALLOWED` 卡着，新增/拆分文件时要同步那张表。
- **图标按钮必须有 `aria-label`**（check-ui 规则）。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节。

---

### Task 1: 行级 diff 与日志高亮两个纯函数

编辑器的「改动行标记」「还原此行」「并排查看差异」三件事都要一份行级 diff；日志着色是
spec §7.2 点名的高亮扩展。两个都是纯函数，先做掉，后面的 UI 才有东西可用。

**Files:**
- Create: `web/src/filediff.ts`
- Modify: `web/src/highlight.ts`

**Interfaces:**
- Produces:
  - `export type LineChange = { kind: 'mod'; was: string } | { kind: 'add' }`
  - `export function diffLines(original: string, current: string): Map<number, LineChange>`
    —— key 是 **1-based 的当前行号**。`mod` 带上磁盘上那一行，供「还原此行」用；`add` 是新插入
    的行，还原即删除。删除掉的行不在结果里（屏幕上没有那一行可标）。
  - `export function sideBySide(a: string, b: string): Array<{ left: string | null; right: string | null; same: boolean }>`
    —— 冲突对话框的并排差异。
  - `export function highlightLog(code: string): string`（在 `highlight.ts` 里）

- [ ] **Step 1: 写 `web/src/filediff.ts`**

```ts
/**
 * Line-level diff for the file editor.
 *
 * Not a general diff library: the only two questions the editor asks are
 * "which lines on screen differ from the version on disk" and "what did this
 * one say before", and both are about the *current* text's line numbers.
 *
 * The shape is prefix/suffix trim first, then a bounded LCS over what is left.
 * Trimming is what makes this cheap on the real input — editing one value in a
 * 2000-line paper-global.yml leaves a band of one line — and the bound is what
 * keeps a pathological case (a whole file re-indented) from turning a keystroke
 * into an O(n²) walk over twenty thousand lines. Past the bound the whole band
 * is reported as changed without per-line provenance, which is the honest
 * answer: the gutter still marks it, and 还原此行 is simply not offered.
 */

/** What happened to one line of the current text, relative to the disk copy. */
export type LineChange =
  | { kind: 'mod'; was: string }
  | { kind: 'add' }

/** Past this many lines on either side of the band, the LCS is skipped. 400²
 *  is 160k cells, which is a fraction of a frame; 20 000² is a hang. */
const LCS_LIMIT = 400

export function diffLines(original: string, current: string): Map<number, LineChange> {
  const out = new Map<number, LineChange>()
  if (original === current) return out

  const a = original.split('\n')
  const b = current.split('\n')

  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++

  let tail = 0
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  ) {
    tail++
  }

  const left = a.slice(head, a.length - tail)
  const right = b.slice(head, b.length - tail)
  if (right.length === 0) return out

  if (left.length > LCS_LIMIT || right.length > LCS_LIMIT) {
    for (let i = 0; i < right.length; i++) out.set(head + i + 1, { kind: 'add' })
    return out
  }

  for (const op of align(left, right)) {
    if (op.right === null) continue
    if (op.left === null) out.set(head + op.right + 1, { kind: 'add' })
    else if (left[op.left] !== right[op.right]) {
      out.set(head + op.right + 1, { kind: 'mod', was: left[op.left] })
    }
  }
  return out
}

/** Pairs of indices into the two bands: a matched pair, an insert (left null)
 *  or a delete (right null). A plain LCS table — the band is bounded above. */
function align(a: string[], b: string[]): Array<{ left: number | null; right: number | null }> {
  const rows = a.length + 1
  const cols = b.length + 1
  const table = new Uint32Array(rows * cols)
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      table[i * cols + j] =
        a[i] === b[j]
          ? table[(i + 1) * cols + j + 1] + 1
          : Math.max(table[(i + 1) * cols + j], table[i * cols + j + 1])
    }
  }

  const out: Array<{ left: number | null; right: number | null }> = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ left: i, right: j })
      i++
      j++
    } else if (table[(i + 1) * cols + j] >= table[i * cols + j + 1]) {
      out.push({ left: i, right: null })
      i++
    } else {
      out.push({ left: null, right: j })
      j++
    }
  }
  while (i < a.length) out.push({ left: i++, right: null })
  while (j < b.length) out.push({ left: null, right: j++ })

  // A delete immediately followed by an insert is one line being rewritten,
  // and the editor has a better answer for that than "this line is new": it
  // can offer the old text back. Pairing them here is what turns most real
  // edits into `mod` rather than `add`.
  const merged: Array<{ left: number | null; right: number | null }> = []
  for (let k = 0; k < out.length; k++) {
    const here = out[k]
    const next = out[k + 1]
    if (here.right === null && next && next.left === null) {
      merged.push({ left: here.left, right: next.right })
      k++
      continue
    }
    merged.push(here)
  }
  return merged
}

/** The conflict dialog's two columns: every line of both versions, lined up.
 *  `null` on a side means that side has no line there. */
export function sideBySide(
  a: string,
  b: string,
): Array<{ left: string | null; right: string | null; same: boolean }> {
  const left = a.split('\n')
  const right = b.split('\n')
  if (left.length > LCS_LIMIT * 4 || right.length > LCS_LIMIT * 4) {
    // Too big to align; show them beside each other row for row, which is
    // still readable when the change is local and is never a lie about what
    // the two files contain.
    const rows: Array<{ left: string | null; right: string | null; same: boolean }> = []
    for (let i = 0; i < Math.max(left.length, right.length); i++) {
      rows.push({
        left: left[i] ?? null,
        right: right[i] ?? null,
        same: left[i] === right[i],
      })
    }
    return rows
  }
  return align(left, right).map((op) => ({
    left: op.left === null ? null : left[op.left],
    right: op.right === null ? null : right[op.right],
    same: op.left !== null && op.right !== null && left[op.left] === right[op.right],
  }))
}
```

- [ ] **Step 2: 在 `web/src/highlight.ts` 里加日志着色**

在 `LANGS` 里把 `log` 的注释换掉（现在写的是「Prism has no grammar that fits it」，本次正是
要给它一个），并在文件末尾加 `highlightLog`：

```ts
  // A server log is not a config, and Prism has no grammar for one. What a log
  // is read for is the level — INFO scrolls past, WARN is looked at, ERROR is
  // why the page was opened — so the level word is the only thing coloured,
  // by highlightLog rather than by a grammar.
  log: { label: '日志', prism: null },
```

```ts
/**
 * Colours a server log by level, and by nothing else.
 *
 * A log is read for one thing: which of these lines is the one that broke the
 * server. Tokenising the rest of the line — timestamps, thread names, the
 * plugin's own prose — would be a second colour scheme competing with the
 * answer. So the level word gets a class and everything else stays ink.
 *
 * Safe for dangerouslySetInnerHTML: the input is escaped first and the only
 * tags added afterwards are the spans below.
 */
export function highlightLog(code: string): string {
  return (
    escapeHTML(code).replace(
      /\b(INFO|WARN|WARNING|ERROR|SEVERE|FATAL|DEBUG|TRACE)\b/g,
      (word) => `<span class="token log-${LOG_TONE[word] ?? 'info'}">${word}</span>`,
    ) + '\n'
  )
}

const LOG_TONE: Record<string, string> = {
  INFO: 'info',
  DEBUG: 'muted',
  TRACE: 'muted',
  WARN: 'warn',
  WARNING: 'warn',
  ERROR: 'error',
  SEVERE: 'error',
  FATAL: 'error',
}
```

- [ ] **Step 3: 验证编译**

Run: `npm --prefix web run build`
Expected: `check-ui: 通过` 然后构建成功（这一步还没有人调用新函数，只验证类型）。

- [ ] **Step 4: 提交**

```bash
git add web/src/filediff.ts web/src/highlight.ts
git commit -m "编辑器: 加行级 diff 与日志分级着色

改动行标记、还原此行、冲突时的并排差异都要一份按当前行号索引的 diff，
所以它是一个纯函数而不是编辑器内部的状态。前后缀裁剪把常见改动收成
一两行的窄带，LCS 只跑在窄带上并设上限——整文件重新缩进不该把一次按键
变成两万行的二次方遍历。"
```

---

### Task 2: Glyph 补图标 + toast 带一个动作按钮

新顶栏和编辑器头需要几个现有 glyph 集里没有的图标；spec §7.4 要求「保存成功后 toast 提示
需重启 + 给一个「重启」按钮」，而现在的 `toast(message)` 只能放一句话。

**Files:**
- Modify: `web/src/components/Glyph.tsx`
- Modify: `web/src/toast.ts`
- Modify: `web/src/components/Toast.tsx`
- Modify: `web/src/styles.css`（`.toast__action`）

**Interfaces:**
- Produces:
  - `GlyphName` 新增 `'left' | 'clock' | 'split' | 'ellipsis' | 'chevron'`
  - `toast(message: string, action?: { label: string; onSelect: () => void }): void`
  - `ToastItem` 新增可选 `action`

- [ ] **Step 1: 在 `Glyph.tsx` 的 `GlyphName` 与 `GLYPHS` 里各加五个**

图形沿用这套图标的画法（`viewBox 0 0 24 24`、`stroke="currentColor"`、
`stroke-width` 由组件统一给、线性、圆头）：

```tsx
  left: <path d="M20 12H5m0 0 6-6m-6 6 6 6" />,
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 1.8" />
    </>
  ),
  split: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M12 4.5v15" />
    </>
  ),
  ellipsis: (
    <>
      <circle cx="5.5" cy="12" r="1.4" />
      <circle cx="12" cy="12" r="1.4" />
      <circle cx="18.5" cy="12" r="1.4" />
    </>
  ),
  chevron: <path d="m6 9.5 6 6 6-6" />,
```

`ellipsis` 的三个点要填充而不是描边，所以在 `Glyph` 组件里给它们
`fill="currentColor" stroke="none"` —— 直接写在 `<circle>` 上即可。

- [ ] **Step 2: `toast.ts` 带上可选动作**

```ts
export interface ToastItem {
  id: number
  message: string
  /** One thing to do about what just happened — "重启" after saving a config
   *  a running server has already read. Optional because nearly nothing
   *  needs it: an outcome that always wants a follow-up is a state, and a
   *  state belongs in the page rather than in a corner that expires. */
  action?: ToastAction
}

export interface ToastAction {
  label: string
  onSelect: () => void
}

export function toast(message: string, action?: ToastAction): void {
  seq += 1
  const next = [...items, { id: seq, message, action }]
  publish(next.length > MAX_STACKED ? next.slice(next.length - MAX_STACKED) : next)
}
```

- [ ] **Step 3: `Toast.tsx` 渲染它**，在 `.toast__body` 之后、`.toast__close` 之前：

```tsx
      {item.action && (
        <button
          className="toast__action"
          onClick={() => {
            item.action?.onSelect()
            close()
          }}
        >
          {item.action.label}
        </button>
      )}
```

- [ ] **Step 4: `styles.css` 里给 `.toast__action` 一条规则**，放在 `.toast__close` 规则之前：

```css
/* The one thing to do about what just landed. A link rather than a button
   face: the toast is already an interruption, and a filled control in the
   corner would be a second first-thing-to-press competing with the page. */
.toast__action {
  flex: none;
  padding: 2px 8px;
  background: none;
  border: none;
  border-radius: var(--radius-sm);
  color: var(--accent);
  cursor: pointer;
  font: inherit;
  font-weight: 600;
}

.toast__action:hover {
  background: var(--accent-soft);
}
```

- [ ] **Step 5: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/Glyph.tsx web/src/toast.ts web/src/components/Toast.tsx web/src/styles.css
git commit -m "toast 可以带一个动作；补五个图标

保存一个正在运行的服务器读过的配置，接下来要做的事是重启——说了「需重启
才生效」却不给按钮，等于让人自己走回控制台。动作是可选的：一个总要跟进
的结果其实是一种状态，状态该留在页面上而不是会消失的角落里。"
```

---

### Task 3: 目录树退回纯导航

spec §2.2 / §5：树只显示文件夹，删掉 `showFiles`、`filter`、行内 `⋯` 菜单和「筛选已展开的
目录」输入框；展开状态按实例持久化。

**Files:**
- Modify: `web/src/components/FileTree.tsx`
- Create: `web/src/localPrefs.ts`

**Interfaces:**
- Produces:
  - `export function readPref<T>(key: string, fallback: T): T`
  - `export function writePref(key: string, value: unknown): void`
  - `FileTree` 的 props 变成：`{ instanceId, path, onOpen, reloadKey? }`
  - `TreeNode` 保持 `{ name, path, isDir }` 导出不变（`FileManager` 还在用）

- [ ] **Step 1: 写 `web/src/localPrefs.ts`**

```ts
/**
 * Small per-browser view preferences — column widths, which folders are open,
 * how dense a list is.
 *
 * Deliberately not the panel's state: none of this is worth a round trip, none
 * of it is worth syncing between two people looking at the same server, and
 * all of it should survive a reload. localStorage is exactly that.
 *
 * Every read is guarded. A browser in private mode, a profile with storage
 * disabled and a value some earlier version wrote in another shape all arrive
 * the same way — as an exception or as JSON that does not parse — and the
 * answer to all three is the default, not a blank page.
 */
export function readPref<T>(key: string, fallback: T): T {
  try {
    const raw = window.localStorage.getItem(key)
    if (raw === null) return fallback
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

export function writePref(key: string, value: unknown): void {
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // A full or disabled store is not a reason to fail the interaction that
    // asked for the write. The preference is lost, the page is not.
  }
}
```

- [ ] **Step 2: 改 `FileTree.tsx`**

- `Props` 删掉 `showFiles` / `openPath` / `dirtyPaths` / `onOpenFile` / `filter` / `menuFor`，
  只留 `{ instanceId, path, onOpen, reloadKey? }`。
- `read()` 里只存文件夹：`listing.entries.filter((e) => e.isDir).map(...)`。头部那段
  「Everything, not just the directories」的注释要改写成新的理由。
- `visible(dir)` 变成 `children[dir] ?? []`。
- 展开集合初值从 localStorage 读，并在变化时写回：

```tsx
  const prefKey = `hc.files.tree.${instanceId}`
  const [open, setOpen] = useState<Set<string>>(
    () => new Set(readPref<string[]>(prefKey, [''])),
  )
  useEffect(() => {
    writePref(prefKey, [...open])
  }, [prefKey, open])
```

  原来那个 `useEffect(() => { setChildren({}); setOpen(new Set(ancestors(path))) }, [...])`
  只保留清缓存和「把 path 的祖先补进 open」，不要再把 open 整个覆盖掉——否则持久化白做。
- `Row` 删掉 `menu` / `label` / `dirty` / `onPin` 四个 prop 与对应的 JSX，`Branch` 同步。
- 顶部那段组件注释把「edit mode is the exception」整段删掉，换成「树只回答『我在哪』」。

- [ ] **Step 3: 验证**

Run: `npm --prefix web run build`
Expected: **失败**，`FileManager.tsx` 还在给 `FileTree` 传已经删掉的 prop。这是预期的——
Task 5 会把调用方改掉。为了让每个 task 都能独立编译，本 step 把 `FileManager.tsx` 里
`<FileTree …/>` 的调用先收敛成新签名，并删掉随之无人调用的 `renameInTree` / `removeInTree` /
`treeMenu` / `treeQuery` / `.ftree__find` 那段 JSX。

Run: `npm --prefix web run build`
Expected: 通过。

- [ ] **Step 4: 提交**

```bash
git add web/src/localPrefs.ts web/src/components/FileTree.tsx web/src/components/FileManager.tsx
git commit -m "目录树退回纯导航：只列文件夹，展开状态记住

同一个控件在两个模式下含义不同，要学两遍；而且 216px 宽度下树里的文件名
大量截断，读起来反而比列表差。树回答「我在哪」，列表回答「这里有什么」，
一人一件事。展开状态按实例存在 localStorage——一棵每次回来都是收起的树，
是每次都要重走一遍的树。"
```

---

### Task 4: 顶栏与可编辑面包屑

spec §4。删掉 `PageHead`（大标题 + 说明文字），换成 56px 的顶栏。

**Files:**
- Create: `web/src/components/FileBar.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: `Glyph`（Task 2 的 `left` / `chevron` / `ellipsis`）、`Menu`、`Button`、
  `ToolbarSearch`
- Produces:

```ts
export interface FileBarProps {
  dir: string
  /** 目录里有多少项、多大，做成 chip。null 表示还没读到。 */
  stats: { count: number; bytes: number } | null
  query: string
  onQuery: (next: string) => void
  searchRef: React.RefObject<HTMLInputElement>
  /** 校验并跳转。返回 false 表示路径不存在，面包屑就地报错、不跳。 */
  onNavigate: (next: string) => Promise<boolean>
  onUpload: () => void
  onNewFile: () => void
  onNewFolder: () => void
  onRefresh: () => void
  more: MenuItem[]
  busy: boolean
  pending: boolean
  writable: boolean
}
export function FileBar(props: FileBarProps): JSX.Element
```

- [ ] **Step 1: 写 `web/src/components/FileBar.tsx`**

结构（类名都要在 Step 2 里定义，否则 check-ui 直接拦下来）：

```tsx
<header className="fbar">
  <Button icon aria-label="返回上一级" title="返回上一级（Backspace）"
          className="fbar__up" disabled={dir === ''} onClick={…}>
    <Glyph name="left" />
  </Button>
  {editing ? <form className="fbar__path">…<input className="fbar__input" …/>…</form>
           : <nav className="fbar__crumbs" aria-label="目录路径">…</nav>}
  {stats && <span className="chip fbar__stats">{stats.count} 项 · {formatBytes(stats.bytes)}</span>}
  <div className="fbar__tools">
    <ToolbarSearch className="fbar__search" …/>
    <Button …>上传</Button>
    <Menu className="btn fbar__new" items={[新建文件, 新建文件夹]} …>
      <Glyph name="new-file" />新建<Glyph name="chevron" className="fbar__caret" />
    </Menu>
    <Button icon aria-label="刷新" …><Glyph name="refresh" …/></Button>
    <Menu className="btn btn--icon" items={more} title="更多" ariaLabel="更多">
      <Glyph name="ellipsis" />
    </Menu>
  </div>
</header>
```

面包屑三条要点，逐条写清楚：

1. **每段可点击**：段列表是 `['', ...dir.split('/')]`，第一段渲染成「实例根目录」。
2. **中间省略**：`parts.length > 3` 时渲染 `首段 / … / 倒数第二段 / 末段`，`…` 本身是一个
   带 `title` 的不可点 span。
3. **末段之后的空白可点**：末段后面跟一个 `<button className="fbar__blank">`，
   `aria-label="编辑路径"`，`flex: 1` 吃掉剩余宽度；按下后 `setEditing(true)`。

编辑态：

```tsx
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(dir)
  const [bad, setBad] = useState<string | null>(null)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    const target = draft.trim().replace(/^\/+|\/+$/g, '')
    if (await onNavigate(target)) {
      setEditing(false)
      setBad(null)
      return
    }
    // Not a navigation that failed — a path that is not there. Said in the
    // bar, next to what was typed, because that is where the eye already is.
    setBad('这个目录不存在')
  }
```

`Esc` 还原：`onKeyDown` 里 `if (event.key === 'Escape') { setEditing(false); setDraft(dir); setBad(null) }`。
进入编辑态时 `useEffect` 聚焦并 `select()`，`setDraft(dir)`。
`dir` 变化时也要 `setDraft(dir)`，否则跳转后输入框还留着旧路径。
错误用 `<small className="fbar__bad" role="alert">` 挂在 input 下方（`.fbar__path` 是
`position: relative`，`.fbar__bad` 绝对定位在下）。

- [ ] **Step 2: `styles.css` 加顶栏样式**

放在「文件页」区块的开头（Task 7 会重写这个区块，本 step 先把 `.fbar*` 写进去）。要点：
`height: 56px; flex: none; display: flex; align-items: center; gap: 14px;`，
`.fbar__crumbs` 与 `.fbar__input` 用 `font-family: var(--mono)`（沿用 `.mono` 在本仓库的写法
—— 查 `styles.css` 里现有的等宽声明并复用同一个字体栈），`.fbar__crumbs` 必须
`min-width: 0; overflow: hidden;` 且 **不换行**（`flex-wrap: nowrap`）——spec §10 明说
「深层目录下面包屑中间省略而非换行或溢出」。`.fbar__tools { margin-left: auto }`。

- [ ] **Step 3: 验证**

Run: `npm --prefix web run build`
Expected: 通过（`FileBar` 此时还没有人用，只验证类型与 check-ui 的类名规则）。

- [ ] **Step 4: 提交**

```bash
git add web/src/components/FileBar.tsx web/src/styles.css
git commit -m "文件页: 56px 顶栏，面包屑可粘贴路径

「服务器目录里的东西：jar、存档、配置和日志」看一次就够了，不该常驻占着
一百像素。路径从日志里复制出来是高频动作，所以末段之后留一块可点的空白，
点下去整条面包屑变成一个可粘贴的输入框；路径不存在就在原地说，不跳。"
```

---

### Task 5: 文件列表

spec §6 全部。

**Files:**
- Create: `web/src/components/FileList.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Produces:

```ts
export type Density = 'compact' | 'detail'
export type SortKey = 'name' | 'size' | 'modified'
export interface Sort { key: SortKey; asc: boolean }

export interface FileListProps {
  entries: FileEntry[]          // 已经过滤+排序好的行，由 FileManager 算
  dir: string
  density: Density
  onDensity: (next: Density) => void
  sort: Sort
  onSort: (key: SortKey) => void
  selected: Set<string>
  onSelect: (path: string, mode: 'replace' | 'toggle' | 'range') => void
  onClearSelection: () => void
  activePath: string | null      // 编辑器前台那个文件，行要高亮
  dirtyPaths: Set<string>
  onOpen: (entry: FileEntry) => void
  onOpenBackground: (entry: FileEntry) => void
  onRename: (entry: FileEntry) => void
  onDelete: (entry: FileEntry) => void
  onMove: (entries: FileEntry[]) => void
  onDownload: (entries: FileEntry[]) => void
  onBulkDelete: (entries: FileEntry[]) => void
  onUpload: () => void
  onDrop: (files: File[]) => void
  onRetry: () => void
  onClearQuery: () => void
  query: string
  error: string | null
  pending: boolean
  busy: boolean
  writable: boolean
  listRef: React.RefObject<HTMLDivElement>
}
export function FileList(props: FileListProps): JSX.Element
```

- [ ] **Step 1: 写 `FileList.tsx` 的骨架与列表头**

```tsx
<section className="flist" data-dropping={dropping || undefined}>
  {selected.size > 0 ? (
    <div className="flist__bulk">
      <span className="flist__bulk-count">
        已选 {selected.size} 项 · {formatBytes(selectedBytes)}
      </span>
      <Button size="small" onClick={…}>下载</Button>
      <Button size="small" onClick={…}>移动到…</Button>
      <Button size="small" variant="danger" onClick={…}>删除</Button>
      <button className="link flist__bulk-clear" onClick={onClearSelection}>取消选择</button>
    </div>
  ) : (
    <div className="flist__head">
      <span className="flist__where">当前目录</span>
      <div className="seg" role="group" aria-label="列表密度">
        <button className={…} aria-pressed={density === 'compact'} onClick={…}>紧凑</button>
        <button className={…} aria-pressed={density === 'detail'} onClick={…}>详情</button>
      </div>
    </div>
  )}
  <div className="flist__cols" role="row">…三个可点的列头…</div>
  <div className="flist__body" ref={listRef} role="listbox" aria-label="文件列表">…</div>
  <div className="flist__foot">…</div>
</section>
```

列头永远在（两种密度都在），紧凑只画「名称 / 大小」，详情多一列「修改时间」。这是 spec
§6.2「列头可切换排序字段与方向」和 §6.1「列的增减只由分段控件控制」两条同时成立的唯一写法：
列的可见性绑在 `density` 上，排序入口不随状态消失。

- [ ] **Step 2: 写行**

```tsx
function Row({ entry, … }: RowProps) {
  const ticked = selected.has(entry.path)
  return (
    <div
      className="frow"
      data-ticked={ticked || undefined}
      data-on={entry.path === activePath || undefined}
      role="option"
      aria-selected={ticked}
      tabIndex={-1}
      onClick={(event) => {
        // Shift is a range, ⌘/Ctrl adds one, a plain click is "this one" —
        // and a plain click also opens, because in a file manager selecting
        // and opening are the same gesture.
        if (event.shiftKey) { onSelect(entry.path, 'range'); return }
        if (event.metaKey || event.ctrlKey) { onSelect(entry.path, 'toggle'); return }
        onSelect(entry.path, 'replace')
        onOpen(entry)
      }}
    >
      <span className="frow__mark">
        <input type="checkbox" className="tick frow__tick" checked={ticked}
               aria-label={`选择 ${entry.name}`}
               onClick={(e) => e.stopPropagation()}
               onChange={() => onSelect(entry.path, 'toggle')} />
        <FileIcon name={entry.name} dir={entry.isDir} />
      </span>
      <span className="frow__name" title={entry.name}>{entry.name}</span>
      {entry.symlink && <Badge>符号链接</Badge>}
      <span className="frow__size num">{entry.isDir ? '—' : formatBytes(entry.size)}</span>
      {density === 'detail' && (
        <time className="frow__time" dateTime={entry.modified} title={stamp(entry.modified)}>
          {formatSince(entry.modified)}
        </time>
      )}
      <span className="frow__ops">…下载 + ⋯…</span>
    </div>
  )
}
```

`.frow__tick` 与 `.frow__mark .fileicon` 互相覆盖：默认图标可见、复选框 `opacity: 0`，
`.frow:hover`、`.frow:focus-within`、`.frow[data-ticked]` 三种情况下反过来。两个都绝对定位在
同一个 `.frow__mark` 里，所以行宽不会因为 hover 抖动。

`stamp()` 是本文件的小工具，`title` 要的是完整时间戳（带秒），和列里的相对时间不是一回事：

```tsx
/** The full stamp for a row's tooltip. formatDate drops seconds because a
 *  column does not need them; a tooltip is where the exact answer goes. */
function stamp(iso: string): string {
  const at = new Date(iso)
  return Number.isNaN(at.getTime()) ? '' : at.toLocaleString('zh-CN', { hour12: false })
}
```

`.frow__ops` 默认 `opacity: 0; pointer-events: none`，`.frow:hover` / `.frow:focus-within`
下变 `opacity: 1; pointer-events: auto`。里面是：

```tsx
  {!entry.isDir && (
    <a className="iconbtn" href={downloadURL(instanceId, entry.path)} download
       title="下载" aria-label={`下载 ${entry.name}`}
       onClick={(e) => e.stopPropagation()}>
      <Glyph name="download" />
    </a>
  )}
  <Menu className="iconbtn" items={rowMenu(entry)}
        title={`${entry.name} 的操作`} ariaLabel={`${entry.name} 的操作`}>
    <Glyph name="ellipsis" />
  </Menu>
```

`rowMenu(entry)` 的顺序固定：重命名、复制路径、移动到…、在新标签打开（仅文件且可编辑）、
**删除**（`danger: true`，末项）。`Menu` 已经把 danger 项画成红色；分隔线由
`.menu__item--danger` 的 `border-top` 提供（Task 7 在 styles.css 里加，若已有则复用）。
**文件夹没有「下载」**（后端没有打包接口，见 spec §9），也没有「复制」。

- [ ] **Step 3: 空态、搜索无结果、读取失败、拖拽**

三种状态都用 `EmptyState`（check-ui 的 `ruleEmptyStatesAreComponents` 规定 `.empty__*` 只能由
它写）。读取失败是**行内**卡片，不是全屏：

```tsx
  {error ? (
    <div className="alert alert--error flist__error">
      {error}
      <Button size="small" onClick={onRetry}>重试</Button>
    </div>
  ) : entries.length === 0 ? (
    query ? (
      <EmptyState … title={`没有匹配「${query}」的文件`} …>
        <button className="link" onClick={onClearQuery}>清除筛选</button>
      </EmptyState>
    ) : (
      <EmptyState glyph="folder-open" title="这个目录还是空的"
                  note="把文件拖进来，或者" …>
        <Button variant="primary" onClick={onUpload}>上传文件</Button>
      </EmptyState>
    )
  ) : (
    entries.map((entry) => <Row key={entry.path} entry={entry} … />)
  )}
```

先读 `web/src/components/EmptyState.tsx` 确认它实际的 props 名字，按它的签名调用，不要照抄
上面的占位名。

拖拽：`onDragEnter`/`onDragOver`/`onDragLeave`/`onDrop` 挂在 `.flist` 上，用现有
`FileManager` 里那套 `dragDepth` 计数（搬过来，注释一起搬），`data-dropping` 时画虚线边框 +
`松手上传到 {dir || '实例根目录'}`。

- [ ] **Step 4: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/FileList.tsx web/src/styles.css
git commit -m "文件列表: 两种密度、hover 行操作、可见的列增减

二十行乘三个常驻图标是六十个图标在跟文件名抢注意力，而且不可逆的删除天天
摆在手边。行尾默认只有大小，hover 才出下载和 ⋯，删除在 ⋯ 里、红色、末项。
列的增减改由「紧凑 / 详情」控制——原来打开文件会静默抽掉「修改时间」，
列的存在与否不该是宽度变化的副作用。"
```

---

### Task 6: 编辑器区

spec §7 全部：标签栏、分屏、改动行标记、状态条、冲突、不进编辑器的文件。

**Files:**
- Create: `web/src/components/FileEditor.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: `diffLines` / `sideBySide`（Task 1）、`highlightLog`（Task 1）
- Produces:

```ts
/** One open file. Keyed by path in FileManager; a pane holds paths, not
 *  copies, so the same file open in both split panes is one buffer. */
export interface OpenFile {
  path: string
  content: string
  /** What is on disk as far as this tab knows. Saving sets it; the change
   *  marks in the gutter are content-vs-original. */
  original: string
  /** The listing's mtime when this was read, for the conflict check. */
  modified: string
  /** Not a text file, or too big for the editor: the pane shows a card. */
  kind: 'text' | 'binary' | 'image' | 'oversize'
  size: number
  readOnly?: boolean
  wrap?: boolean
  /** The disk changed under an edited buffer; the banner is up. */
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
  onLocate: (path: string) => void
  onShowKeys: () => void
  busy: boolean
}
export function FileEditor(props: FileEditorProps): JSX.Element
```

- [ ] **Step 1: 标签栏**

一个 `Pane` 子组件，`panes.map` 出来，中间用 `.fedit__split` 分隔。每个 pane：

```tsx
<div className="fedit__tabs" role="tablist" aria-label="打开的文件">
  {pane.tabs.map((path) => (
    <div key={path}
         className={`etab${path === pane.active ? ' etab--on' : ''}`}
         onContextMenu={(event) => { event.preventDefault(); setContext({ x: event.clientX, y: event.clientY, path }) }}
         onAuxClick={(event) => { if (event.button === 1) { event.preventDefault(); onCloseTab(index, path) } }}>
      <button type="button" role="tab" aria-selected={path === pane.active}
              className="etab__pick" onClick={() => onSelectTab(index, path)} title={path}>
        <FileIcon name={baseName(path)} />
        <span className="etab__name">{baseName(path)}</span>
        {dirty && <span className="etab__dot" aria-label="有未保存的修改" />}
      </button>
      <button type="button" className="etab__close" onClick={() => onCloseTab(index, path)}
              aria-label={`关闭 ${baseName(path)}`}>×</button>
    </div>
  ))}
</div>
```

右键菜单是本文件自己的一个小 portal，不用 `Menu`（`Menu` 锚在触发按钮上，右键要锚在指针上）。
复用现成的 `.menu__sheet` / `.menu__item` 类和 `useDismiss`：

```tsx
{context && createPortal(
  <div className="menu__sheet" role="menu" data-state="in" data-dir="down"
       style={{ left: context.x, top: context.y }}>
    <button role="menuitem" className="menu__item" onClick={…}>关闭</button>
    <button role="menuitem" className="menu__item" onClick={…}>关闭其他</button>
    <button role="menuitem" className="menu__item" onClick={…}>关闭右侧</button>
    <button role="menuitem" className="menu__item" onClick={…}>复制路径</button>
  </div>, document.body)}
```

关闭其他 / 关闭右侧 也要出现在标签栏右侧的 `⋯` 菜单里——右键在键盘上没有入口，
而这两条不能只靠右键（可访问性，见 `FileTree` 里那条「不劫持右键」的注释的同一条理由）。

标签栏右侧：

```tsx
<div className="fedit__tools">
  {onOpenHistory && (
    <button className="link fedit__history" onClick={() => onOpenHistory(active.path)}>
      <Glyph name="clock" />配置历史
    </button>
  )}
  <Button icon size="small" aria-label="分屏对照" title="分屏对照" onClick={onSplit}
          disabled={panes.length >= 2}><Glyph name="split" /></Button>
  <Button icon size="small" aria-label="查找替换" title="查找替换（⌘/Ctrl+F）"
          onClick={() => setFinding(true)}><Glyph name="search" /></Button>
  <Menu className="btn btn--icon btn--small" items={moreItems} title="更多" ariaLabel="更多">
    <Glyph name="ellipsis" />
  </Menu>
</div>
```

`moreItems`：格式化（只有 JSON 可用，其余 `disabled` 且 label 说明原因）、切换换行、
只读模式、在文件列表中定位、关闭其他、关闭右侧、快捷键。

- [ ] **Step 2: 正文与改动行标记**

保留现有 `FileEditor` 里三层镜像（gutter / `<pre class="editor__hl">` / `<textarea>`）的全部
做法和注释——那段注释记的是 `transform` 而非 `scrollTop` 的坑，仍然成立，**不要动**。
变化只有两处：

1. gutter 从一个文本节点变成一行一个 `<div>`，因为改动行要给行号上底色：

```tsx
<div className="editor__lines" ref={gutter}>
  {Array.from({ length: lines }, (_, i) => {
    const change = changes.get(i + 1)
    return (
      <div key={i} className={change ? 'editor__ln editor__ln--changed' : 'editor__ln'}>
        {i + 1}
        {change && (
          <button className="editor__undo" title="还原此行"
                  aria-label={`还原第 ${i + 1} 行`}
                  onClick={() => revertLine(i + 1, change)}>↺</button>
        )}
      </div>
    )
  })}
</div>
```

   一行一个元素的代价是现有注释点名过的那个：两万行就是两万个元素。所以**沿用它已有的
   `lines === 0` 阈值**（`content.length > 400_000` 时 `lines` 为 0、gutter 整个不画），
   并且 `changes` 也在同一个阈值上关掉。这一条要写成注释留在代码里。

2. 正文左侧的竖条：`.editor__hl` 是一整块 `<pre>`，没法逐行加背景。用一个**第三层**
   `.editor__marks`，和另外两层同样的字体与行高、`aria-hidden`、跟着同一个 `transform`
   平移，里面一行一个空 `<div>`，改动行带 `--changed`（`background: var(--caution-soft)` +
   `box-shadow: inset 2px 0 0 var(--caution)`）。它在 `.editor__hl` 之下、textarea 之上的
   z 序之外——`pointer-events: none`。

```tsx
  const changes = useMemo(
    () => (huge || file.kind !== 'text' ? new Map() : diffLines(file.original, deferred)),
    [huge, file.kind, file.original, deferred],
  )
```

   用 `deferred`（已有的 `useDeferredValue`）而不是 `content`：diff 和高亮是同一笔交易，
   打字不能等在它们后面。

   `revertLine`：

```tsx
  /** Puts one line back the way the disk has it. `add` means the line is not
   *  on disk at all, so putting it back is removing it. */
  const revertLine = (line: number, change: LineChange) => {
    const rows = file.content.split('\n')
    if (change.kind === 'add') rows.splice(line - 1, 1)
    else rows[line - 1] = change.was
    onChange(file.path, rows.join('\n'))
  }
```

3. 高亮：`langOf(path).prism` 为 `null` 且扩展名是 `.log` 时走 `highlightLog`。

4. 光标与滚动位置按 `实例 + 路径` 缓存：模块级 `const spots = new Map<string, {caret:number; top:number; left:number}>()`，
   key 是 `${instanceId}:${path}`。切换标签时 `useLayoutEffect` 里写回 textarea。

- [ ] **Step 3: 查找替换**

`finding` 为真时，正文上方浮一条 `.efind`：查找输入、替换输入、`上一个`/`下一个`/
`全部替换`、`Esc` 关闭。实现用 `content.indexOf(needle, from)` + `textarea.setSelectionRange`
+ `focus()`，不引正则以外的任何东西（大小写敏感开关一个 checkbox 就够）。

- [ ] **Step 4: 状态条**

```tsx
<div className="editor__status">
  <span className="editor__facts mono">
    {lang.label} · UTF-8 · {file.content.includes('\r\n') ? 'CRLF' : 'LF'} ·
    {' '}行 {pos.line}，列 {pos.column}
  </span>
  {changes.size > 0 && <span className="chip chip--caution">{changes.size} 行已改</span>}
  <span className="editor__facts editor__facts--end mono">
    {formatBytes(bytes)}{lines > 0 && ` · ${lines} 行`}
  </span>
  <Button size="small" disabled={busy || !dirty} onClick={() => onRevert(file.path)}>还原</Button>
  <Button size="small" variant="primary" disabled={busy || !dirty}
          title="保存（⌘/Ctrl + S）" onClick={() => onSave(file.path)}>保存</Button>
</div>
```

没有「关闭」按钮，没有「Ctrl / ⌘ + S 也能保存」那行字——后者变成保存按钮的 `title`。
`chip--caution` 若 `styles.css` 里没有，在 Task 7 里加（**两个主题块都要加**，如果它引入了
新令牌的话；用现有的 `--caution` / `--caution-soft` / `--caution-ink` 就不需要新令牌）。

- [ ] **Step 5: 磁盘变化提示条与冲突对话框**

提示条在标签栏与正文之间：

```tsx
{file.stale && (
  <div className="alert alert--warn editor__stale">
    磁盘上的文件已变化。
    <Button size="small" onClick={() => onReload(file.path)}>重新加载</Button>
    <Button size="small" onClick={() => onKeepMine(file.path)}>保留我的版本</Button>
  </div>
)}
```

`alert--warn` 在 `styles.css` 里是否存在要先确认，没有就用现成的那一种 alert 变体，别新造。

冲突对话框由 `FileManager` 拥有（它才有 api 与 busy），`FileEditor` 不负责——见 Task 7。
并排差异用 Task 1 的 `sideBySide`，画成两列 `.ediff__row`，`same` 为假的两边分别用
`--danger-soft` / `--ok-soft` 打底。

- [ ] **Step 6: 不进编辑器的文件**

`file.kind !== 'text'` 时正文换成卡片：

```tsx
<div className="fcard">
  <FileIcon name={baseName(file.path)} />
  <b className="fcard__name">{baseName(file.path)}</b>
  <p className="fcard__note">
    {file.kind === 'oversize'
      ? `文件较大（${formatBytes(file.size)}），超过编辑器能打开的上限 ${formatBytes(maxEditable)}。`
      : '这是一个二进制文件，面板不提供在线编辑。'}
  </p>
  {file.kind === 'image' && <img className="fcard__shot" src={previewURL(instanceId, file.path)} alt={baseName(file.path)} />}
  <a className="btn" href={downloadURL(instanceId, file.path)} download>下载</a>
</div>
```

- [ ] **Step 7: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

- [ ] **Step 8: 提交**

```bash
git add web/src/components/FileEditor.tsx web/src/styles.css
git commit -m "编辑器: 标签栈、分屏、改动行标记、精简状态条

页面上一直写着「可以同时开着几个对照」，实现却只有一个标签。补成真正的
标签栈，加上左右分屏——对照两份配置是这一页存在的理由。行号槽给改动行上
警告色底、正文左侧补一条竖条，悬停行号可以还原那一行：这和配置历史是同一
件事的两端，改之前看得见，改之后回得去。底部只留还原和保存，「配置历史」
挪到标签栏右侧——它是「关于这个文件」的入口，不是编辑动作。"
```

---

### Task 7: 编排器与新布局

把上面四块装进 `FileManager`，删掉 `editing` / `narrowPane` / `treeOpen` 那套状态，
换成 A/B/C 三态 + 可拖拽列宽，并重写 `styles.css` 的「文件页」区块。

**Files:**
- Modify: `web/src/components/FileManager.tsx`（大改）
- Modify: `web/src/components/InstanceView.tsx`、`web/src/App.tsx`（`onWorkspaceChange`）
- Modify: `web/src/styles.css`（重写 `/* ---- 文件页 */` 到下一个区块之间）
- Modify: `web/scripts/check-ui.mjs`（`PRIMARY_ALLOWED`）

- [ ] **Step 1: 状态改形**

```tsx
  const [files, setFiles] = useState<Map<string, OpenFile>>(() => new Map())
  const [panes, setPanes] = useState<EditorPane[]>([{ tabs: [], active: null }])
  const [focusedPane, setFocusedPane] = useState(0)
  const open = panes.some((pane) => pane.tabs.length > 0)   // ← 状态 A / B 的唯一判据
```

`open` 就是 spec §2.1 那张表：没有它就是 A，有它就是 B。没有开关，没有别的输入。

- [ ] **Step 2: 列宽、折叠与持久化**

```tsx
  const widthKey = `hc.files.cols.${instance.id}`
  const [cols, setCols] = useState(() =>
    readPref(widthKey, { tree: 216, list: 264 }),
  )
  useEffect(() => { writePref(widthKey, cols) }, [widthKey, cols])

  const [treeFolded, setTreeFolded] = useState(false)
  const [listFolded, setListFolded] = useState(false)
```

`TREE_MIN = 180`, `TREE_MAX = 360`, `LIST_MIN = 220`, `LIST_MAX = 560`, `EDITOR_MIN = 640`。
拖拽用 `onPointerDown` + `setPointerCapture`，`pointermove` 里算新宽度并 clamp：

```tsx
  /** Drags one divider. The editor's floor is enforced here rather than in CSS
   *  because CSS cannot say "take it out of whichever rail is being dragged":
   *  a minmax floor on the editor track would simply overflow the grid. */
  const drag = (which: 'tree' | 'list') => (event: React.PointerEvent) => {
    const frame = grid.current
    if (!frame) return
    event.currentTarget.setPointerCapture(event.pointerId)
    const start = event.clientX
    const from = cols[which]
    const room = frame.clientWidth
    const move = (move: PointerEvent) => {
      const raw = from + (move.clientX - start)
      const other = which === 'tree' ? (open ? cols.list : 0) : cols.tree
      const ceiling = Math.max(
        which === 'tree' ? TREE_MIN : LIST_MIN,
        room - other - (open ? EDITOR_MIN : 0) - GAP * (open ? 2 : 1),
      )
      const next = Math.min(
        Math.min(which === 'tree' ? TREE_MAX : LIST_MAX, ceiling),
        Math.max(which === 'tree' ? TREE_MIN : LIST_MIN, raw),
      )
      setCols((current) => ({ ...current, [which]: next }))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
  }
```

`gridTemplateColumns` 由 JS 拼：

```tsx
  const RAIL = 40
  const template = [
    treeFolded ? `${RAIL}px` : `${cols.tree}px`,
    'var(--fm-grip)',
    ...(narrow && open ? [] : [listFolded ? `${RAIL}px` : open ? `${cols.list}px` : 'minmax(0, 1fr)']),
    ...(open ? ['var(--fm-grip)', 'minmax(0, 1fr)'] : []),
  ].join(' ')
```

`narrow` = `useMediaQuery('(max-width: 1024px)')`（和 `App.tsx:59` 的 `DRAWER_QUERY` 同一个数，
注释里要点名这一点）。`tight` = `useMediaQuery('(max-width: 1280px)')`：spec §3，
这一档下列表强制紧凑且 grip 不可拖。

- [ ] **Step 3: 密度**

```tsx
  const densityKey = `hc.files.density.${instance.id}`
  const [chosen, setChosen] = useState<Density | null>(() => readPref<Density | null>(densityKey, null))
  // A is 详情, B is 紧凑 — until somebody says otherwise, and then that is what
  // it is in both. The automatic default is a convenience, not a rule that
  // gets to overrule a choice that was made on purpose.
  const density: Density = chosen ?? (tight || open ? 'compact' : 'detail')
```

- [ ] **Step 4: 打开文件**

```tsx
  const openEntry = async (entry: FileEntry, background = false) => {
    if (entry.isDir) { void load(entry.path); return }
    await openPath(entry.path, { background, hint: entry })
  }
```

`openPath` 决定 `kind`：目录项的 `editable` 为假时，图片 → `'image'`，
`isBinary(name)` → `'binary'`，否则 → `'oversize'`；`editable` 为真才去 `api.readFile`。
`modified` 从 `hint.modified` 取；没有 hint（配置历史跳过来）就先列一次父目录拿。
**不再有预览标签**（spec §6.2「双击不额外定义」），所以 `EditorState.preview` 整个删掉。

12 个上限 + LRU：

```tsx
  /** Twelve is where a tab strip stops being a strip and starts being a list
   *  you scroll to find things in. Past it the oldest *saved* tab goes —
   *  never one with unsaved work in it, which is the one thing a cap must
   *  not be allowed to throw away. */
  const MAX_TABS = 12
```

- [ ] **Step 5: 保存与冲突**

```tsx
  const save = async (path: string) => {
    const file = files.get(path)
    if (!file || file.content === file.original) return
    setBusy(true)
    try {
      const now = await mtimeOf(path)
      if (now !== null && now !== file.modified) {
        setConflict({ path, mine: file.content, theirs: (await api.readFile(instance.id, path)).content })
        return
      }
      await api.writeFile(instance.id, path, file.content)
      const after = await mtimeOf(path)
      patchFile(path, { original: file.content, modified: after ?? file.modified, stale: false })
      announceSaved(path)
    } catch (err) { … } finally { setBusy(false) }
  }
```

```tsx
/**
 * The file's mtime, read out of its directory listing.
 *
 * The read endpoint returns content and nothing else, and the write endpoint
 * takes no precondition, so this is the only mtime the panel can get and the
 * comparison happens here rather than on the server. That makes it a check
 * with a race in it: somebody can write the file between this listing and the
 * PUT that follows. It is strictly better than not looking — the window is
 * milliseconds instead of however long the tab was open — but it is not a
 * guarantee, and it should be replaced the day the API carries an mtime or an
 * ETag through read and write. See §9 of the design note.
 */
  const mtimeOf = async (path: string): Promise<string | null> => { … }
```

`announceSaved`：路径命中 `server.properties` / `bukkit.yml` / `spigot.yml` / `paper-*.yml` /
`plugins/**/config.yml` 且 `instance.state === 'running'` 时，
`toast('已保存 · 需重启服务器后生效', { label: '重启', onSelect: … })`；否则普通 toast。
重启走 `api.power(instance.id, 'restart')`，并且**先 `ask` 一次**——重启会把在线玩家踢下线，
那不是一个可以从 toast 上一键完成的动作。

- [ ] **Step 6: 外部变更轮询**

```tsx
  // No file-change stream exists (the WebSocket carries the console and
  // nothing else), so this is a poll, and it is deliberately a lazy one: ten
  // seconds, only while the tab is in front, and one listing per directory
  // rather than one per open file.
  useEffect(() => {
    if (!active || files.size === 0) return
    const tick = async () => { … }
    const timer = window.setInterval(() => { if (document.hasFocus()) void tick() }, 10_000)
    return () => window.clearInterval(timer)
  }, [active, files, instance.id])
```

磁盘 mtime 变了：`content === original` 时静默重载（`api.readFile` + 更新 original/content/
modified），否则 `patchFile(path, { stale: true })` 让提示条出来。

- [ ] **Step 7: 删除确认与移动对话框**

- 文件：现有 `ask` 即可。
- 文件夹：新的本地 `ConfirmNameDialog`（`Modal` + 一个必须与文件夹名逐字相同的输入框）。
  `world` / `plugins` / `libraries` / `versions` / `config` 这几个名字额外挂一条
  `detail` 警告。
- 批量：`ask` + `NameList`（已有）前三个名字 + `等 N 项`。
- 移动到…：本地 `MoveDialog`，里面就是一棵 `FileTree`（`onOpen` 选中目标目录），
  确认后对每一项 `api.renameFile(id, entry.path, joinPath(target, entry.name))`。
  和 `removeMany` 一样**串行**，理由相同——注释照抄那一条。

- [ ] **Step 8: 快捷键**

一个 `useEffect`，`active` 为真时挂 `window` 的 `keydown`：

| 键 | 行为 | 备注 |
| --- | --- | --- |
| `⌘/Ctrl+F` | 焦点在编辑器 → 编辑器查找；否则聚焦顶栏搜索 | `event.preventDefault()` |
| `⌘/Ctrl+S` | 保存前台标签 | 否则浏览器要存网页 |
| `⌘/Ctrl+W` | 关闭前台标签 | 浏览器可能吃掉，`preventDefault` 尽力 |
| `↑ ↓` | 列表里移动选择 | 焦点在输入框 / textarea 里时不接管 |
| `Enter` | 打开选中项 | 同上 |
| `Backspace` | 返回上级 | 同上，且不在编辑态面包屑里 |
| `Esc` | 退出路径编辑 → 关查找框 → 取消多选 | 按这个顺序，一次只退一层 |

判断「焦点在可输入的地方」用一个小函数，注释说明为什么不能只看 `tagName`
（`contenteditable` 与 `role="textbox"`）。

- [ ] **Step 9: 外壳与 `onWorkspaceChange`**

`FileManager` 顶层从 `<div className={editing ? 'stack stack--full' : 'stack'}>` 变成恒定的
`<div className="stack stack--full fmpage">`——A 和 B 都是一屏一件事的工作区。
`onWorkspaceChange` 整个 prop 从 `FileManager` 删掉（它是「编辑模式要把侧栏收成导轨」的遗物，
spec §1 明说不改侧边导航），`InstanceView.tsx:152` 那一行跟着删。`InstanceView` 自己的
`onWorkspaceChange` prop 与 `App.tsx:783` 的 `setWorkspace` 如果没有别的调用者也一起删——
先 `grep -rn "onWorkspaceChange\|setWorkspace\|workspace" web/src` 确认，**有其它调用者就留着**。

- [ ] **Step 10: 重写 `styles.css` 的「文件页」区块**

删掉 `.fm--editing` / `.fm--open` / `.file-mode` / `.ftree__bar` / `.ftree__where` /
`.ftree__find` / `.ftree__leave` / `.ftree__more` / `.editor__back` / `.editor__tree` /
`.editor__hint` / `.data-table--files` 的整套规则和 `.fm--open` 那条按 pane 丢列的媒体查询
（列的增减现在由密度控制，spec §6.1）。新区块的骨架：

```css
.fm {
  display: grid;
  gap: 14px;
  flex: 1;
  min-height: 0;
  align-items: stretch;
  /* The grip is a track of its own rather than a border on a pane: a border
     cannot be grabbed, and a pane with a resize cursor over its own edge
     starts a drag when somebody meant to click the row under it. */
  --fm-grip: 6px;
}

@media (max-width: 1024px) {
  /* The list stops being a column and becomes a drawer: below this width
     three panes is one pane and two slivers. The number is App.tsx's
     DRAWER_QUERY and the sheet's own 1024 step — all three move together. */
  .fm { gap: 10px; }
}
```

三个面板都要 `min-width: 0; min-height: 0; overflow: hidden;` 和
`display: flex; flex-direction: column;`。grip 是 `.fm__grip`，`cursor: col-resize`，
`tight` 时 `pointer-events: none; opacity: 0`。折叠导轨 `.fm__rail` 是 40px 宽、
里面一个竖排文字的按钮。

**新令牌**：这一版不需要任何新颜色令牌——警告色用 `--caution` / `--caution-soft` /
`--caution-ink`，强调色用 `--accent` / `--accent-soft`，都是现成的。如果实现中确实需要新令牌，
**light 和 dark 两个块都要加**，否则暗色下是空值。

- [ ] **Step 11: 同步 check-ui 的实心按钮表**

`FileManager.tsx` 拆开之后实心按钮换了位置。跑一遍 `npm --prefix web run check:ui`，按它报的
数字把 `PRIMARY_ALLOWED` 改成实际值，并给每条写清楚理由（那张表里每一条都是「有人看过」的
承诺）。预期：
- `components/FileManager.tsx`：重命名、删除文件夹、移动、冲突四个对话框各一个 → 按实测填。
- `components/FileList.tsx`：空目录状态里的「上传文件」→ 1。
- `components/FileEditor.tsx`：状态条的「保存」→ 1。

- [ ] **Step 12: 验证**

```bash
npm --prefix web run build
```
Expected: `check-ui: 通过` + 构建成功。

- [ ] **Step 13: 提交**

```bash
git add -A web/src web/scripts
git commit -m "文件页: 删掉「编辑模式」，布局跟着有没有打开文件走

那个开关同时做了三件不相关的事——收侧栏、把树从只有文件夹换成文件夹加
文件、放大编辑器——于是想看清代码得先去点一个叫「编辑模式」的按钮。现在
没有开关：没开文件是树 + 列表，开了文件自动变成树 + 列表 + 编辑器，栏宽
可以拖、按实例记住。编辑器在 1600px 视口下拿到 ≥780px，Paper 默认配置最长
的那行注释不再触发横向滚动。"
```

---

### Task 8: 收尾——CHANGELOG、后端检查、自查

- [ ] **Step 1: `CHANGELOG.md` 的「未发布」小节**

```markdown
### 变更
- 文件管理页重排：移除「编辑模式」开关，布局跟随「是否打开文件」自动在两栏与三栏之间切换；
  栏宽可拖拽并按实例记住。
- 目录树只列文件夹，展开状态按实例保留；删掉「筛选已展开的目录」输入框。
- 文件列表加「紧凑 / 详情」两种密度，列的增减只由这个开关控制；行操作改为 hover 显示，
  删除收进 ⋯ 菜单；删除文件夹需要输入文件夹名确认。
- 编辑器支持多标签与左右分屏，改动行在行号槽与正文左侧有标记并可逐行还原；
  底部状态条精简为「还原 / 保存」，「配置历史」移到标签栏右侧。
- 面包屑每一段可点击，末段右侧留出可点区域，点击后可直接粘贴路径跳转。
- 保存时会先比对磁盘上的修改时间，冲突时可以选择覆盖、重载或并排查看差异；
  文件在面板外被改动时编辑器顶部会提示。
```

- [ ] **Step 2: 后端没动，但还是跑一遍**

```bash
make lint && make test
```
Expected: 全绿（本次不改 Go 代码，这一步是确认没有误伤）。

- [ ] **Step 3: 提交并合回 main**

按 CLAUDE.md 的工作流程：推功能分支 → 切 `main` → `git pull origin main` → 合并 → 推 `main`
→ 切回功能分支。

---

## 验收

对着 spec §10 逐条过。**下面这几条因为后端没有对应能力，本次不实现**，在 PR 里说明：

- 文件夹「打包下载」与批量「下载 zip」（没有 archive 接口）
- 目录树底部的「实例配额」进度条（没有配额 API）
- 大文件的「仅查看前 2 MB」（没有 range 读）
- 非 UTF-8 编码的切换与重新解码（读接口不返回检测到的编码）
- 行操作里的「复制」（没有服务端复制接口）
- YAML / Properties 的「格式化」（只有 JSON 可用，其余禁用并说明原因）

**布局**
- [ ] 页面上不存在名为「编辑模式」的开关或按钮
- [ ] 未打开文件时为两栏；打开任意文件后自动变三栏，无需任何点击
- [ ] 三栏状态下编辑器实际渲染宽度 ≥ 780px（1600px 视口）
- [ ] 打开 `bukkit.yml`，文件头部最长的那行注释不触发横向滚动
- [ ] 拖拽调整栏宽后刷新页面，宽度保持

**树与列表**
- [ ] 目录树在任何状态下都只显示文件夹
- [ ] 列表列的增减只由「紧凑 / 详情」切换控制
- [ ] 未 hover 的行内看不到下载 / 重命名 / 删除图标
- [ ] hover 行尾出现下载与 ⋯，删除在 ⋯ 内且为红色末项
- [ ] 删除文件夹必须输入文件夹名才能提交
- [ ] 选中 ≥1 项后，列表头变为批量操作条
- [ ] 修改时间在同一列内格式统一（7 天内相对，其余绝对）

**面包屑**
- [ ] 每一段可点击跳转
- [ ] 点击末段右侧空白可切换为路径输入框，能粘贴路径并回车跳转
- [ ] 输入不存在的路径显示行内错误且不跳转
- [ ] 深层目录下面包屑中间省略而非换行或溢出

**编辑器**
- [ ] 可同时打开 ≥3 个文件并以标签切换
- [ ] 未保存标签显示圆点，关闭时弹确认
- [ ] 分屏后左右两栏各自独立切换标签
- [ ] 改动行在行号槽与正文左侧有可见标记，保存后消失
- [ ] 底部只有「还原」和「保存」两个按钮，「配置历史」在标签栏右侧
- [ ] 无改动时「保存」「还原」为禁用态
- [ ] `⌘/Ctrl+S` 可保存
- [ ] 打开 `paper-*.jar` 不进入文本编辑器，显示信息卡片 + 下载

**边界**
- [ ] 空目录、搜索无结果、读取失败三种状态各有对应界面
- [ ] 拖拽文件到列表区有高亮反馈并能上传
- [ ] 服务器运行中保存 `server.properties` 会提示需重启

**样式自查（frontend-design skill）**
- [ ] `npm --prefix web run build` 通过
- [ ] 明暗两种模式都看过；没有引入只在一个主题块里定义的令牌
- [ ] 1440 / 1200 / 1024 / 768 / 390 宽度下无横向溢出、无错位
- [ ] 折叠侧栏、打开抽屉、开着控制台的实例页三处没被波及
