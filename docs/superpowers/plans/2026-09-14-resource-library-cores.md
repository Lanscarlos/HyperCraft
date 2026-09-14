# 资源库重构 · 服务端核心（参照实现）Implementation Plan

> **For agentic workers:** 用 executing-plans 逐条实现（本仓库默认 Inline Execution，见 CLAUDE.md）。

**Goal:** 把「服务端核心」从「列表 + 常驻下载向导」改成「列表占满整页 + 添加核心弹层」，并把其中可复用的部分做成组件，供另外四个资源页后续套用。

**Architecture:** 列表页用既有的 `Page(wide)` + `Toolbar` + `DataTable`；行的形状、使用者单元格、`⋯` 菜单、相对时间、删除保护、清理未使用抽成 `components/ResourceList.tsx`，五个页面共用。「添加核心」是一个 1040px 的 `Modal`，类型／版本／构建三段同屏。下载沿用已有的全局下载队列（`useDownloads` + `DownloadTray`），不新建进度条。

**Tech Stack:** Go 1.2x（`internal/serverjar`、`internal/api`）、React 18 + TypeScript + Vite、单一 `web/src/styles.css`。

**Spec:** 会话中的《HyperCraft 资源库重构 · 通用模板》（下称「模板」）。

## Global Constraints

- 样式只进 `web/src/styles.css`，只用令牌区已有的令牌；新增令牌 light / dark 两块都要加。
- 类名必须在 `styles.css` 里真实存在（`npm --prefix web run check:ui` 的 `ruleNoUndefinedClasses` 卡这条）。
- 一个文件最多一个 `variant="primary"`，超了要在 `check-ui.mjs` 的 `PRIMARY_ALLOWED` 里登记理由。
- `.alert` 不带修饰符；条件说明用 `<Note tone>`，操作结果用 `toast()`。
- 段头用 `<Section>`，空状态用 `<EmptyState>`，角标用 `<Badge>`，下拉用 `<Select>`。
- 注释用英文，文档／CHANGELOG 用中文。
- 栅格优先 `repeat(auto-fill, minmax(…, 1fr))`；可能装长文本的 flex/grid 子项写 `min-width: 0`。

## 先确认的后端事实（模板 §9 要求「不要猜」）

已核对 `internal/serverjar` 与 `internal/api/routes.go`：

1. **上游只有 Paper 和 Velocity**（`serverjar.Projects`），都走 PaperMC Fill API。模板 §4.2 列的 Purpur / Folia / BungeeCord / Fabric / Forge 在后端没有任何客户端，本次不伪造卡片——类型网格渲染 `listCoreProjects()` 返回的内容，外加第「上传自定义 jar」张。
2. **构建列表没有接口**，只有 `GET …/versions/{v}/build`（最新一个）。§4.3 的右栏需要新增 `GET …/versions/{v}/builds`。
3. **兼容性元数据**：`Version.JavaMinimum` 上游给；「支持的 MC 版本」服务端就是版本号本身，代理端上游不给——用 `internal/serverjar` 里一张静态表，查不到就留空，前端显示「未知」+ `信息不全`。已下载的核心当前**没有**存 Java 要求，要在下载落库时记进 `index.json`。
4. **`usedBy` 反查**：`handleCoreLibrary` 已经按文件名反查实例，够用。
5. **本机 Java 列表**：`useJava` 已有，`overview.runtimes[].major` 可直接比。
6. **下载队列**：`useDownloads` + `DownloadTray` 已是全局的，直接用。

## 与模板不一致、按现状调整的几处

- **顶栏 chip**：模板 §2.2 要把「7 个 · 275 MB」「存放路径」放进顶栏面包屑后面。本仓库的页面语法把这些放在 `PageHead` 的 `facts`（`.meta-chips`），全panel一致；另起一处会变成第二套。**按 `facts` 实现**，模板真正要删的那段常驻说明文字（`lead`）照删。
- **删除保护的文案**：本仓库的核心是「实例各持一份副本」，删库里的 jar **不会**让实例起不来（见 `handleApplyCore` 与现有确认文案）。所以照 §6 挡住删除，但文案说实话：说的是「删了就没法再复制出一模一样的一份」，不写「该实例无法启动」。
- **行菜单**：`重命名` 和 `查看详情`（§7 明说二期）后端没有对应能力，本次不做。`下载到本地` 新增一个流式端点。菜单留 `复制文件路径 · 复制 SHA-256 · 下载到本地 ·（分隔）· 删除`。

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `internal/serverjar/client.go` | 新增 `Builds()`；`Version` 加 `Minecraft`；代理端 MC 区间静态表 |
| `internal/serverjar/library.go` | `Core` 加 `JavaMinimum` / `Minecraft`；新增 `Import()` 落盘上传的 jar |
| `internal/serverjar/downloader.go` | 下载落库时带上两项元数据 |
| `internal/api/handlers_downloads.go` | `handleListCoreBuilds`、`handleUploadCore`、`handleFetchCore` |
| `internal/api/routes.go` | 三条新路由 |
| `web/src/types.ts` | `CoreBuild.changelog`、`CoreVersion.minecraft`、`ServerCore.javaMinimum/minecraft` |
| `web/src/api.ts` | `listCoreBuilds`、`uploadCore`、`coreFileURL` |
| `web/src/components/ResourceList.tsx` | **五页共用**：行、使用者单元格、`⋯` 菜单、相对时间、删除保护、清理未使用、存储卫生卡片 |
| `web/src/components/AddCoreDialog.tsx` | 添加核心弹层（类型／版本／构建／兼容性条／确认条／上传） |
| `web/src/components/CoreLibraryPage.tsx` | 重写：筛选条 + 表格 + 两张卡片 |
| `web/src/components/Sidebar.tsx` | 命名统一、顺序、条目数角标、底部占用卡片 |
| `web/src/routes.ts` | `LIBRARY_SECTIONS` 改名改序；library 路由加 `want` |
| `web/src/styles.css` | 新区块 `resource list` / `add-core dialog` |
| `CHANGELOG.md` | 未发布小节 |

---

### Task 1: 后端 · 构建列表与兼容性元数据

**Files:** `internal/serverjar/client.go`、`internal/serverjar/library.go`、`internal/serverjar/downloader.go`、`internal/api/handlers_downloads.go`、`internal/api/routes.go`、`internal/serverjar/client_test.go`

**Produces:**
- `func (c *Client) Builds(ctx, projectID, versionID string) ([]Build, error)` — 新到旧
- `Build.Changelog string`（取上游 commits 的第一行摘要）
- `Version.Minecraft string`、`Core.JavaMinimum int`、`Core.Minecraft string`
- `GET /api/downloads/projects/{project}/versions/{version}/builds`

- [ ] Step 1: `client_test.go` 里加一个假上游，断言 `Builds()` 解析出两条、新的在前、带 changelog；跑 `go test ./internal/serverjar/ -run Builds` 看它失败
- [ ] Step 2: 实现 `Builds()`（`/projects/{p}/versions/{v}/builds`，复用 `LatestBuild` 的 downloads 挑选与 `safeFileName` / `checkDownloadURL` 校验）
- [ ] Step 3: 加 `proxyMinecraft` 静态表（Velocity 大版本 → MC 区间），`Versions()` 填 `Minecraft`；服务端项目填版本号自身
- [ ] Step 4: `Core` 加两个字段，`downloader` 落库时写入
- [ ] Step 5: handler + route，`make lint && make test`
- [ ] Step 6: 提交

### Task 2: 后端 · 上传自定义 jar 与取回

**Files:** `internal/serverjar/library.go`、`internal/api/handlers_downloads.go`、`internal/api/routes.go`、`internal/api/handlers_downloads_test.go`

**Produces:**
- `func (l *Library) Import(name string, src io.Reader, meta Core) (Core, error)`
- `POST /api/cores/upload`（multipart：`file`、`kind`、`version`、`javaMinimum`、`minecraft`）
- `GET /api/cores/{id}/file`

- [ ] Step 1: 测试：上传一个 jar，`GET /api/cores` 能看到它，`imported` 为真且元数据在；跑，失败
- [ ] Step 2: `Import()` —— 写 `.hypercraft-part` 再 rename，算 SHA-256，`record()` 落索引；拒绝非 `.jar` 与已存在同名
- [ ] Step 3: handler（限制体积，复用 `validCoreID`）+ 两条 route
- [ ] Step 4: `make lint && make test`，提交

### Task 3: 前端 · 共用的资源列表组件

**Files:** `web/src/components/ResourceList.tsx`（新建）、`web/src/styles.css`、`web/src/format.ts`

**Produces:**（另外四页直接进口这些）
- `<ResourceTable head={ResourceColumn[]}>`、`<ResourceRow …>`
- `<ResourceName tile label chips fileName>`、`<ResourceUsers names>`（空时「未使用」）
- `<ResourceWhen iso>`（相对时间 + `title` 绝对时间）
- `<ResourceMenu items>`（hover / 聚焦才出现的 `⋯`）
- `confirmResourceDelete(entry)` —— usedBy 非空则挡住并列出使用者
- `<StorageHygiene>`、`<ResourceHint>`（§3.5 两张卡片）
- `formatAgo(iso)` 放进 `format.ts`

- [ ] Step 1: 写 `formatAgo`，覆盖「刚刚 / N 分钟前 / N 小时前 / N 天前 / N 个月前 / N 年前」
- [ ] Step 2: 写组件与对应样式块（行高 56、表头 34、列宽按模板 §3.2；1100 / 820px 逐级丢列）
- [ ] Step 3: `npm --prefix web run build`，提交

### Task 4: 前端 · 添加核心弹层

**Files:** `web/src/components/AddCoreDialog.tsx`（新建）、`web/src/api.ts`、`web/src/types.ts`、`web/src/styles.css`、`web/scripts/check-ui.mjs`

- [ ] Step 1: `api.listCoreBuilds` / `api.uploadCore`，类型补齐
- [ ] Step 2: 弹层骨架：52px 头部 + 类型网格（`listCoreProjects()` + 上传卡）+ 左版本右构建 + 64px 确认条
- [ ] Step 3: 构建表加载时 3 行骨架（`<Skeleton>`），不用文字
- [ ] Step 4: 选中构建立即跑兼容性检查（比 `java.overview.runtimes[].major`），不过时在确认条上方插 `<Note tone="warn">` + 「去装 Java N」按钮（跳 `{kind:'library', section:'java', view:'installed', want:N}`），按钮文案变「仍然下载」
- [ ] Step 5: 上传分支：拖拽区 + 类型／版本／Java／MC 四个字段
- [ ] Step 6: `PRIMARY_ALLOWED` 登记（下载与上传两个互斥状态各一个实心按钮）
- [ ] Step 7: build，提交

### Task 5: 前端 · 核心库列表页重写

**Files:** `web/src/components/CoreLibraryPage.tsx`、`web/src/useCores.ts`、`web/src/styles.css`

- [ ] Step 1: 删掉 `lead` 与页内的「下载核心」整段；`facts` 留「N 个 · 合计」与存放路径
- [ ] Step 2: `actions` 放 `上传 jar`（次级）+ `添加核心`（实心，唤起弹层）
- [ ] Step 3: `Toolbar`：分段筛选（全部 / 服务端 / 代理端 / 未使用，各带计数）+ 右侧 `Select` 排序（最近加入 / 名称 / 体积 / 使用最多）
- [ ] Step 4: 表格用 Task 3 的组件；下载中插占位行（读 `cores.job` 的进度）
- [ ] Step 5: 列表下方两张卡片；空状态两个出口
- [ ] Step 6: `useCores` 加 `removeMany(ids)` 给「清理未使用」
- [ ] Step 7: build，提交

### Task 6: 前端 · 资源库外壳

**Files:** `web/src/routes.ts`、`web/src/components/Sidebar.tsx`、`web/src/components/JavaPage.tsx`、`web/src/App.tsx`、`web/src/styles.css`

- [ ] Step 1: `LIBRARY_SECTIONS` 改名（Java 运行时 / 数据库 / 插件 / 建筑与地图）并把服务端核心排第一；`shelfRows` 同步
- [ ] Step 2: 角标改条目数（插件仍优先显示可更新数，警告色）
- [ ] Step 3: 侧栏底部「资源库占用」卡片：总量 + 分段条 + 分类明细
- [ ] Step 4: library 路由加 `want`，`JavaPage` 读它预选主版本
- [ ] Step 5: build，提交

### Task 7: 收尾

- [ ] Step 1: `CHANGELOG.md` 未发布小节
- [ ] Step 2: `npm --prefix web run build && make lint && make test`
- [ ] Step 3: 人工自查：明暗两种模式，1440 / 1200 / 1024 / 768 / 390
- [ ] Step 4: 合并回 `main` 并推送（CLAUDE.md 工作流程第 4 条）
