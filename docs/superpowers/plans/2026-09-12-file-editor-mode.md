# 文件页编辑模式 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给实例文件页加一个编辑模式——进入后左侧导航折成图标条、文件列表让位、目录树吸收文件、编辑器铺满并带语法高亮。

**Architecture:** 不换编辑器内核，不动路由，后端零改动。编辑模式是 `FileManager` 内部的一个布尔状态，通过两层 props 镜像到 `App` 决定外壳形态；高亮是垫在 textarea 下面的一层只读 `<pre>`，复用现有行号栏那套滚动镜像。

**Tech Stack:** React 18 + TypeScript + Vite，`prismjs`（新增），单个 `web/src/styles.css`。

**Spec:** `docs/superpowers/specs/2026-09-12-file-editor-mode-design.md`

## Global Constraints

- **样式只进 `web/src/styles.css`**。不引 CSS 框架、组件库、CSS-in-JS，不拆分样式文件。
- **颜色、圆角、阴影、时长、缓动只用令牌**，不写裸 hex。新增令牌加在 `:root,[data-theme='light'][data-palette='sakura']`（styles.css:74）和 `:root[data-theme='dark'],[data-theme='dark'][data-palette='sakura']`（styles.css:291）两个块里，**值一律写成 `var(--已有令牌)`**，这样 green / yellow / blue 三套配色自动跟着走，不必改另外六个块。
- **代码注释用英文，文档和 CHANGELOG 用中文。** 沿用所处文件的语言。
- **注释解释「为什么」**，不解释代码在做什么。现有注释记录的是踩过的坑，不要清理。
- **`min-width: 0`**（列方向 `min-height: 0`）：任何可能装长文本或终端的 flex/grid 子项都要写。这是本仓库最高频的布局 bug。
- **1024px 断点在两处**：`styles.css` 的媒体查询和 `App.tsx` 的 `DRAWER_QUERY`（App.tsx:70）。改一处必须改另一处。本次新增的 1200px 断点同样要成对。
- **不准碰 `--term-*` 和 `--shell-*`**。那两块终端画布在明暗两种模式下都要保持深色且明显不同色，这是防止把危险命令敲进错误终端的唯一屏障。
- **前端没有单测和 lint，`tsc -b` 是唯一的自动检查**。所以每个任务的验证都是 `npm --prefix web run build` 加一段明确写死的人工核对清单——不是「自己看看对不对」，是照着清单一条条点。
- 提交信息写**为什么改**，不只写改了什么。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节（最后一个任务统一做）。

---

### Task 1: 把文件图标抽成共享模块

`FileManager.tsx` 现在 1731 行，其中 1378–1660 是一段自成一体的图标模块。树要用同一套图标，所以先原样搬出来。**这个任务零行为变化**：搬完界面必须跟搬之前一模一样。

**Files:**
- Create: `web/src/components/Glyph.tsx`
- Create: `web/src/components/FileIcon.tsx`
- Modify: `web/src/components/FileManager.tsx`（删掉 1378–1660 的定义，改成 import）

**Interfaces:**
- Consumes: 无
- Produces:
  - `Glyph.tsx`: `export type GlyphName = 'up' | 'home' | ...`（原样照搬那个联合类型），`export function Glyph({ name, className }: { name: GlyphName; className?: string }): ReactElement`
  - `FileIcon.tsx`: `export type Kind = 'dir' | 'jar' | ...`（原样照搬），`export function kindOfName(name: string): Kind`，`export function FileIcon({ name, dir }: { name: string; dir?: boolean }): ReactElement` —— 渲染 `<span class="fileicon fileicon--{TONE[kind]}"><Glyph name={GLYPH[kind]} /></span>`，`dir` 为真时强制 `kind = 'dir'`

- [ ] **Step 1: 建 `web/src/components/Glyph.tsx`**

把 `FileManager.tsx` 的 `type GlyphName`（1378 起）、`const GLYPHS`（1400 起）、`function Glyph`（1527 起）整段剪过去，三个都加 `export`。**一个字符都不要改**，连注释一起搬。文件顶部加：

```tsx
import type { ReactElement } from 'react'

/**
 * The line icons the file pane draws with.
 *
 * Lifted out of FileManager so the tree can use the same set: in edit mode the
 * tree shows files, and a file drawn with one icon in the listing and another
 * in the tree is two answers to the same question.
 */
```

- [ ] **Step 2: 建 `web/src/components/FileIcon.tsx`**

把 `type Kind`（1549 起）、`const KIND_BY_EXT`（1561 起）、`const GLYPH`（1611 起）、`const TONE`（1627 起）、`function extensionOf`（1642 起）、`function kindOfName`（1651 起）整段剪过去。`Kind` 和 `kindOfName` 加 `export`，其余保持模块私有。**注释一起搬**——`TONE` 上面那段「四种色调而不是九种」的理由仍然成立。然后补一个组件：

```tsx
import { Glyph } from './Glyph'

/** One file's icon, tinted by what kind of file it is. Directories pass
 *  `dir`: a folder is not decided by its extension. */
export function FileIcon({ name, dir }: { name: string; dir?: boolean }) {
  const kind = dir ? 'dir' : kindOfName(name)
  return (
    <span className={`fileicon fileicon--${TONE[kind]}`}>
      <Glyph name={GLYPH[kind]} />
    </span>
  )
}
```

- [ ] **Step 3: 改 `FileManager.tsx` 用 import**

删掉刚才搬走的六段定义，顶部加：

```tsx
import { FileIcon, kindOfName } from './FileIcon'
import { Glyph } from './Glyph'
```

`FileManager.tsx` 里现在有两处手写 `<span className={`fileicon fileicon--${TONE[...]}`}>`（标签页那处在 1157 附近，列表行那处在 940 附近）。把它们换成 `<FileIcon name={...} dir={...} />`。`kindOfName` 如果换完没人用了就把 import 去掉——`tsc -b` 会因为 `noUnusedLocals` 直接报错，以它为准。

- [ ] **Step 4: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS，无 TS 报错。

- [ ] **Step 5: 人工核对（零行为变化）**

打开实例 → 文件页，逐条确认：
1. 列表里文件夹是文件夹图标、`.jar` 是包图标、`.yml` 是配置图标，**颜色跟改之前一样**。
2. 打开两个文件，标签页上的小图标正常，颜色跟列表里同一个文件一致。
3. 工具栏的上传 / 新建文件夹 / 新建文件 / 刷新 / 搜索图标都在。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/Glyph.tsx web/src/components/FileIcon.tsx web/src/components/FileManager.tsx
git commit -m "$(cat <<'EOF'
文件图标抽成共享模块

编辑模式下目录树要显示文件，用的必须是列表里那一套图标——同一个文件
在两个地方画成两个样子，是对同一个问题给两个答案。图标表原本埋在
FileManager 中间三百行，树够不着。

纯搬运，一个字符没改。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 2: 第三种页面形态 `full`

现在的规矩是「只有散文（880px）和瓦片（1440px）两种形态，不要引第三种宽度」。编辑模式要铺满，所以给规矩加一个**有名有姓**的第三种，而不是开暗门。

**Files:**
- Modify: `web/src/components/Page.tsx`
- Modify: `web/src/styles.css`（`.page` 附近加 `.page--full`；`.instance__pane .stack` 那条规则附近，styles.css:3060 一带，加 `.stack--full`）
- Modify: `CLAUDE.md`（「前端界面布局优化」里「一个页面框」那条）
- Modify: `.claude/skills/frontend-design/SKILL.md`

**Interfaces:**
- Consumes: 无
- Produces: `Page` 的 props 多一个 `full?: boolean`；CSS 类 `.page--full` 和 `.stack--full` 可被后续任务使用

- [ ] **Step 1: `Page.tsx` 加 `full`**

`Props` 里 `wide` 下面加：

```tsx
  /**
   * A full-bleed workspace: one canvas that takes every pixel it is given,
   * rather than a column of content to read. The file pane's edit mode is the
   * only caller today. Content pages must not use it — prose has
   * --content-max and tiles have --content-max-wide, and those two are still
   * the whole of the choice for anything you read rather than work in.
   */
  full?: boolean
```

`Page` 的签名和返回改成：

```tsx
export function Page({ title, lead, aside, above, wide, full, children }: Props) {
  const head = above ?? title ?? lead ?? aside
  // full wins over wide: a caller that asks for both means the workspace.
  const form = full ? 'page page--full' : wide ? 'page page--wide' : 'page'

  return (
    <div className={form}>
      {head !== undefined && <PageHead title={title} lead={lead} aside={aside} above={above} />}
      {children}
    </div>
  )
}
```

- [ ] **Step 2: `styles.css` 加两条规则**

找到 `.page--wide`（`max-width: var(--content-max-wide)` 那条），在它后面加：

```css
/* The third form: a full-bleed workspace. Not a third width — an absence of
   one. What goes in here is a canvas that should take every pixel it is given
   (the file pane's edit mode), not a column of content, so it also owns its
   own scrolling instead of handing it to .page. Prose and tiles are still the
   whole of the choice for anything you read rather than work in. */
.page--full {
  max-width: none;
  flex: 1;
  min-height: 0;
  padding-bottom: 0;
  overflow: hidden;
}
```

再找到 `.instance__pane .stack, .instance__pane .skeleton-screen--stack { max-width: var(--content-max-wide) }`（styles.css:3060 一带），在它后面加：

```css
/* The same third form, for a section that lives in an instance pane rather
   than in a Page. The pane brings its own scrolling, which is exactly what a
   full-bleed workspace must not have on top of its own — two nested scrollers
   means the editor never reaches the bottom of the window. */
.instance__pane .stack--full {
  max-width: none;
  flex: 1;
  min-height: 0;
}

.instance__pane--scroll:has(.stack--full) {
  overflow: hidden;
}
```

- [ ] **Step 3: 更新 `CLAUDE.md`**

把「前端界面布局优化」下面那条

```
- **一个页面框**：所有面板级页面用 `components/Page.tsx`，只有「散文」和「瓦片（`wide`）」两种形态，对应 `--content-max`(880px) 和 `--content-max-wide`(1440px)。不要再造页面框，不要引第三种宽度。
```

改成

```
- **一个页面框**：所有面板级页面用 `components/Page.tsx`，只有三种形态——「散文」`--content-max`(880px)、「瓦片（`wide`）」`--content-max-wide`(1440px)、「全屏工作区（`full`）」不设上限。不要再造页面框，不要引第四种。`full` 严格限定于「一屏一件事的工具页」：里面装的是一块占满空间的画布（文件页的编辑模式），不是一段要读的内容。内容页一律在前两种里选。
```

- [ ] **Step 4: 更新 `frontend-design` skill**

在 `.claude/skills/frontend-design/SKILL.md` 里找到讲两种页面形态的那一节，同样补上 `full`。措辞与 `CLAUDE.md` 保持一致，并写明边界：只给工具页用，内容页不准用。

- [ ] **Step 5: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。

- [ ] **Step 6: 人工核对（此时还没人用 `full`，只确认没弄坏别的）**

1. 面板设置页（散文形态）宽度没变。
2. 实例的监控、插件、文件三个页（瓦片形态）宽度没变。
3. 1440px 下所有页面仍然居中、两侧留白一致。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/Page.tsx web/src/styles.css CLAUDE.md .claude/skills/frontend-design/SKILL.md
git commit -m "$(cat <<'EOF'
页面框加第三种形态：全屏工作区

编辑模式需要取消宽度上限铺满屏幕，而现在的规矩写的是「只有散文和
瓦片两种形态，不要引第三种宽度」。与其开一个不写在纸面上的例外，
不如把第三种命名、划定边界、写进规矩：full 只给「一屏一件事的工具
页」用，内容页一律在前两种里选。

规则同时更新 CLAUDE.md 和 frontend-design skill，否则下一个人读到的
还是「只有两种」。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 3: 编辑模式骨架

加状态、加按钮、两栏铺满、编辑器吃满高度。这一步做完就能进出编辑模式了，只是侧栏还没折、树里还没有文件、还没有高亮。

**Files:**
- Modify: `web/src/components/FileManager.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 2 的 `.stack--full`
- Produces: `FileManager` 内部的 `editing: boolean` 状态和 `setEditing`；DOM 上 `.fm--editing`；后续任务往这里挂东西

- [ ] **Step 1: 加状态**

`FileManager` 里，`narrowPane` 那个 `useState` 后面加：

```tsx
  // The file pane has two jobs — managing files and reading/writing one — and
  // they want opposite layouts. Editing mode is the second one: the listing
  // steps aside, the tree takes over finding things, and the editor gets the
  // width that was being spent on a column of file sizes.
  //
  // Deliberately not persisted. Landing on 文件 in a mode you set last week,
  // with no listing and no toolbar, is a page that looks broken.
  const [editing, setEditing] = useState(false)
```

- [ ] **Step 2: 工具栏按钮**

在 `file-toolbar` 里、刷新按钮后面（`<Glyph name="refresh" .../>` 那个 `</button>` 之后）加：

```tsx
          <button
            className="btn"
            onClick={() => setEditing(true)}
            title="把这一屏交给编辑器：列表让位，目录树带上文件"
          >
            <Glyph name="doc" />
            编辑模式
          </button>
```

- [ ] **Step 3: 退出的两条路**

编辑模式的根容器上方（`.fm` 那个 div 之前）加一个 effect：

```tsx
  // Escape leaves the mode. Not while typing: Escape in the editor is how you
  // dismiss the browser's own find bar, and in the filter box it clears the
  // box — neither should throw the whole layout away.
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
```

并在编辑模式的树栏顶部放一个退出按钮（Task 6 会把它并进树顶工具栏，先放着）：

```tsx
          {editing && (
            <button className="btn btn--icon" onClick={() => setEditing(false)} title="退出编辑模式（Esc）" aria-label="退出编辑模式">
              <Glyph name="up" />
            </button>
          )}
```

- [ ] **Step 4: 根节点和栅格**

根 div 的 `className="stack"` 改成：

```tsx
      className={editing ? 'stack stack--full' : 'stack'}
```

`PageHead` 那一段包起来：

```tsx
      {/* The mode's whole point is vertical room; the title and the sentence
          under it are the first eight lines it buys back. */}
      {!editing && (
        <PageHead
          title="文件"
          lead="服务器目录里的东西：jar、存档、配置和日志。点一个文件直接打开，可以同时开着几个对照。"
        />
      )}
```

`.fm` 那个 div 改成：

```tsx
      <div className={editing ? 'fm fm--editing' : 'fm'} data-pane={editor ? narrowPane : 'list'}>
```

列表那一整个 `<section className={`panel files...`}>` 用 `{!editing && (...)}` 包起来。

空态那段话也要跟着换——编辑模式下「中间的列表」不在了：

```tsx
            <div className="fm__blank">
              <Glyph name="doc" />
              <p>{editing ? '从左边的目录树里点一个文件，会在这里打开。' : '从中间的列表里点一个文件，会在这里打开。'}</p>
              <p className="muted">可以同时开着几个，用上面的标签切换。</p>
            </div>
```

- [ ] **Step 5: 样式**

`.fm` 规则后面加：

```css
/* Edit mode: the tree and the editor, and nothing between them. The listing is
   not narrowed here, it is gone — a column of file sizes beside an open config
   is the width that was making the config scroll sideways. */
.fm--editing {
  grid-template-columns: 260px minmax(0, 1fr);
  flex: 1;
  min-height: 0;
  align-items: stretch;
}

.fm--editing .fm__tree {
  max-height: none;
  min-height: 0;
}

/* Every layer from the pane down has to give up its content height, or the
   editor sizes to its text and pushes a second scrollbar onto the page. */
.fm--editing .fm__editor,
.fm--editing .fm__editor > .stack,
.fm--editing .editor-pane {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

/* Outside the mode the editor is a fixed box you can drag taller. Inside it,
   it is the page. */
.fm--editing .editor {
  height: auto;
  flex: 1;
  min-height: 0;
  resize: none;
}

/* Only the layout below 1024 hides the listing behind a button; in edit mode
   the listing is not somewhere else, it is off. */
.fm--editing .editor__back {
  display: none;
}
```

- [ ] **Step 6: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。

- [ ] **Step 7: 人工核对**

在 1440px 下，明暗两种模式各走一遍：
1. 文件页 → 点「编辑模式」→ 列表消失，树在左、编辑器在右，编辑器**高度吃满到窗口底部**，页面**没有**纵向滚动条。
2. 「文件」大标题和那句说明不见了。
3. 开着一个文件时进编辑模式 → 文件还在，光标位置还在，脏点状态还在。
4. 按 `Esc` → 回到三栏，开着的文件还在。
5. 在编辑器里按 `Esc` → **不**退出模式（先点进 textarea 再按）。
6. 编辑模式下没有「← 文件列表」按钮。

- [ ] **Step 8: 提交**

```bash
git add web/src/components/FileManager.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
文件页加编辑模式：列表让位，编辑器吃满这一屏

三栏在「管文件」时是对的，在「改文件」时把编辑器压到 600px 出头，
一行中文注释就触发横向滚动。这一屏真正在做的事只有一件，布局却按
「可能有多少东西」分配空间。

模式不持久化：落在一个没有列表也没有工具栏的页面上，看起来像坏了。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 4: 外壳联动

进编辑模式时把左侧导航折成图标条，退出时还回去。**最容易出的 bug 是侧栏卡在图标条**，所以归还有三条路径都要走通。

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/InstanceView.tsx`
- Modify: `web/src/components/FileManager.tsx`

**Interfaces:**
- Consumes: Task 3 的 `editing`
- Produces:
  - `FileManager` props 多两个：`active: boolean`、`onWorkspaceChange?: (full: boolean) => void`
  - `InstanceView` props 多一个：`onWorkspaceChange?: (full: boolean) => void`

- [ ] **Step 1: `App.tsx` 加 `workspace`**

`const [railed, setRailed] = useState(...)`（App.tsx:235）后面加：

```tsx
  // A section can ask for the shell to get out of the way — the file pane's
  // edit mode does. Kept separate from `railed` rather than writing into it:
  // the operator's own choice has to survive the mode, and leaving it untouched
  // is what makes "put it back" free.
  const [workspace, setWorkspace] = useState(false)
```

`data-rail`（App.tsx:529）改成：

```tsx
        data-rail={!compact && (railed || workspace) ? 'on' : undefined}
```

传给 `Sidebar` 的 `railed`（App.tsx:542）改成：

```tsx
          railed={!compact && (railed || workspace)}
```

`[` 快捷键（App.tsx:351 附近，`setRailed((value) => !value)` 之前）加一道：

```tsx
      // The rail is not the operator's to fold while a section is holding it
      // open; toggling a value nothing reads is a key that looks broken.
      if (workspaceRef.current) return
      event.preventDefault()
      setRailed((value) => !value)
```

那个 effect 的依赖是 `[]`（只挂一次），所以不能直接读 `workspace`，要一个 ref。在 `workspace` state 下面加：

```tsx
  // The keydown listener is installed once, so it reads the flag through a ref
  // rather than through a stale closure.
  const workspaceRef = useRef(workspace)
  workspaceRef.current = workspace
```

> 备注：spec 里写的「给 Sidebar 加 railLocked 把折叠按钮禁掉」这次不做——那个按钮只在 `scope === 'global'` 时渲染（Sidebar.tsx:171），而编辑模式一定在实例 scope 里，按钮根本不在场。只锁 `[` 就够了。

- [ ] **Step 2: `App.tsx` 把回调传下去**

找到渲染 `<InstanceView ...>` 的地方（App.tsx:760 一带），加一个 prop：

```tsx
                    onWorkspaceChange={setWorkspace}
```

再加一个保险：路由离开实例页时归还。`useEffect(() => { if (route.kind === 'instance') remember(route.id) }, ...)`（App.tsx:313 一带）后面加：

```tsx
  // Third way home. A section that is unmounted by a route change never gets to
  // hand the shell back itself, and a sidebar stuck at 64px with no way to
  // widen it is the worst outcome this feature has.
  useEffect(() => {
    if (route.kind !== 'instance') setWorkspace(false)
  }, [route.kind])
```

- [ ] **Step 3: `InstanceView.tsx` 透传**

`Props` 里加：

```tsx
  /** A section can ask the shell to fold to the rail. 文件 does, in edit mode. */
  onWorkspaceChange?: (full: boolean) => void
```

在组件签名的解构里加上它，然后给 `FileManager` 传两个 prop：

```tsx
          <FileManager
            instance={instance}
            active={section === 'files'}
            onWorkspaceChange={onWorkspaceChange}
            jump={jump}
```

- [ ] **Step 4: `FileManager.tsx` 接住，并把三条归还路径都走通**

props 类型里加：

```tsx
  /** Whether this section is the one on screen. Sections stay mounted behind
   *  whatever replaced them (see InstanceView), so "I am no longer visible" is
   *  not the same event as unmounting — and edit mode has to end on both. */
  active: boolean
  /** Asks the shell to fold to the rail while edit mode is on. */
  onWorkspaceChange?: (full: boolean) => void
```

`editing` 声明后面加：

```tsx
  // Leaving the section leaves the mode. It could be remembered instead, but
  // then coming back to 文件 would land on a page with no listing and no
  // toolbar, which is the same thing persisting it would do.
  useEffect(() => {
    if (!active) setEditing(false)
  }, [active])

  // The shell follows the mode, and gets it back on the way out — including on
  // unmount, which is the path a route change takes.
  useEffect(() => {
    onWorkspaceChange?.(editing)
  }, [editing, onWorkspaceChange])

  useEffect(() => () => onWorkspaceChange?.(false), [onWorkspaceChange])
```

> 注意：`onWorkspaceChange` 来自 `App` 的 `setWorkspace`，是 `useState` 的 setter，引用稳定，所以放进依赖不会让 effect 反复跑。

- [ ] **Step 5: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。

- [ ] **Step 6: 人工核对（归还路径是重点）**

1. 侧栏展开着 → 进编辑模式 → 侧栏折成 64px 图标条，实例的各个 section 还能点。
2. 退出编辑模式 → 侧栏**回到展开**。
3. 进编辑模式 → 点侧栏的「监控」→ 侧栏展开，监控页正常。再点回「文件」→ 是普通三栏模式。
4. 进编辑模式 → 点「返回实例列表」→ 侧栏展开。
5. **侧栏本来就是折叠状态**时：进编辑模式 → 仍是图标条；退出 → **仍是折叠**（没有被撑开）。
6. 进编辑模式 → 按 `[` → 侧栏不动（不是变宽又变窄的闪烁）。退出后按 `[` → 正常折叠/展开。

- [ ] **Step 7: 提交**

```bash
git add web/src/App.tsx web/src/components/InstanceView.tsx web/src/components/FileManager.tsx
git commit -m "$(cat <<'EOF'
编辑模式把左侧导航折成图标条

编辑模式的收益是宽度，而 240px 的导航是这一屏最大的一笔固定开销。
折成已有的图标条而不是藏掉：改完配置要能立刻跳去控制台重启，这正是
面板比通用编辑器强的地方。

不写进用户自己的折叠状态，所以退出时「放回去」是免费的。归还走三条
路——退出模式、离开 section、组件卸载——少一条都会让侧栏卡在 64px，
而那是这个功能能造成的最坏结果。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 5: 目录树吸收文件

**Files:**
- Modify: `web/src/components/FileTree.tsx`
- Modify: `web/src/components/FileManager.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 1 的 `FileIcon`、Task 3 的 `editing`
- Produces: `FileTree` props 多四个：`showFiles?: boolean`、`openPath?: string | null`、`dirtyPaths?: Set<string>`、`onOpenFile?: (path: string) => void`

- [ ] **Step 1: `Node` 带上 `isDir`，缓存存全部条目**

`FileTree.tsx` 的 `interface Node` 改成：

```tsx
interface Node {
  name: string
  path: string
  isDir: boolean
}
```

`read()` 里那段 `.filter((entry) => entry.isDir).map(...)` 改成：

```tsx
        setChildren((current) => ({
          ...current,
          // Everything, not just the directories: which of the two modes is on
          // is a rendering question, and filtering here would mean re-reading
          // every directory on the way in and out of edit mode.
          [dir]: listing.entries
            .map((entry) => ({ name: entry.name, path: entry.path, isDir: entry.isDir }))
            .sort(byKindThenName),
        }))
```

文件末尾加：

```tsx
/** Directories first, then names. Not the order the listing arrives in: the
 *  API's order is the listing's business, and a tree with files scattered
 *  between folders is a tree nobody can scan. */
function byKindThenName(a: Node, b: Node): number {
  if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
  return a.name.localeCompare(b.name, 'zh')
}
```

- [ ] **Step 2: props 和渲染过滤**

`interface Props` 加：

```tsx
  /** Edit mode: the listing is gone, so the tree answers "what is in here" as
   *  well as "where is here". */
  showFiles?: boolean
  /** The file in front of the editor, so the tree can mark it. */
  openPath?: string | null
  /** Open files with unsaved changes, marked the way the tabs mark them. */
  dirtyPaths?: Set<string>
  onOpenFile?: (path: string) => void
```

`Branch` 里遍历的地方，把传进去的数组先过一道：`showFiles ? nodes : nodes.filter((node) => node.isDir)`。最省事的做法是在 `FileTree` 里定义一个

```tsx
  const visible = useCallback(
    (dir: string) => {
      const all = children[dir] ?? []
      return showFiles ? all : all.filter((node) => node.isDir)
    },
    [children, showFiles],
  )
```

然后把 `Branch` 的 `dirs={children[''] ?? []}` / `dirs={children_[node.path] ?? []}` 全部换成走 `visible(...)`。`Branch` 需要拿到 `visible`，所以把它的 `children_` prop 换成 `visible: (dir: string) => Node[]`，递归处一并改。

`hasChildren` 的判断也跟着走 `visible`：`children_[node.path] === undefined || visible(node.path).length > 0`。

- [ ] **Step 3: 文件行**

`Row` 加 props：`isDir`（从 `node` 上读）、`open`、`dirty?: boolean`。文件行与目录行的差别有三处：

- 没有展开箭头（`hasChildren` 传 false，`twist` 按钮保持占位，否则同级的名字会左右错开）。
- 名字前面画 `<FileIcon name={node.name} dir={node.isDir} />`。
- 点名字时，目录走 `onOpen`，文件走 `onOpenFile`。

`Row` 的按钮区改成：

```tsx
      <button
        type="button"
        className="ftree__name"
        onClick={onOpen}
        title={node.path || '/'}
      >
        <FileIcon name={node.name} dir={node.isDir} />
        <span className="ftree__label">{node.name}</span>
        {dirty && <span className="ftree__dot" aria-label="有未保存的修改" />}
      </button>
```

（`onOpen` 由调用处决定指向 `onOpen(dir)` 还是 `onOpenFile(path)`。）

当前打开的文件用 `ftree__row--on` 标记：`current` 的判断从 `path === node.path` 变成 `node.isDir ? path === node.path : openPath === node.path`。

- [ ] **Step 4: `FileManager` 把参数接上**

`.fm__tree` 里的 `<FileTree>` 改成：

```tsx
          <FileTree
            instanceId={instance.id}
            path={dir}
            reloadKey={treeKey}
            showFiles={editing}
            openPath={activeTab}
            dirtyPaths={dirtyPaths}
            onOpen={(next) => void load(next)}
            onOpenFile={(next) => void openPath(next)}
          />
```

`dirtyPaths` 在 `editor` 那个 `useMemo` 附近算：

```tsx
  // The tabs already show this; the tree shows it too because in edit mode the
  // tree is what you scan, and an unsaved file you cannot see is one you lose.
  const dirtyPaths = useMemo(
    () => new Set(tabs.filter((tab) => tab.content !== tab.original).map((tab) => tab.path)),
    [tabs],
  )
```

`openPath` 这个名字现在既是 `FileManager` 里的函数又是 `FileTree` 的 prop。函数那个保持原名（它已经被 `jump` 用着），prop 传值时写 `onOpenFile={(next) => void openPath(next)}` 即可，不冲突。

- [ ] **Step 5: 样式**

`.ftree__row` 附近加：

```css
.ftree__label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Same mark as the tab strip's, for the same fact. */
.ftree__dot {
  flex: 0 0 auto;
  width: 6px;
  height: 6px;
  margin-left: auto;
  background: var(--accent);
  border-radius: 50%;
}

/* The tree's icons sit in a 13px row rather than a table cell, so they take a
   size of their own instead of the listing's. */
.ftree .fileicon {
  flex: 0 0 auto;
}
```

`.ftree__name` 如果还没有 `display: flex; gap; min-width: 0`，补上——名字要能省略号，图标不能被压扁。

- [ ] **Step 6: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。

- [ ] **Step 7: 人工核对**

1. 普通模式下的树**只有目录**，跟改之前一样（这是回归重点）。
2. 编辑模式下树里出现文件，文件夹排在文件前面，同类按名字排。
3. 点树里的文件 → 在右边打开，树里那一行高亮。
4. 改一个字不保存 → 树里那一行出现小圆点，跟标签页上的一致；保存后消失。
5. 展开 `plugins/` 再退出编辑模式再进来 → **没有重新请求**（展开状态和内容都在）。
6. 很长的文件名 → 省略号，不撑破树栏。

- [ ] **Step 8: 提交**

```bash
git add web/src/components/FileTree.tsx web/src/components/FileManager.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
编辑模式下目录树带上文件

列表让位之后，「这个目录里有什么」得有人回答。树原本只装目录是对的
——它和列表各答一半——但列表不在场时，那条分工就没有意义了。

缓存改成存全部条目、渲染时再过滤：按模式过滤会让每次进出编辑模式
都把整棵树重读一遍，而走一趟树正是这个页面最频繁的动作。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 6: 树顶工具栏、右键菜单、过滤框

编辑模式下上传和新建不能只能靠退出模式。

**Files:**
- Modify: `web/src/components/FileManager.tsx`
- Modify: `web/src/components/FileTree.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 5 的树、`Menu` (`web/src/components/Menu.tsx`)、`FileManager` 已有的 `upload` / `createFile` / `createFolder` / `refresh` / `rename` / `downloadURL`
- Produces: `FileTree` props 多两个：`filter?: string`、`menuFor?: (node: { name: string; path: string; isDir: boolean }) => MenuItem[]`

- [ ] **Step 1: 树顶工具栏**

`.fm__tree` 的 `<aside>` 里、`<FileTree>` 之前插入（只在编辑模式渲染）：

```tsx
          {editing && (
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
          )}
```

把 Task 3 Step 3 那个临时的退出按钮删掉——它的位置由这里接管。

上传和新建都作用于 `dir`（列表当前所在的目录），所以树顶还要说清楚「当前目录」是哪个，否则在编辑模式里点上传会不知道传去了哪。工具栏下面加一行：

```tsx
              <p className="ftree__where" title={dir || '实例根目录'}>
                {dir === '' ? '实例根目录' : dir}
              </p>
```

（放在 `.ftree__bar` 外面、`<FileTree>` 之前。）

- [ ] **Step 2: 过滤框**

`FileManager` 里加状态（跟列表的 `query` 分开，两者过滤的不是同一个东西）：

```tsx
  // Edit mode's own filter. Separate from the listing's 在当前目录中查找: that
  // one filters rows in one directory, this one filters the tree — and only
  // what the tree has already read, since walking every unopened directory to
  // answer a keystroke is a request storm, not a search.
  const [treeQuery, setTreeQuery] = useState('')
```

工具栏下面加输入框：

```tsx
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
```

`FileTree` 收一个 `filter?: string`，在 `visible()` 里再过一道：

```tsx
  const visible = useCallback(
    (dir: string) => {
      const all = children[dir] ?? []
      const kept = showFiles ? all : all.filter((node) => node.isDir)
      const needle = (filter ?? '').trim().toLowerCase()
      if (needle === '') return kept
      // Directories always survive the filter: hiding a folder hides the path
      // to the file that did match, and the tree would go empty for a name it
      // is in fact showing one level down.
      return kept.filter((node) => node.isDir || node.name.toLowerCase().includes(needle))
    },
    [children, showFiles, filter],
  )
```

- [ ] **Step 3: 右键菜单**

`FileManager` 里写一个 builder（放在 `dirtyPaths` 附近）：

```tsx
  /** The row actions the listing has in its 操作 column, for a tree that is
   *  standing in for the listing. */
  const treeMenu = useCallback(
    (node: { name: string; path: string; isDir: boolean }): MenuItem[] => [
      {
        label: '重命名',
        disabled: busy || !listing?.writable,
        onSelect: () => void rename({ ...node, size: 0, modified: '' } as FileEntry),
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
        onSelect: () => void removeFromTree(node),
      },
    ],
    [busy, listing?.writable, instance.id],
  )
```

删除要连带关掉标签页——一个指向不存在文件的编辑器是保存时才报错的陷阱：

```tsx
  /** Delete, and take the editor tab with it: a tab pointing at a file that is
   *  no longer there fails at save time, which is the worst moment to find out. */
  const removeFromTree = async (node: { name: string; path: string; isDir: boolean }) => {
    const yes = await ask({
      title: `删除 ${node.name}？`,
      lead: node.isDir ? '文件夹和里面的东西都会被删除，不进回收站。' : '删除后不进回收站。',
      confirmLabel: '删除',
      danger: true,
    })
    if (!yes) return
    await guard(() => api.deleteFile(instance.id, node.path), `已删除 ${node.name}`)
    setTabs((current) =>
      current.filter((tab) => tab.path !== node.path && !tab.path.startsWith(`${node.path}/`)),
    )
    setActiveTab((current) =>
      current === node.path || current?.startsWith(`${node.path}/`) ? null : current,
    )
    setTreeKey((key) => key + 1)
  }
```

`FileTree` 的 `Row` 外层包一个 `Menu`——`Menu` 是「一个按钮和它背后的动作表」，所以文件行右边加一个只在 hover / focus 时显形的三点按钮，而不是劫持浏览器右键菜单（劫持右键会让「在新标签页打开」这类习惯失效，也没法用键盘）：

```tsx
      {menuFor && (
        <Menu
          className="ftree__more"
          items={menuFor(node)}
          title={`${node.name} 的操作`}
          ariaLabel={`${node.name} 的操作`}
        >
          ⋯
        </Menu>
      )}
```

`FileManager` 传 `menuFor={editing ? treeMenu : undefined}`。

两处 import 别漏：`FileManager.tsx` 要 `import type { MenuItem } from './Menu'`，`FileTree.tsx` 要 `import { Menu } from './Menu'` 和 `import type { MenuItem } from './Menu'`。`FileTree` 的 `menuFor` prop 类型就是 `(node: Node) => MenuItem[]`。

- [ ] **Step 4: 样式**

```css
.ftree__bar {
  display: flex;
  gap: 4px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--border);
}

/* Where 上传 and 新建 will land. The listing used to say this in its
   breadcrumb; in edit mode the breadcrumb is not on screen and a button that
   writes into an unnamed directory is a button nobody presses twice. */
.ftree__where {
  overflow: hidden;
  margin: 6px 0 4px;
  color: var(--text-faint);
  font-size: 11.5px;
  text-overflow: ellipsis;
  white-space: nowrap;
  direction: rtl;
  text-align: left;
}

.ftree__find {
  width: 100%;
  margin-bottom: 6px;
}

/* Out of the way until the row is under the pointer or the keyboard. */
.ftree__more {
  flex: 0 0 auto;
  opacity: 0;
  transition: opacity var(--dur) var(--ease);
}

.ftree__row:hover .ftree__more,
.ftree__more:focus-visible,
.ftree__row:focus-within .ftree__more {
  opacity: 1;
}
```

`direction: rtl` 是为了让长路径的省略号出现在**左边**——`plugins/Atalanta/artifact/model/armor` 被截断时，要留住的是右边那几段。

- [ ] **Step 5: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。

- [ ] **Step 6: 人工核对**

1. 编辑模式下树顶有五个按钮，功能都对；上传落在 `ftree__where` 显示的那个目录里。
2. 只读目录（用一个受限角色登录，或临时把 `listing.writable` 改 false 验证）里，上传 / 新建 / 删除 / 重命名都是禁用的，悬停有原因。
3. 筛选框输 `mail` → 只剩匹配的文件，路径上的目录仍在；按 `Esc` 清空。
4. 文件行悬停出现 `⋯`，菜单里重命名、复制路径、下载、删除都在；文件夹的菜单**没有**下载。
5. 打开 `mail.yml` → 删掉它 → 标签页跟着关掉，编辑器不再指向它。
6. 删一个开着文件的**文件夹** → 里面的标签页也关掉了。
7. 普通模式下树里**没有** `⋯`、没有工具栏、没有筛选框。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/FileManager.tsx web/src/components/FileTree.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
编辑模式的树自带工具栏、筛选和行内菜单

上传一个 jar 不该需要先退出编辑模式。列表让位之后，它那一栏的动作要
有地方去：常用的四个进树顶，单个文件的四个进行内菜单。

菜单挂在一个按钮上而不是劫持浏览器右键：劫持右键会让「在新标签页
打开」这类习惯失效，键盘也够不着。

筛选只筛已经读进来的目录，并且永远留下目录行——把路径上的文件夹筛
掉，等于为一个确实存在的匹配显示一棵空树。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 7: 语法高亮

**Files:**
- Create: `web/src/highlight.ts`
- Modify: `web/src/components/FileManager.tsx`（`FileEditor` 和 `languageOf`）
- Modify: `web/src/styles.css`
- Modify: `web/package.json`（`npm install` 自动改）

**Interfaces:**
- Consumes: 无
- Produces: `export function highlight(code: string, lang: string): string`、`export function langOf(path: string): { label: string; prism: string | null }`

- [ ] **Step 1: 装依赖**

```bash
npm --prefix web install prismjs
npm --prefix web install -D @types/prismjs
```

装完确认 `web/package.json` 里 `prismjs` 在 `dependencies`、`@types/prismjs` 在 `devDependencies`，并把解析出来的版本号照抄进提交（不要手写范围）。

- [ ] **Step 2: 建 `web/src/highlight.ts`**

```ts
import Prism from 'prismjs/components/prism-core'
import 'prismjs/components/prism-markup'
import 'prismjs/components/prism-yaml'
import 'prismjs/components/prism-json'
import 'prismjs/components/prism-toml'
import 'prismjs/components/prism-properties'
import 'prismjs/components/prism-ini'
import 'prismjs/components/prism-bash'
import 'prismjs/components/prism-markdown'

/**
 * Syntax colouring for the file editor.
 *
 * Prism's core only — the languages are listed one by one above rather than
 * pulled in wholesale, because everything here is embedded into a single Go
 * binary and a hundred grammars nobody opens is a hundred grammars every
 * operator downloads. markup is not in the list for its own sake: markdown
 * needs it.
 *
 * What this is *not* is a language server. A YAML file the panel colours wrong
 * is a cosmetic bug; a YAML file the server refuses is reported by the server.
 */

/** Extension → the name shown in the status line, and the Prism grammar to
 *  colour with. A null grammar is plain text: shown, not coloured. */
const LANGS: Record<string, { label: string; prism: string | null }> = {
  yml: { label: 'YAML', prism: 'yaml' },
  yaml: { label: 'YAML', prism: 'yaml' },
  json: { label: 'JSON', prism: 'json' },
  toml: { label: 'TOML', prism: 'toml' },
  properties: { label: 'Properties', prism: 'properties' },
  conf: { label: 'Conf', prism: 'ini' },
  cfg: { label: 'Conf', prism: 'ini' },
  ini: { label: 'INI', prism: 'ini' },
  md: { label: 'Markdown', prism: 'markdown' },
  sh: { label: 'Shell', prism: 'bash' },
  txt: { label: '纯文本', prism: null },
  log: { label: '日志', prism: null },
  kts: { label: 'Kotlin Script', prism: null },
}

export function langOf(path: string): { label: string; prism: string | null } {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  const known = LANGS[ext]
  if (known) return known
  return { label: ext ? ext.toUpperCase() : '纯文本', prism: null }
}

/**
 * Colours `code` as `lang`, as an HTML string.
 *
 * Safe to hand to dangerouslySetInnerHTML: Prism escapes every character of
 * the input it does not itself wrap, so the only tags in the output are the
 * <span class="token …"> it generated. A config file full of <script> comes
 * back as text. Do not "simplify" this by interpolating the raw code.
 */
export function highlight(code: string, lang: string): string {
  const grammar = Prism.languages[lang]
  if (!grammar) return escapeHTML(code)
  // A trailing newline is eaten by <pre>, and an overlay one line shorter than
  // the textarea above it drifts by a line at the bottom of every file that
  // ends the way every file ends.
  return Prism.highlight(code, grammar, lang) + '\n'
}

function escapeHTML(text: string): string {
  return text.replace(/[&<>]/g, (ch) => (ch === '&' ? '&amp;' : ch === '<' ? '&lt;' : '&gt;'))
}
```

> 如果 `prismjs/components/prism-core` 这个入口在当前 Prism 版本下解析不了，退路是 `import Prism from 'prismjs'`（会带上 markup/css/clike/javascript 四个默认语法，产物大一点但行为一致），并在注释里写明为什么退。

- [ ] **Step 3: `languageOf` 让位给 `langOf`**

`FileManager.tsx` 里删掉 `function languageOf`，状态栏那行改成 `<span>{lang.label}</span>`；`FileEditor` 里算一次：

```tsx
  const lang = useMemo(() => langOf(editor.path), [editor.path])
```

- [ ] **Step 4: 叠层**

`FileEditor` 里，在 `bytes` 附近加：

```tsx
  const hl = useRef<HTMLPreElement | null>(null)
  // Past the threshold the gutter is already off (see `lines`), and tokenising
  // a 400 000-character log on every keystroke is the same bad trade twice.
  const huge = lines === 0
  // One frame behind the textarea on purpose: typing must never wait on a
  // tokeniser, and a colour that lands a frame late is invisible.
  const deferred = useDeferredValue(editor.content)
  const painted = useMemo(
    () => (huge || !lang.prism ? null : highlight(deferred, lang.prism)),
    [deferred, huge, lang.prism],
  )
```

（`useDeferredValue` 加进 React 的 import。）

`.editor` 里的 textarea 用一个 wrapper 包起来，叠层放在它前面：

```tsx
        <div className="editor__wrap">
          {painted !== null && (
            <pre
              className="editor__hl"
              ref={hl}
              aria-hidden="true"
              // Safe: see highlight() — Prism escapes everything it does not
              // wrap itself, so the only tags here are its own token spans.
              dangerouslySetInnerHTML={{ __html: painted }}
            />
          )}
          <textarea
            className={painted !== null ? 'editor__text editor__text--lit' : 'editor__text'}
            ...
            onScroll={(event) => {
              if (gutter.current) gutter.current.scrollTop = event.currentTarget.scrollTop
              if (hl.current) {
                hl.current.scrollTop = event.currentTarget.scrollTop
                hl.current.scrollLeft = event.currentTarget.scrollLeft
              }
            }}
          />
        </div>
```

（textarea 其余的 props 一个都不动。）

状态栏 `formatBytes(bytes)` 那一段后面补一条：

```tsx
            {huge && ' · 文件过大，已关闭高亮'}
```

- [ ] **Step 5: 样式**

现有的

```css
.editor__gutter,
.editor__text {
  padding: 12px 0;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.65;
  tab-size: 2;
}
```

改成三个选择器共用（**这一份是对齐的唯一来源，不要在别处再写第二份**）：

```css
/* Three layers that must agree to the pixel: the numbers in the margin, the
   colours underneath, and the text you actually type into. One declaration,
   three selectors — a second copy of any of these five properties is a drift
   waiting to happen, and it shows up as colour sliding off the words. */
.editor__gutter,
.editor__hl,
.editor__text {
  padding: 12px 0;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.65;
  tab-size: 2;
}
```

后面加：

```css
.editor__wrap {
  position: relative;
  flex: 1;
  min-width: 0;
}

/* Underneath the textarea, never in front of it: it must not take a click, a
   selection, or a screen reader's attention. */
.editor__hl {
  position: absolute;
  inset: 0;
  margin: 0;
  padding-right: 14px;
  padding-left: 14px;
  overflow: hidden;
  color: var(--text);
  white-space: pre;
  pointer-events: none;
}

/* The text itself goes invisible and the layer below shows through; the caret
   and the selection do not, which is why both are named here rather than left
   to inherit from a colour that is now transparent. */
.editor__text--lit {
  position: relative;
  color: transparent;
  caret-color: var(--text);
}

.editor__text--lit::selection {
  background: var(--selection);
  color: var(--on-selection);
}

.editor__hl .token.comment,
.editor__hl .token.prolog {
  color: var(--code-comment);
  font-style: italic;
}

.editor__hl .token.key,
.editor__hl .token.property,
.editor__hl .token.attr-name,
.editor__hl .token.selector {
  color: var(--code-key);
}

.editor__hl .token.string,
.editor__hl .token.attr-value {
  color: var(--code-string);
}

.editor__hl .token.number {
  color: var(--code-number);
}

.editor__hl .token.boolean,
.editor__hl .token.null,
.editor__hl .token.keyword,
.editor__hl .token.important,
.editor__hl .token.tag {
  color: var(--code-bool);
}

.editor__hl .token.punctuation,
.editor__hl .token.operator {
  color: var(--code-punct);
}

.editor__hl .token.title,
.editor__hl .token.bold {
  color: var(--code-heading);
  font-weight: 600;
}
```

`.editor__text` 原来的 `padding-right: 14px; padding-left: 14px` 保持不变——叠层上面那份是它的镜像。

- [ ] **Step 6: 令牌**

在 `:root, [data-theme='light'][data-palette='sakura']`（styles.css:74）块里，`--term-*` 那组**之前**加：

```css
  /* The editor's syntax colours. Every one of them is a reference rather than
     a hue of its own, so the three other palettes inherit a set that belongs
     to them without redefining anything. Five roles is the whole scheme: what
     you are meant to skip (comments), what names a setting (keys), and the
     three shapes a value comes in. A rainbow would be a legend to memorise. */
  --code-comment: var(--text-faint);
  --code-key: var(--accent);
  --code-string: var(--ok-ink);
  --code-number: var(--caution-ink);
  --code-bool: var(--danger);
  --code-punct: var(--text-dim);
  --code-heading: var(--text);
```

在 `:root[data-theme='dark'], [data-theme='dark'][data-palette='sakura']`（styles.css:291）块里加同样七条，但两个 `-ink` 换成正色——深底上 ink 那一档压不出来：

```css
  --code-comment: var(--text-faint);
  --code-key: var(--accent);
  --code-string: var(--ok);
  --code-number: var(--caution);
  --code-bool: var(--danger);
  --code-punct: var(--text-dim);
  --code-heading: var(--text);
```

**不要动 `--term-*` 和 `--shell-*`。**

- [ ] **Step 7: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。同时记录一下 `internal/webui/dist/assets/` 的产物大小，跟改之前对比，确认增量在几十 KB 量级而不是几百。

- [ ] **Step 8: 人工核对（对齐是重点）**

明暗两种模式各走一遍：
1. 开一个有整段中文注释的 `.yml`（比如插件的 `mail.yml`）→ 注释、键、字符串、数字、布尔各有颜色，**中文注释和后面的值颜色不同**。
2. **对齐**：把光标放在最后一行行首，确认它落在高亮层同一行的字上；横向滚到最右边，确认颜色没有相对文字偏移；缩进很深的 JSON 同样确认。
3. 选中一段文字 → 选区可见，选中的字读得出来（不是透明字压在选区上）。
4. 行号栏、颜色、文字三者一起滚动，不错行。
5. 开一个两万行的 `.log` → 状态栏出现「文件过大，已关闭高亮」，文字是正常颜色（不是透明），滚动不卡。
6. 开一个 `.jar` 之外的无扩展名文件 → 纯文本，不报错。
7. 快速连续敲字 → 不卡顿，颜色跟上。
8. 服务器控制台和主机 shell 两块终端的配色**没有变化**，两者仍然明显不同色。

- [ ] **Step 9: 提交**

```bash
git add web/package.json web/package-lock.json web/src/highlight.ts web/src/components/FileManager.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
编辑器加语法高亮

在这个框里被打开的是 config.yml 和 server.properties：YAML 的缩进错、
JSON 少一个逗号，改之前全靠肉眼，而整段中文注释和实际的值是同一个
颜色。

叠一层只读的 pre 在 textarea 底下，复用行号栏那套滚动镜像——光标、
选区、脏点、Ctrl+S、标签页、beforeunload 一个都不用碰。三层共用同一
条字体行高声明，写第二份就会漂。

颜色全部是对已有令牌的引用，所以另外三套配色不必各写一遍。超过四十
万字符就不挂叠层：两万行的日志仍然要能打开。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

### Task 8: 响应式收尾、CHANGELOG、全面验证

**Files:**
- Modify: `web/src/components/FileManager.tsx`
- Modify: `web/src/styles.css`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: 前面全部
- Produces: 无（收尾）

- [ ] **Step 1: 1024 以下不提供编辑模式**

`FileManager` 顶部加：

```tsx
  // Below the drawer breakpoint the pane is already one column at a time (see
  // narrowPane), which is what edit mode is *for* — offering it there would be
  // a second state that changes nothing. The number is the one in App's
  // DRAWER_QUERY; the media query in styles.css is the third place it lives.
  const roomy = !useMediaQuery('(max-width: 1024px)')

  useEffect(() => {
    if (!roomy) setEditing(false)
  }, [roomy])
```

（`useMediaQuery` 从 `../useMediaQuery` 引。）

工具栏那个「编辑模式」按钮用 `{roomy && (...)}` 包起来。

- [ ] **Step 2: 1200 断点，树默认收起**

```tsx
  // Between the drawer breakpoint and this one there is room for two columns,
  // but not for two comfortable ones: 260 of tree out of 1100 is a quarter of
  // the width spent on a column you glance at. So it starts folded and opens
  // over the editor instead of squeezing it.
  const tight = useMediaQuery('(max-width: 1200px)')
  const [treeOpen, setTreeOpen] = useState(true)

  useEffect(() => {
    if (editing) setTreeOpen(!tight)
  }, [editing, tight])
```

`.fm` 那个 div 加 `data-tree={editing ? (treeOpen ? 'on' : 'off') : undefined}`，树顶工具栏里加一个折叠按钮（`onClick={() => setTreeOpen((on) => !on)}`）。树收起时，编辑器那一侧要有个把它叫回来的按钮——放在 `.editor-pane` 的标签页条左边：

```tsx
        {onShowTree && (
          <button type="button" className="editor__tree" onClick={onShowTree} title="显示目录树" aria-label="显示目录树">
            <Glyph name="folder" />
          </button>
        )}
```

`FileManager` 传 `onShowTree={editing && !treeOpen ? () => setTreeOpen(true) : undefined}`。

样式：

```css
@media (max-width: 1200px) {
  /* Folded: the editor takes the row, and the tree comes back over it rather
     than taking a quarter of a width that is already short. */
  .fm--editing[data-tree='on'] .fm__tree {
    position: absolute;
    z-index: 2;
    top: 0;
    bottom: 0;
    left: 0;
    width: 260px;
    box-shadow: var(--shadow-lg);
  }

  .fm--editing {
    position: relative;
    grid-template-columns: minmax(0, 1fr);
  }
}

.fm--editing[data-tree='off'] .fm__tree {
  display: none;
}

.fm--editing[data-tree='off'] {
  grid-template-columns: minmax(0, 1fr);
}
```

（`--shadow-lg` 如果不存在，用 `styles.css` 令牌区里实际有的那个最大阴影名。）

- [ ] **Step 3: 构建验证**

Run: `npm --prefix web run build`
Expected: PASS。

- [ ] **Step 4: CHANGELOG**

`CHANGELOG.md` 的「未发布」小节加：

```markdown
- 文件页新增编辑模式：左侧导航折成图标条、文件列表让位、目录树带上文件，编辑器铺满整屏。
- 编辑器支持 YAML / JSON / TOML / Properties / INI / Markdown / Shell 的语法高亮，超大文件自动关闭高亮以保证能打开。
```

（照抄「未发布」小节现有条目的格式和缩进。**不要**把「未发布」改成版本号——那会触发发版。）

- [ ] **Step 5: 全面人工核对**

这是最后一道，五个宽度 × 两种模式，一条都不能跳：

**1440 / 1200 / 1024 / 768 / 390，明暗各一遍：**
1. 普通模式的文件页跟这次改动之前没有区别。
2. 无横向溢出、无错位。
3. 1024 和 768、390 下没有「编辑模式」按钮。
4. 1200 下进编辑模式 → 树是收起的，编辑器占满；点文件夹图标 → 树盖在编辑器上滑出，不挤压它。
5. 1440 下进编辑模式 → 树常驻，两栏并排。
6. 在 1440 下进编辑模式，把窗口拖窄到 1024 以下 → **自动退出**，回到单栏切换，开着的文件还在。

**三处回归重点：**
7. 折叠侧栏正常（普通模式下 `[` 能折能开）。
8. 打开抽屉（<1024）正常，导航能点，scrim 能关。
9. 开着控制台的实例页：日志在滚、命令行能敲、配色没变。

**两块终端画布：**
10. 服务器控制台和主机 shell 在明暗两种模式下都是深色，且两者明显不同色。

**跨模式状态：**
11. 编辑模式下改一个文件不保存 → 退出模式 → 内容、光标、脏点全在。
12. 进编辑模式 → 切到监控 → 切回文件 → 是普通模式，侧栏是展开的。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/FileManager.tsx web/src/styles.css CHANGELOG.md
git commit -m "$(cat <<'EOF'
编辑模式的响应式收尾

1024 以下不提供这个模式：那里本来就是一次只显示一栏，等价于全屏
编辑器，再加一个状态只会多一个要记的东西。已经在模式里把窗口拖窄
会自动退出。

1200 到 1024 之间两栏塞得下但塞不舒服——260 的树占掉四分之一宽度，
换来的是一列你只是瞥一眼的东西。所以那一档树默认收起，需要时盖在
编辑器上滑出，而不是挤它。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01Dwaus1A7uKZqkXXK7wRTXc
EOF
)"
```

---

## 自查记录

**Spec 覆盖**：形态与进出 → Task 3；外壳 rail → Task 4；第三种页面形态 → Task 2；树吸收文件 → Task 5；树顶工具栏/菜单/筛选 → Task 6；图标模块抽出 → Task 1；高亮叠层与令牌 → Task 7；编辑器铺满高度 → Task 3；响应式三档 → Task 8；错误处理（删除连带关标签页、只读目录禁用）→ Task 6；验证与 CHANGELOG → Task 8。

**与 spec 的一处偏离**：spec 第 2 节说给 `Sidebar` 加 `railLocked` 禁用折叠按钮。实际那个按钮只在 `scope === 'global'` 时渲染（Sidebar.tsx:171），而编辑模式必定在实例 scope 下，按钮不在场。改为只锁 `[` 快捷键，见 Task 4 Step 1。

**与 spec 的第二处偏离**：spec 提到新增 `--code-selection` 令牌。样式表里已经有 `--selection` / `--on-selection`（styles.css:186 一带），编辑器的 `::selection` 直接用它们，不新增。

**命名一致性**：`editing` / `setEditing`（FileManager）、`workspace` / `setWorkspace`（App）、`onWorkspaceChange`（两层 props）、`showFiles` / `openPath` / `dirtyPaths` / `onOpenFile` / `filter` / `menuFor`（FileTree）、`highlight()` / `langOf()`（highlight.ts）、`.fm--editing` / `.stack--full` / `.page--full` / `.editor__wrap` / `.editor__hl` / `.editor__text--lit` / `.ftree__bar` / `.ftree__where` / `.ftree__find` / `.ftree__more` / `.ftree__label` / `.ftree__dot`（CSS）——整份计划前后一致。
