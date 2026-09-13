# Java 环境统一登记 与 核心变更闸门 设计文档

日期：2026-09-13
状态：已确认，待实施

## 背景

两个问题来自同一个观察：**实例创建之后，它的两个启动输入——Java 和服务端核心——都可以被改成面板一无所知的东西。**

### 现状一：Java 是一个裸字符串

`internal/instance/config.go:74`：

```go
Java string `json:"java"` // java executable, "java" resolves via PATH
```

`applyDefaults` 把它兜底成 `"java"`（`config.go:132`），`instance.go:507` 拿它直接当 argv[0] 并导出进环境变量。

面板界面上，`LaunchSettings.tsx:563` 已经是一个下拉框，选项有三类：

1. `系统 java（PATH）`——魔法字符串 `"java"`
2. 面板装的 runtime——来自 `javaruntime.Store.List()`
3. **`自定义路径…`**——`CUSTOM_JAVA` 哨兵（`LaunchSettings.tsx:60`），选中后是一个自由文本框

创建向导有同一个口子（`NewInstanceWizard.tsx:384`、`:467`）。

于是「统一管理」只成立了三分之一：面板知道第 2 类的版本号（`Runtime.Major`），对第 1 类和第 3 类一无所知。

**这不是有意为之的自由度，而是一处未完工。** `LaunchSettings.tsx:222` 的注释已经把正确的约定写下来了：

> Not Java and not the jar, even though the script names both. By the time an
> instance exists the panel owns those two — Java comes from 资源库 → Java 环境
> and the jar from the core library — and letting a run.sh from 2019 put
> /usr/lib/jvm/java-8 back would undo a choice made deliberately.

脚本导入已经在遵守这条约定了，但设置页自己没有。

### 现状二：实例不知道自己跑的是哪个核心

`InstanceCorePicker` 把库里的 jar **复制**进实例目录并设为启动 jar（`InstanceCorePicker.tsx:31` 的注释解释了为什么是复制而非共享路径）。复制之后，`Config` 里留下的只有：

- `Jar string`——一个文件名
- `Loader` / `GameVersion`——两个字段，`config.go:57` 的注释自己说明它们是「最后手段的提示」，且可能为空

核心库那边的 `ServerCore.usedBy`（`web/src/types.ts:923`）是**靠文件名反查**算出来的。

所以换核心这个动作目前完全没有约束：

- 不校验 `core.kind` 与 `Config.Kind` 是否一致——代理端 jar 可以被设成 server 实例的启动 jar
- 不看游戏版本方向，降级不提醒
- 不看 Java 版本够不够
- 实例运行中也能换（复制会覆盖 JVM 正在读的那个 jar）
- 换完 `Loader` / `GameVersion` 不更新——而认错 loader 的后果 `config.go:57` 写得很清楚：mod 装进 `plugins/`，插件市场里每一条都标「未知」

### 结论

**核心应该能改，Java 应该被登记。**

禁止换核心是错的：Paper 升构建、跟游戏版本升级、回滚一次炸掉的升级，都是周常运维。禁掉等于逼人删实例重建，世界和配置全要手搬——比「能改」危险得多。

真正的问题是**换的时候什么都不拦**。所以方向是加闸，不是关门。

Java 则相反：自由路径没有带来自由度，只带来了盲区。把它收进一张登记表，面板才有能力回答「这个核心要 Java 21，你选的是 17」。

## 不做什么

- **不实现实例目录备份。** 它是独立能力（换核心、升版本、装插件、手改配置都该用它），塞进换核心流程会让这个改动驮上一个更大的子系统，且以后做真备份时要推倒重写。
- **不实现跨核心迁移流程。** 面板不假装能把一个 Paper 服务器安全地变成 Fabric 服务器。跨 project 换核心直接挡掉并说明正确做法（新建实例迁世界）。
- **不给存量实例反推核心身份。** 不拿 `Loader` / `GameVersion` / 文件名去猜——用猜出来的身份驱动一道安全闸，比没有闸更糟。
- **不改启动路径。** 见下节。

## 一条被否决的设计

最自然的写法是：`Config.Java` 从路径改成登记记录的 id，启动时查表解析。

**否决。**

`instance.go:507` 现在拿 `cfg.Java` 直接当 argv[0]，配置里有什么就跑什么——没有解析、没有查表、没有失败模式。改成 id 之后，「登记表读不出来」「表里没这条 id 了」都变成**开不了服**，而这是这个项目里最不该脆弱的一条路径（CLAUDE.md：服务端进程属于守护进程，不属于任何一个请求或连接）。

所以：

> **`Config.Java` 继续存路径。登记表是一份白名单，在 API 写入实例配置时校验，不在启动时解析。**

「统一管理」管的是**人能选什么**，不是**进程怎么起**。约束长在写入口。

附带好处：登记表里删掉一条，正在跑的服务器不受任何影响——与 `InstanceCorePicker.tsx:31` 的「复制而非共享，库里删了不影响在跑的服务器」是同一条哲学，仓库里已有先例。

## 架构

### 组件边界

| 组件 | 职责 | 依赖 |
| --- | --- | --- |
| `javaruntime.Registry`（新） | 持有外部 Java 引用的登记表，增删查、探测、失效判定 | 文件系统、`probe` |
| `javaruntime.Store`（已有） | 扫 `<data>/java` 目录，列出面板装的 runtime | 文件系统 |
| 合并列表（新，`javaruntime` 内） | 把 Store 和 Registry 合成一个「这台机器上可用的 Java」列表 | 上两者 |
| `api` java 路由 | 登记 / 删除 / 重新检测；overview 返回合并列表 | `javaruntime` |
| `api` 实例写入 | 校验 `java` 在白名单内 | `javaruntime` 合并列表 |
| `api` `handleApplyCore` | 核心变更闸门 | `instance.Config`、核心库 |
| `api` `checkJarLaunch` | 新增 `java-too-old` 开服前检查 | `Config.Core.MinJava` + 合并列表的 `Major` |
| `instance` 包 | **不变**。不依赖 `javaruntime`，启动路径零改动 | — |

### 数据流

```
登记：  用户填路径 → POST /api/java/registry → probe 探版本 → 写 Registry
                                            ↘ 探不到 → 400，不写
选择：  实例设置下拉 ← GET /api/java（Store ∪ Registry）
保存：  PUT 实例 → java 有变化时校验 ∈ (Store ∪ Registry) → 落盘
                   ↘ 不在 → 400，指向 Java 环境页
启动：  cfg.Java → argv[0]（不查表，不解析）
```

## 第一部分：Java 登记表

### 数据模型

下载安装的 runtime 已由 `Store` 扫目录得到（`store.go:57`），**不进表**。登记表只装外部引用：

```go
// Entry is a Java the panel did not install: a path the operator pointed at.
type Entry struct {
    ID       string
    JavaPath string // absolute, or "java" to follow PATH
    Vendor   string
    Version  string
    Major    int
    AddedBy  string // manual | detected | migrated
    AddedAt  time.Time
}
```

持久化为 `<root>/java-registry.json`，新增 `config.Paths.JavaRegistryFile()`。

**放在 `JavaRoot()` 外面而不是里面**，沿用 `DatabasesFile()` 已经立下的理由（`config.go:296`）：登记表放进它所描述的那个目录里，条目 id 就可能跟目录名撞车。`Store.List()` 扫 `JavaRoot()` 下的目录，这样它连看都不会看到这个文件。

### 合并列表

`Store` 之上加一层，输出统一形状，每条带：

- `origin`：`managed`（面板安装）/ `external`（登记的路径）
- `valid`：List 时 `stat` 算出；`JavaPath == "java"` 走 `exec.LookPath`

字段叫 `origin` 而不是 `source`：`javaruntime` 包里已经有 `Source` 了——`SourceAuto` / `SourceOfficial` 是**下载镜像**（`source.go:26-29`），还有一个 `Source` 结构体类型。两个都叫 Source 的枚举，一个讲 CDN 一个讲这个 JDK 是不是面板自己解压的，会被当成同一个。

版本信息在**登记那一刻**用 `probe`（`store.go:261`，现在只服务 `DetectSystem`）探一次存下，之后不重探，除非用户点「重新检测」。

### 自动探测 + 人工确认

`GET /api/java` 的 overview 已经返回 `System *SystemJava`（`handlers_java.go:54`）。前端拿它与登记表比对：探到的路径不在表里，就显示一条「面板在 PATH 上发现了 Java 21，登记进来？」，点一下走 `POST /api/java/registry`。

**探测归探测，进表要点一下。** 列表里的每一条都是人为决定过的。

### 新增路由

```
POST   /api/java/registry              登记一条路径（探测失败 400 并说明原因）
DELETE /api/java/registry/{id}         删除；有实例在用时 409 + 实例列表
POST   /api/java/registry/{id}/probe   重新检测
```

权限沿用现有 java 路由的 `authz.CapPanelJava`（`routes.go:274-278`）。

删除规则**与 managed runtime 的删除完全一致**，复用 `handleDeleteJava` 的策略（`handlers_java.go`）：

- 有实例**正在运行**在它上面 → 409，指名是哪个实例
- 仅仅有已停实例指向它 → 允许删除，响应里列出受影响的实例，前端告知

不给两种删除编两套心智模型。留下的「空洞」是无害的——见下面的约束点：校验的是改动，不是状态，所以指向一个已删条目的实例照样能编辑、能启动（启动路径不查表）。

`usersOf`（`handlers_java.go`）现在按 `runtime.Path` 前缀匹配，因为 managed runtime 是一棵目录树。登记条目没有目录树，**按 `JavaPath` 精确匹配**，需要给它加一条分支。

### 存量迁移

面板启动、实例加载完之后跑一次：

1. 收集所有 `cfg.Java` 去重
2. 凡是不在（Store 目录 ∪ 登记表）里的，逐个 `probe` 并写入登记表，`AddedBy = migrated`
3. 探不出版本的**照样写入**，标失效，原样保留路径

第 3 条沿用 `migrate.go` 开头立的规矩（拆不出来的就标记，不猜）：一条标着「认不出版本」的记录，好过一条被面板编出来的版本号。

`"java"` 保留成合法记录（`JavaPath: "java"`，跟随 PATH），**不解析成绝对路径**——有人就是故意要跟随 PATH 的，钉死是替用户做了个他没要的决定。

`migrate.go:109`（退役脚本迁移）会写 `cfg.Java`，但它跑在登记表建立之前，本迁移会把它写进去的任何值自动登记。两者不冲突。

### 约束点

只有一处：`handleCreateInstance` 和 `handleUpdateInstance`。

**校验的是改动，不是状态：**

> body 里带了 `java`、trim 后非空、**且与服务端当前存的值不同**时，才校验它在合并列表内。不在则 400，消息指向 Java 环境页。

「不是状态」这一点是必须的。设置页把整份 config 读进表单（`LaunchSettings.tsx:42`）后整份 PUT 回来，所以一个 `java` 值不在白名单里的存量实例——迁移时探测失败的，或者 managed runtime 被删之后的——**会连改名字都做不到**。那是个荒唐的失败。

这跟同一个 handler 里权限检查的规则**故意不同**，且有根据。`handlers_instances.go:203` 解释权限为什么用「present, not different」：

> Present, not different: a client echoing the whole form back unchanged still
> has to hold the capability, because "unchanged" is a claim the server would
> have to take the client's word for.

权限那条成立，是因为服务端无法独立判断「没变」。白名单这条不受这个限制——**服务端手里就有当前 config，自己比对，不听客户端一面之词。** 实现时必须是服务端比对，不能新增一个 `javaChanged` 之类的请求字段。

`java` 缺省或为空时**不校验**，交给 `applyDefaults`（`config.go:132`）兜底成 `"java"`。那表示「我没选」，保持历史行为；创建向导在改造后总会送一个明确的值。

实例导入（`ImportInstanceDialog.tsx:150`）走同一个创建入口，自动受约束。

## 第二部分：核心变更闸门

### 实例记住自己的核心

`Config` 加字段，由 `applyCore` 写入：

```go
// CoreRef identifies the library entry this instance's jar came from, so a
// later swap can tell "newer build of the same thing" from "a different
// server entirely". Nil means nobody knows: a hand-dropped jar, an imported
// instance, or anything created before this field existed.
type CoreRef struct {
    Project string // paper, purpur, velocity, …
    Version string // game version
    Build   int
    SHA256  string
    MinJava int // upstream's minimum Java; 0 when unknown
}
```

挂在 `Config` 上：

```go
Core *CoreRef `json:"core,omitempty"`
```

**`MinJava` 是白捡的**：`handlers_downloads_test.go:33` 的 fixture 显示上游 version 数据里带 `"java":{"version":{"minimum":21}}`。不用自己维护「1.20.5+ 要 Java 21」的映射表——上游给了，下载时存下来即可。手动导入的 jar（`ServerCore.imported`）拿不到，就是 0，不检查。

### `Core == nil` 的处理

存量实例全部为 `nil`。**不卡死**：换核心放行，但提示「面板不知道这个实例现在跑的是什么，换完请自己确认」。换完 `Core` 就有值，从此受闸约束。渐进收紧。

### 闸拦什么

`handleApplyCore` 判定，前端据同一套规则禁用选项——**服务端说了算，前端只是不让你白点**。

硬拦（400）：

| 情况 | 为什么 |
| --- | --- |
| `core.kind` 与 `cfg.Kind` 不符 | 代理端 jar 设成 server 实例的启动 jar。`Kind` 一直都有，对存量实例也生效。 |
| 实例正在运行（`State.Running()`） | 不只是「换了要重启」——复制会覆盖 JVM 正在读的 jar。运行中整个 `applyCore` 都拦，不只是 `setAsJar`。 |
| 跨 project（`Core != nil` 且 project 不同） | 消息说清楚这是另一种服务端、正确做法是新建实例迁世界。 |

二次确认后放行：

- **游戏版本降级**（`core.version` 低于 `Core.Version`）。世界数据向下不兼容，但这是正当操作——回滚一个炸了的升级。

  版本比较按 Minecraft 版本号的点分数字段逐段比（`1.20.4` < `1.21`，**不是字符串比较**）。任一侧解析不出来（快照、`imported` 的 jar）就**不判定为降级**，直接放行——这里宁可漏报也不要误拦，误拦会挡住一次正当的回滚。

### Java 版本检查放在开服前检查，不放在 applyCore

`handlers_launch.go` 已有 `launchIssue{Level, Code, Message}` 框架（`:36`），新增一条：

```
Code:  "java-too-old"
Level: fatal
这个核心要求 Java 21，当前选的是 Java 17。去启动方式里换一个。
```

数据来源：`Config.Core.MinJava` 对合并列表中该 `JavaPath` 对应记录的 `Major`。两者任一为 0 / 未知则不报。

**理由是解耦。** 用户点「换核心」的下一步很可能就是去换 Java；在复制这一步拦住他，是让两个本来独立的设置互相卡住。而「这个实例现在能不能开起来」正是开服前检查在回答的问题——`checkLoaderKnown`（`handlers_launch.go:130`）已经是同样形状的一条。

### 换完之后

`applyCore` 成功后同时更新 `Loader` / `GameVersion`，消除 `checkLoaderKnown` 报的 `unknown-loader` 警告。

### 一个故意留着的逃生口

`LaunchSettings` 里手填 `jar` 文件名的字段保留，但手填之后 **`Core` 清空**——面板不再知道那是什么，于是 `java-too-old` 也不再报。

这是诚实的代价：绕过登记，面板就不再做它保证不了的判断。比假装还知道要好。

## 第三部分：前端

**实现这部分之前必须先调用 `frontend-design` skill**（`.claude/skills/frontend-design/SKILL.md`）。本文档只定「要改什么」，不定「怎么排版」。

### Java 环境页（`JavaPage.tsx`）

从「面板装了什么」变成「这台机器上哪些 Java 可用」：

- 列表合并两个来源，每条标来源（面板安装 / 本机路径）、版本、**「N 个实例在用」**
- 新增「添加本机 Java」：填路径 → 探测 → 探到了显示版本再登记；探不到报错说明原因，**不提供「强行添加」**
- PATH 探测结果不在表里时，顶部一条「发现 Java 21 在 PATH 上，登记进来？」
- 删除时若有实例在用：拒绝并列出是哪几个

### 实例设置（`LaunchSettings.tsx`）

- 删除 `CUSTOM_JAVA` 哨兵（`:60`）、`customJava` state（`:91`）、`showCustomJava` 分支（`:444`）及自由文本框（`:589-594`）
- 下拉每条带版本和来源；当前指向失效记录时，该条仍显示并标「路径已失效」
- 底部指路文案（`:599`）改成唯一入口的措辞
- `InstanceCorePicker`：kind 不符与跨 project 的选项 disabled，各带一行说明；实例运行中整个折叠区禁用

### 创建向导（`NewInstanceWizard.tsx`）

删掉自由输入（`:384`、`:467`）之后会撞上一个真实场景：**全新安装时登记表是空的。**

让新用户先跳去 Java 环境页装一个再回来重走向导，是很差的第一印象。所以：

- 列表为空但 PATH 上探到了 Java 时，给一个「用这个（Java 21），同时登记进面板」的按钮——**一步完成登记 + 选择**
- 探不到才引导去 Java 环境页

### 样式

全部进 `web/src/styles.css`，只用令牌，不新增页面框。按 `docs/design-system.md` 的「只有异常才上色」：**只有失效状态上色**，正常条目不上色。

## 错误处理

| 场景 | 行为 |
| --- | --- |
| 登记时 probe 失败（路径不存在 / 不是 java / 超时） | 400，消息区分三种原因，不写表 |
| 登记表文件损坏 | 记日志，当成空表继续，**不阻止面板启动**；下次写入时重建 |
| 登记的路径后来失效（JDK 被卸载） | 列表里标失效；实例照跑（启动路径不查表）；开服前检查报 fatal |
| 删除时有实例在用 | 409 + 实例列表，不删 |
| 保存实例时 java 不在白名单 | 400，指向 Java 环境页 |
| 迁移时 probe 失败 | 写入并标失效，保留原路径 |
| `applyCore` 撞上硬拦 | 400，消息说明原因和正确做法 |
| `applyCore` 降级未确认 | 409，body 带 `{"code": "needs-confirm"}`，前端据此弹二次确认并重发带 `confirm: true` 的请求 |

## 测试

Go 侧按 TDD 先写测试（`.claude/skills/test-driven-development`）：

**`javaruntime`**
- 登记表增删查
- `probe` 失败的各种原因
- 失效判定，含 `"java"` 走 `LookPath`
- 合并列表：同一路径同时出现在目录和登记表时不重复
- 迁移：表空 + 实例带裸路径 → 自动登记；已在表里 → 不重复；探不出版本 → 写入并标失效
- 登记表文件损坏 → 当空表处理

**`api`**
- 写入实例配置时拒绝未登记的 `java`
- `applyCore` 三条硬拦各一个用例
- `applyCore` 降级需确认
- `applyCore` 成功后 `Core` / `Loader` / `GameVersion` 被写入
- 手填 `jar` 后 `Core` 被清空
- `checkJarLaunch` 的 `java-too-old`：报 / 不报（`MinJava == 0`、`Major` 未知）各一例

**前端**：无单测，`tsc -b` 是唯一自动检查。样式人工确认：1440 / 1200 / 1024 / 768 / 390 宽度 × 明暗两种模式，外加「折叠侧栏、打开抽屉、开着控制台的实例页」三处回归。

**CHANGELOG**：用户可见的行为变化写进「未发布」小节。

## 分期

两个阶段可独立合并：

**一期：Java 登记表**
`javaruntime.Registry` + 合并列表 + 三个路由 + 启动时迁移 + 实例写入校验 + 三处前端。
合进去之后「Java 只能从面板登记过的里面选」这条即成立。

**二期：核心闸门**
`CoreRef` + `applyCore` 的硬拦与降级确认 + 换完更新 `Loader` / `GameVersion` + `InstanceCorePicker` 的禁用态，最后补 `java-too-old` 开服前检查。

`java-too-old` 需要一期的 `Major` 和二期的 `MinJava` 同时在场，所以放在二期末尾。
