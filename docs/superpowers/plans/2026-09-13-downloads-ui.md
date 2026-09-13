# 下载聚合端点与下载页 实施方案（二期 / 共二期）

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把统一队列暴露成一个按权限过滤的聚合端点，并在面板右上角给它一个常驻入口：弹层看「现在」，`/downloads` 页看历史与失败。

**Architecture:** 后端加 `/api/downloads` 四条路由，逐条按 `job.Kind` 过滤能力；前端把 `PluginQueuePage`（已经是两段式队列页）泛化成 `DownloadsPage`，新增 `DownloadTray` 弹层挂进 topbar，四个业务 hook 停掉各自的全量轮询改读统一源。

**Tech Stack:** Go 标准库 `net/http`；React 18 + TypeScript + Vite；样式全部在 `web/src/styles.css`，无框架无组件库。

**Spec:** `docs/superpowers/specs/2026-09-13-unified-downloads-design.md`

**前置：** 一期 `docs/superpowers/plans/2026-09-13-download-kernel.md` 必须已经合入——本期假定 `internal/download.Queue` 存在且五个包都已接入。

## Global Constraints

- **代码注释用英文**，文档用中文。（CLAUDE.md）
- **所有样式写在 `web/src/styles.css`**，不引框架、不引组件库、不用 CSS-in-JS、不拆文件。
- **只用令牌**，不写裸 hex；新增令牌 light / dark 两个块都要加。
- 栅格用 `repeat(auto-fill, minmax(<下限>, 1fr))`，**能不加断点就不加断点**。
- 任何可能装长文本的 flex/grid 子项写 `min-width: 0`（列方向 `min-height: 0`）。这是本仓库最高频的布局 bug。
- **1024px 断点在两处**：`styles.css` 的媒体查询与 `App.tsx:59` 的 `DRAWER_QUERY`。本期不动它，但若动必须两处同改。
- 动效时长按**动作性质**选：指针反馈 `--dur-1`、原地变状态 `--dur`、原地出现/消失（菜单气泡）`--dur-3`、横跨或覆盖屏幕 `--dur-4`。进场 `--ease`、退场 `--ease-in`。
- `npm --prefix web run build`（`tsc -b` + vite）是前端唯一的自动检查；样式必须人工在明暗两模式、多个宽度下确认。
- 用户可见的变化写进 `CHANGELOG.md`「未发布」，**不要**改成版本号。

---

## File Structure

**新建**

| 文件 | 职责 |
| --- | --- |
| `internal/api/handlers_downloads_queue.go` | 四条聚合路由 + 能力过滤 |
| `internal/api/handlers_downloads_queue_test.go` | 能力过滤测试（本期最关键的测试） |
| `web/src/useDownloads.ts` | 统一的下载源，替代四个 hook 各自的轮询 |
| `web/src/components/DownloadsPage.tsx` | `/downloads` 页（由 `PluginQueuePage.tsx` 泛化而来） |
| `web/src/components/DownloadTray.tsx` | topbar 弹层 |

**改造**

| 文件 | 改什么 |
| --- | --- |
| `internal/api/routes.go` | 加四条；删 `:181 /api/cores/cancel`、`:221 /api/plugins/cancel`、`:277 /api/java/install/cancel`、`:296 /api/databases/engines/install/cancel` |
| `web/src/routes.ts` | 加 `{kind:'downloads'}`；**加 `/library/plugins/queue` 显式重定向**；`LIBRARY_VIEWS.plugins` 去掉 `queue` |
| `web/src/types.ts` | 加 `DownloadJob` / `DownloadKind` / `DownloadState`；四个旧 Job 类型保留为该类型的别名或删除 |
| `web/src/api.ts` | 加 `downloads` / `cancelDownload` / `clearDownloads` / `downloadRoutes`；删四个旧 cancel |
| `web/src/components/TopBar.tsx` | `topbar__right` 最左插入 `<DownloadTray>` |
| `web/src/App.tsx` | 接 `useDownloads`；路由分支加 `downloads` |
| `web/src/components/Sidebar.tsx:499-530` | 徽标数据源改成统一队列 |
| `web/src/useCores.ts` / `usePlugins.ts` / `useJava.ts` / `useDatabases.ts` | 停掉 `ACTIVE_POLL_MS` 全量轮询，job 从 `useDownloads` 取 |
| `web/src/components/PluginQueuePage.tsx` | 删除（内容迁入 `DownloadsPage.tsx`） |
| `web/src/styles.css` | `.downloads__*` 与 `.tray__*` 两个区块 |

---

## Task 1: 聚合端点与能力过滤

**Files:**
- Create: `internal/api/handlers_downloads_queue.go`, `internal/api/handlers_downloads_queue_test.go`
- Modify: `internal/api/routes.go`

**Interfaces:**
- Consumes: 一期的 `download.Queue`、`download.Job`、`download.Kind`
- Produces: `GET /api/downloads`、`POST /api/downloads/{id}/cancel`、`DELETE /api/downloads`、`GET /api/downloads/routes`

- [ ] **Step 1: 写失败测试**

这是本期最关键的测试——把四个端点并成一个，最容易漏掉的就是权限：

```go
package api

import (
	"net/http"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/download"
)

// Merging four endpoints into one must not merge four capabilities into one.
// An account that may see plugin jars has no business learning which JDKs the
// panel is installing.
func TestDownloadsListIsFilteredPerKind(t *testing.T) {
	cases := []struct {
		name string
		caps []authz.Cap
		want []download.Kind
	}{
		{"only plugins", []authz.Cap{authz.CapLibraryPlugins}, []download.Kind{download.KindPlugin}},
		{"only java", []authz.Cap{authz.CapPanelJava}, []download.Kind{download.KindJava}},
		{"cores and databases",
			[]authz.Cap{authz.CapLibraryCores, authz.CapPanelDatabases},
			[]download.Kind{download.KindCore, download.KindDatabase}},
		{"nothing", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, queue := newDownloadTestServer(t)
			seedOneJobPerKind(t, queue)

			got := listDownloadsAs(t, srv, tc.caps)
			assertKinds(t, got, tc.want)
		})
	}
}

// The badge in the top bar counts what the list returns, so a job an account
// cannot see must not be counted either.
func TestDownloadsCountMatchesTheFilteredList(t *testing.T) {
	srv, queue := newDownloadTestServer(t)
	seedOneJobPerKind(t, queue)

	body := listDownloadsRaw(t, srv, []authz.Cap{authz.CapLibraryPlugins})
	if body.Active != len(body.Jobs) {
		t.Fatalf("active = %d but list holds %d", body.Active, len(body.Jobs))
	}
}

// Cancelling by id is the one place an account could reach a job it cannot
// see, because the id is all the request carries.
func TestCancellingAJobOfAnUnseenKindIsRefused(t *testing.T) {
	srv, queue := newDownloadTestServer(t)
	ids := seedOneJobPerKind(t, queue)

	code := cancelDownloadAs(t, srv, ids[download.KindJava], []authz.Cap{authz.CapLibraryPlugins})
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — a job you cannot see must not be distinguishable from one that does not exist", code)
	}
}
```

辅助函数 `newDownloadTestServer` / `seedOneJobPerKind` / `listDownloadsAs` / `listDownloadsRaw` / `cancelDownloadAs` / `assertKinds` 照 `internal/api/server_test.go` 现有的构造方式写（复用那里的 server 与角色搭建）。

注意 `TestCancellingAJobOfAnUnseenKindIsRefused` 断言 **404 而不是 403**：能不能看见这个 id 本身就是信息。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/api/ -run 'TestDownloads|TestCancellingAJob' -v
```
预期：`undefined: newDownloadTestServer` 等。

- [ ] **Step 3: 写 handler**

```go
// kindCaps says what an account needs to see each shelf's downloads.
//
// The panel-wide list is the one place four separate grants meet, so the
// filter is spelled out rather than derived: adding a Kind without adding its
// capability here would leak it to everyone, and a map that has to be edited
// is one a reviewer notices.
var kindCaps = map[download.Kind]authz.Cap{
	download.KindCore:     authz.CapLibraryCores,
	download.KindJava:     authz.CapPanelJava,
	download.KindDatabase: authz.CapPanelDatabases,
	download.KindPlugin:   authz.CapLibraryPlugins,
}

type downloadsResponse struct {
	Jobs []download.Job `json:"jobs"`
	// Active is what the top bar's badge shows. Counted here rather than in the
	// browser so it can never disagree with the filtered list.
	Active int `json:"active"`
}

func (s *Server) visibleJobs(r *http.Request) []download.Job {
	all := s.downloads.Jobs()
	out := make([]download.Job, 0, len(all))
	for _, job := range all {
		cap, known := kindCaps[job.Kind]
		if !known || !s.can(r, cap) {
			continue
		}
		out = append(out, job)
	}
	return out
}
```

`s.can(r, cap)` 用 `internal/api` 现有的能力查询方式（照 `visibleInstances` 的写法找）。

四条路由加进 `routes.go`。**每条的 `rt(...)` 不挂具体能力**——能力是逐条过滤的，路由层挂任何一个都会把其余三类挡在门外。用一个「登录即可」的门槛，过滤在 handler 里做，并在路由旁写注释说明为什么这里是个例外。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/api/ -race -v
```

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/api/ && go vet ./internal/api/
git add internal/api/
git commit -m "feat(api): 面板级下载列表，按 Kind 逐条过滤能力

把四个端点并成一个，不能把四份授权并成一份。路由层故意不挂能力——挂
任何一个都会把其余三类挡在门外——改为在 handler 里逐条过滤，并让计数
与过滤后的列表同源，这样 topbar 的徽标不可能显示一个看不见的任务。

按 id 取消是唯一能触到看不见的任务的入口，它回 404 而不是 403：这个 id
存不存在本身就是信息。"
```

---

## Task 2: 删除四条旧的取消路由

**Files:**
- Modify: `internal/api/routes.go:181,221,277,296`, 对应四个 handler, `web/src/api.ts`

- [ ] **Step 1: 确认前端是唯一消费者**

```bash
grep -rn 'cores/cancel\|plugins/cancel\|java/install/cancel\|engines/install/cancel' --include='*.ts' --include='*.tsx' --include='*.go' --include='*.md' . | grep -v '_test.go'
```
只应命中 `routes.go`、四个 handler、`web/src/api.ts`。若命中文档，一并更新。

- [ ] **Step 2: 删路由与 handler，前端 `api.ts` 改指 `/api/downloads/{id}/cancel`**
- [ ] **Step 3: `make lint && make test`**，顺手 `npm --prefix web run build`
- [ ] **Step 4: 提交**

---

## Task 3: 前端类型、API 与 `useDownloads`

**Files:**
- Create: `web/src/useDownloads.ts`
- Modify: `web/src/types.ts`, `web/src/api.ts`

**Interfaces:**
- Produces: `DownloadJob`、`DownloadKind`、`DownloadState`、`isDownloadActive(state)`、`useDownloads(enabled): DownloadController`

- [ ] **Step 1: 写类型**

```ts
export type DownloadKind = 'core' | 'java' | 'database' | 'plugin'

export type DownloadState =
  | 'queued'
  | 'downloading'
  | 'extracting'
  | 'done'
  | 'failed'
  | 'cancelled'

/** True while a job is still going to do something. */
export function isDownloadActive(state: DownloadState): boolean {
  return state === 'queued' || state === 'downloading' || state === 'extracting'
}

export interface DownloadJob {
  id: string
  kind: DownloadKind
  /** What the panel shows. Built by the daemon, which is the only side that
   *  knows a build number from a major version. */
  title: string
  subtitle?: string
  fileName: string
  /** Which route actually served the bytes — with 自动 on, not obvious. */
  route?: string
  total: number
  downloaded: number
  state: DownloadState
  error?: string
  /** What the finished download produced, by the id its own shelf knows it as.
   *  How the UI offers "go and look at it" without guessing. */
  ref?: string
  queuedAt: string
  startedAt?: string
  finishedAt?: string
}
```

- [ ] **Step 2: 写 `useDownloads.ts`**

关键是轮询节奏——这是替掉四个全量轮询的那一个：

```ts
/** While something is coming down. The bar has to move. */
const ACTIVE_POLL_MS = 800

/**
 * The panel's downloads, as one source.
 *
 * This replaces four hooks that each polled their whole list every 800ms
 * whether or not anything was happening — downloading one JDK meant re-fetching
 * the complete runtime inventory eight times a second to read one byte count.
 * Here there is one request, it carries only jobs, and it stops when the queue
 * is quiet.
 *
 * Polled at the app level rather than per page for the same reason the queue
 * lives in the daemon: a download belongs to the panel, not to whichever page
 * happened to start it.
 */
export function useDownloads(enabled: boolean): DownloadController {
  const [jobs, setJobs] = useState<DownloadJob[]>([])
  const active = jobs.filter((job) => isDownloadActive(job.state)).length

  const refresh = useCallback(async () => {
    if (!enabled) return
    try {
      const next = await api.downloads()
      setJobs(next.jobs)
    } catch {
      // A failed poll is not worth a toast: the next one is 800ms away, and the
      // bar freezing for one tick is less alarming than an error that appears
      // and vanishes on its own.
    }
  }, [enabled])

  // One fetch whenever the hook wakes, so the history is there before anything
  // is downloaded — the page has to be readable when the queue is empty.
  useEffect(() => {
    void refresh()
  }, [refresh])

  // The interval exists only while something is actually coming down. This is
  // the whole point of the rewrite: the four hooks this replaces polled their
  // full lists forever.
  useEffect(() => {
    if (!enabled || active === 0) return
    const timer = window.setInterval(() => void refresh(), ACTIVE_POLL_MS)
    return () => window.clearInterval(timer)
  }, [enabled, active, refresh])

  return { jobs, active, refresh, cancel, clearFinished }
}
```

空闲时**完全停掉** interval，但保留一次性拉取（进历史要看得到）。状态从空闲变活跃由「开始下载」的调用方主动 `refresh()` 触发——照 `usePlugins.ts:175-190` 现有的「先把 job 塞进去，轮询接手」写法。

- [ ] **Step 3: `npm --prefix web run build`**
- [ ] **Step 4: 提交**

---

## Task 4: `DownloadsPage`

**Files:**
- Create: `web/src/components/DownloadsPage.tsx`
- Delete: `web/src/components/PluginQueuePage.tsx`

- [ ] **Step 1: 泛化**

`PluginQueuePage.tsx` 已经是这个页面：`Page wide`、两段式、三种状态同一行型。逐条搬：

- **`Page wide` 不变。** 不要改成 `full`——`full` 只给「一屏一件事的工具页」，装的是画满空间的画布；这是一份要读的列表。
- 两段式与排序规则**原样保留**，连同它的注释：进行中最旧在前（那是它将要运行的顺序，一个会自己重排的队列没人读得懂），历史最新在前。
- `lead` 改写：不再只说插件，要说清四类下载共用这个队列、下载归守护进程管、关掉标签页也会下完。
- 每行加 Kind 标记。**用 `Icon` 现有的 `cores` / `java` / `database` / `plugins` 四个图标 + 文字**，不要只用颜色——`design-system.md` 的「只有异常才上色」意味着颜色不能独自承担分类含义。
- 加按 Kind 筛选。筛选是一组 chip，选中项进查询串（`?kind=java`），照 `routes.ts` 里「Filters that scope a list live in the query string」的既有约定。
- 完成的任务若有 `ref`，给一个跳回去的链接。

- [ ] **Step 2: 样式**

在 `styles.css` 新开 `.downloads__*` 区块。行是 grid，`grid-template-columns: auto minmax(0, 1fr) auto`；标题与文件名所在的列必须 `min-width: 0`。进度条复用现有的 meter 视觉语言但**不复用 `Meter` 组件**——`Meter` 的配色是「严重度」，下载进度不是严重度。

- [ ] **Step 3: `npm --prefix web run build`**
- [ ] **Step 4: 提交**

---

## Task 5: 路由与那个必须显式处理的坑

**Files:**
- Modify: `web/src/routes.ts`, `web/src/App.tsx`

- [ ] **Step 1: 加路由**

`Route` union 加 `| { kind: 'downloads'; only?: DownloadKind }`。字段叫 `only` 而不是 `kind`——
`Route` 的每个成员都已经有一个 `kind` 作为判别式，第二个同名字段会让类型收窄读不通。

`pathOf` 产出 `/downloads`，带筛选时产出 `/downloads?kind=java`；`readRoute` 从查询串读回 `only`。
筛选走查询串而不是路径段，照 `routes.ts` 顶部既有的约定：「Filters that scope a list live in the
query string」。

- [ ] **Step 2: 处理老书签**

`LIBRARY_VIEWS.plugins` 去掉 `{ id: 'queue', label: '下载队列' }`。**同时必须**在 `readRoute` 里、`if (section === 'plugins' && second)` 那条**之前**加：

```ts
// 下载队列 left 插件库 for the top level: it was never only about plugins,
// and a panel-wide queue does not belong inside one shelf. Old bookmarks would
// otherwise fall through to the plugin-id branch below and go looking for a
// plugin called "queue".
if (section === 'plugins' && second === 'queue') {
  return { kind: 'downloads' }
}
```

写在 `second === 'source'` 那条旁边，两者是同一类处理。`'queue'` 这个 id 保留在 `LibraryView` union 里当历史，与 `'download' / 'install' / 'engines'` 一致。

- [ ] **Step 3: 写一个守着这个坑的断言**

前端没有单测框架，所以这条靠 `App.tsx` 里的路由分支 + 人工验证。**人工验证项**：浏览器访问 `/library/plugins/queue`，应落到 `/downloads`，**不是**落到一个「找不到插件 queue」的抽屉。

- [ ] **Step 4: `parentOf` 与 `navKeyOf`**

`downloads` 的 `parentOf` 回 `{kind:'overview'}`；`navKeyOf` 回 `null`（它不是一个 scope，不参与侧栏替换动画）。

- [ ] **Step 5: `npm --prefix web run build`，人工验证老链接**
- [ ] **Step 6: 提交**

---

## Task 6: `DownloadTray` 与 topbar 入口

**Files:**
- Create: `web/src/components/DownloadTray.tsx`
- Modify: `web/src/components/TopBar.tsx:167-180`, `web/src/styles.css`

- [ ] **Step 1: 写组件**

复用 `useAnchor`（`placeVertically`、`GAP`、`EDGE`）+ `useDismiss` + `createPortal`。**不要套 `Menu`**——`Menu` 收的是 `MenuItem[]`，这里是富内容。照 `Menu.tsx` 的结构抄一份壳。

```tsx
/**
 * What the panel is downloading, from wherever you are.
 *
 * In the top bar rather than the sidebar because a download belongs to no
 * scope, and the sidebar is replaced wholesale between them — see Scope in
 * routes.ts. A tray as well as a page because the two answer different
 * questions: the 新建实例向导 is a page rather than a dialog precisely because
 * two of its steps start a download (see routes.ts), and "how much longer" is
 * exactly what you want to know while you are still in it. Making that a trip
 * to another page would mean leaving the wizard half-finished.
 *
 * The button is always here, idle or not. One that appeared only during a
 * download would shift the whole top bar every time one started or ended, and
 * would leave no way into the history.
 */
```

- **位置**：`topbar__right` 的最左，在 `topbar__search` 之前。
- **徽标**：`<Badge tone="update">{active}</Badge>`，`active === 0` 时不渲染徽标（按钮仍在）。
- **图标**：`<Icon name="queue" />`，已存在。
- **`aria-label`**：有活动时「下载（N 个进行中）」，空闲时「下载」。
- **动效**：`--dur-3` + `--ease` 进场、`--ease-in` 退场（原地出现/消失）。
- **焦点**：打开时进入弹层，关闭时归还触发按钮；离屏时 `visibility: hidden` 而不是只 `transform` 挪走，否则 Tab 还能走进去。
- **内容**：进行中的任务（最多 5 条）+ 每条一个取消；全空时一句「当前没有下载」；底部「查看全部 →」通向 `/downloads`。
- **历史不进弹层**——失败原因和重试要一个能读的地方，弹层装不下，那是页面的职责。

- [ ] **Step 2: 样式**

`.tray` 区块。宽度 `min(360px, calc(100vw - 32px))`；每行 `min-width: 0`。表面用 `--surface-2` 或更高（它压在页面之上）。

- [ ] **Step 3: 窄屏实测**

390px 下 topbar 右侧现在是「搜索 + 主题 + 账号（含用户名）」，加第四个按钮是否挤 **必须实测**。若挤，优先隐藏 `usermenu__name`（窄屏下用户名是次要信息，头像仍在），**不要**新增断点。

- [ ] **Step 4: `npm --prefix web run build`**
- [ ] **Step 5: 提交**

---

## Task 7: 四个 hook 改读统一源

**Files:**
- Modify: `web/src/useCores.ts`, `web/src/usePlugins.ts`, `web/src/useJava.ts`, `web/src/useDatabases.ts`, `web/src/components/Sidebar.tsx`, `web/src/App.tsx`

- [ ] **Step 1: 逐个摘掉 `ACTIVE_POLL_MS` 全量轮询**

四个 hook 里的 `window.setInterval(() => void refresh(), ACTIVE_POLL_MS)` 删掉。各页面上的进度条改从 `useDownloads` 按 Kind 取。

**但保留「下载完成后刷新一次列表」**——`useJava.ts:100` 和 `useDatabases.ts:101` 那两个 effect 正是干这个的（job 变成 `done` 时重拉一次）。它们现在改成监听统一源里对应 Kind 的任务完成。这条不能丢：完成之后那份新的运行时清单还得拉一次。

`useDatabases.ts:17` 的 `TRANSITION_POLL_MS = 1500` 是数据库服务启停的轮询，**与下载无关，保留**。

- [ ] **Step 2: 侧栏徽标改源**

`Sidebar.tsx:499-530` 的 `java.installing` / `cores.downloading` / `plugins.downloading` / `plugins.active` 改为读 `useDownloads` 的按 Kind 计数。**徽标本身保留**——它回答的是「这一格有事」，跟 topbar 的「面板一共在下什么」是两个问题。

- [ ] **Step 3: 验证轮询真的少了**

打开面板，开始一个 Java 安装，在浏览器 Network 面板确认：**只有 `/api/downloads` 在 800ms 轮询**，`/api/java`、`/api/cores`、`/api/plugins`、`/api/databases` 都不再持续请求。

- [ ] **Step 4: `npm --prefix web run build`**
- [ ] **Step 5: 提交**

---

## Task 8: 收口与全量验证

- [ ] **Step 1: CHANGELOG**

「未发布」追加：

```markdown
- 新增「下载」页与右上角入口：面板正在下载的东西（核心、Java、数据库、插件）现在集中在一处，失败原因与历史不再被下一个下载覆盖
- 插件库的「下载队列」页移到面板级的 `/downloads`，旧链接会自动跳转
```

- [ ] **Step 2: 后端全量**

```bash
make lint && make test && make build
```

- [ ] **Step 3: 前端构建**

```bash
npm --prefix web run build
```

- [ ] **Step 4: 人工验收清单**（前端没有单测，这一步不能省）

- [ ] 明暗两种模式都看过，新令牌两个块都加了
- [ ] 1440 / 1200 / 1024 / 768 / **390** 宽度下无横向溢出、无错位
- [ ] **390px 下 topbar 四个按钮不挤**
- [ ] 折叠侧栏 / 打开抽屉 / 开着控制台的实例页 —— 三处未被波及
- [ ] `/library/plugins/queue` 跳到 `/downloads`，**不是**去找一个叫 queue 的插件
- [ ] 弹层：Tab 能进、Esc 能关、关闭后焦点回到按钮
- [ ] 空闲时按钮仍在且无徽标；开始下载后出现计数
- [ ] 一个只有插件权限的账号：看不到 Java 任务，徽标计数也不含它
- [ ] 长内容（很长的文件名、很长的错误信息）不撑破行

- [ ] **Step 5: 提交并推送**

```bash
git push -u origin claude/brave-cray-nxdlue
```

- [ ] **Step 6: 合并回 main**

按 CLAUDE.md 的顺序：推功能分支 → 切 `main` → `git pull origin main` → 合并 → 推 `main` → 切回功能分支。若 `main` 已前进，先把 `main` 合进功能分支解决冲突，别把冲突带上 `main`。
