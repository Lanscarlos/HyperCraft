# 面板排版六项改动 实施方案

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把设计稿里站得住的六条结构性改动落到面板上，同时保住面板自己的视觉身份（像素字体、赤陶主色、明暗双模、两块终端画布）。

**Architecture:** 全部改动都在 `web/src` 里，只有第 2 条需要动一处 Go（给 `knownPropertyUI` 加分组/等效指令/风险三个字段）。不新增页面框、不新增样式文件、不引框架——沿用 `Page.tsx` 的两种形态和 `styles.css` 的令牌。六条互不依赖，可以按任意顺序单独验收。

**Tech Stack:** React 18 + TypeScript + Vite；样式全在 `web/src/styles.css`；后端 Go（仅 `internal/api/handlers_files.go`）。

**Spec:** 设计稿 artifact `https://claude.ai/code/artifact/65792558-bd3b-4c9d-aa22-3aa26b2dbb71`，以及本仓库会话里逐条比对得出的取舍结论（下面「设计稿里不采纳的部分」一节把取舍写死了）。

## Global Constraints

这些约束对每一条任务都成立，不再在任务里重复：

- **样式只写在 `web/src/styles.css`**，不拆文件、不引 CSS 框架/组件库/CSS-in-JS、不加 `!important`、不写行内 `style`（JS 必须算出来的动态值除外）。
- **颜色只用令牌**，不写裸 hex。新增令牌必须 light 和 dark 两个块都加，否则暗色下是空值。
- **不引入新的主色**。设计稿的绿 `#12a06c` 不采纳：`--ok` 在本仓库是状态色，注释里写明了它有独立色相就是为了「运行中」不被读成品牌装饰。品牌保持 `--accent`（赤陶）。
- **不换字体**。像素字体（`pixelfont.ts` 默认开）保留。
- **不动 `--term-*` / `--shell-*`**。两块终端画布必须在明暗两种模式下都保持深色且明显不同色。
- **不新增断点**。先用 `repeat(auto-fill, minmax(<下限>, 1fr))` 这类内在响应解决；确实要断点时只能复用现有的 1240 / 1024 / 1280 / 1100 / 900 / 820 / 780。
- **`1024px` 这一档在两处**：`styles.css` 的媒体查询和 `App.tsx:66` 的 `DRAWER_QUERY`。改一处必须改另一处。
- **动效时长只从 `--dur-1`(90ms) / `--dur`(140ms) / `--dur-3`(220ms) / `--dur-4`(300ms) / `--dur-data`(400ms) 取**，按动作性质选：指针反馈 `--dur-1`、原地变状态 `--dur`、原地出现消失 `--dur-3`、横跨或覆盖屏幕 `--dur-4`。JS 里写的动效必须自己调 `motion.ts` 的 `reducedMotion()`。
- **任何可能装长文本或终端的 flex/grid 子项都要 `min-width: 0`**（列方向 `min-height: 0`）。这是本仓库最高频的布局 bug。
- **代码注释用英文，文档和用户可见文案用中文。** 注释解释「为什么」，不解释代码在做什么。
- **改动前先读目标区块的现有注释。** 这个仓库大量注释记录的是踩过的坑；注释描述的约束仍然成立就别动那段代码。
- **用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节**，不要改成版本号标题。
- 分支：`claude/epic-fermat-tqxe6k`（已与 `origin/main` 同步到 `01df8df`）。

## 验证手段（每条任务收尾都要跑）

前端没有单测也没有 lint，**`tsc -b` 是唯一的自动检查**，所以每条任务的验收都是「构建过 + 人工看过」两段：

```bash
npm --prefix web run build      # tsc -b + vite build，类型和构建一起过
make lint && make test          # 只有第 2 条动了 Go，那条必须跑
```

人工验收用已经验证可用的本地回路（`internal/webui/dist` 是构建产物，`make build-go` 会把它嵌进去）：

```bash
# 1. 起守护进程（首次会打印一次性密码，记下来）
go run ./cmd/hypercraft -data /tmp/hc-verify -listen 127.0.0.1:19190

# 2. 用 Playwright 截图（Chromium 已预装，不要跑 playwright install）
#    登录后逐页截 1440 / 1200 / 1024 / 768 / 390 五个宽度 × 明暗两种模式
```

每条任务的「人工自查」都必须覆盖这四项：

- 明暗两种模式都看过（新令牌两个块都加了没有）。
- 1440 / 1200 / 1024 / 768 / 390 五个宽度下没有横向溢出、没有挤成错位的行。
- 折叠侧栏（`data-rail='on'`）、打开抽屉（≤1024）、开着控制台的实例页——这三处没被波及。
- 长内容（六层深的路径、几百行日志、很长的实例名）不会撑破容器。

## 设计稿里不采纳的部分（写死，避免执行时又被捡回来）

| 设计稿的做法 | 不采纳的理由 |
| --- | --- |
| 主色改绿 `#12a06c` | 和 `--ok`（运行中）撞色。设计稿里选中导航、主按钮、图表线和「运行中」是同一个绿，一眼扫不出异常。 |
| 换 IBM Plex Sans + Noto Sans SC | 像素字体是面板身份，`styles.css` 开头三十行是双字体 unicode-range 围栏的踩坑记录。 |
| 只有浅色 | 本仓库有两套完整令牌块 + 四套配色，暗色不能丢。 |
| 单块终端配色 | `--term-*` / `--shell-*` 必须一眼可分，这是防止把 `rm -rf` 敲进错误终端的唯一屏障。 |
| 顶栏显示 TPS / 在线人数 | **后端没有这两个数。** 守护进程只读 stdout，`MetricSample` 只有 `cpuPercent` / `memoryBytes` / `processes`。任务 1 只显示真实存在的数。 |
| 右栏在线玩家列表（带 kick/ban） | 同上，需要先改后端。`InstanceCockpit.tsx` 的 QuickPanel 注释已经写明了这件事。本方案不含。 |
| 配置页逐行挂「需重启」徽章 | server.properties 是启动时读一遍，**每一项**都要重启，逐行挂等于每行都挂、也就等于没挂。改成：重启在保存条上说一次，逐行说的是反过来那件事——有等效控制台指令的少数几项（`/whitelist on` 等）。见 Task 2。 |
| 「点告警里的查看报告跳回对应时刻的控制台」 | 控制台没有按时间寻址的接口，硬做出来是个假链接。见 Task 4。 |

---

### Task 1: 顶栏常驻运行状态条 + 启停

**背景：** 现在状态和开关机只活在 `InstanceCockpit` 的 `.cockpit__bar` 上。人在「文件」「插件」页想看一眼状态或者重启，必须先退回控制台。顶栏（`TopBar.tsx`）现在只有 返回 / 面包屑 / 搜索 / 主题 / 账号，1440 宽下中间是一大片空白——正好是这条状态带该待的地方。

**显示什么：** 只显示后端真有的数据——状态点 + 状态文字、CPU、内存、已运行时长，外加 重启 / 停止。**不显示 TPS 和在线人数**（后端没有）。

**只在实例路由下出现。** `App.tsx:446` 已经算好了 `const selected = instances.find(...) ?? null`；`selected` 为 null（概览、Java 环境这些面板级页面）时整条不渲染，顶栏回到现在的样子。

**Files:**
- Create: `web/src/useSeries.ts`（把 `InstanceCockpit.tsx` 里的 `useSeries` 抽出来，让顶栏和驾驶舱共用一份轮询，不要轮询两遍）
- Modify: `web/src/components/InstanceCockpit.tsx`（删掉本地 `useSeries`，改为从 props 收 metrics）
- Modify: `web/src/components/TopBar.tsx`（新增 `instance` / `metrics` / `onChanged` 三个可选 prop，渲染状态带）
- Modify: `web/src/App.tsx`（在 App 层持有 metrics 轮询，同时喂给 TopBar 和 InstanceView）
- Modify: `web/src/styles.css`（`.topbar__status` 及其子元素）
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces: `useSeries(instanceId: string | null, active: boolean): InstanceMetrics | null` —— 从 `web/src/useSeries.ts` 导出。`instanceId` 为 null 或 `active` 为 false 时不轮询并返回 null。
- Produces: `TopBar` 新增可选 props：`instance?: InstanceStatus | null`、`metrics?: InstanceMetrics | null`、`onInstanceChanged?: (next: InstanceStatus) => void`。
- Consumes: `PowerControls`（`web/src/components/PowerControls.tsx`）现有的组件契约，顶栏直接复用它，不要另写一套启停按钮——启停有确认对话框和权限判定，重写一定漏。

- [ ] **Step 1: 把 `useSeries` 抽成共享 hook**

先读 `web/src/components/InstanceCockpit.tsx` 底部的 `useSeries` 实现（连同它的注释，注释说明了为什么 `active` 为 false 时要停轮询）。原样搬进新文件 `web/src/useSeries.ts`，只加一件事：`instanceId` 允许为 `null`。

```ts
/**
 * One instance's resource samples, polled while the caller is in front.
 *
 * Lifted out of InstanceCockpit so the top bar's status strip and the cockpit
 * share a single poll: the strip is on screen on every instance page, and a
 * second interval hitting the same endpoint would double the daemon's sampling
 * load for one readout.
 */
export function useSeries(instanceId: string | null, active: boolean): InstanceMetrics | null {
  // ... 原实现，instanceId 为 null 时直接 return，不建 interval
}
```

- [ ] **Step 2: 构建，确认抽取本身没坏东西**

Run: `npm --prefix web run build`
Expected: 通过。`InstanceCockpit` 改成从 props 收 metrics 之后，`App.tsx` 还没传，TypeScript 会报缺参数——这一步允许先让 `InstanceView` 透传。

- [ ] **Step 3: 在 App 层持有轮询并传下去**

`App.tsx` 里 `selected` 算出来之后加：

```tsx
// The strip in the top bar and the cockpit's tiles read the same samples, so
// the poll lives here rather than in either of them.
const metrics = useSeries(selected?.id ?? null, selected != null && isLive(selected.state))
```

传给 `<TopBar instance={selected} metrics={metrics} onInstanceChanged={...} />` 和 `<InstanceView metrics={metrics} ... />`。`onInstanceChanged` 复用 App 里已有的实例更新回调（找 `setInstances` 附近现成的那个，不要新写）。

- [ ] **Step 4: 渲染状态带**

`TopBar.tsx` 里，面包屑 `<nav className="crumbs">` 之后、`.topbar__right` 之前插入。`instance` 为 null 时整段不渲染：

```tsx
{instance && (
  <div className="topbar__status" aria-label="实例状态">
    <span className={`status__dot status__dot--${instance.state}`} />
    <b>{STATE_LABELS[instance.state]}</b>
    {/* Dropped first as the strip narrows: the state and the power buttons are
        what someone reaching for the panel on a phone actually came for. */}
    <span className="topbar__status-fact">CPU <b className="num">…</b></span>
    <span className="topbar__status-fact">内存 <b className="num">…</b></span>
    <span className="topbar__status-fact">已运行 <b className="num">…</b></span>
    <PowerControls instance={instance} onChanged={onInstanceChanged} onError={...} compact />
  </div>
)}
```

数值格式沿用现成的：`formatPercent` / `formatBytes`（`web/src/format.ts`）、`useUptime`（`web/src/useUptime.ts`）。没有采样时（停机、刚启动）显示 `—`，不要显示 0——`InstanceCockpit` 里的 `Reading` 组件已经把这条规矩写死了，读它的注释并沿用同样的处理。

`PowerControls` 如果没有 `compact` 形态，加一个只出图标的变体，不要在顶栏里塞全宽按钮。

- [ ] **Step 5: 写样式**

`styles.css` 里 `.topbar` 规则之后加。要点：`min-width: 0` 必须有（实例名和路径会长）；分隔线用现成的做法（找 `.cockpit__facts > span` 的分隔写法照抄，不要新造）。

```css
/* Sits between the trail and the account controls, in the space the trail
   leaves empty on a wide window. It is the only place on a file or plugin page
   that still says whether the server is up — and the only place those pages
   can stop it. */
.topbar__status {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  height: 32px;
  padding: 0 4px 0 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-2);
}
```

- [ ] **Step 6: 窄屏逐级丢**

复用现有断点，不要新增。在 `max-width: 1240px` 里丢掉「已运行」，在 `max-width: 1024px` 里丢掉 CPU 和内存——只留状态点、状态文字和启停。丢掉的都是别处还能看到的信息（驾驶舱的 tiles 上有），符合仓库「逐级丢列」的规矩。参考 `.ptable__row` 的注释。

- [ ] **Step 7: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工：起守护进程，建一个实例，进「文件」页确认状态带在、数字在跳、停止按钮能用；按上面「人工自查」四条过一遍。

- [ ] **Step 8: CHANGELOG + 提交**

`CHANGELOG.md` 的「未发布」加一行，说明顶栏现在常驻实例状态和启停。

```bash
git add web/src/useSeries.ts web/src/components/TopBar.tsx web/src/components/InstanceCockpit.tsx web/src/components/PowerControls.tsx web/src/App.tsx web/src/styles.css CHANGELOG.md
git commit -m "顶栏常驻实例状态与启停：文件页和插件页不用再退回控制台看服务器活着没有"
```

---

### Task 2: 服务器配置页 —— 锚点栏 + 逐项改动标记 + 常驻保存条

**背景：** 现在 `ServerConfigPage.tsx` 用 chip 做配置文件切换，`PropertiesEditor.tsx` 把字段摊成「常用设置 + 其他设置」两块 panel，底部一个保存按钮。缺三样东西：左侧分组锚点、逐项的「已修改 / 原值」、底部常驻保存条。高风险项（`online-mode`）现在只有一句 hint「离线服请关闭」，没有说清后果。

**这条要动 Go。** `knownPropertyUI` 加三个字段。

**Files:**
- Modify: `internal/api/handlers_files.go:14-58`（`knownPropertyUI` 加 `Group` / `Restart` / `Risk`，并给 `knownProperties` 表逐项填上）
- Modify: `internal/api/handlers_files_test.go`（如无则 Create：断言分组齐全、断言高风险项带 Risk）
- Modify: `web/src/types.ts:240-248`（`KnownProperty` 同步三个字段）
- Modify: `web/src/components/PropertiesEditor.tsx`（分组渲染 + 锚点栏 + 逐项徽章 + 保存条）
- Modify: `web/src/styles.css`（`.cfg-*` 一族）
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces（Go → JSON → TS）：

```go
type knownPropertyUI struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	Default string   `json:"default"`
	// Group is the section this setting belongs to in the form. Empty means
	// 未分类, which is where a key nobody has filed yet lands rather than
	// disappearing.
	Group string `json:"group,omitempty"`
	// Live is the in-game command that applies this setting without a restart,
	// for the few keys that have one. Empty for the rest.
	//
	// The design this came from marked each row 需重启 instead. That badge
	// would be on every row: the server reads server.properties once, at
	// startup, so *every* edit here waits for a restart, and a badge that is
	// always on is read by nobody. What actually varies is the opposite — the
	// handful of settings you can also change right now from the console — so
	// that is what the row carries, and the restart is said once, in the save
	// bar, where it is true of everything being saved.
	Live string `json:"live,omitempty"`
	// Risk spells out what goes wrong if this is set carelessly. Only the few
	// keys that can open a server up carry one; a warning on every row is a
	// warning on none.
	Risk string `json:"risk,omitempty"`
}
```

- Produces（TS）：`KnownProperty` 加 `group?: string`、`live?: string`、`risk?: string`。

- [ ] **Step 1: 先写 Go 的失败测试**

`internal/api/handlers_files_test.go`：

```go
func TestKnownPropertiesAreGrouped(t *testing.T) {
	// The rail renders these in this order, so a group outside the set would
	// either vanish from the rail or append a section nobody designed.
	allowed := map[string]bool{
		"基础": true, "世界与生成": true, "玩家与权限": true, "网络与端口": true, "性能": true,
	}
	for _, p := range knownProperties {
		if p.Group == "" {
			t.Errorf("%s 没有分组：分组是配置页左侧锚点栏的唯一来源", p.Key)
			continue
		}
		if !allowed[p.Group] {
			t.Errorf("%s 的分组 %q 不在锚点栏的五个分组里", p.Key, p.Group)
		}
	}
}

func TestOnlineModeCarriesRisk(t *testing.T) {
	for _, p := range knownProperties {
		if p.Key == "online-mode" {
			if p.Risk == "" {
				t.Fatal("online-mode 必须写明关闭后的后果，一个裸开关不够")
			}
			return
		}
	}
	t.Fatal("knownProperties 里没有 online-mode")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/ -run 'TestKnownProperties|TestOnlineMode' -v`
Expected: FAIL —— 每个 key 都报「没有分组」，online-mode 报没有 Risk。

- [ ] **Step 3: 填 Go 侧的数据**

分组用这五个，**顺序就是锚点栏的顺序**：`基础`、`世界与生成`、`玩家与权限`、`网络与端口`、`性能`。

现有 18 项全部归组如下，照抄（保留每项原有的 `Label` / `Type` / `Options` / `Default` / `Hint`，只补 `Group` / `Live` / `Risk`）：

```go
var knownProperties = []knownPropertyUI{
	{Key: "motd", Label: "服务器标语 (MOTD)", Type: "text", Default: "A Minecraft Server", Hint: "中文会自动转成 \\uXXXX 转义，游戏内显示正常", Group: "基础"},
	{Key: "gamemode", Label: "默认游戏模式", Type: "select", Default: "survival", Options: []string{"survival", "creative", "adventure", "spectator"}, Group: "基础", Live: "/defaultgamemode <模式>"},
	{Key: "difficulty", Label: "难度", Type: "select", Default: "easy", Options: []string{"peaceful", "easy", "normal", "hard"}, Group: "基础", Live: "/difficulty <难度>"},
	{Key: "pvp", Label: "允许 PVP", Type: "boolean", Default: "true", Group: "基础"},
	{Key: "hardcore", Label: "极限模式", Type: "boolean", Default: "false", Group: "基础",
		Risk: "开启后玩家死亡即永久旁观，且这个改动对已有存档不可逆"},

	{Key: "level-name", Label: "存档名称", Type: "text", Default: "world", Group: "世界与生成",
		Risk: "改成一个不存在的名字会生成一个全新世界，原存档不会被删除但服务器不再加载它"},
	{Key: "level-seed", Label: "世界种子", Type: "text", Default: "", Hint: "留空为随机生成", Group: "世界与生成"},
	{Key: "allow-nether", Label: "允许下界", Type: "boolean", Default: "true", Group: "世界与生成"},

	{Key: "max-players", Label: "最大玩家数", Type: "number", Default: "20", Group: "玩家与权限"},
	{Key: "online-mode", Label: "正版验证", Type: "boolean", Default: "true", Group: "玩家与权限",
		Risk: "关闭后任何人都能冒用他人 ID 进入，必须配合前置验证插件，且不要直接暴露在公网"},
	{Key: "white-list", Label: "启用白名单", Type: "boolean", Default: "false", Group: "玩家与权限", Live: "/whitelist on|off"},
	{Key: "enable-command-block", Label: "启用命令方块", Type: "boolean", Default: "false", Group: "玩家与权限"},
	{Key: "spawn-protection", Label: "出生点保护半径", Type: "number", Default: "16", Group: "玩家与权限"},
	{Key: "allow-flight", Label: "允许飞行", Type: "boolean", Default: "false", Group: "玩家与权限"},
	{Key: "enforce-secure-profile", Label: "强制安全档案", Type: "boolean", Default: "true", Group: "玩家与权限"},

	{Key: "server-port", Label: "端口", Type: "number", Default: "25565", Group: "网络与端口"},

	{Key: "view-distance", Label: "视距 (区块)", Type: "number", Default: "10", Group: "性能",
		Hint: "对 TPS 影响最大的单项设置，8–12 通常是性价比区间"},
	{Key: "simulation-distance", Label: "模拟距离 (区块)", Type: "number", Default: "10", Group: "性能",
		Hint: "比视距更吃 CPU：这个半径内的实体和红石才会真的运行"},
}
```

`Live` 只给确实有等效指令的那三项。`Risk` 只给能把服开成裸奔、或者改动不可逆的那四项——每行都有警告等于每行都没有。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/ -run 'TestKnownProperties|TestOnlineMode' -v`
Expected: PASS。

Run: `make lint && make test`
Expected: 全绿。gofmt 是 CI 硬卡，提交前必过。

- [ ] **Step 5: TS 类型同步**

`web/src/types.ts` 的 `KnownProperty` 加三个可选字段，注释说明 `group` 空串落到「未分类」。

- [ ] **Step 6: 配置页改成三段式**

`PropertiesEditor.tsx` 重构渲染部分（读取/保存的逻辑不要动，尤其是 `dirty` / `present` 那套——它的注释说明了为什么保存只写这两个集合的并集，动它会让打开页面点保存就写出一墙默认值）。

三件事：

1. **左侧锚点栏**：按 `group` 分组，每组一个锚点，显示该组条数；点击滚到对应 section。顶部放一个「仅看已修改 N」的开关。分组顺序按上面 Go 表里的五个，不要按字母序。
2. **逐项标记**：`dirty.has(key)` 为真时，该行加左侧色条 + `已修改` 徽章 + 划掉的原值（原值取 `data.entries` 里这个 key 的值；文件里没有这个 key 时显示 `prop.default` 并标明「默认值」）；`risk` 非空时在 hint 位置用 `--danger` 色出一行后果说明；`live` 非空且该行已修改时，出一行「也可以直接在控制台敲 `<live>` 立即生效」。
3. **常驻保存条**：`dirty.size > 0` 时从底部升起，写「N 项更改待保存 · 重启服务器后生效」，右侧 `放弃更改` / `保存`。重启这件事在这里说一次就够——server.properties 是启动时读一遍，每一项都一样，所以它是保存条的属性而不是某一行的属性。

栅格用内在响应，不要为排布加断点。锚点栏在窄屏（复用 `max-width: 900px`）折到内容上方变成横向 chip 行。

- [ ] **Step 7: 写样式**

`.cfg-rail`（锚点栏）、`.cfg-row`（配置行）、`.cfg-row--dirty`（左色条）、`.cfg-savebar`（保存条）。保存条用 `position: sticky; bottom: 0`，升起动效用 `--dur-3` + `--ease`（原地出现）。色条用 `--accent`，不要用 `--ok`——绿是状态色。

- [ ] **Step 8: 验证**

Run: `npm --prefix web run build && make lint && make test`
Expected: 全过。

人工：改一项、确认徽章和原值出现、保存条升起、条数对；改 `white-list` 确认「也可以直接敲 /whitelist on」那行出来；`online-mode` 和 `level-name` 两行确认风险说明出来了；锚点栏点一遍确认五组都能滚到；「仅看已修改」开关确认能过滤；按「人工自查」四条过一遍。

- [ ] **Step 9: CHANGELOG + 提交**

```bash
git add internal/api/handlers_files.go internal/api/handlers_files_test.go web/src/types.ts web/src/components/PropertiesEditor.tsx web/src/styles.css CHANGELOG.md
git commit -m "服务器配置页说清改了什么：分组锚点、逐项已修改与原值、常驻保存条、高风险项写明后果"
```

---

### Task 3: 内容吃满宽度 + 密度收紧一档

**背景：** 在「服务端核心」页实测能看到：说明文字被压在约 540px 里断成难看的两行，右边同一行的路径 chip 却铺得很宽——`.page__head` 是 `display: flex` + `justify-content: space-between`，标题列 `flex: 1; min-width: 280px`，说明文字和右侧事实列抢同一行。路径一长就挤。

**这条风险最高**（全局密度改动最容易连坐），所以范围写死，只做两件事，不做第三件。

**Files:**
- Modify: `web/src/styles.css`（`.page__head` 一族、卡片头高度、表格行高）
- Modify: `CHANGELOG.md`

- [ ] **Step 1: 先读注释**

读 `styles.css` 里 `.page__head`、`.page__head > div:first-child`、`.page > *`、`.page--wide > *` 这几条的注释。特别是 `.page__head > div:first-child` 用 `:first-child` 而不是 `> div` 的那条——注释写明了为什么（`> div` 会误伤标题旁边的按钮行，把两个按钮叠进三分之一宽的盒子里）。**不要改这个选择器。**

- [ ] **Step 2: 让说明文字不和事实列抢行**

**先搞清真正的原因，不要照着「加个宽度上限」去改：** `.page__lead`（`styles.css:4479`）已经有 `max-width: 660px` 了。文字被压成两行不是因为没有上限，而是因为标题列 `.page__head > div:first-child`（`styles.css:4413`）是 `flex: 1; min-width: 280px`，和右侧 `aside` 抢同一行——`aside` 里塞一个长路径 chip 时，标题列被压到 660px 以下，lead 的上限根本没机会生效。

修的是标题列的伸缩基准，不是 lead 的上限。`.page__head` 已经是 `flex-wrap: wrap`，把基准抬到一个「够读一行散文」的宽度，两边挤不下时 `aside` 自己就换行下去了：

```css
.page__head > div:first-child {
  display: flex;
  flex-direction: column;
  gap: 8px;
  /* Basis, not floor. The lead is capped at 660px and never got near it: the
     column is flex:1 beside an aside, so one long data directory in those
     facts squeezed the prose to about a third of the window and broke it
     mid-clause. A 420px basis makes the head's existing wrap drop the aside to
     its own row instead of shrinking the sentence — while min-width keeps the
     column collapsible on a phone, where 420 would overflow. */
  flex: 1 1 420px;
  min-width: 280px;
}
```

`min-width: 280px` 必须留着：390px 宽的屏幕上 420px 的下限会直接横向溢出，而这正是本仓库最常见的那类 bug。

**不要改 `> div:first-child` 这个选择器本身**——它上面的注释写明了为什么不能写成 `> div`（会误伤标题旁边的按钮行）。

- [ ] **Step 3: 密度收一档**

只动两处，各减 2px，沿用偶数 px 的节奏（不要引入 13px、17px 这种值）：

1. `.panel`（`styles.css:3486`）：`gap: 14px` → `12px`，`padding: 18px 20px` → `16px 20px`。只收纵向，横向 20px 不动——横向一收，卡片里的表格就贴边了。
2. `.ptable__row`（`styles.css:10752`）：`min-height: 52px` → `50px`。

**不要动**：`.page` 的 `gap: 16px`（页面级节奏）、`.main` 的 padding、侧栏宽度、`--content-max` / `--content-max-wide`。

- [ ] **Step 4: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工重点（这条最容易连坐，必须逐页看）：概览、所有实例、Java 环境、服务端核心、插件库、主机、面板设置，外加实例的控制台/文件/插件/配置。按「人工自查」四条过一遍，五个宽度一个都不能省。

- [ ] **Step 5: CHANGELOG + 提交**

```bash
git add web/src/styles.css web/src/components/Page.tsx CHANGELOG.md
git commit -m "页面说明文字不再被右侧长路径挤成两行，卡片内部密度收紧一档"
```

---

### Task 4: 监控页 KPI 给结论 + 事件流

**背景：** `ResourcePanel.tsx` 现在是范围选择 + 两条曲线 + 可选表格。设计稿的改进是 KPI 卡直接给结论（`正常` / `偏高`），曲线只画过程，外加右侧事件流。

**诚实的范围：** 事件流**不新增后端**。数据来自已经存在的三处：`alerts.ts` 的判据（内存超售、磁盘吃紧）、实例自身的状态变化（`exitCode` / `message`）、插件加载失败（`PluginFailure`，`types.ts:1462` 已有）。不做「点查看报告跳回对应时刻控制台」——控制台没有按时间寻址的接口，硬做会是个假链接。

**Files:**
- Modify: `web/src/components/ResourcePanel.tsx`
- Create: `web/src/instanceEvents.ts`（把三处来源归一成一个事件列表）
- Modify: `web/src/styles.css`（`.kpi-*`、`.events-*`）
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces: `web/src/instanceEvents.ts`

```ts
export type EventLevel = 'error' | 'warn' | 'ok' | 'info'

export interface InstanceEvent {
  id: string
  level: EventLevel
  title: string
  detail?: string
  /** ISO timestamp, or undefined for a condition that is true now rather than
   *  something that happened at a moment. */
  time?: string
}

export function instanceEvents(
  instance: InstanceStatus,
  metrics: InstanceMetrics | null,
  failures: PluginFailure[],
): InstanceEvent[]
```

- Produces: `verdictOf(value: number, warn: number, high: number): { label: string; tone: EventLevel }` —— KPI 卡的结论，同文件导出。

- [ ] **Step 1: 写 KPI 卡**

在现有曲线之上加一行 KPI 卡：CPU、内存、进程数。每张卡 = 大数值 + 结论徽章 + 一行脚注。结论阈值写成常量并在注释里说明依据，不要散落魔数：

```ts
// A Minecraft server's main thread is effectively single-threaded, so 100 here
// is one core saturated — which is normal under load, not an alarm. The alarm
// is sustained saturation, which is what 偏高 marks.
const CPU_WARN = 85
const CPU_HIGH = 100
```

内存的阈值用 `effectiveMaxMemoryMB` 做分母（不是 `maxMemoryMB`——`types.ts:80-89` 的注释写明了为什么：@argfiles 启动时面板的 -Xmx 根本没到 JVM）。`effectiveMaxMemoryMB` 为 0 时不画结论也不画参考线，显示「没有设置 -Xmx」。

栅格用 `repeat(auto-fill, minmax(200px, 1fr))`，不加断点。

- [ ] **Step 2: 写事件归一**

`instanceEvents.ts`：状态相关（崩溃退出码、非正常停止的 message）、资源相关（内存逼近上限、CPU 持续偏高）、插件相关（`PluginFailure` 每条一个 error 事件）。按时间倒序，没有时间的排在最前（「现在成立的状况」比「过去发生的事」更该先看到）。

- [ ] **Step 3: 事件流排进页面**

曲线区和事件流并排：`grid-template-columns: minmax(0, 1fr) 320px`，复用现有的 `min-width: 1280px` 断点（控制台右栏用的就是这一档），窄于它时事件流落到曲线下方。`minmax(0, 1fr)` 里的 0 是必须的——曲线是 SVG，不写会撑破。

- [ ] **Step 4: 曲线收敛成单色单线**

现有 `TimeSeriesChart` 保持一图一线（已经是了），确认网格线用最淡的一档令牌，不要双 Y 轴。颜色继续用 `--series-cpu` / `--series-memory`，不要换成主色。

- [ ] **Step 5: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工：停机实例上确认 KPI 显示 `—` 而不是 0、事件流里有「已停止」而不是空白；跑起来之后确认结论徽章会变；按「人工自查」四条过一遍。

- [ ] **Step 6: CHANGELOG + 提交**

```bash
git add web/src/instanceEvents.ts web/src/components/ResourcePanel.tsx web/src/styles.css CHANGELOG.md
git commit -m "监控页先给结论再给过程：KPI 卡标正常/偏高，右栏汇总实例的告警与插件加载失败"
```

---

### Task 5: 插件页三标签（已安装 / 可更新 / 市场）

**⚠️ 这条和仓库里一条写明的设计决定冲突，执行前先读：**

`InstancePlugins.tsx` 顶部的组件注释写着：「What this page cannot do is acquire a plugin. It hands this server things the library already holds; downloading one is a panel-wide act with its own page, and 去插件市场 goes there carrying this server as the compatibility reference so the trip costs the context and nothing else.」

这是有意的分层：**下载是面板级行为（进插件库），装到某个服是实例级行为**。设计稿的侧栏注解自己也说「运维工具最常见的困惑就是分不清改的是哪一层」。所以本任务**不把面板级的插件库搬进实例页**，而是：

- `已安装` / `可更新` 两个标签直接用现有的 `StatusFilter`（`InstancePlugins.tsx:27` 已经有 `'all' | 'broken' | 'updatable' | 'duplicate'`），只是从 chip 筛选提成显式标签。
- `市场` 标签内嵌 `PluginBrowse`，并把 `against={[instance.id]}` 传进去——它本来就收这个 prop 做兼容性参照。下载落点仍然是面板级插件库，页面上要明说这一点（一行说明文字），不能让人以为下载 = 装到这个服了。

这样是「一页三标签」，但没有把两层揉成一层。

**Files:**
- Modify: `web/src/components/InstancePlugins.tsx`
- Modify: `web/src/components/PluginBrowse.tsx`（如需一个「内嵌」形态：不画自己的 `PageHead`）
- Modify: `web/src/styles.css`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `PluginBrowse({ against, recents, onChooseAgainst, onOpenLibrary })` —— 现有签名。内嵌时 `against` 固定为 `[instance.id]`，`onChooseAgainst` 传空实现（实例页里参照对象就是本实例，不给改）。
- Produces: `PluginBrowse` 新增可选 prop `embedded?: boolean`。为真时不渲染自己的页面标题，交给宿主页。

- [ ] **Step 1: 标签栏**

在 `InstancePlugins` 顶部加三段标签（`已安装` / `可更新 N` / `市场`）。`可更新` 的角标数直接用现有算好的 updatable 计数，不要重算。标签样式复用现有的 `.chip--active` 那套，不要新造一套 tab。

- [ ] **Step 2: 市场标签内嵌**

```tsx
{tab === 'market' && (
  <>
    <p className="muted">
      这里下载的插件会进面板的插件库，不会自动装到本服——装是下一步，在「已安装」里选版本。
    </p>
    <PluginBrowse embedded against={[instance.id]} recents={[instance.id]} onChooseAgainst={() => {}} onOpenLibrary={onOpenLibrary} />
  </>
)}
```

- [ ] **Step 3: 别丢掉现有的问题优先**

`InstancePlugins` 的核心是「先暴露加载失败」（注释里写死了）。切到 `市场` 标签时，**红色的加载失败横幅仍然要在标签栏上方常驻**，不能被标签切走。

- [ ] **Step 4: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工：三个标签都切一遍；确认失败横幅在三个标签下都在；确认市场里的兼容性判定是按当前实例算的；按「人工自查」四条过一遍。

- [ ] **Step 5: CHANGELOG + 提交**

```bash
git add web/src/components/InstancePlugins.tsx web/src/components/PluginBrowse.tsx web/src/styles.css CHANGELOG.md
git commit -m "插件页收成三个标签：已安装/可更新/市场，市场按当前实例判兼容但下载仍进面板插件库"
```

---

### Task 6: 文件管理三栏 + 标签页

**背景：** `FileManager.tsx:513` 是 `if (editor) return <FileEditor .../>`——编辑器**整页替换**列表，一次只能开一个文件，没有目录树。改完一个配置想对照另一个，得退出去重新进。这是六条里工作量最大的一条，也是体验差距最明显的一条。

**目标形态：** 左 目录树 / 中 列表 / 右 编辑器（带标签页，未保存画小圆点，底部状态栏给编码、换行符、光标位置）。窄屏（≤1024，复用现有断点）退回现在的单栏行为。

**Files:**
- Create: `web/src/components/FileTree.tsx`（目录树，只管目录不管文件）
- Modify: `web/src/components/FileManager.tsx`（三栏布局 + 多标签编辑器状态）
- Modify: `web/src/styles.css`（`.fm-*` 一族）
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces: `FileTree`

```tsx
export function FileTree({
  instanceId,
  /** The directory the listing is showing, so the tree highlights it. */
  path,
  onOpen,
}: {
  instanceId: string
  path: string
  onOpen: (path: string) => void
}): JSX.Element
```

- Produces（`FileManager` 内部）：`editor` 从单个 `EditorState | null` 变成 `{ tabs: EditorState[]; active: string | null }`。`EditorState` 本身不变（保持 `path` / `content` / `original` 三个字段），这样 `FileEditor` 的改动面最小。

- [ ] **Step 1: 先把编辑器状态改成多标签**

只改状态，不改布局。`editor` 换成 tabs 数组 + active path。打开文件时若已在 tabs 里就切过去，不重新读。关闭标签时若内容有改动，沿用现有的 `ask(...)` 确认（`FileManager.tsx:413` 附近那段，读它的文案再复用，不要另写一套提示）。

- [ ] **Step 2: 构建**

Run: `npm --prefix web run build`
Expected: 通过。此时仍是整页替换，只是可以在多个文件之间切。

- [ ] **Step 3: 写目录树**

`FileTree.tsx`：只列目录，懒加载（展开才请求子目录），当前目录高亮。用现有的 `api.listFiles(instanceId, dir)`，照 `FileManager` 现在的调用抄（含它的错误处理）。长路径必须能截断——`min-width: 0` + `text-overflow: ellipsis`。

- [ ] **Step 4: 三栏布局**

`FileManager` 的返回值从「列表或编辑器」变成一个三栏容器：

```css
/* Tree / listing / editor. The editor takes what is left rather than a fixed
   share: a config file is read at whatever width the window has to give, and
   the two rails beside it are navigation, not content. */
.fm {
  display: grid;
  grid-template-columns: 220px minmax(280px, 1fr) minmax(0, 1.4fr);
  gap: 12px;
  min-height: 0;
  flex: 1;
}
```

没有打开任何文件时，第三栏显示空态（「从中间选一个文件，会在这里打开」），**不要把三栏塌成两栏**——布局跳动比一块空白更难受。

- [ ] **Step 5: 窄屏退回单栏**

在现有的 `max-width: 1024px` 里把 `.fm` 改成单列，并且只显示当前那一栏（有打开的文件就显示编辑器，否则显示列表）。目录树在窄屏下不出现——列表里的 `..` 已经能上行，树是纯增益。同步确认 `App.tsx:66` 的 `DRAWER_QUERY` 没被动过（这条不需要改它，只是确认两处仍然一致）。

- [ ] **Step 6: 编辑器底部状态栏**

给 `FileEditor` 加一条底栏：语言（按扩展名猜）、编码（UTF-8，除非后端另说）、换行符、行列位置。行列位置从 textarea 的 `selectionStart` 算，不要引编辑器库。

- [ ] **Step 7: 验证**

Run: `npm --prefix web run build`
Expected: 通过。

人工重点：开三个文件切标签、改其中一个确认小圆点出现、关它确认有未保存提示、六层深的路径不撑破树栏、几百 KB 的文件不卡；1024 上下各看一次确认三栏/单栏切换正确；按「人工自查」四条过一遍。

- [ ] **Step 8: CHANGELOG + 提交**

```bash
git add web/src/components/FileTree.tsx web/src/components/FileManager.tsx web/src/styles.css CHANGELOG.md
git commit -m "文件页改成目录树/列表/编辑器三栏：点文件在右边直接开，多个文件同时开着对照"
```

---

## 收尾

六条都完成后：

- [ ] `npm --prefix web run build && make lint && make test` 全绿
- [ ] 五个宽度 × 明暗两种模式整体再过一遍
- [ ] 按 CLAUDE.md 的工作流合回 main：推功能分支 → 切 `main` → `git pull origin main` → 合并 → 推 `main` → 切回功能分支。若 `main` 已前进，先把 `main` 合进功能分支解决冲突，别把冲突带上 `main`。
