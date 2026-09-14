# 文件管理 v2 · VS Code 式布局 实施方案

> **For agentic workers:** REQUIRED SUB-SKILL: 本仓库默认 Inline Execution ——
> 用 `executing-plans` skill 在当前会话里逐条实现（见 CLAUDE.md「执行实施计划」）。
> 步骤用 `- [ ]` 勾选框跟踪。

**Goal:** 把面板一级导航换成常驻图标栏、加上全局状态条，并把文件页重做成
VS Code 式的「图标栏 + 侧边栏 + 主区（浏览态／编辑态）」结构，带分屏、
全文搜索与 `⌘P` 转到文件。

**Architecture:** 不引入任何新框架。前端仍是 React 18 + 单一 `styles.css`；
后端在 `internal/serverfiles` 上加一个带 TTL 缓存的实例文件索引，供
`⌘P` 模糊匹配、侧边栏全文搜索和实例占用统计三个端点复用。文件页的
`panes`/`OpenFile` 数据结构已存在，分屏在它上面扩一个方向字段即可。

**Tech Stack:** Go 1.2x（`os.Root` 受限文件访问）、React 18 + TypeScript + Vite、
`web/src/styles.css` 单文件样式。

**Spec:** `docs/superpowers/specs/2026-09-14-file-manager-rework-v2-design.md`
（v1 在 `2026-09-14-file-manager-rework-design.md`，其被 v2 §1 作废的条目不再有效）

## Global Constraints

- 只用 `styles.css` 开头令牌区的命名令牌，不写裸 hex；新增令牌 light / dark 两个块都要加。
- 不引入 CSS 框架、组件库、CSS-in-JS，不拆分 `styles.css`。
- 代码注释用英文，文档 / CHANGELOG / CI 步骤名用中文。
- Go 提交前 `gofmt`；CI 跑 `make lint` → `make test` → `make build`。
- 前端唯一自动检查是 `npm --prefix web run build`（含 `scripts/check-ui.mjs` 与 `tsc -b`）。
- 任何可能装长文本的 flex/grid 子项写 `min-width: 0`（列方向 `min-height: 0`）。
- `--term-*`（服务器控制台）与 `--shell-*`（主机 shell）两套终端配色不得靠拢。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节，不要改版本号。
- 硬指标：任何断点下单个编辑器组的代码可视宽度 ≥ **620px**（spec §11）。

## 本方案对 spec 的四处调整（已与用户确认或有据）

1. **§3.2 的固定 10 条清单改为「当前 scope 的条目」**（用户裁定）。HyperCraft 是
   多实例面板，侧栏按 scope（global / instance / library / host / settings）整体
   替换，见 `routes.ts` 与 `Sidebar.tsx` 的注释。图标栏保留这套语义：永远 56px
   常驻、条目是当前 scope 的导航项，其余 §3.2 / §3.3 的视觉规格照做。
2. **§11 的 `< 1024px 侧边栏改为全屏抽屉`**：全局图标栏在所有宽度常驻 56px；它的
   220px 展开态在 `< 1024px` 变成覆盖式浮层，沿用 `App.tsx` 现有的 `DRAWER_QUERY`
   焦点管理与 `.scrim`。文件页自己的侧边栏在该断点下是全屏抽屉。
3. **§5.5 实例配额**：面板没有「每实例配额」这个概念。改为实例目录实际占用 /
   宿主磁盘总量，由新端点 `GET /files/usage` 提供（带 TTL 缓存的递归统计）。
4. **§10 的 `⌘P` 与现有 `⌘K` 并存**：`⌘K` 是面板级命令面板（跨实例导航），
   `⌘P` 只搜当前实例的文件。两者不合并。

---

## 文件清单

**后端（新建）**

- `internal/serverfiles/index.go` — 实例文件索引：递归 walk（走 `os.Root`，
  respect scope）、TTL 缓存、默认排除重目录、子序列模糊匹配、内容 grep、占用统计。
- `internal/serverfiles/index_test.go` — 索引与匹配的单测。

**后端（修改）**

- `internal/serverfiles/browser.go` — 暴露 `Index()`，写操作后让缓存失效。
- `internal/api/routes.go:337-348` — 注册三条新路由。
- `internal/api/files.go`（或 handleListFiles 所在文件）— 三个 handler。

**前端（新建）**

- `web/src/components/NavRail.tsx` — 56/220px 图标栏外壳（从 `Sidebar.tsx` 抽出
  头/尾，body 仍由现有 scope 组件渲染）。
- `web/src/components/StatusBar.tsx` — 全局状态条 + 供页面写入内容的 context。
- `web/src/components/FileSidebar.tsx` — 文件页侧边栏容器（头部 / 面板切换 / 底部）。
- `web/src/components/FileSearchPanel.tsx` — 侧边栏「搜索」面板。
- `web/src/components/FileRecentPanel.tsx` — 侧边栏「最近」面板。
- `web/src/components/FileBrowse.tsx` — 主区浏览态（面包屑 + 工具条 + 表头 + 列表）。
- `web/src/components/GoToFile.tsx` — `⌘P` 浮层。
- `web/src/fuzzy.ts` — 子序列匹配与高亮区间（前端只负责高亮，排序用后端得分）。
- `web/src/useFileIndex.ts` — find / search / usage 三个请求的封装与防抖。

**前端（修改）**

- `web/src/App.tsx` — 图标栏展开偏好、状态条挂载、`⌘P` 全局键。
- `web/src/components/Sidebar.tsx` — 头尾换成 NavRail 规格，`railed` 语义反转。
- `web/src/components/FileManager.tsx` — 两栏结构、分屏方向、快捷键、⌘P 接线。
- `web/src/components/FileTree.tsx` — 树里带文件。
- `web/src/components/FileList.tsx` — 表格化（固定列宽），删掉密度切换。
- `web/src/components/FileEditor.tsx` — 组四件套、分屏方向、标签拖拽、行列号上交。
- `web/src/components/FileBar.tsx` — 拆成主区面包屑条 + 工具条两件。
- `web/src/components/FileNav.tsx` — **删除**（上下分栏的职责由侧边栏面板切换取代）。
- `web/src/components/Glyph.tsx` — 新增 `split-row`、`plus`、`sidebar` 三个字形。
- `web/src/styles.css` — 图标栏、状态条、文件页全部区块。
- `web/src/types.ts`、`web/src/api.ts` — 三个新端点的类型与绑定。
- `CHANGELOG.md` — 未发布小节。

---

# PR 1 — 全局外壳

## Task 1: 图标栏骨架与展开偏好

**Files:**
- Create: `web/src/components/NavRail.tsx`
- Modify: `web/src/App.tsx:69-76`（`DRAWER_QUERY` / `RAIL_KEY`）、`:236-240`、`:526-570`
- Modify: `web/src/components/Sidebar.tsx:36-67`（Props）、`:128-190`（外壳）
- Modify: `web/src/styles.css:1085-1130`（`.app` / `.sidebar`）、`:1670-1800`（rail 规则）

**Interfaces:**
- Produces: `NavRail` 组件，props `{ expanded: boolean; onToggle: () => void; user: User;
  brand: ReactNode; children: ReactNode; compact: boolean; railRef: Ref<HTMLElement> }`。
- Produces: `App` 里 `navExpanded` 状态，持久化到 `localStorage['nav.expanded']`（spec §3.3 指名的键）。
- Consumes: 现有 `Sidebar` 的 scope 子组件（`GlobalScope` 等）原样作为 `children`。

- [ ] **Step 1: 反转 rail 语义并改键名**

`App.tsx` 把 `RAIL_KEY = 'hypercraft.sidebar'` 换成 spec 指名的键，默认**收起**：

```ts
/** Whether the icon rail is showing its labels. A per-device preference, like
 *  the theme. The rail itself is never folded away — only its width changes —
 *  so this is a density preference, not a mode. Key named by the design note. */
const NAV_KEY = 'nav.expanded'
```

```ts
const [navExpanded, setNavExpanded] = useState(
  () => window.localStorage.getItem(NAV_KEY) === 'true',
)
useEffect(() => {
  window.localStorage.setItem(NAV_KEY, String(navExpanded))
}, [navExpanded])
```

`data-rail` 属性改为 `data-nav`（值 `rail` / `wide`），并且**不再受 `compact` 影响**——
图标栏在所有宽度常驻：

```tsx
<div
  className="app"
  data-nav={navExpanded ? 'wide' : 'rail'}
  data-drawer={compact && navOpen ? 'open' : undefined}
>
```

原来的 `data-nav={compact && navOpen ? 'open' : undefined}` 改名成 `data-drawer`，
避免一个属性两种含义。`styles.css` 里 `.app[data-nav='open']` 的所有选择器同步改名。

- [ ] **Step 2: 宽度与过渡换成 spec 的数**

`styles.css` `.app`：

```css
.app {
  /* The rail is permanent furniture: 56px of it is always on screen, and the
     preference only decides whether it carries its labels. Nothing auto-folds
     it, at any width — on a phone it is the one thing that keeps 控制台 one
     tap away instead of one drawer and one tap. */
  --sidebar-w: 56px;
  display: grid;
  grid-template-columns: var(--sidebar-w) 1fr;
  height: 100%;
  transition: grid-template-columns var(--dur-3) var(--ease);
}

.app[data-nav='wide'] {
  --sidebar-w: 220px;
}
```

`--dur-3` 是 220ms；spec §3.3 要 160ms。令牌区没有 160ms，**不新增令牌**（frontend-design
规定时长只从五个令牌里选），按「横跨屏幕的布局位移」取 `--dur-4`？不——展开只移动
一个数且幅度小，取 `--dur-3`（原地变形，220ms）。在 CHANGELOG 里不必提。

`< 1024px` 时 220px 展开态改为浮层，不占栅格：

```css
@media (max-width: 1024px) {
  /* The rail stays in the grid at every width; only its wide form leaves it,
     because 220px out of a 390px screen is not a column, it is a cover. */
  .app,
  .app[data-nav='wide'] {
    --sidebar-w: 56px;
  }

  .app[data-nav='wide'] .sidebar {
    position: fixed;
    inset: 0 auto 0 0;
    width: 220px;
    z-index: 60;
    box-shadow: var(--shadow-lg);
  }
}
```

- [ ] **Step 3: NavRail 的头与尾**

新建 `web/src/components/NavRail.tsx`。它只拥有 spec §3.2 规定的**头**（48px 产品图标
+ 1px 分隔线）和**尾**（展开按钮 + 用户头像），中间交给 `children`：

```tsx
/**
 * The icon rail every page sits beside.
 *
 * It owns the two ends — the mark at the top and the pair of controls at the
 * foot — and nothing in between: which destinations are on it is a question
 * about where you are, and the scope components answer that. See Sidebar.
 */
export function NavRail({ expanded, onToggle, user, brand, children, railRef }: Props) {
  return (
    <aside className="sidebar" id="sidebar" ref={railRef} tabIndex={-1}>
      <div className="sidebar__mark">{brand}</div>
      <div className="sidebar__scroll">{children}</div>
      <div className="sidebar__foot">
        <button
          type="button"
          className="sidebar__fold"
          onClick={onToggle}
          aria-label={expanded ? '收起导航' : '展开导航'}
          aria-expanded={expanded}
          aria-controls="sidebar"
          title={expanded ? '收起导航' : '展开导航'}
        >
          <Icon name={expanded ? 'collapse' : 'expand'} />
          <span className="sidebar__name">收起导航</span>
        </button>
        <UserChip user={user} />
      </div>
    </aside>
  )
}
```

`UserChip` 是一个 28px 圆形头像按钮，`aria-label={user.username}`，点击打开
`TopBar` 已有的 `usermenu`（把 `TopBar.tsx:207` 的 `usermenu__button` 抽成
`components/UserChip.tsx`，两边共用，不复制一份）。

- [ ] **Step 4: 条目规格**

`styles.css`，条目 44px / 图标 19px / 左侧 2px 竖条：

```css
.sidebar__link {
  /* 44px is the rail's row: an icon-only target under a thumb. The wide form
     drops to 36px because a label makes the row findable without the height. */
  min-height: 44px;
}

.app[data-nav='wide'] .sidebar__link {
  min-height: 36px;
}

.sidebar__link .icon {
  width: 19px;
  height: 19px;
}

/* The current page's marker, hard against the column's edge. It is the one
   thing that still says where you are once the labels are gone. */
.sidebar__link--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  translate: 0 -50%;
  width: 2px;
  height: 22px;
  background: var(--accent);
  border-radius: 0 var(--radius-sm) var(--radius-sm) 0;
}
```

（`.sidebar__link::before` 已存在于 `:1270`，改这一处而不是新增。）

- [ ] **Step 5: tooltip 延迟 400ms**

不用 `title`（浏览器延迟不可控且样式不可改），用一个纯 CSS 的 tooltip，只在
收起态出现：

```css
.app[data-nav='rail'] .sidebar__link .sidebar__name {
  /* The label does not disappear when the rail folds, it becomes the tooltip:
     one string, one source of truth, and a screen reader keeps reading it. */
  position: absolute;
  left: calc(100% + 8px);
  padding: 5px 9px;
  background: var(--surface-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow);
  color: var(--text);
  font-size: 12.5px;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transition: opacity var(--dur) var(--ease) 400ms;
}

.app[data-nav='rail'] .sidebar__link:hover .sidebar__name,
.app[data-nav='rail'] .sidebar__link:focus-visible .sidebar__name {
  opacity: 1;
}
```

现有 `:1700-1760` 那批 `.app[data-rail='on'] .sidebar :is(...)` 里把
`.sidebar__name` 设成 `display: none` 的规则要删掉，否则 tooltip 没有文字。

- [ ] **Step 6: 验证**

```bash
npm --prefix web run build
```

预期：通过。人工在 1440 / 1024 / 390 三个宽度、明暗两种模式下确认：
图标栏常驻、当前页有竖条、hover 400ms 后出 tooltip、展开按钮切到 220px、
刷新后保持、切页面后保持。

- [ ] **Step 7: 提交**

```bash
git add web/src/App.tsx web/src/components/NavRail.tsx web/src/components/UserChip.tsx \
  web/src/components/Sidebar.tsx web/src/components/TopBar.tsx web/src/styles.css
git commit -m "导航壳: 图标栏常驻 56px，展开成 220px 文字版

一级导航过去默认 240px、折叠是可选项，于是「导航占多宽」这个问题每台设备
都要重新回答一次，而窄屏上答案总是「收起来再说」——代价是手机上换一次
section 要先拉开抽屉。现在反过来：56px 的图标栏在任何宽度都在，展开只是
密度偏好，持久化在 nav.expanded。"
```

---

## Task 2: 全局状态条

**Files:**
- Create: `web/src/components/StatusBar.tsx`
- Modify: `web/src/App.tsx:573-600`（`.shell` 内）
- Modify: `web/src/styles.css`（`.shell` 之后新增 `.statusbar` 区块）

**Interfaces:**
- Produces: `StatusBar` 组件 + `useStatusFacts()` hook。
- Produces: `StatusProvider` 的 context 值
  `{ setFacts: (facts: StatusFacts | null) => void }`，`StatusFacts` 为
  `{ left?: ReactNode; right?: ReactNode }`。页面在 effect 里写入，卸载时写 null。
- Consumes: `user.version` 作为右端常驻内容。

- [ ] **Step 1: 组件与 context**

```tsx
/**
 * The strip along the foot of every page.
 *
 * It carries two kinds of fact: one the shell always knows — which version of
 * the panel this is — and one only the page knows, such as where the caret is
 * in the file being edited. The second kind arrives through a context rather
 * than through props, because the page that has the fact is several layers
 * below the bar that shows it and nothing in between has any use for it.
 *
 * A page that contributes nothing leaves the left half empty; the bar still
 * stands, because furniture that comes and goes is furniture the eye has to
 * re-find on every navigation.
 */
export interface StatusFacts {
  left?: ReactNode
  right?: ReactNode
}

const StatusContext = createContext<(facts: StatusFacts | null) => void>(() => {})

export function useStatusFacts(facts: StatusFacts | null, deps: unknown[]) {
  const set = useContext(StatusContext)
  useEffect(() => {
    set(facts)
    return () => set(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}
```

`StatusBar` 自身：

```tsx
export function StatusBar({ version, facts }: { version: string; facts: StatusFacts | null }) {
  return (
    <footer className="statusbar">
      <div className="statusbar__left">{facts?.left}</div>
      <div className="statusbar__right">
        {facts?.right}
        <span className="statusbar__version">{version}</span>
      </div>
    </footer>
  )
}
```

- [ ] **Step 2: 挂进 shell**

`App.tsx` 的 `.shell` 里，`<main>` 之后：

```tsx
<StatusBar version={user.version} facts={statusFacts} />
```

`.shell` 是 flex 列，`.main` 已经 `overflow: hidden`，所以状态条不需要额外定位。

- [ ] **Step 3: 样式**

```css
/* 26px, and it is a line of facts rather than a place to put controls: nothing
   in here is clickable, so nothing in here needs a target height. */
.statusbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex: none;
  height: 26px;
  padding: 0 12px;
  background: var(--surface-1);
  border-top: 1px solid var(--border);
  color: var(--text-faint);
  font-size: 11.5px;
}

.statusbar__left,
.statusbar__right {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.statusbar__left {
  /* The open directory's absolute path lives here and it is the one thing in
     the bar that can be longer than the screen. */
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

@media (max-width: 768px) {
  /* On a phone the version is the first thing to go: it is the least useful
     fact in the bar and the only one nobody is mid-task about. */
  .statusbar__version {
    display: none;
  }
}
```

- [ ] **Step 4: 验证**

```bash
npm --prefix web run build
```

人工确认：状态条在概览、实例控制台、插件库、主机、面板设置每一页都在，高度一致。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/StatusBar.tsx web/src/App.tsx web/src/styles.css
git commit -m "导航壳: 每页脚下加一条 26px 全局状态条

编辑器过去在自己的状态条上报行列号，分屏之后两组各报一份，读者要先想
「哪一份是我的光标」。行列号这类「当前焦点的事实」只有一份，所以它属于
壳而不是组件。空着的半边留着不收起：来去不定的家具每次导航都要重新找。"
```

---

# PR 2 — 文件页

## Task 3: 后端文件索引与三个端点

**Files:**
- Create: `internal/serverfiles/index.go`
- Create: `internal/serverfiles/index_test.go`
- Modify: `internal/serverfiles/browser.go`（`Index()` 与写后失效）
- Modify: `internal/api/routes.go:340-348`
- Modify: `internal/api/` 中 `handleListFiles` 所在文件

**Interfaces:**
- Produces: `func (b *Browser) Find(q string, limit int, all bool) ([]Hit, error)`
  `Hit{Path string; Name string; IsDir bool; Size int64; Modified time.Time; Score int; Match []int}`
- Produces: `func (b *Browser) Grep(q string, limit int, all bool) ([]FileHits, error)`
  `FileHits{Path string; Lines []LineHit}`，`LineHit{N int; Text string; Col int; Len int}`
- Produces: `func (b *Browser) Usage() (Usage, error)` `Usage{Bytes int64; Files int; Dirs int; Partial bool}`
- Produces: 路由 `GET /api/instances/{id}/files/find`、`/files/search`、`/files/usage`，
  三者都要 `authz.CapInstanceFilesRead`。

- [ ] **Step 1: 写失败的测试**

`internal/serverfiles/index_test.go`：

```go
func TestFindMatchesSubsequenceAcrossSegments(t *testing.T) {
	b, dir := newBrowser(t)
	mkdirAll(t, dir, "plugins/Vulpecula")
	write(t, dir, "plugins/Vulpecula/config.yml", "a: 1\n")
	write(t, dir, "plugins/Other/config.yml", "b: 2\n")

	hits, err := b.Find("vulp con", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "plugins/Vulpecula/config.yml" {
		t.Fatalf("want plugins/Vulpecula/config.yml first, got %+v", hits)
	}
}

func TestFindSkipsHeavyDirsUnlessAsked(t *testing.T) {
	b, dir := newBrowser(t)
	mkdirAll(t, dir, "world/region")
	write(t, dir, "world/region/r.0.0.mca", "x")

	if hits, _ := b.Find("mca", 10, false); len(hits) != 0 {
		t.Fatalf("region should be skipped by default, got %+v", hits)
	}
	if hits, _ := b.Find("mca", 10, true); len(hits) != 1 {
		t.Fatalf("all=true should reach region, got %+v", hits)
	}
}

func TestGrepReportsLineAndColumn(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "server.properties", "motd=hi\nmax-players=20\n")

	out, err := b.Grep("max-players", 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Lines) != 1 {
		t.Fatalf("want one hit, got %+v", out)
	}
	if out[0].Lines[0].N != 2 || out[0].Lines[0].Col != 0 {
		t.Fatalf("want line 2 col 0, got %+v", out[0].Lines[0])
	}
}

func TestGrepSkipsBinaryAndOversize(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "plugin.jar", "PK\x03\x04needle")

	if out, _ := b.Grep("needle", 10, false); len(out) != 0 {
		t.Fatalf("binary files must not be grepped, got %+v", out)
	}
}

func TestUsageCountsBytesUnderTheRoot(t *testing.T) {
	b, dir := newBrowser(t)
	write(t, dir, "a.txt", "12345")
	mkdirAll(t, dir, "sub")
	write(t, dir, "sub/b.txt", "123")

	u, err := b.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// server.properties from newBrowser is 8 bytes.
	if u.Bytes != 5+3+8 {
		t.Fatalf("want 16 bytes, got %d", u.Bytes)
	}
}

func TestFindRespectsScope(t *testing.T) {
	b, dir := newBrowser(t)
	mkdirAll(t, dir, "plugins/Mine")
	write(t, dir, "plugins/Mine/config.yml", "a: 1\n")
	write(t, dir, "secret.txt", "x")

	confined := b.WithScope([]string{"plugins/Mine"})
	hits, _ := confined.Find("txt", 10, true)
	for _, h := range hits {
		if h.Path == "secret.txt" {
			t.Fatal("a confined browser must not index outside its scope")
		}
	}
}
```

辅助函数 `mkdirAll` / `write` 写在 `index_test.go` 顶部（`newBrowser` 已在
`browser_test.go` 里，同包可用）。`WithScope` 的真实名字以 `scope.go` 为准，
实现前先 `grep -n 'func (b \*Browser)' internal/serverfiles/*.go` 确认。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/serverfiles/ -run 'TestFind|TestGrep|TestUsage' -v
```

预期：编译失败，`b.Find undefined`。

- [ ] **Step 3: 实现索引**

`internal/serverfiles/index.go` 的要点（完整实现按这些约束写）：

```go
// Package-level, alongside browser.go.

// heavy names directories whose contents are machine-generated bulk: a world's
// region files, the launcher's library cache. A panel user looking for a file
// by name is never looking for r.0.-1.mca, and walking them turns a 30ms index
// into a 4s one on a real server. `all` on the endpoint reaches them.
var heavy = map[string]bool{
	"region": true, "entities": true, "poi": true, "data": false,
	"libraries": true, "cache": true, "versions": true, "logs": false,
}

// indexTTL is how long a walk is reused. The index backs a type-ahead, so it
// has to be cheap to consult and is allowed to be a little stale: a file
// created a second ago that does not show up in ⌘P for another half minute is
// a smaller problem than a walk per keystroke. Writes through this Browser
// drop it immediately; writes from outside the panel wait for the TTL.
const indexTTL = 30 * time.Second

// maxGrepBytes caps the file size the content search will read. Above it a
// file is a log or a world, and the search box is not the instrument for those.
const maxGrepBytes = 1 << 20
```

- `walk(all bool) ([]Entry, error)`：用 `b.open()` 拿 `*os.Root`，`fs.WalkDir`
  走 `root.FS()`，对每个目录名查 `heavy` 并在 `!all` 时 `fs.SkipDir`；每条
  entry 经过 `b.allowed(path)`（scope.go 现有的判断，名字以实际为准）过滤。
- 缓存：`type cache struct { at time.Time; all bool; entries []Entry }`，
  `b.idx *cache` 加 `sync.Mutex`。`all=false` 的结果不能拿来服务 `all=true`。
  `Browser` 目前是值语义（`New` 返回指针，但 handler 每次新建），所以缓存要放在
  **包级** `map[string]*cache`（键是 `b.dir`）+ `sync.RWMutex`，并在
  `Delete` / `Write` / `Mkdir` / `Rename` / `Upload` 成功后调用 `invalidate(b.dir)`。
- `Find`：把查询按空白切成若干片，每片对 `strings.ToLower(path)` 做子序列匹配，
  全部命中才算命中。得分：连续命中 +8、段首（`/` 之后或驼峰起点）命中 +12、
  命中在 basename 里 +20、路径越短 +（64 - len/4）。`Match []int` 只对
  最后一次成功匹配的下标集合去重排序后返回，供前端高亮。目录也进结果。
- `Grep`：先 `Find` 不到就全量走索引，对每个 `Editable` 且 `Size <= maxGrepBytes`
  的文件读全文；含 `\x00` 的直接跳过（二进制判定）；按行 `strings.Index`
  大小写不敏感匹配，每文件最多 20 行，整体最多 `limit` 个文件。
- `Usage`：走索引累加 `Size`，`Partial` 在 `all=false` 跳过过重目录时为 true——
  但配额显示要真实数字，所以 `Usage` 内部固定用 `all=true`。

- [ ] **Step 4: 跑测试确认通过**

```bash
gofmt -l internal/serverfiles && go test ./internal/serverfiles/ -v
```

预期：`gofmt -l` 无输出，测试全绿。

- [ ] **Step 5: 三个 handler 与路由**

`internal/api/routes.go`，紧跟现有 files 路由之后：

```go
rt("GET /api/instances/{id}/files/find", s.handleFindFiles, authz.CapInstanceFilesRead),
rt("GET /api/instances/{id}/files/search", s.handleSearchFiles, authz.CapInstanceFilesRead),
rt("GET /api/instances/{id}/files/usage", s.handleFileUsage, authz.CapInstanceFilesRead),
```

handler 照 `handleListFiles` 的写法：解析 `q`、`limit`（默认 50，上限 200）、
`all`（`"1"` / `"true"`），拿 browser，调方法，`writeJSON`。`q` 为空时 `find`
返回空数组而不是全量——空查询的答案在前端（最近打开）。

`/files/usage` 的响应带上宿主磁盘总量，前端一次请求就能画进度条：

```go
type usageResponse struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
	// DiskTotal is the host filesystem the instance sits on, so the panel can
	// draw "24.6 / 80 GB" without a second request to a different endpoint.
	DiskTotal int64 `json:"diskTotal"`
}
```

宿主磁盘总量从 `internal/metrics`（`system.disk`）已有的实现取，别新写一个
`statfs`。实现前 `grep -rn 'Total' internal/metrics/*.go` 找现成的。

- [ ] **Step 6: 验证**

```bash
make lint && make test
```

预期：都通过。

- [ ] **Step 7: 提交**

```bash
git add internal/serverfiles/index.go internal/serverfiles/index_test.go \
  internal/serverfiles/browser.go internal/api/
git commit -m "文件: 实例文件索引，支撑转到文件与全文搜索

树是用来「知道自己在哪」的，不是用来找文件的：world/region 下几百个 .mca、
每个插件十几个文件，靠展开树找是最慢的路径。索引在服务端做模糊匹配和内容
搜索，前端只拿前 50 条，因此不需要把上万条路径拉过去再筛。默认跳过
region/entities/libraries/cache/versions 这些机器生成的批量目录，带 all=1
才走进去。"
```

---

## Task 4: 前端接线与模糊高亮

**Files:**
- Modify: `web/src/types.ts`（三个响应类型）
- Modify: `web/src/api.ts:715-740`
- Create: `web/src/fuzzy.ts`
- Create: `web/src/useFileIndex.ts`

**Interfaces:**
- Produces: `FileHit { path: string; name: string; isDir: boolean; size: number;
  modified: string; score: number; match: number[] }`
- Produces: `FileSearchHit { path: string; lines: { n: number; text: string; col: number; len: number }[] }`
- Produces: `FileUsage { bytes: number; files: number; dirs: number; diskTotal: number }`
- Produces: `api.findFiles(id, q, opts?)` / `api.searchFiles(id, q, opts?)` / `api.fileUsage(id)`
- Produces: `segments(text: string, match: number[]): { text: string; hit: boolean }[]`
- Produces: `useFileIndex(instanceId)` → `{ find, search, usage, usageState }`，`find`/`search`
  内部 160ms 防抖并丢弃过期响应。

- [ ] **Step 1: 类型与绑定**

`api.ts`：

```ts
findFiles: (id: string, q: string, opts: { limit?: number; all?: boolean } = {}) =>
  request<FileHit[]>(
    'GET',
    `/api/instances/${id}/files/find?q=${encodeURIComponent(q)}` +
      `&limit=${opts.limit ?? 50}${opts.all ? '&all=1' : ''}`,
  ),
```

`searchFiles` / `fileUsage` 同形。

- [ ] **Step 2: 高亮切段**

`web/src/fuzzy.ts`：

```ts
/**
 * Splits a string into runs, marking the characters the server matched.
 *
 * The match itself happens on the server — it is the half that has to be fast
 * over ten thousand paths — and it sends back the indices it used. All this
 * does is turn those indices into something React can render without putting
 * one <mark> per character into the tree.
 */
export function segments(text: string, match: number[]): { text: string; hit: boolean }[] {
  if (match.length === 0) return [{ text, hit: false }]
  const on = new Set(match)
  const out: { text: string; hit: boolean }[] = []
  for (let i = 0; i < text.length; i++) {
    const hit = on.has(i)
    const last = out[out.length - 1]
    if (last && last.hit === hit) last.text += text[i]
    else out.push({ text: text[i], hit })
  }
  return out
}
```

- [ ] **Step 3: 验证**

```bash
npm --prefix web run build
```

- [ ] **Step 4: 提交**

```bash
git add web/src/types.ts web/src/api.ts web/src/fuzzy.ts web/src/useFileIndex.ts
git commit -m "文件: 索引三个端点的前端绑定与命中高亮切段"
```

---

## Task 5: 文件页两栏骨架与侧边栏容器

**Files:**
- Create: `web/src/components/FileSidebar.tsx`
- Modify: `web/src/components/FileManager.tsx:1195-1400`（render）、`:200-230`（状态）
- Delete: `web/src/components/FileNav.tsx`
- Modify: `web/src/styles.css`（`.fm*` 区块整体重写）

**Interfaces:**
- Produces: `FileSidebar` props `{ width: number; onWidth: (n: number) => void;
  collapsed: boolean; onToggle: () => void; panel: 'tree' | 'search' | 'recent';
  onPanel: (p: Panel) => void; onNew: () => void; onRefresh: () => void;
  more: MenuItem[]; usage: FileUsage | null; overlay: boolean; children: ReactNode }`
- Produces: `FileManager` 内 `sidebar` 状态
  `{ width: number; collapsed: boolean; panel: Panel }`，持久化到
  `hc.files.side.<instanceId>`（新键；旧的 `hc.files.nav.<id>` 描述的是已经不存在的布局，
  不迁移，直接换键）。

- [ ] **Step 1: 容器与折叠**

`FileSidebar` 的结构，严格按 spec §5.1 / §5.2 / §5.3 / §5.5：

```tsx
if (collapsed) {
  // 40px is a strip with one job: get back. Vertical text does not fit in it
  // and a label nobody can read is worse than none, so the button is all
  // there is — and it is the same glyph that folded it, in the same corner.
  return (
    <div className="fside fside--shut">
      <button
        type="button"
        className="fside__open"
        onClick={onToggle}
        aria-label="展开侧边栏（⌘/Ctrl + B）"
        title="展开侧边栏（⌘/Ctrl + B）"
      >
        <Glyph name="sidebar" />
      </button>
    </div>
  )
}
```

展开态：38px 头部（`资源管理器` 小号大写标签 + 四个 28px 图标按钮）、24px 分段
面板切换、`children`、底部配额行。

- [ ] **Step 2: 三个面板各自记住滚动位置**

三个面板**同时挂载**、用 `hidden` 切换而不是条件渲染——条件渲染会把滚动位置
和搜索框里的字一起丢掉，而 spec §5.3 明确要求各自保留滚动位置：

```tsx
<div className="fside__body">
  <div className="fside__panel" hidden={panel !== 'tree'}>{tree}</div>
  <div className="fside__panel" hidden={panel !== 'search'}>{search}</div>
  <div className="fside__panel" hidden={panel !== 'recent'}>{recent}</div>
</div>
```

（`[hidden]` 在 `styles.css` 里要确保是 `display: none`，浏览器默认就是，但
`.fside__panel` 如果设了 `display: flex` 会盖掉它 —— 写成
`.fside__panel[hidden] { display: none; }`。）

- [ ] **Step 3: 拖拽调宽**

沿用 `FileManager.tsx:985` 现有 `drag` 的写法（pointer 事件 + window 监听），
上下限换成 spec §5.1 的 240 / 480，落点写进 `sidebar.width` 并持久化。
编辑器 620px 硬指标的执行位置在这里：

```ts
const SIDE_MIN = 240
const SIDE_MAX = 480
/** The floor under one editor group, from the spec: 82 monospace columns, which
 *  is the longest comment line in Paper's own default configs. Enforced while
 *  dragging rather than in CSS, because CSS cannot say "take it out of the rail
 *  being dragged" — a minmax floor on the editor track overflows the grid
 *  instead of stopping the drag. */
const GROUP_MIN = 620
```

- [ ] **Step 4: 两栏栅格**

`FileManager` 的 render 收敛成：

```tsx
<div className="fm" data-overlay={overlaySidebar ? '' : undefined}
     style={{ gridTemplateColumns: template }}>
  {sidebarShown && <FileSidebar …>{panelBody}</FileSidebar>}
  {!collapsed && !overlaySidebar && <div className="fm__grip" … />}
  <div className="fm__main">
    {open ? <FileEditor … /> : <FileBrowse … />}
  </div>
</div>
```

`template`：折叠时 `40px minmax(0, 1fr)`；overlay 时
`40px minmax(0, 1fr)`（侧边栏 `position: absolute` 浮在主区上）；否则
`min(${width}px, max(${SIDE_MIN}px, 100% - ${GRIP}px - ${GROUP_MIN}px)) ${GRIP}px minmax(0, 1fr)`。

`.fm__main` 必须 `min-width: 0; min-height: 0; display: flex; flex-direction: column`。

- [ ] **Step 5: `⌘B`**

接进 `FileManager` 现有的 `onKey`（`:1043`）：

```ts
if (mod && event.key.toLowerCase() === 'b') {
  event.preventDefault()
  toggleSidebar('user')
  return
}
```

`toggleSidebar(by: 'user' | 'auto')`：`by === 'user'` 时置
`pinnedRef.current = true`（spec §11 的 `sidebar.userPinned`，会话内有效，
关闭分屏时清除）。

- [ ] **Step 6: 删掉 FileNav 与密度切换**

```bash
git rm web/src/components/FileNav.tsx
```

`FileList.tsx` 删掉 `Density` / `DensitySwitch` 及其全部分支（v2 §1 作废 v1 §6.1），
`FileManager` 里 `densityKey` / `chosenDensity` / `NAV_DETAIL_MIN` 一并删除。
`styles.css` 里 `.fm__nav` / `.fm__sec*` / `.fm__grip--row` 整段删除。

- [ ] **Step 7: 验证**

```bash
npm --prefix web run build
```

人工：`⌘B` 折叠成 40px 竖条且能点回来；拖边缘调宽、刷新后保持；
1024px 以下侧边栏是全屏抽屉。

- [ ] **Step 8: 提交**

```bash
git add -A web/src/components web/src/styles.css
git commit -m "文件页: 换成侧边栏 + 主区两栏

上下两段的导航列是在 264px 宽度下的妥协——树和列表抢同一份高度，谁也不够
用。列表搬进主区之后宽度不再是稀缺资源，导航列只剩一件事：你在哪。于是它
变成一个 300px、可拖、⌘B 能收成 40px 的侧边栏，树、搜索、最近三个面板在里
面切换，各自记住滚动位置。"
```

---

## Task 6: 树里带文件

**Files:**
- Modify: `web/src/components/FileTree.tsx`（全文）
- Modify: `web/src/styles.css`（`.ftree*`）

**Interfaces:**
- Consumes: `api.listFiles`（不变）
- Produces: `FileTree` 新增 props
  `{ openPaths: Set<string>; activePath: string | null; onOpenFile: (entry: FileEntry) => void;
    menuFor: (entry: FileEntry) => MenuItem[] }`

- [ ] **Step 1: 缓存文件而不是只缓存目录**

`read()` 里去掉 `.filter((entry) => entry.isDir)`，`TreeNode` 加 `entry: FileEntry`
（行操作和右键菜单都要它）。排序按 spec §5.4：文件夹在前按名升序，文件在后按名升序。

```ts
const order = (a: TreeNode, b: TreeNode) =>
  a.isDir === b.isDir ? a.name.localeCompare(b.name, 'zh-CN') : a.isDir ? -1 : 1
```

组件顶部的注释要重写——现有那段解释的是「只放文件夹」的理由，且理由里写明了
是 216px 宽度下的结论。新注释要说清楚 300px 下这个权衡变了，并保留
「懒加载 + 缓存，刷新才丢」这条仍然成立的约束。

- [ ] **Step 2: 行规格**

```css
/* 26px rows, 14px per level. Both numbers are the design note's; the indent is
   padding on the row rather than a margin so the hover tint and the current-row
   tint still run the full width of the column. */
.ftree__row {
  min-height: 26px;
}

.ftree__row .icon,
.ftree__row .fileicon {
  width: 14px;
  height: 14px;
}
```

行的 `paddingLeft` 从 `6 + depth * 12` 改成 `6 + depth * 14`。

三种状态，spec §5.4：

```css
/* Open and in front. */
.ftree__row--on .ftree__label {
  color: var(--accent);
  font-weight: 600;
}

/* Open in some tab, but not the one being read: full text colour, against the
   dimmed colour of everything merely listed. Without this step the tree cannot
   answer "what have I got open", which is the question it grew files for. */
.ftree__row--held .ftree__label {
  color: var(--text);
}

.ftree__label {
  color: var(--text-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

- [ ] **Step 3: 展开箭头只在确有子项时出现**

文件永远没有箭头（`hasChildren = false`）。目录沿用现有「未读过就画成可展开」的
逻辑——读过之后为空则箭头消失，这是已有行为，注释已经解释过，保留。

- [ ] **Step 4: 右键菜单**

`onContextMenu` 用 `FileEditor.tsx:330` 已有的 `createPortal` + `.menu__sheet`
写法（同一套外观），项目来自 `menuFor(entry)` —— 由 `FileManager` 传入，
**和主区列表行的 `⋯` 用的是同一个函数**，spec §5.4 要求两处同组同序。

- [ ] **Step 5: 只展开当前路径**

现有 `ancestors(path)` 的行为已经正确（只展开到当前目录那条路径），不要改。

- [ ] **Step 6: 验证**

```bash
npm --prefix web run build
```

人工：树里有文件；当前文件强调色加粗；其他已打开文件是主文字色；
右键菜单和列表行 `⋯` 一致；长名截断且 `title` 给全名。

- [ ] **Step 7: 提交**

```bash
git add web/src/components/FileTree.tsx web/src/styles.css
git commit -m "文件页: 目录树里同时显示文件

只放文件夹是 216px 宽度下的结论——那时文件名进来就是 banned-players... 和
version_histor...，比它要替代的列表更难读。侧边栏 300px 之后这个理由没了，
而树能回答「我开着哪些文件」是列表搬走之后新出现的需求。"
```

---

## Task 7: 侧边栏的搜索与最近面板

**Files:**
- Create: `web/src/components/FileSearchPanel.tsx`
- Create: `web/src/components/FileRecentPanel.tsx`
- Modify: `web/src/components/FileManager.tsx`（最近列表的记录）

**Interfaces:**
- Produces: `FileSearchPanel` props `{ instanceId: string; onOpen: (path: string, line?: number) => void }`
- Produces: `FileRecentPanel` props `{ paths: string[]; activePath: string | null;
  onOpen: (path: string) => void; onForget: (path: string) => void }`
- Produces: `FileManager` 里 `recent` 状态，最多 20 条，持久化到 `hc.files.recent.<instanceId>`

- [ ] **Step 1: 搜索面板**

输入框 + 结果按文件分组、可展开看命中行。请求走 `useFileIndex` 的 `search`，
160ms 防抖。空态 / 无结果 / 失败三种都用 `EmptyState` 与 `Note`，不要新写第七种空状态
（design-system.md §3）。

命中行的高亮用 `col` / `len` 切三段，而不是再在前端搜一遍：

```tsx
<span className="fsearch__line">
  {line.text.slice(0, line.col)}
  <mark>{line.text.slice(line.col, line.col + line.len)}</mark>
  {line.text.slice(line.col + line.len)}
</span>
```

点击命中行 → `onOpen(path, line.n)`。`FileManager.openPath` 加一个可选
`line` 参数，打开后把编辑器滚到该行（复用 `FileEditor` 里 `spots` 的
`caret`/`top` 机制：把 caret 设到该行首字符的偏移即可）。

- [ ] **Step 2: 最近面板**

`FileManager` 在 `openPath` 成功后把路径推到 `recent` 头部、去重、截断 20，
写 `localStorage`。面板列出它们，每行文件名 + 次行灰色目录，`⋯` 里一项「从最近移除」。

- [ ] **Step 3: `⌘⇧F`**

`FileManager` 的 `onKey` 里：

```ts
if (mod && event.shiftKey && event.key.toLowerCase() === 'f') {
  event.preventDefault()
  setSidebar((s) => ({ ...s, collapsed: false, panel: 'search' }))
  // The box has to take the caret, or the shortcut lands on a panel and stops.
  window.setTimeout(() => searchPanelBox.current?.focus(), 0)
  return
}
```

注意这一条必须排在现有 `mod && 'f'` 的分支**之前**，否则 `⌘⇧F` 会先被
「组内查找 / 聚焦目录筛选」吃掉。

- [ ] **Step 4: 验证**

```bash
npm --prefix web run build
```

人工：搜 `max-players` 能在 `server.properties` 下看到命中行，点进去落在第 2 行；
`⌘⇧F` 打开搜索面板并聚焦输入框；三个面板来回切换后滚动位置还在。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/FileSearchPanel.tsx web/src/components/FileRecentPanel.tsx \
  web/src/components/FileManager.tsx web/src/styles.css
git commit -m "文件页: 侧边栏加搜索与最近两个面板"
```

---

## Task 8: 主区浏览态

**Files:**
- Create: `web/src/components/FileBrowse.tsx`
- Modify: `web/src/components/FileBar.tsx`（拆成 `FileCrumbs` + `FileTools`）
- Modify: `web/src/components/FileList.tsx`（固定列宽表格）
- Modify: `web/src/styles.css`

**Interfaces:**
- Produces: `FileBrowse` props：把 `FileManager` 现有传给 `FileBar` 与 `FileList` 的
  那两组 props 合并，外加 `stats`。
- Produces: `FileCrumbs` props `{ dir: string; pending: boolean; onNavigate: (p: string) => Promise<boolean> }`
- Produces: `FileTools` props `{ query; onQuery; searchRef; onUpload; onNew; onRefresh; more; busy; writable }`

- [ ] **Step 1: 四条横带**

```tsx
<div className="fbrowse">
  <FileCrumbs … />          {/* 36px */}
  <FileTools … />           {/* 46px，选中 ≥1 项时整条换成批量操作条 */}
  <FileList … />            {/* 表头 32px + 行 40px */}
</div>
```

`FileCrumbs` 就是现有 `FileBar` 的面包屑半边（`:100-200`）原样搬过来——v1 §4
的规则 v2 明确保留，**不要重写**，只把它从一条混合了工具按钮的 bar 里摘出来。
`FileTools` 是另外半边。`上传 / 新建 / 刷新` 在两种形态下同组同序（spec §6.2），
所以它们放在 `FileTools` 里，而 `FileTools` 在编辑态**也渲染**（见 Task 9）。

- [ ] **Step 2: 列宽**

`FileList` 从 flex 行换成固定列宽，spec §6.3：

```css
/* One grid, one set of tracks, shared by the header and every row: a header
   whose columns are declared separately from its rows is a header that drifts
   out of line the first time a filename is long. */
.flist__cols,
.frow {
  display: grid;
  grid-template-columns: 22px minmax(0, 1fr) 110px 190px 76px;
  gap: 10px;
  align-items: center;
}

.flist__cols {
  height: 32px;
}

.frow {
  height: 40px;
}

.frow__size,
.flist__col--size {
  text-align: right;
}
```

`minmax(0, 1fr)` 那一列是名称——`0` 是最小值，不是笔误：没有它，长文件名会把
整个栅格撑破（这是本仓库最高频的布局 bug）。

窄屏逐级丢列（沿用 `.ptable__row` 的做法，丢的是别处能看到的信息）：

```css
@media (max-width: 900px) {
  /* The time is the column to lose: it is in the row's title attribute and in
     the editor's own status line, and the name is what the list is for. */
  .flist__cols,
  .frow {
    grid-template-columns: 22px minmax(0, 1fr) 110px 76px;
  }

  .frow__time,
  .flist__col--time {
    display: none;
  }
}
```

- [ ] **Step 3: 批量操作条不再叠在表头上**

现有 `.flist__bulk`（`:222`）浮在列表头。改成：选中 ≥1 项时 `FileTools`
整条替换成批量条，高度不变（46px），所以列表不会跳。

- [ ] **Step 4: 验证**

```bash
npm --prefix web run build
```

人工：1440px 下「修改时间」列不截断；7 天内显示相对时间；未 hover 看不到
下载/删除图标；hover 时复选框原地替换类型图标且文字不位移；删除文件夹要输名字；
选中 ≥1 项后工具条变批量条。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/FileBrowse.tsx web/src/components/FileBar.tsx \
  web/src/components/FileList.tsx web/src/styles.css
git commit -m "文件页: 目录列表搬进主区，换成固定列宽的表格

列表过去在 264-380px 的导航列里，四列挤不下，于是有了紧凑/详情两种密度，
于是「修改时间在不在」取决于一个开关。主区宽度充足之后这两件事一起没了：
一套列宽，一种形态，修改时间不再被截断。"
```

---

## Task 9: 编辑态的组四件套

**Files:**
- Modify: `web/src/components/FileEditor.tsx`
- Modify: `web/src/styles.css`（`.fedit*` / `.editor__status`）

**Interfaces:**
- Produces: `EditorPane` 不变（`{ tabs, active }`）。
- Produces: `FileEditor` 新增 props `{ dir: string; instanceName: string;
  onCaret: (at: { line: number; column: number } | null) => void; tools: ReactNode }`
- Consumes: Task 2 的 `useStatusFacts`（在 `FileManager` 里调用，不在 `FileEditor` 里）。

- [ ] **Step 1: 组内加面包屑（28px）**

标签栏之下、正文之上，spec §7.3。它描述**当前标签的文件**，所以数据来自
`pane.active` 而不是 `dir`：

```tsx
{file && (
  <nav className="fedit__crumbs" aria-label="当前文件的位置">
    <button type="button" onClick={() => onWalk('')}>{instanceName}</button>
    {crumbsOf(file.path).map((step) => (
      <Fragment key={step.path}>
        <span className="fedit__crumb-sep" aria-hidden="true">/</span>
        {step.last ? (
          <span aria-current="page">{step.name}</span>
        ) : (
          <button type="button" onClick={() => onWalk(step.path)}>{step.name}</button>
        )}
      </Fragment>
    ))}
  </nav>
)}
```

样式：等宽小字、`--text-faint`、`min-width: 0` + 横向可截断。

- [ ] **Step 2: 行列号上交，组状态条只剩两个按钮**

`FileEditor.tsx:668` 那一行拆掉 `行 X，列 Y`：

```tsx
<span className="editor__facts">
  {lang.label} · UTF-8 · {file.content.includes('\r\n') ? 'CRLF' : 'LF'}
</span>
```

`Body` 里 `at`（光标行列）通过新 prop 往上报：

```ts
// Reported upward rather than printed here: with two groups on screen there
// are two of these lines and only one caret, and a reader should not have to
// work out which half is theirs. The shell's status bar shows the focused
// group's position and nothing else. See StatusBar.
useEffect(() => {
  if (!focused) return
  onCaret(at)
  return () => onCaret(null)
}, [focused, at.line, at.column, onCaret])
```

组状态条高度 30px（spec §7.5），标签栏 38px（§7.2）。

- [ ] **Step 3: 行号槽宽度随组数变**

```css
.fedit__pane {
  /* 52px holds five digits with room; split in half there is no room to spare
     and four digits is what a 4000-line paper-global.yml actually needs. */
  --gutter-w: 52px;
}

.fedit[data-panes='2'] .fedit__pane {
  --gutter-w: 46px;
}
```

`.editor__gutter` 的固定宽度换成 `var(--gutter-w)`。

- [ ] **Step 4: `上传 / 新建 / 刷新` 在编辑态也在**

spec §6.2 最后一句。`FileManager` 把 `<FileTools>` 同时渲染在编辑态的主区顶部
（在 `FileEditor` 之上），组件同一个，props 同一组。

- [ ] **Step 5: 验证**

```bash
npm --prefix web run build
```

人工：组状态条只有「还原」「保存」；行列号出现在页脚状态条；分屏时只有一份行列号；
组内面包屑指的是当前标签的文件，切目录不变。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/FileEditor.tsx web/src/components/FileManager.tsx web/src/styles.css
git commit -m "文件页: 编辑器组补齐面包屑，行列号交给全局状态条

一个组现在是标签栏 + 面包屑 + 正文 + 状态条四件套，分屏时每组各有一份。
行列号是例外：光标只有一个，两组各印一份的结果是读者要先判断哪一份是自己
的，所以它上交给页脚。"
```

---

## Task 10: 分屏

**Files:**
- Modify: `web/src/components/FileManager.tsx`（`panes` / 方向 / 拖拽 / 快捷键）
- Modify: `web/src/components/FileEditor.tsx`（分屏菜单、标签拖放）
- Modify: `web/src/components/Glyph.tsx`（`split-row`）
- Modify: `web/src/styles.css`（`.fedit`）

**Interfaces:**
- Produces: `FileManager` 状态 `layout: { dir: 'col' | 'row'; ratio: number }`，
  `'col'` = 左右（两列），`'row'` = 上下（两行）。持久化进 `hc.files.side.<id>`。
- Produces: `FileEditor` 新增 props
  `{ layout: Layout; onSplit: (dir: 'col' | 'row') => void; onRatio: (n: number) => void;
    onMoveTab: (from: number, to: number, path: string) => void; canSplitCol: boolean }`

- [ ] **Step 1: 分屏是两项菜单**

`FileEditor.tsx:296` 那个单一 `分屏` 按钮换成 `Menu`：

```tsx
<Menu
  className="btn btn--icon btn--small"
  items={[
    {
      label: '左右分屏',
      disabled: panes.length >= 2 || !canSplitCol || file === null,
      onSelect: () => onSplit('col'),
    },
    {
      label: '上下分屏',
      disabled: panes.length >= 2 || file === null,
      onSelect: () => onSplit('row'),
    },
  ]}
  title="分屏对照"
  ariaLabel="分屏对照"
>
  <Glyph name="split" />
</Menu>
```

`canSplitCol` 由 `FileManager` 按视口宽度给（`< 1280px` 为 false，spec §11）。
上下分屏在任何宽度下都在——它不吃宽度，spec §9.2 明确它不是窄屏备胎。

- [ ] **Step 2: 方向与比例**

```css
.fedit {
  display: grid;
  /* 12px between groups, and it is a gap rather than a border: the two groups
     are peers, and a line between them would read as one containing the other. */
  gap: 12px;
  min-width: 0;
  min-height: 0;
}
```

方向和比例由行内样式给（必须由 JS 计算的动态值，属于 frontend-design 允许的例外）：

```ts
const style =
  panes.length < 2
    ? undefined
    : layout.dir === 'col'
      ? { gridTemplateColumns: `${layout.ratio}fr 12px ${1 - layout.ratio}fr` }
      : { gridTemplateRows: `${layout.ratio}fr 12px ${1 - layout.ratio}fr` }
```

组之间插一个 `.fedit__grip`，`role="separator"`，按方向设 `aria-orientation`，
拖拽复用 Task 5 的 pointer 写法。左右分屏时每组的下限是 `GROUP_MIN`（620px）。

- [ ] **Step 3: 拖标签换组**

标签加 `draggable`，`dataTransfer` 里放 `{pane, path}` 的 JSON；组的正文区
`onDragOver` / `onDrop` 接收：

```ts
const onMoveTab = (from: number, to: number, path: string) => {
  if (from === to) return
  setPanes((current) =>
    current.map((pane, index) => {
      if (index === from) {
        const tabs = pane.tabs.filter((one) => one !== path)
        return { tabs, active: pane.active === path ? (tabs[0] ?? null) : pane.active }
      }
      if (index === to) {
        const tabs = pane.tabs.includes(path) ? pane.tabs : [...pane.tabs, path]
        return { tabs, active: path }
      }
      return pane
    }),
  )
}
```

搬空的那一组由现有 `dropTabs` 的收尾逻辑处理（`:540` 的 `kept`），
但 `onMoveTab` 走的是 `setPanes`，所以这里要自己做同一件事：搬完之后如果
`from` 组空了就把它去掉，`focusedPane` 收敛。**把这段收尾抽成一个
`prunePanes(next: EditorPane[])` 纯函数，`dropTabs` 和 `onMoveTab` 共用**，
否则两条路径会对「空组怎么办」给出两个答案。

- [ ] **Step 4: 快捷键 `⌘\` 与 `⌘1/2`**

```ts
if (mod && event.key === '\\') {
  event.preventDefault()
  if (activePathNow.current && canSplitColNow.current) splitInto('col')
  return
}
if (mod && (event.key === '1' || event.key === '2')) {
  const want = Number(event.key) - 1
  if (want < panesNow.current.length) {
    event.preventDefault()
    setFocusedPane(want)
  }
  return
}
```

- [ ] **Step 5: 验证**

```bash
npm --prefix web run build
```

人工：分屏菜单两项；1600px 左右分屏后每组可视宽度 ≥ 620px（用开发者工具量
`.editor__text` 的 `clientWidth`）；1280px 以下左右分屏项禁用、上下分屏仍可用；
拖标签能换组；关掉一组最后一个标签后另一组占满。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/FileEditor.tsx web/src/components/FileManager.tsx \
  web/src/components/Glyph.tsx web/src/styles.css
git commit -m "文件页: 分屏分左右和上下两种

对比两份 YAML 时上下排列让行号和缩进天然对齐，比左右好读，而且不吃宽度——
它是一个平等的选项，不是窄屏的备胎。左右分屏在 1280px 以下禁用，因为两组
各 620px 的下限在那个宽度下已经放不下。"
```

---

## Task 11: `⌘P` 转到文件

**Files:**
- Create: `web/src/components/GoToFile.tsx`
- Modify: `web/src/components/FileManager.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Produces: `GoToFile` props `{ instanceId: string; recent: string[];
  onOpen: (path: string, split: boolean) => void; onClose: () => void }`

- [ ] **Step 1: 浮层**

复用 `CommandPalette.tsx` 的浮层骨架与键盘处理（`↑↓` / `Enter` / `Esc`），
不要新写一套。差别只有三处：数据源是 `api.findFiles`、空输入显示 `recent` 前 10、
`⌘Enter` 在新分屏组打开。

结果行：文件名（`segments()` 高亮）+ 次行灰色目录：

```tsx
<span className="gotofile__name">
  {segments(hit.name, nameMatch(hit)).map((run, i) =>
    run.hit ? <mark key={i}>{run.text}</mark> : <span key={i}>{run.text}</span>,
  )}
</span>
<span className="gotofile__dir">{parentOf(hit.path) || '/'}</span>
```

`nameMatch(hit)`：后端给的 `match` 是**整条路径**的下标，行里显示的是
basename，所以要减去目录前缀长度并丢掉落在目录里的下标。这个换算写成
`fuzzy.ts` 里的 `shift(match, offset, length)`，附注释说明为什么不能直接用。

- [ ] **Step 2: 排序里把最近打开的顶上去**

后端按匹配得分排；前端把 `recent` 里出现过的条目提前（spec §10）：

```ts
const ranked = useMemo(() => {
  const rank = new Map(recent.map((path, index) => [path, index]))
  return hits.slice().sort((a, b) => {
    const ra = rank.get(a.path) ?? Infinity
    const rb = rank.get(b.path) ?? Infinity
    // Recency first, and only then the server's score: the file you were just
    // in is almost always the file you are looking for, whatever it scores.
    return ra === rb ? b.score - a.score : ra - rb
  })
}, [hits, recent])
```

- [ ] **Step 3: 键与 loading 态**

`FileManager` 的 `onKey` 加 `mod && 'p'`（`event.preventDefault()` 要有，
否则触发浏览器打印）。请求期间浮层显示 `Skeleton`，不显示「无结果」——
spec 验收项要求首次拉取有 loading 态。

- [ ] **Step 4: 验证**

```bash
npm --prefix web run build
```

人工：`⌘P` 唤起；空输入显示最近；输 `vulp con` 命中
`plugins/Vulpecula/config.yml`；命中字符高亮；`Enter` 打开；`⌘Enter` 开新分屏组。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/GoToFile.tsx web/src/components/FileManager.tsx \
  web/src/fuzzy.ts web/src/styles.css
git commit -m "文件页: ⌘P 转到文件"
```

---

## Task 12: 响应式、自动折叠与快捷键表

**Files:**
- Modify: `web/src/components/FileManager.tsx`
- Modify: `web/src/styles.css`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: 断点**

`FileManager` 里四个 `useMediaQuery`，值与 spec §11 的表一致：

```ts
/** The four widths the file pane changes shape at, from the design note. They
 *  are viewport widths rather than column widths on purpose: what matters is
 *  how much room there is in total, and a column that has been dragged narrow
 *  is a choice rather than a constraint. */
const ROOMY = '(min-width: 1600px)'
const SNUG = '(max-width: 1360px)'
const NO_COL_SPLIT = '(max-width: 1280px)'
const DRAWER = '(max-width: 1024px)'
```

`DRAWER` 的 1024 与 `App.tsx` 的 `DRAWER_QUERY` 和 `styles.css` 的媒体查询是同一档，
三处一起改（CLAUDE.md 点名的坑）。

自动折叠：

```ts
// Splitting at a middling width costs the sidebar, and un-splitting gives it
// back — unless the reader has said otherwise this session, in which case the
// layout stops having opinions. The pin is deliberately not persisted: it is
// an answer to "not now", not a setting.
useEffect(() => {
  if (pinned) return
  if (panes.length > 1 && !roomy) setSidebar((s) => ({ ...s, collapsed: true }))
  else if (panes.length === 1 && snug === false) setSidebar((s) => ({ ...s, collapsed: false }))
}, [panes.length, roomy, snug, pinned])

// Closing the split is what clears it, per the design note.
useEffect(() => {
  if (panes.length === 1) setPinned(false)
}, [panes.length])
```

`< 1360px` 手动展开时浮在主区之上，选中文件后自动收回：`overlay = snug && !collapsed`，
`openPath` 成功后 `if (overlayRef.current) setSidebar((s) => ({ ...s, collapsed: true }))`。

- [ ] **Step 2: 快捷键弹窗**

`FileManager` 里的 `KeysDialog` 补齐 spec §12 的全部 11 条，措辞和表格一致。

- [ ] **Step 3: CHANGELOG**

`CHANGELOG.md` 的「未发布」小节加（只加，不改版本号）：

```markdown
### 变更

- 一级导航改为常驻的 56px 图标栏，底部按钮可展开成 220px 文字版并记住选择。
- 每个页面脚下多了一条状态条，显示当前位置、未保存计数和面板版本。
- 文件页重做：左侧是可折叠的侧边栏（文件树 / 搜索 / 最近），右侧没开文件时是
  目录列表、开了文件就是编辑器，不再需要切换。
- 文件页支持左右和上下两种分屏，可以拖标签在两组之间移动。
- 新增 `⌘/Ctrl + P` 转到文件，对当前实例的全部文件路径做模糊匹配。
- 侧边栏的搜索面板可以搜文件内容，点命中行直接跳到那一行。
- 文件列表的「修改时间」不再被截断，紧凑/详情两种密度合并成一种。
```

- [ ] **Step 4: 全量验证**

```bash
npm --prefix web run build && make lint && make test && make build
```

- [ ] **Step 5: 提交**

```bash
git add web/src/components/FileManager.tsx web/src/styles.css CHANGELOG.md
git commit -m "文件页: 按视口宽度自动折叠侧边栏，补齐快捷键表与更新日志"
```

---

## 收尾

- [ ] 按 `verification-before-completion` skill 跑完整检查并贴输出。
- [ ] 按 CLAUDE.md 工作流程第 4 条：推功能分支 → 切 `main` → `git pull origin main`
      → 合并 → 推 `main` → 切回功能分支。

## 自查清单（对着 spec §14 逐条）

实现完成后逐项确认，尤其是这些容易漏的：

- [ ] 页面上不存在名为「编辑模式」的开关（本来就没有，不要因为加了形态切换而
      重新引入一个）。
- [ ] hover 时复选框原地替换类型图标，行内文字位置不位移。
- [ ] 打开 `.jar` 不进文本编辑器。
- [ ] 任何断点下单组代码可视宽度 ≥ 620px。
- [ ] 明暗两种模式都看过；新令牌两个块都加了。
- [ ] 1440 / 1200 / 1024 / 768 / 390 五个宽度无横向溢出。
- [ ] 折叠侧栏、打开抽屉、开着控制台的实例页三处没被波及。
