# 启动方式独立成页 设计文档

把 `实例设置` 里的「启动方式」抽成一条独立的导航页面，按设计稿重排成「左表单 + 右对照」两栏，
并把散落在字段下面的说明文字收编成右栏的启动前检查。

设计来源：设计画布 `HyperCraft 面板界面设计` 的 `Startup.dc.html` artboard，
以及画布注释 `n-startup`（「启动方式这一页为什么显得乱」）。

前作：`2026-09-12-instance-settings-layout-design.md` 把这一页的**表单栅格**理顺了（四档字段宽度、
`.field-row` wrap flex、`FieldHelp` 折叠、sticky 保存条）。那些成果全部保留，这份文档不动它们，
只做**页面级的拆分与重组**。

## 背景

### 现状：一页装了六段，其中一段自己就占了三分之二

`web/src/components/LaunchSettings.tsx` 1154 行，一个 `<form>` 里装六段：

| 段 | 行 | 内容 |
| --- | --- | --- |
| 开服前检查 | 491 | `LaunchCheckPanel` |
| 基本信息 | 499 | 名称 / 类型 / 版本 / 目录 |
| **启动方式** | 563–762 | 模式 segmented、Java、jar、内存、JVM 参数、服务端参数、装核心 |
| 控制台 | 764 | 编码、颜色 |
| 进程管理 | 832 | 自启、崩溃重启、停止方式 |
| 危险操作 | 888 | 删除 |

### 设计稿点出的六个问题

画布注释 `n-startup` 的诊断，对照代码逐条核实：

1. **文字比控件多** —— 八个控件配九段说明，眼睛找不到「哪里是我要填的」。
2. **同一份数据显示两遍** —— `LaunchSettings.tsx:711-731`：`--nogui` 胶囊和 `serverText` textarea
   上下各一份，改哪个都对，所以用户不知道改哪个。
3. **空状态比满状态还占地方** —— `+ 添加参数` 虚线框近 100px，里面一句话。
4. **只有一层层级** —— 五组东西同字号同间距垂直堆着。
5. **最该有的东西没有** —— 段头写着「面板拼出来的那条命令行」，整页看不到那条命令行。
6. **右边的空白不是没利用，是没对齐** —— 内容左对齐 + 固定窄宽度，余量全堆在一侧。

### 现状：面板确实会偷偷加参数

`internal/instance/config.go:296-370`，`commandLine(tty)` 在用户填的参数之外还会注入：

- `-Dterminal.jline=false` / `-Dterminal.ansi=true`（取决于 `tty` 和 `colorForced()`）
- 七个编码参数 `-Dfile.encoding=UTF-8` / `-Dstdout.encoding` / `-Dsun.*`（取决于 `Encoding`）

而这些注入的开关**来自「控制台」那一段**——拆页之后它在另一页上。所以设计稿右栏那句
「启动时用的就是它——不存在面板偷偷加参数的情况」，照着这一页的字段拼出来的命令是**假的**。

另外 argfile 模式下 Go 明确**不发** `-Xms/-Xmx`（`config.go:343`，注释解释了原因：
`@file` 就地展开、最后一个 `-Xmx` 获胜）。左边内存卡和右边命令必须反映这个差异。

### 现状：启动前检查查的是已保存的配置

`internal/api/handlers_launch.go:58` 的 `checkLaunch()` 读 `inst.Config()`——**已保存**的配置，
不是表单里的草稿。现有 issue 只有四种：`needs-setup` / `jar-unset` / `jar-missing`·`argfile-missing` /
`unknown-loader`。设计稿右栏那四条（Java 版本是否满足核心、Aikar 要求 Xms=Xmx、目录下找到几个 jar、
端口有没有被占用）**一条都不存在**。

## 目标

1. `启动方式` 成为独立的实例导航页，URL `/i/<id>/startup`。
2. 左栏四张卡片，右栏「实际执行的命令 + 启动前检查」，改一项右边立刻跟着变。
3. 那条命令**是真的**——包括面板注入的参数，且不存在第二份拼装逻辑。
4. 散落的说明文字收编成校验条目；能自动修的给一个按钮。
5. 不破任何一条仓库硬规矩：不引第四种页面测量、不造第二条保存条、不加新断点。

## 非目标

- **第三种启动方式「自定义命令」**。设计稿 segmented 里有三项，后端只有 jar / argfile 两种。
  真做得改 `instance.Config`、`commandLine()` 和迁移逻辑，且与仓库特意退掉「执行启动脚本」的
  方向相反（见 `internal/instance/migrate.go`）。**不做。**
- **把 PUT 改成真 PATCH**。见「保存契约」一节：拆页不改变权限粒度，想改得动整个请求语义。
- **保存条上的「查看差异」**。`配置历史` 已经是这件事的家，改成一个跳过去的链接。
- **JVM 参数改成可删胶囊**。见下面的决定 4。

## 已确认的六个决定

1. **拆两页**，不拆三页、不在页内再分标签。`启动方式` 独立，其余四段留在 `实例设置`。
2. **命令预览由后端出接口**，不在前端镜像一份拼装逻辑。
3. **这一轮做**：草稿实时校验 + 可执行修复、宿主机内存分配条 + 锁定 Xms=Xmx、端口占用检查。
   **不做**自定义命令。
4. **JVM 参数保留现有的卡片编辑器，不降级成胶囊。** `JVMArgsEditor.tsx:28-37` 的注释记着：
   行式版本上线过并被证伪，卡片还保留每行原始文本，好让 `配置历史` 的 diff 是一行而不是一堵墙。
   换成胶囊等于把这些全扔了，换来的只是更矮。（上线后若视觉上确实不好看，再单独评估改胶囊。）
5. **服务端参数也做卡片化**，与 JVM 参数同构。
6. **命令预览做成深色画布。**

## 架构

### 1. 路由与导航

| 项 | 值 |
| --- | --- |
| `InstanceSection` id | `'startup'` |
| 侧栏标签 | `启动方式` |
| 权限（开页） | `CAP.instanceSettings`，与 `实例设置` 同一个 |
| 位置 | `routes.ts` 的 `INSTANCE_SECTIONS` 插在 `settings` **之前** |
| URL | `/i/<id>/startup` |

`routes.ts:443` 用的是 `pick(INSTANCE_SECTIONS, …)`，**往清单里加一条，URL 解析自动就有了**，
不用碰解析逻辑。

`Sidebar.tsx:457` 的 `INSTANCE_ICONS` 加一条 `startup: 'bolt'`；`Icon.tsx` 新增 `bolt`
（用设计稿自己那个闪电 polygon）。`实例设置` 继续用齿轮。

`InstanceView.tsx` 加一个 `<Pane id="startup">`，沿用现有的懒挂载 + `leaving` 退场动画，
不发明新写法。

**老书签 `/i/<id>/settings` 继续可用**，只是里面不再有启动方式。不做重定向——那会把
「我就是要改个名字」的人弹走。

### 2. 六段东西的去向

| 现在的段 | 去向 |
| --- | --- |
| 开服前检查 | → `启动方式` 右栏（整块搬走） |
| 基本信息 | 留在 `实例设置` |
| **启动方式** | → **`启动方式`**，拆成四张卡 |
| 控制台 | 留在 `实例设置` |
| 进程管理 | 留在 `实例设置` |
| 危险操作 | 留在 `实例设置` |

检查面板整块搬走之后，`实例设置` 就一条检查都不显示了，而最要命的 `needs-setup`
（旧脚本迁移过来、还没配好、根本开不了服）恰恰是用户会在 `实例设置` 找的。所以
**`实例设置` 顶部留一条 fatal-only 窄横幅**：只在有致命项时出现，一句话 + 「去启动方式 →」。
不重复整个检查面板——那是右栏的活。

### 3. 文件拆分

`LaunchSettings.tsx` 1154 行拆开，拆完没有一个文件超过 ~300 行：

```
StartupSettings.tsx     新：页面框 + 草稿状态 + 保存
  JavaCoreCard.tsx      模式 segmented / Java 环境 / 服务端 jar
  MemoryCard.tsx        Xms / Xmx / 锁定开关 / 宿主机分配条
  JvmArgsCard.tsx       预设三选一 + 现有 JVMArgsEditor
  ServerArgsCard.tsx    卡片化的服务端参数
  LaunchConsole.tsx     新：命令预览 + 启动前检查（右栏）
LaunchSettings.tsx → InstanceSettings.tsx   剩下四段
```

### 4. 左栏四张卡片

每张卡头带一个 7px 色块，**和右栏命令行里对应片段的行首色块是同一个**——它不是装饰，
是图例键（见第 6 节）。

#### 卡 1 · Java 与核心

- **模式 segmented 提到卡头**，从现在两个 96px 高的大卡片（`LaunchSettings.tsx:564-600`）
  压成 32px。**只有两项**：`核心 jar` / `参数文件`。
- 卡身双列：`Java 环境` + `服务端 jar`，沿用现有 `field-row`。
- **`InstanceCorePicker` 从页面最底部挪到 `服务端 jar` 标签行右侧**，变成「从核心库安装…」
  链接触发的展开层。它是 jar 的动作，不是页脚的动作。
- 「目录下找到 N 个 jar」这句字段说明 → **移进右栏检查条目**（`jar-count`）。
- 参数文件模式下双列换成 `Java 环境` + `参数文件` textarea，`从启动脚本读参数…` 提到卡头。

#### 卡 2 · 内存

- 卡头右侧 **`锁定 Xms = Xmx` 开关**（新）。开着时改任一个另一个跟着走，中间显示 `=`。
- 右侧**宿主机内存分配条**（新，纯前端）：`其他实例 / 本实例 / 剩余` 三段 + 图例。
  - 数据源：宿主机总量取 `InstanceMetrics.memoryTotal`，其他实例取 `instances[]` 各自的
    `maxMemoryMB`。**两者都已经是 `InstanceView` 的现成 props，不用动后端。**
  - 它是**各实例配置的 Xmx 之和，不是实时占用**，所以图例文字写「已分配」，不写「已用」。
  - `metrics` 为 `null`（还没轮询到）时整条不渲染，不占位、不显示 0。
- argfile 模式下这张卡换成现有 `ArgFileMemory`，并明说「@file 模式面板不发 `-Xms/-Xmx`」
  （`config.go:343` 那条约束），右栏命令必须与之一致。

#### 卡 3 · JVM 参数

- 卡头：`N 项` 计数 + `从启动脚本读入…` + `卡片/文本` 切换（都已有，只是搬进卡头）。
- 三个预设**做出明确选中态**（打勾 + 描边）。`jvmPresets.ts:77` 里正好就是设计稿那三个，
  标签一字不差，只是现在只有「选中后才出现的一段说明」，看不出选了谁。
- 空状态从近 100px 的虚线框**压成一行**。
- `aikarNeedsEqualHeap` 那条 `.alert--warn`（`LaunchSettings.tsx:702`）**从卡内挪走**，
  变成右栏带修复按钮的 `heap-mismatch` 条目。

#### 卡 4 · 服务端参数

- **修掉「同一份数据显示两遍」**：`卡片/文本` 互斥切换，不再上下各一份。
- 卡片化，与 JVM 参数同构。**连带工作量**：`JVMArgsEditor` 架在 `jvmFlags.ts` 的 JVM 参数词表上
  （`parseFlag` / `knownFor` / `wantsUnit`），而服务端参数是 `--key value` 空格分隔，语法和词表都不同。
  所以要**把卡片外壳抽成共享组件，再配一份 `serverFlags.ts` 小词表**
  （`--nogui` / `--world-dir` / `--port` / `--forceUpgrade` 等，按 vanilla·Paper·Velocity 分）。
- 常用参数从常驻 chip 行改成一行链接。proxy 实例不显示——Velocity 遇到不认识的参数直接退出，
  现有说明里就写着。

### 5. 预览接口

```
POST /api/instances/:id/launch/preview
```

**请求**：这一页管的草稿字段（`java` / `jar` / `argFiles` / `minMemoryMB` / `maxMemoryMB` /
`jvmArgs` / `serverArgs` / `loader`）。

**编码和颜色不在请求里**——它们搬去 `实例设置` 了，这一页改不了，所以后端从 `inst.Config()`
读已保存值。

**响应**：

```jsonc
{
  "mode": "jar",
  "program": "/opt/java/temurin-25/bin/java",
  "segments": [
    { "origin": "panel",  "args": ["-Dfile.encoding=UTF-8", "…"] },
    { "origin": "memory", "args": ["-Xms2560M", "-Xmx2560M"] },
    { "origin": "jvm",    "args": ["-XX:+UseG1GC", "…"] },
    { "origin": "jar",    "args": ["-jar", "paper-26.1.2-74.jar"] },
    { "origin": "server", "args": ["--nogui"] }
  ],
  "issues": [ … ]
}
```

### 6. 防漂移：Go 侧只留一份拼装

这是「后端出接口」能不能兑现承诺的关键，也是整份文档里最重要的一条约束。

**把 `commandLine(tty)` 内部改成先建 `[]segment{origin, args}`、再压平**：

- 启动走压平结果，**行为完全不变**
- 预览走分段结果
- 一个 Go 测试断言 `flatten(segments)` 等于原 `commandLine` 的输出

这样不存在「两份拼装逻辑」，也就不存在漂移。argfile 模式不发 `-Xms/-Xmx` 这类分支天然继承，
不用在预览里重写一遍。

**一个必须在实现时解决的坑**：`commandLine(tty bool)` 注入的那组参数**取决于 `tty`**，
而 `tty` 是启动那一刻才定的。预览必须调用守护进程用的**同一个判定函数**，不能自己猜一个。
如果确实到 spawn 前不可知，那一组要在界面上明确标注为不确定。**这一条在 plan 里单列一项验证。**

### 7. 上色：色块做图例，命令文字保持单色

**不给命令行上五种颜色。** 理由：`--term-*` 只有 `blue` 和 `black` 两个色相，不是完整 ANSI 调色板；
而 `--term-selection` 在 `styles.css` 里出现在 **8 个主题块**里，新增 5 个令牌意味着 40 处改动，
漏一处就是暗色下的空值。CLAUDE.md 的「只有异常才上色」也不支持这么做。

改成：

- 每个 `origin` 一行，行首一个 7px 色块，**和左边对应卡片的卡头色块是同一个**
- 命令文字统一 `--term-fg`
- **唯一的例外是 `panel` 那组用 `--term-bright-black` 压暗**——它不是你填的，压暗正好读作
  「这个不归你改」，一个色相都不用新增

色块颜色全部取现有令牌（`--accent` / `--ok` / `--code-key` / `--code-number` 一类），
**不新增 `--term-*`**。

这比设计稿的五色方案更守规矩，也顺手解决了那句谎话——**面板注入的参数现在明明白白在预览里**，
那句「启动时用的就是它」才第一次成立。

**深色画布**：CLAUDE.md 规定 `--term-*`（服务器控制台）和 `--shell-*`（主机 shell）两块深色画布
必须一眼可分，这是防止把危险命令敲进错误终端的唯一屏障。命令预览会是第三块深色面，
**经确认仍做深色**：它只读、没有输入框，敲错的风险是单向的；且它描述的正是控制台里那个进程。
配色跟 `--term-*` 走，不另起一套。

### 8. 启动前检查

`LaunchIssue.level` 增加 `'ok'`——设计稿那三条绿色「满足」有真实价值：空面板说不出
「我替你看过了」。

| code | 级别 | 状态 |
| --- | --- | --- |
| `needs-setup` / `jar-unset` / `jar-missing` / `argfile-missing` / `unknown-loader` | 现有 | 不动 |
| `jar-count` | ok | 新，从左边字段说明搬来 |
| `heap-mismatch` | warn | 新，从 `aikarNeedsEqualHeap` 搬来，**带修复按钮** |
| `java-version` | ok / warn | 新，核心要求的 Java 版本 vs 选中的 Java |
| `port-conflict` | ok / warn | 新，读 `server.properties` 的端口，和其他实例比 |

**`port-conflict` 的边界**：端口字段属于 `服务器配置` 页，不属于这一页。这里**只读地跨页核对**，
不让这一页拥有端口这个字段，也不提供修改入口——发现冲突就指向 `服务器配置`。

**修复动作设计成通用的**：

```ts
fix?: { label: string; patch: Partial<StartupDraft> }
```

后端发 `{ label: "把 Xms 改成 2560", patch: { minMemoryMB: 2560 } }`，前端**无脑合并进草稿**。
前端不写任何按 `code` 分支的逻辑，以后加检查项不用动前端。

`StartupDraft` 是**这一页表单的字段集**（第 5 节请求体里那几个），要新起一个名字：
`types.ts:126` 的 `LaunchDraft` 已经被占用了，指的是「从别人的启动脚本里解析出来的草稿」，
是另一件事，不要复用。

## 数据流

### 保存契约

`handlers_instances.go` 的两条注释把话说死了：

> *"The settings page reads the whole config into a form and PUTs the whole thing back"*
> *"Present, not different: a client echoing the whole form back unchanged still has to hold the capability"*

而 `toConfig()`（`handlers_instances.go:72`）从请求**构造一个完整的 `instance.Config`——
缺席的字段直接取零值**。所以：

> **如果哪一页只 PUT 自己那部分字段，它会把另一页的所有字段清空。**
> 实例名变空串、`autoStart` 变 false、`stopCommand` 变空。

**规则定死**：两页都用 `toInput(instance)` 读全量配置，**各自只编辑自己那部分，PUT 时仍然发全量**。
不发部分 body，不碰后端语义。

两个诚实的后果：

1. **权限没有变细。** `namedLaunchFields(raw)`（`handlers_instances.go:249`）按**请求里出现的 key**
   判定，不看有没有改。`directory` 是 launchField 且留在 `实例设置`，所以**两页保存时都仍然需要
   `CapInstanceLaunch`**——和今天完全一样，不是退步，但拆页换不来更细的权限。
   真要那个得把 PUT 改成真 PATCH（指针字段 + 存在性判定），已列为非目标。
2. **两个标签页同时开着仍然是「后写的赢」。** 这个风险今天就存在，拆页既没加重也没减轻。

各页一套自己的 `dirty` 集合、一条自己的 `.cfg__savebar`、自己的「放弃」。

### 预览的请求节奏

- 输入停 **300ms** 后发，`AbortController` 掐掉在途请求；挂载时立刻发一次。
- 刷新期间**保留上一次的好结果并压暗**，绝不闪空——命令行闪烁比慢一拍糟得多。

## 错误处理

- **预览请求失败**：留住上一次结果 + 一句轻提示，**绝不拦住表单**。预览挂了不该让人存不了盘。
- **`metrics` 为 null**：内存分配条整条不渲染，不占位。
- **保存失败**：沿用现有 `.alert--error` 紧贴页头的写法，不新造。

## 布局与响应式

### 页面测量

`启动方式` 用**现成的 `.stack`**（实例 pane 里默认就是 1440 的瓦片测量），不加 `--narrow`。
**不引第四种测量**——`styles.css:3818` 那段注释记着「按卡片封顶试过，更糟」。
`实例设置` 继续用 `.stack--narrow`（880），它仍是从头到尾的表单。

### 两栏：flex-wrap，不加断点

```
左栏  flex: 1 1 420px;  min-width: 0
右栏  flex: 1 1 340px;  min-width: 0
```

装不下自动换行，右栏落到表单下面占满宽度。**一个断点都不用加**，符合「能用内在响应解决的
就不加断点」。

- 两栏都写 `min-width: 0`——skill 点名的本仓库最高频布局 bug。
- `position: sticky` 只在未换行时有意义，换行后无害，不用条件化。
- **命令预览自己套 `overflow-x: auto`**——长 Java 路径 + 二十个 Aikar 参数，
  390px 下绝不能撑破页面。
- 卡内双列字段用 `repeat(auto-fill, minmax(…, 1fr))`。
- **1024 那档完全不碰**，所以 `styles.css` 和 `App.tsx:59` 的 `DRAWER_QUERY`
  两处同步的约束不受影响。

### 保存条

复用 `.cfg__savebar`（`styles.css:8433`）+ `ConfigLayout.tsx:142` 的结构，只在有改动时出现，
现有的 `animation: rise` 直接沿用。**不造第二条保存条。**

设计稿保存条上的「查看差异」**改成跳 `配置历史` 的链接**——那已经是这件事的家，
再造一个 diff 视图就是第二套。

### 动效

保存条用已有的 `rise`；预览刷新时压暗是「原地变状态」，用 `--dur`(140ms)；
**不给命令行做逐字打字动画**——它是要被核对的东西，不是表演。

## 测试与验证

| 类型 | 内容 |
| --- | --- |
| 前端 | `npm --prefix web run build`（`tsc -b` + `check:ui`，前端唯一的自动检查） |
| 后端 | `make lint && make test` |
| 新 Go 测试 | ① **`flatten(segments)` 等于原 `commandLine` 输出**——防漂移的那条命 ② preview handler ③ 三个新 check code ④ `TestInstanceRequestFieldsAreClassified` 仍然过 |
| 人工 | 1440 / 1200 / 1024 / 768 / 390 × 明暗两种模式 |
| 人工 · 三处高危 | 折叠侧栏、打开抽屉、开着控制台的实例页 |
| 人工 · 长内容 | 很长的 Java 路径、二十个 Aikar 参数、很长的实例名，都不撑破容器 |
| 文档 | `CHANGELOG.md`「未发布」小节；`docs/design-system.md` 补右栏这个新形态 |

## 风险

| 风险 | 处置 |
| --- | --- |
| **预览与真实 argv 漂移** | 唯一真正的风险。用「Go 侧只留一份拼装 + flatten 等价测试」堵死；这条测试挂了就是预览在说谎。 |
| `tty` 在 spawn 前不可知 | plan 里单列一项验证；真不可知就在界面上标注该组为不确定。 |
| 两页各自发全量 PUT 互相覆盖 | 规则定死「读全量、改局部、发全量」；两标签页「后写的赢」是既有风险，不新增。 |
| 第三块深色画布与两个终端混淆 | 只读、无输入框，风险单向；配色跟 `--term-*` 走不另起一套。 |
| 服务端参数卡片化被低估 | 需要新的 `serverFlags.ts` 词表 + 卡片外壳抽共享，已在卡 4 里写明是独立工作量。 |
| 老书签落到不再有启动方式的页面 | `实例设置` 顶部 fatal-only 横幅 + 侧栏新入口；不做重定向。 |

## 文档

- `CHANGELOG.md`「未发布」：新增 `启动方式` 页、命令预览、启动前检查升级。
- `docs/design-system.md`：补「左表单 + 右对照」这个新的页面形态，说明它用 `.stack` 的瓦片测量
  而不是新测量，以及色块作图例键的用法。
