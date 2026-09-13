# 统一下载内核、统一线路表 与 下载页 设计文档

面板里「下载」这件事目前有五套互不相干的实现。这份文档把它们收成一个内核、一张线路表、一个入口。

## 背景

### 现状一：五份几乎一样的 Job

| 位置 | 并发模型 | 行数 |
| --- | --- | --- |
| `internal/serverjar/downloader.go:49` 服务端核心 | 单槽，第二个请求 `ErrBusy` → 409 | 395 |
| `internal/plugin/downloader.go:76` 插件 | 真队列：有 `ID`、`QueuedAt`、按 id 取消、保留历史 | 871 |
| `internal/javaruntime/installer.go:45` Java | 单槽 | 533 |
| `internal/dbruntime/installer.go:34` 数据库 | 单槽 | 440 |
| `internal/selfupdate/service.go:93` 面板更新 | `Phase` + `Progress int`（百分比，无字节数） | — |

前四个各有一个 `type JobState string`、各有一个 `progressWriter`、各有一个 `Status()`，字段是同一组
`Total / Downloaded / State / Error / StartedAt / FinishedAt`。

`internal/schemlib` 没有 job：建筑几百 KB，同步下完（见 `web/src/useSchematics.ts:24` 的注释）。这份文档不动它。

### 现状二：进度寄生在业务列表响应里

没有独立的进度端点。进度是挂在各自列表响应上的字段：

- `GET /api/cores` → `{root, cores, job}`
- `GET /api/java` → `{…, job}`
- `GET /api/databases` → `{…, job}`
- `GET /api/plugins` → `{…, jobs, job}`

取消是四条互不相干的路径（`internal/api/routes.go:181,221,277,296`）。

前端对应四个 hook（`useCores` / `usePlugins` / `useJava` / `useDatabases`），各自 `setInterval(800ms)`
拉**整份列表**，而且四个都在 `App.tsx:253-256` 顶层常驻——不管打开哪个页面，四个轮询都在跑。下一个
Java 运行时，面板每 800ms 重新拉一遍完整运行时清单，只为读一个 `downloaded` 数字。

### 现状三：「源」这个词指着三种不同的东西

**① 代理型**（拼 URL 前缀，只对 `github.com` 有效）。ghfast 这一条线路被独立实现了三遍：

- `internal/plugin/mirrors.go` — 最完整：`{ID,Name,Note,Prefix}` 列表、`auto` 逐个 fallback、支持自定义前缀、记录实际命中的那个
- `internal/javaruntime/source.go:69` — `{ID:"ghproxy", link: proxyLink("https://ghfast.top/")}`
- `internal/config/config.go:30` — `DefaultUpdateMirror`，退化成一个裸字符串：无列表、无 auto、UI 只能手填

**② 副本型**（不同 host + 各自的路径模板）。`javaruntime` 的 tuna / nju / huawei，靠 SHA-256 才敢信
（见 `internal/javaruntime/source.go` 开头的注释）。

**③ 目录源**（货架，不是线路）。`schemlib.Source`（用户自己加的建筑索引仓库）、`plugin.Source` + `TokenID`
（一个被追踪的插件来自哪个仓库）。**跟「从哪条线路取字节」无关，不并入本设计。**

### 现状四：两处没有镜像

- `internal/serverjar/client.go:27` 写死 `https://fill.papermc.io/v3`，核心 jar 一个加速选项都没有。Paper jar 50MB+。
- `dbruntime` 同样没有，URL 由上游元数据给出，直下。

### 结论

不是「每个页面各有一个队列」，而是**五套各自为政的实现，其中只有一套是真队列**。代价是：并发能力不一致
（插件能排队，核心第二个报 409）、没有任何地方能回答「面板此刻一共在下什么」、每新增一种可下载的东西
就要再抄一份 Job + progressWriter + 轮询。

## 不做什么

- **不动 `schemlib`**。建筑是同步下载，没有 job 可统一。它的「索引源」是货架不是线路。
- **不把 `selfupdate` 塞进共享队列**。面板自更新要停掉所有实例再替换自己，它跟「并行下载三个东西」在语义上冲突。
  它只接线路表，`Phase` 保留。
- **不统一「哪类下载走哪条线路」的选择**。见下面「一条被否决的设计」。
- **不做 WebSocket 推送**（本期）。见「推送 vs 轮询」。
- **不给数据库补镜像**（本期）。MySQL / PostgreSQL / MongoDB 的下载 URL 由各自上游的元数据给出，三个引擎三个
  host，与 GitHub 和 PaperMC 都不重叠，没有一条已验证的镜像可接。线路表的结构容得下它——将来验到了就加一张
  `RouteSet`，不需要改内核。
- **不做续传**。现有队列不持久化，`internal/plugin/downloader.go:77` 的注释写明：中断的下载是重来而不是续传。
  这个不变量保留。

## 一条被否决的设计

**「面板设置里放一个全局『本机网络线路』，所有下载继承。」**

否决依据是 `internal/config/config.go:86` 的注释，它仍然成立：

> Separate from UpdateMirror on purpose, even though both proxy the same GitHub CDN: the panel updates a few
> times a year and plugins download weekly, so "the proxy that works for my plugins" is a choice worth making
> on its own rather than inheriting from a page about panel updates.

**线路目录统一，选择不统一。** `config.JavaSource` / `PluginMirror` / `UpdateMirror` 原样保留，新增 `CoreSource`。

还有一条技术理由：一条线路能不能服务某个下载，取决于下载 URL 的 host。ghfast 包不住 `cdn.azul.com`，
tuna 的 Adoptium 树里没有 Paper。这天然是一张**线路 × 上游**的适配表，不是一个扁平下拉框——`javaruntime`
已经在用 `temurinSources` / `zuluSources` 两份列表硬扛这件事。

## 一条被验证过的新线路

`serverjar` 要接的镜像，验证记录在此，免得后来人重查：

- 清华 `mirrors.tuna.tsinghua.edu.cn/papermc/`、南大 `mirror.nju.edu.cn/papermc/` → 404，没有。
- 华为云 `mirrors.huaweicloud.com/papermc/` → **200 是误报**。它的镜像门户是 SPA，对任何路径都回 200
  （拿一个瞎编的路径对照验过，同样 200）。实际没有。
- **FastMirror `download.fastmirror.net`** → 有，Paper 和 Velocity 都覆盖。

决定性的一步是字节比对。官方元数据给出 Paper 1.21.4 build 232 的
`sha256 = 5ee4f542f628a14c644410b08c94ea42e772ef4d29fe92973636b6813d4eaffc`，`size = 51437498`；
从 `https://download.fastmirror.net/download/Paper/1.21.4/build232` 取回的文件两项**完全一致**。

所以它是标准的副本型线路：元数据（几 KB）永远走官方 `fill.papermc.io`，字节走镜像，**用官方的 sha256 校验**
——正是 `internal/javaruntime/source.go` 开头描述的安全模型。它自己发布的是 sha1，不使用。

它的路径模板 `/download/{Project}/{mcVersion}/build{N}` 与官方那个内容寻址的
`fill-data.papermc.io/v1/objects/<sha256>/<name>.jar` 完全不同，因此需要自己的 link 函数——形状与
`javaruntime` 的 `mirrorLink(base)` 一致。

## 架构

### 组件边界

```
internal/download      ← 队列内核 + 线路表。不认识插件、Java、核心、数据库。
  ├─ plugin            ← 第一个调用方：resolve/asset 挑选/入库 留在本包
  ├─ serverjar         ← 入库留在本包
  ├─ javaruntime       ← 解包 + 注册留在本包
  ├─ dbruntime         ← 解包 + 注册留在本包
  └─ selfupdate        ← 只用线路表，不用队列
```

**内核不是新写的，是从 `internal/plugin/downloader.go` 提取的。** 那 871 行干净地一分为二：

- **通用（约 500 行，整体搬出）**：`JobState`+`Active()`、`Job` 快照、`Downloader` 的 worker 池、
  `duplicate()` 去重、`prune()` 历史裁剪、`dispatch()`、`work()`、`transfer()`（带进度 + 校验的字节搬运）、
  `verifyDigest()`、`finish()`、`Jobs/Status/Cancel/CancelAll/ClearFinished/Close`、`progressWriter`
- **插件专属（留在 `plugin`）**：`syncVisibility()`、`Check()/CheckAll()`（版本检查，与下载无关）、
  `resolve()/pickNamed()/firstOf()`、`run()` 的编排、`Client()/Library()`

选它当内核的理由：它是五个里唯一把事情做对的——有 id、有并发上限、有去重、有历史裁剪、有 fallback 链、
有校验、取消能按 id。其余四个是它的退化版本。新写一个内核等于推翻唯一一份经过实战的实现，还要重写它的测试。

**搬运时连注释一起搬，不做「清理」。** `transfer()` 的 fallback 链、`prune()` 的保留策略，注释记的是坑。

### 数据流

```
调用方 ──Submit(Request)──▶ Queue ──排队──▶ worker
                                            │
                                            ├─ 选线路（RouteSet + 操作员的选择 + Serves 过滤）
                                            ├─ 取字节（逐条线路 fallback，最后落到 official/direct）
                                            ├─ 校验 SHA-256（上游元数据给的那个）
                                            └─ 调 Request.Install(ctx, temp, pub) ──▶ 调用方的领域逻辑
                                                                                        （解包/入库/注册）
```

## 第一部分：`internal/download` 内核

### 数据模型

```go
type Job struct {
    ID       string
    Kind     Kind   // core | java | database | plugin
    Title    string // 「Paper 1.21.4 #232」「Temurin 21」
    Subtitle string // 「经 FastMirror」
    FileName string
    Total, Downloaded int64
    State    State  // queued|downloading|verifying|extracting|done|failed|cancelled
    Route    string // 实际命中的线路 id
    Error    string
    Ref      string // 完成后指回 coreId / runtimeId / installId / pluginId
    QueuedAt time.Time
    StartedAt, FinishedAt *time.Time
}
```

`State` 是五套的并集：`extracting` 目前只有 Java / 数据库用，`queued` 目前只有插件用，统一后都有。

`Route` 是新字段：`Job.Mirror` 只有插件有，而「我选了直连，它怎么走了 ghfast」对四类下载都是会问的问题。

`Ref` 是新字段：让 UI 能从一条完成的任务跳回它产出的东西。

### 提交接口

```go
type Request struct {
    Kind             Kind
    Title, Subtitle  string
    Upstream         string // 上游 URL
    SHA256           string // 空表示上游不发布校验和（GitHub release assets 就没有）
    Size             int64
    RouteSet         string // 哪张线路表
    RoutePref        string // 操作员的选择，"" = auto
    DedupeKey        string // 同 key 重复提交返回既有 job，而不是排第二份
    Install          func(ctx context.Context, temp string, pub *Progress) (ref string, err error)
}

func (q *Queue) Submit(Request) (Job, error)
func (q *Queue) Jobs() []Job
func (q *Queue) Cancel(id string) error
func (q *Queue) CancelAll(kinds ...Kind) int
func (q *Queue) ClearFinished() int
func (q *Queue) Close()
```

**`Install` 回调是四个包留住自己领域逻辑的钩子。** javaruntime 的解包、serverjar 的入库、plugin 写 library
都在这里跑；`pub` 让解包阶段继续汇报进度（这就是 `extracting` 状态的来源）。内核只负责排队、去重、取字节、
校验、进度、取消、历史。

### 并发额度按 Kind 分，不是一个全局池

`internal/plugin/downloader.go:37` 的注释写明，插件限 3 个 worker 的理由是 GitHub 的 60 次/小时——那是**上游的**
限制，不是面板的。合成一个全局池会让「下三个插件」把「装一个 Java」堵在后面，而这两件事的上游毫无关系。

初始额度：plugin 3（沿用现值），core / java / database 各 1（等于保持现有行为，只是排队而不再 409）。

### 行为变化：单槽 → 可排队

核心 / Java / 数据库从单槽变成能排队，`ErrBusy` → 409 那条路径消失。这是用户可见的行为变化，进
`CHANGELOG.md` 的「未发布」。判断是改进：同时装两个 Java 大版本是合理需求，而现在第二个直接被拒。

## 第二部分：`download.Route` 线路表

```go
type RouteKind int // Proxy（拼前缀）| Copy（另一份拷贝）| Direct

type Route struct {
    ID, Name, Note string
    Kind   RouteKind
    Serves []string             // 能服务哪些上游 host
    Link   func(Upstream) string // 服务不了这一个就返回 ""
}
```

四张表：

| RouteSet | 线路 | 备注 |
| --- | --- | --- |
| `github` | ghfast / gh-proxy / moeyy / direct | **ghfast 三份合一**：plugin + selfupdate + javaruntime 的 ghproxy 共用 |
| `adoptium` | tuna / nju / huawei / ghfast / official | 现有 `temurinSources` 平移 |
| `azul` | official + 自定义前缀 | 现有 `zuluSources` 平移 |
| `papermc` | **fastmirror（新）** / official | 见上面的验证记录 |

**每张表都以 official / direct 结尾。** 这是现有两个包已有的不变量，`mirrors.go` 和 `source.go` 的注释都写了
理由：代理挂了不该把「能装」变成「装不上」。

`ResolveRoute` 沿用 `plugin.ResolveMirror` 的行为：接受已知 id、接受自定义 `https://…/` 前缀、其余**拒绝**
而不是悄悄回落到默认——悄悄从别处下载正是这个功能要消除的意外。

### 配置

`config.JavaSource` / `JavaDistribution` / `PluginMirror` / `UpdateMirror` **原样保留**，新增 `CoreSource`
（语义与 `JavaSource` 一致：从上次下载记住，而不是设置页上的一项）。

## 第三部分：HTTP 与权限

```
GET    /api/downloads              聚合列表（按调用者能力过滤）
POST   /api/downloads/{id}/cancel
DELETE /api/downloads              清历史
GET    /api/downloads/routes       线路表 + 当前选择
```

**权限过滤是这一部分的重点，必须有测试。** `GET /api/downloads` 按 `job.Kind` 逐条过滤：

| Kind | Capability |
| --- | --- |
| `core` | `CapLibraryCores` |
| `java` | `CapPanelJava` |
| `database` | `CapPanelDatabases` |
| `plugin` | `CapLibraryPlugins` |

一个只有插件权限的账号，不能因为新增了一个聚合端点就看见 Java 任务。这是把四个端点并成一个最容易漏掉的事。
过滤要贯穿到计数：看不到的 Kind 不能算进 topbar 徽标的数字里。

旧的四条 cancel 路径（`routes.go:181,221,277,296`）直接删，前端同步改——前端是唯一消费者。

各列表响应里的 `job` / `jobs` 字段**保留**，值改从统一队列取：核心库页面上那条进度条不能消失。但四个 hook
不再各自轮询。

### 推送 vs 轮询

本期**不做 WebSocket**。仓库现在只有按实例的控制台 socket（`internal/api/ws.go`），没有通用的 pub/sub hub；
新建一个还要带上面那套能力过滤，那是它自己的一块工作。

本期就一个 `GET /api/downloads`，有活动时 800ms、空闲时停。账面上仍是净赚：**四个全量列表轮询 → 一个瘦轮询**。
WS 留作后续。

## 第四部分：前端

### 页面 `/downloads`

`web/src/components/PluginQueuePage.tsx` 泛化为 `DownloadsPage.tsx`。

它已经是那个下载页了——`Page wide`、两段式（进行中 / 历史）、三种状态同一行型。它的注释里那段理由
（单槽设计会在下一个任务开始时弄丢失败记录）对四类下载一字不差地成立。所以前端与后端是同一个动作：
**把已经做对的那一份往上提。**

- `Page wide`（`--content-max-wide` 1440px）。**不用 `full`**：`full` 只给「一屏一件事的工具页」，装的是画布；
  下载页是要读的列表。
- 两段式和排序规则原样保留：进行中最旧在前（那是它将要运行的顺序），历史最新在前。
- 新增 Kind 列与按 Kind 筛选。
- 新增路由 `{ kind: 'downloads' }` → `/downloads`。

**一个必须显式处理的坑。** `/library/plugins/queue` 不能只是从 `LIBRARY_VIEWS` 里删掉。`readRoute` 匹配不到
view 会往下落到

```js
if (section === 'plugins' && second) → pluginId = 'queue'
```

老书签会变成「查找 id 为 queue 的插件」。必须在那之前加一条显式重定向，写法与已有的
`second === 'source'` 那条一致。`'queue'` 这个 id 保留在 `LibraryView` union 里当历史，与 routes.ts 里
`'download' / 'install' / 'engines'` 的处理一致。

### 弹层 `DownloadTray.tsx`

- 位置：`topbar__right` 的**最左**，在 `topbar__search` 之前。从左到右读成 状态 → 动作 → 身份。
- 复用 `useAnchor` + `useDismiss` + `createPortal`，**不套 `Menu`**（Menu 是 item 列表，这里是富内容），
  也不新造浮层机制。
- 动效：原地出现 / 消失 → `--dur-3`(220ms)，进场 `--ease`，退场 `--ease-in`。
- 焦点打开时进入、关闭归还触发按钮；图标按钮带 `aria-label`；装饰元素 `aria-hidden`。
- **空闲时按钮常驻**，不是只在下载时出现——否则 topbar 会在下载开始 / 结束时抖宽度，而且没有下载时就没路进历史。
  有活动时挂 `Badge tone="update"` 计数，沿用侧栏现有用法。
- 底部「查看全部 →」通向 `/downloads`。

为什么既要弹层又要页面：`web/src/routes.ts` 里写明新建实例向导之所以是页面而不是弹窗，正因为**其中两步会
启动下载**。若看下载必须跳页，用户就得在向导进行到一半时离开再回来——而「还有多久」恰恰是他在向导里最想
知道的事。弹层解决这个，页面解决不了；反过来，失败原因、历史、重试需要一个能读的地方，弹层装不下。

### 侧栏徽标保留

`Sidebar.tsx:499-530` 现有的 `cores.downloading` / `plugins.active` 徽标保留。它们回答的是别的问题——
「服务端核心这一格有事」不等于「面板一共在下什么」——只是数据源改成统一队列。

### 样式

- 只用 `styles.css` 开头的令牌，不写裸 hex；新增令牌 light / dark 两个块都加。
- 栅格用 `repeat(auto-fill, minmax(<下限>, 1fr))`，不为排布新增断点。
- 任务行里的标题、文件名、错误信息都是可能很长的文本，所在的 flex/grid 子项要有 `min-width: 0`。

## 错误处理

- **失败原因进历史，永不被下一个任务覆盖。** 这是单槽设计丢掉的那一条记录（`PluginQueuePage.tsx` 的注释点了名），
  现在四类都拿到。
- `Job.Route` 记实际命中的线路，线路 fallback 不留悬案。
- 上游不发布校验和时（GitHub release assets），`SHA256` 为空，**不做内容校验**——`plugin.verifyDigest` 现有行为
  就是 `want == ""` 直接放行，本设计不收紧也不放松。长度只在校验和**不符**时用来区分「连接断了」和「这个源
  发错了 jar」，不是独立的一道校验。这也是 `mirrors.go` 开头那段注释的前提：代理型线路拿不到校验和，所以选代理
  等于扩大信任范围。
- 队列不持久化，守护进程重启后队列空。

## 测试

后端走 TDD（先测后写）。每一步结束都能独立跑 `make lint && make test`。

1. 提取 `internal/download`，`plugin/downloader_test.go` 的通用部分搬成内核测试 → plugin 接入
2. `download.Route` + 四张表 + ghfast 三合一 → 测解析 / fallback / `Serves` 过滤 / 拒绝未知 id
3. serverjar 接入 + FastMirror 线路 → 测「用官方 sha256 校验镜像字节」、测校验失败时回落官方
4. javaruntime 接入（含 `extracting` 阶段的进度汇报）
5. dbruntime 接入
6. selfupdate 只接线路表
7. API 聚合端点 + **权限过滤测试**（四种 Kind × 有无对应 capability）
8. 前端：`DownloadsPage` + `DownloadTray` + 路由 + 四个 hook 改读统一源

前端验证（`tsc -b` 是唯一的自动检查，其余靠人工）：

- `npm --prefix web run build` 通过
- 明暗两种模式都看过
- 1440 / 1200 / 1024 / 768 / 390 宽度下无横向溢出、无错位
- 折叠侧栏、打开抽屉、开着控制台的实例页——三处未被波及
- **390px 下 topbar 右侧加到第四个按钮是否挤**，实测，不预判

## CHANGELOG

「未发布」记两条用户可见的变化：

- 服务端核心 / Java / 数据库的下载改为排队，不再「上一个没下完就拒绝下一个」
- 服务端核心新增下载镜像（FastMirror），可在下载时选择
