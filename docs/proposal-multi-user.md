# 提案：多用户与角色权限

> **状态：全部实现。** 账号、角色、能力鉴权、实例授权、目录限制，以及
> 「面板设置 → 账号与角色」这一页。这一页从提案变成了记录：每一节末尾写的是实际做成什么样，
> 以及实现过程中改掉的结论。

- [要解决的问题](#要解决的问题)
- [先说清楚：哪些边界是真的](#先说清楚哪些边界是真的)
- [模型：能力固定，角色自由](#模型能力固定角色自由)
- [能力词表](#能力词表)
- [数据模型](#数据模型)
- [鉴权链路的改动](#鉴权链路的改动)
- [目录白名单](#目录白名单)
- [明确不做的事](#明确不做的事)
- [落地顺序](#落地顺序)
- [未决问题](#未决问题)

## 要解决的问题

一个服主带团队开服时，典型的分工是：

- **服主 / 系统管理员** —— 管整个面板。
- **运维** —— 开关机、看控制台、处理日常故障，不碰插件和配置。
- **开发** —— 对自己负责的实例有完整控制权，装插件、改配置、传文件。
- **更细的授权** —— 比如「只让他管这一个插件的配置目录」。

面板现在给不出这些，只有一份凭据（`internal/config/config.go:117`），谁拿到谁就是服主。

目标是让上面四类分工能在面板里表达出来，**并且对每一类都如实说明它到底挡住了什么**。第二点比第一点重要：
一个看起来像隔离、实际上不是的权限系统，比没有权限系统更危险。

## 先说清楚：哪些边界是真的

### 实例进程和面板是同一个系统用户

`internal/instance/process_unix.go:20` 启动服务端时只设了 `Setpgid`，**没有降权**。全仓库唯一降权的地方是
`internal/dbruntime/runas_unix.go:104`，而那是因为 MySQL 拒绝以 root 启动，不是安全设计。

于是只要能让服务端 JVM 执行代码，就等于拿到了面板那个系统用户：

- `panel.json` 是 `0600`，但**属主正是这个用户** —— 密码哈希、GitHub 令牌全在里面。
- `databases.json` 存的是**明文**数据库密码（见 `store.SaveDatabases` 的注释）。
- 其他所有实例的目录，随便读写。

### 三条通向代码执行的路

| 入口 | 为什么 |
| --- | --- |
| 往 `plugins/` 传 jar | JVM 加载即执行 |
| 改实例启动参数 | `instance.Config.Command` 是「full argv; when set, the fields above are ignored」（`internal/instance/config.go:62`），`JVMArgs` 同理 |
| 改实例目录 | `instanceRequest.Directory` 在**创建和修改**两条路由上都可写（`internal/api/handlers_instances.go:15`），指到 `/` 就是全盘文件管理器 |

后两条在同一条路由 `PUT /api/instances/{id}` 上，和「改个名字」「开自动重启」混在一起。

### 结论

| 角色 | 是安全边界吗 | 实际挡住的东西 |
| --- | --- | --- |
| 服主 | — | 全权 |
| 运维 | ✅ 是 | 挡住宿主机。**挡不住游戏内** —— 控制台能 `/op` 任何人 |
| 开发 | ❌ 不是 | 等价于宿主机代码执行。它防的是误操作和职责混乱，不是防人 |
| 目录级授权 | ✅ 是（只读写文件的前提下） | 见[目录白名单](#目录白名单) |

还有一条横向的：**任何能读某实例配置的人，都能读到该实例的凭据** —— rcon 密码、bot token、插件里的数据库
账号。配置历史默认把它们打码（`internal/confighist/mask.go`），但点一下就能看，这是刻意的设计。所以
`instance:config` 和 `instance:confighist` 的读权限要按「能看到这个服的全部密码」来授予。

真隔离需要每个实例跑在独立系统用户下。`dbruntime` 已经演示了做法，但文件属主、上传落盘、日志读取全都要
跟着改，是另一个量级的工程。**本提案不做**，只在文档里把话说清楚。

## 模型：能力固定，角色自由

两种常见做法都不选：

- **固定角色**（内置三个，写死）—— 今天能说出三类，明年就会有第四类（「只管代理端不管子服」）。
- **自由 ACL**（用户定义权限语义）—— 无底洞，且守不住。

取中间：**面板定义一份封闭的能力词表，角色只是给能力集合起个名字。**

```
用户 ──► 角色 ──► 能力集合（面板定义，用户不可增删）
  └────► 实例授权（这个用户能碰哪些实例）
```

三个概念各管一件事：

- **能力（capability）** —— 代码里的常量，每条 API 路由声明自己要哪一条。用户改不了。
- **角色（role）** —— 能力的命名组合，存在 `users.json`，管理员随便增删改。出厂带三个预设。
- **实例授权（scope）** —— 和角色正交。角色说「能做什么」，授权说「对哪些实例」。

好处是词表有限、可穷举、可测试；而「让用户自己组合」的成本几乎为零。

## 能力词表

⚠️ 标记的是**授予它约等于授予管理员**。UI 上这些要单独分组并明确警告，不能和普通能力混在一个列表里。

### 实例级（受实例授权约束）

| 能力 | 覆盖的路由 |
| --- | --- |
| `instance:view` | `GET /api/instances`、`GET .../{id}`、`/logs`、`/metrics`、`/properties`、`/configs`、`/velocity`、`/eula`，以及控制台 WebSocket 的**输出方向** |
| `instance:power` | `/start` `/stop` `/restart` `/kill` |
| `instance:console` | `POST .../{id}/command`，以及控制台 WebSocket 的**输入方向**（= 游戏内最高权限） |
| `instance:config` | `PUT /properties`、`PUT /configs/{file}`、`PUT /velocity`、`POST /eula` |
| `instance:confighist` | 全部 `/config-history/*`（含 `/restore`，它只能还原配置文件） |
| `instance:files:read` | `GET /files`、`/files/content`、`/files/download`、`/files/schematic` |
| `instance:schematics` | `POST /api/instances/{id}/schematics` |
| `instance:settings` | `PUT /api/instances/{id}` 的**安全字段**：名称、编码、TTY、自动启动 / 重启、停服命令与超时 |
| ⚠️ `instance:files:write` | `PUT /files/content`、`/upload`、`/mkdir`、`/rename`、`DELETE /files` |
| ⚠️ `instance:plugins` | 全部 `/api/instances/{id}/plugins*` |
| ⚠️ `instance:launch` | `PUT /api/instances/{id}` 的**危险字段**：`directory` `java` `jar` `jvmArgs` `serverArgs` `command`；以及 `POST /api/instances/{id}/core` |
| `instance:delete` | `DELETE /api/instances/{id}` |

`instance:settings` 和 `instance:launch` 落在同一条路由上，靠**字段级**区分：缺 `instance:launch` 时，
请求里带了那六个字段之一就直接 400。这是唯一一处字段级检查，字段集合由面板定义且封闭 —— 和
[明确不做的事](#明确不做的事) 里拒绝的「插件配置项级 ACL」不是一回事。

### 面板级

| 能力 | 覆盖的路由 |
| --- | --- |
| `panel:system` | `GET /api/system` |
| `panel:security` | `GET /api/auth/events`（登录记录） |
| `library:cores` | `/api/cores*`、`/api/downloads/projects*` |
| `library:plugins` | `/api/plugins*`（面板级插件库，不含 `/config/*`） |
| `library:schematics` | `/api/schematics*` |
| `panel:java` | `/api/java*` |
| `panel:network` | `/api/network/*`（会同时改代理端和子服的配置文件） |
| ⚠️ `panel:instances:create` | `POST /api/instances`（可选任意宿主机目录） |
| ⚠️ `panel:databases` | `/api/databases*`（明文密码） |
| ⚠️ `panel:terminal` | `/api/terminal*`（宿主机 shell） |
| ⚠️ `panel:hostfs` | `/api/fs`、`/api/fs/inspect`（全盘浏览） |
| ⚠️ `panel:update` | `/api/update*`（换二进制） |
| ⚠️ `panel:settings` | `/api/plugins/config/*`（GitHub 令牌、镜像） |
| ⚠️ `panel:users` | 新增的用户与角色管理路由（能给自己加权限 = 能变成管理员） |

### 不需要能力的基线

每个登录用户天然可做，不出现在角色编辑器里：`GET /api/auth/me`、`POST /api/auth/logout`、
改**自己**的密码、管理**自己**的设备（`GET`/`DELETE /api/auth/devices`，按属主过滤）。

这些路由在表里写作 `authz.CapSignedIn`。它**故意不在词表里** —— `Valid()` 不认它，角色编辑器
列不出它，所以它永远不可能被授予。写它是一次明确的声明（「这条是所有人的」），而不是把能力字段
留空；留空会被测试判红。

### 一条路由，多条能力

实现时发现「一条路由一条能力」这个说法不够用：有些路由同时跨了两条边界，只声明一条就等于漏掉另一条。
规则改成**跨了几条边界就声明几条，全部必须持有**：

| 路由 | 为什么是两条 |
| --- | --- |
| `POST /api/instances/{id}/core` | 写实例的启动配置 + 读核心库 |
| `POST /api/instances/{id}/plugins/library` | 读实例插件 + **写共享库** |
| `POST /api/plugins/bulk/upgrade` | 读共享库 + 写多个实例的插件 |
| `POST /api/schematics/{id}/install` | 读建筑库 + 写实例 |
| `POST /api/instances/{id}/schematics` | 读实例 + 写建筑库 |
| `POST .../config-history/restore` | 配置历史 + **等价于编辑配置**（还原就是写配置文件） |

### 实例不在路径里的那些地方

**实现第 3 步时发现这个清单严重低估了。** 提案原本点名 5 条路由，实际有 **16 处** 跨实例枚举
（`s.mgr.List()`）—— 因为泄露不在 `/api/instances/{id}` 这种明显的地方，而在聚合视图里：
「哪些实例在用这个 Java 运行时」「哪些实例的这个插件过期了」「哪些端口被占了」。这些读起来像报表，
不像访问控制，所以最容易漏。

解决办法不是把 16 处都记住，而是**让这个选择无法回避**：`mgr.List()` 不再允许出现在 handler 里，
所有调用都走 `scope.go` 的两个具名函数之一，`TestHandlersDoNotListInstancesDirectly` 用 AST
扫描守着这条规则。

- `visibleInstances(r)` —— 给调用者看的列表，按授权过滤。
- `allInstances()` —— 必须看到全部的检查。删一个还在被使用的 Java 运行时、把已属于某实例的目录
  再分配出去、两个实例抢同一端口 —— 这类检查如果只看得见调用者自己的实例，结果是一个人能悄悄
  搞坏另一个人的服务器。代价是拒绝信息里可能出现看不到的实例名，这是值得的交换。

选哪一个是一次要写下来的判断，每个调用点都留了注释说明理由。

### 两条铁律

1. 内置 `admin` 角色不可编辑、不可删除，其能力集合恒等于全集 —— 新增能力时它自动包含。
2. 系统里必须至少保留一个启用中的 admin 账号。删除 / 降级最后一个 admin 直接拒绝，否则会把自己锁在外面。

### 出厂预设

| 预设 | 能力 | 实例范围 |
| --- | --- | --- |
| 服主 | `admin`（内置全集） | 全部 |
| 运维 | `instance:view` `instance:power` `instance:console` `instance:confighist` `panel:system` | 授权的实例 |
| 开发 | 运维全部 + `library:cores` `library:plugins` `library:schematics` `instance:config` `instance:files:read` `instance:schematics` `instance:settings` ⚠️`instance:files:write` ⚠️`instance:plugins` ⚠️`instance:launch` | 授权的实例 |

「开发」带 ⚠️`instance:launch` 是定下来的：开发确实要调 JVM 参数，挡住它只会逼人每次去找服主，
而这个角色本来就不是安全边界（见[上文](#结论)）。靠角色编辑器里的警告兜。

三条 `library:*` 是映射路由时才发现要加的：装插件得先让插件进库，换核心得先能列出核心库，
所以「开发」离不开共享库的读写。代价是共享库的改动对所有人可见 —— 见[未决问题](#未决问题) 3。

预设只是初始值，建好之后就是普通角色，管理员可以改。

## 数据模型

新增 `users.json`，与 `instances.json`、`databases.json` 同级，同样 `0600`（它存密码哈希）。

```json
{
  "users": [
    {
      "id": "u_7f3a…",
      "username": "carlos",
      "displayName": "服主",
      "roleId": "admin",
      "credential": { "salt": "…", "hash": "…", "iterations": 210000 },
      "instances": null,
      "disabled": false,
      "createdAt": "2026-09-11T10:00:00Z"
    },
    {
      "id": "u_91c2…",
      "username": "xiaoming",
      "roleId": "r_ops",
      "credential": { "…": "…" },
      "instances": ["inst_survival", "inst_lobby"]
    }
  ],
  "roles": [
    { "id": "r_ops", "name": "运维", "capabilities": ["instance:view", "instance:power", "…"] },
    { "id": "r_dev", "name": "开发", "capabilities": ["…"], "paths": ["plugins/MyPlugin/"] }
  ]
}
```

几处刻意的选择：

- **`instances: null` 表示「全部」**，空数组表示「一个都没有」。第 2 步刻意没有先把这个字段放
  进去（一个存在但不生效的授权字段，会让管理员以为自己限制了范围），第 3 步连同鉴权一起加。
  Go 的 nil slice 和空 slice 在 JSON 里天然是 `null` 和 `[]`，所以不需要指针 —— 但**不能加
  `omitempty`**，否则两者都会消失，读回来都变成「全部」。
- **`paths`（目录白名单）还没做。** 见[目录白名单](#目录白名单)。
- **`id` 和 `username` 分开。** 改名不能让设备令牌和实例授权失效 —— 参考 `GitHubToken.ID` 的注释。
- **角色是 id 引用而不是内嵌。** 改一次角色，所有用这个角色的账号立刻跟着变，这正是角色存在的意义。
- **`credential` 复用现有的 `auth.Credential`**，PBKDF2 参数和校验逻辑一行都不用动。

### 迁移

**已实现**，在 `cmd/hypercraft/main.go` 的 `openAccounts` 里。`config.Panel.Credential` 变成
`users.json` 里的第一个用户，角色 `admin`；`config.Panel.Devices` 里的设备加上 `userId`，全部
归给这个 admin。第一次启动的新面板走同一条路，区别只在凭据是新生成的还是捡来的。

**写入顺序是有意义的**：先写 `users.json`，成功之后才清空 `panel.json` 里的老凭据。中间挂掉的话，
下次启动看到 `users.json` 已存在就直接跳过迁移 —— `panel.json` 里那份过期凭据从此不再被读取，
难看但不是一条进门的路。反过来先清空的话，两个文件之间就有一个窗口，崩在那里等于把自己锁在外面。

降级仍然会丢：`panel.json` 里的凭据已经空了，旧版本会当成首次启动重新生成一份。

## 鉴权链路的改动

### 好消息：缝已经留好了

- `auth.Session` 本来就带 `Username`（`internal/auth/auth.go:108`），会话层天生多用户，一行不改。
- `principal` 已经贯穿所有 handler（`internal/api/handlers_auth.go:23`，`principalFrom(ctx)`），
  加权限只需往这个结构上挂字段，handler 签名不动。
- CSRF、限速、登录记录都在 principal 之外，不受影响。

### 每条路由都要声明能力

**已实现。** 路由从一串 `mux.HandleFunc` 调用改成了 `internal/api/routes.go` 里的两张表
（公开的和受保护的），`s.routes()` 只剩一个把表注册上去的循环。第 3 步在这里插入 `requireCap`，
位置是 `requireAuth` 之后、`requireCSRF` 之前。

配套四道守卫（`routes_test.go`），针对的都是「看起来没问题的代码里的静默越权口子」：

| 守卫 | 拦住的事 |
| --- | --- |
| `TestProtectedRoutesDeclareCapabilities` | 新路由忘了声明能力 |
| `TestPublicRoutesArePinned` | 往门外偷加路由（公开集合被钉死成三条） |
| `TestEveryCapabilityIsReachable` | 词表里有条能力没有任何路由要求 —— 授予它等于什么都没授予 |
| `TestRoutesRegistersOnlyFromTheTables` | 绕过表，直接往 mux 上挂路由 |

最后一道读的是 `server.go` 的 AST，因为 `net/http` 没有枚举 `ServeMux` 内容的办法：任何注册都得
带一个模式字面量，所以断言 `routes()` 里除了两个挂载点之外没有别的字符串常量。四种情况都实际
制造过一遍，确认会红 —— 不会失败的测试等于没有测试。

### 唯一的例外：控制台 WebSocket

`GET /api/instances/{id}/console` 一条路由承载两个方向：既推送输出，也接受 `{"type":"command"}`
（`internal/api/ws.go:57`、`:250`）。所以它要声明 `instance:view`，并在 `:250` 那个 `case "command"`
里**再查一次** `instance:console`；缺这条能力就把连接降级成只读，而不是拒绝握手 —— 运维看得到输出，
只是敲不进去。

这个例外必须写在词表旁边，否则下一个人会以为路由级检查就够了。

### 实例作用域

所有 `/api/instances/{id}/*` 在 `requireCap` 之后多一道 `requireInstance`：`instances` 为 `null` 放行，
否则查列表。`GET /api/instances`（`handlers_instances.go:75`）改成按授权过滤。

**未授权的实例返回 404 而不是 403** —— 403 等于告诉对方「这个 id 存在」。

### 设备令牌

`server.go:500` 现在把 `s.credential().Username` 贴到 principal 上，改成从 `DeviceToken.UserID` 查用户，
能力和作用域跟着那个用户走。配对接口 `POST /api/auth/devices` 校验哪个用户的密码，令牌就属于哪个用户。

### 改密码不再是核弹

`handlers_auth.go:280` 现在一次 `RevokeAll()` 撤销所有人的会话和设备。改成只撤销**自己**的。

管理员另外需要一个「强制某用户下线」的动作（撤销该用户的全部会话与设备），归 `panel:users`。

### 前端

`api.me()` 已经返回 `User` 类型（`web/src/api.ts:137`），加上 `capabilities: string[]` 和
`instances: string[] | null` 即可。然后：

- 一个 `useCan('instance:plugins')`，**不要**在 31 个组件里散着写 `if (role === 'admin')`。
- 侧栏和导航按能力过滤。`web/src/routes.ts` 已经把路由收敛成一个带类型的值，过滤在一处完成。
- 新增用户 / 角色管理页，挂在面板设置下。

前端工作量大于后端，因为按钮散落在 31 个组件里（`web/src/components/` 下 import `api` 的数量）。

## 目录白名单

**已实现。** 落在 `internal/serverfiles/scope.go`：`Browser.Restrict(prefixes)` 返回一个受限的
浏览器，所有方法的路径都过 `b.resolve`（或 `b.resolveDir`，给那两个需要走到范围的方法），
`TestEveryMethodResolvesThroughScope` 用 AST 守着这条规则 —— 新加一个方法直接调 `clean`，
限制就对它不生效，而且它看起来和旁边那个一模一样。

实现时多做了两件设计里没写的：**通往范围的上级目录可以浏览但只显示那一条路**（不然文件管理器
走不到被授权的目录），以及**列表按条目标出可写性**（`plugins/` 本身不在范围内，它那一行的删除
按钮不该是活的）。

另一件事定了下来：**目录限制只管文件管理器**。配置编辑、建筑导入、换核心、代理连线各有各的能力，
`internal/api` 里分成 `browserFor(r, inst)`（受限）和 `unconfinedBrowser(dir)`（不受限）两个具名
入口，`TestFileRoutesUseTheConfinedBrowser` 守着「不许直接 `serverfiles.New`」。

下面是原来的设计。

「只让他改这一个插件的目录」是可以做的，而且很便宜：`internal/serverfiles/browser.go` 里所有读写都过同一个
`Browser`，路径已经被 `clean()` 归一成相对路径（`:82`），`os.Root` 负责兜底防穿越（`:65`）。

角色上加一个 `paths: ["plugins/MyPlugin/"]`，在 `Browser` 外面包一层前缀检查即可，`instance:files:read`
和 `instance:files:write` 都受它约束。空表示不限制。

注意这道边界只在「这个人没有其他代码执行能力」时才成立 —— 给了 `instance:plugins` 或 `instance:launch`，
目录白名单就形同虚设。UI 上要在同时勾选时给出提示。

## 明确不做的事

- **插件配置项级的权限**（「只能改 config.yml 里的这三个键」）。要求面板理解任意插件的 YAML 语义，
  写法千奇百怪，插件一升级字段就变，而且守不住 —— 改这个键和改那个键落到磁盘上是同一次写入。
  目录级已经覆盖绝大多数真实诉求，剩下的用配置历史 + 回滚兜底。
- **每实例独立系统用户**。见[上文](#先说清楚哪些边界是真的)，本提案的前提是不做这个。
- **LDAP / OIDC / SSO**。自托管面板，两位数用户量，不值得。
- **权限继承、角色嵌套、否定规则**（`deny` 覆盖 `allow`）。能力集合是并集，就这样。
- **审计日志落盘**。现有的登录记录仍然只在内存里（见 `docs/security.md`），多用户不改变这一点；
  需要长期留存仍然看系统日志。

## 落地顺序

1. ~~**能力词表 + 路由声明 + 那个强制测试。**~~ **已完成。** `internal/authz` 是 26 条能力的词表，
   `internal/api/routes.go` 把 136 条路由声明成一张表，`routes_test.go` 有四道守卫：漏声明能力、
   偷加公开路由、某条能力没有任何路由要求、绕过表直接往 mux 上挂路由 —— 四种都验证过会红。
   行为零变化：改动前后注册的 136 条路由逐字一致。
2. ~~**`users.json` + 迁移 + 用户 / 角色的增删改查 API。**~~ **已完成，并且把原定第 3 步的
   `requireCap` 一起做了。**

   原因值得记下来：按原计划，第 2 步单独合入会是一个**故意留下的洞** —— 管理员建一个「运维」
   账号，对方登进来，而鉴权还没做，于是他拿到整个面板，包括本机终端。能建账号和能拦住账号，
   必须同时到位。所以这一步实际包含：`internal/users`（账号、角色、校验、预设）、`users.json`
   的读写与迁移、登录 / 改密码 / 配对改走注册表、`requireCap` 接进链路、11 条用户与角色路由。

   **实例范围（`requireInstance`）刻意没做，字段也刻意没加** —— 理由同上，反过来说：一个存在
   但不生效的「只管这几个服」，比没有这个功能更危险。

3. ~~**实例范围。**~~ **已完成。** `instances` 字段、`requireInstance`（按路由模式自动套用，
   未授权返回 404 而非 403）、`PUT /api/instances/{id}` 的字段级检查、控制台 WebSocket 的
   双向检查、16 处跨实例枚举的分类，以及两个新守卫：
   `TestHandlersDoNotListInstancesDirectly`（AST 扫描，禁止 handler 直接枚举实例）和
   `TestBodyScopedRequestTypesArePinned`（钉住带 `instanceId` 的类型，新增一个就要分类）。

   两处**明确不过滤**并记进了文档：代理连线（面板级能力，半张图更容易让人配错）和必须看到全部
   实例的安全检查。

4. ~~**前端。**~~ **已完成。** `useCan()` 是一个 context（能力在三十来个互不相干的组件里要用，
   一层层传参会把 `can` 加到所有中间层上）；侧栏四个作用域、命令面板、「新建实例」、实例页分区、
   开关机按钮、控制台输入框和快捷命令都按能力隐藏；「面板设置 → 账号与角色」是账号、角色和实例
   授权的编辑页。

   能力列表由 `GET /api/capabilities` 提供而不是前端写死 —— 新增一条能力，编辑器自己多一行。
   前端只有 26 个能力 **id** 是写死的（`web/src/useCan.tsx` 的 `CAP`），用来隐藏具体入口；
   写错一个会**静默地把页面藏起来**，对谁都看不出来，所以 `TestFrontendCapabilityIdsExist`
   从 Go 那边读这个文件，拿真词表对一遍。

   **隐藏不是屏障**，这一点写进了代码注释和文档：拦截在服务端，前端少判一处是毛刺不是漏洞。
5. ~~**目录白名单**（角色的 `paths`）。~~ **已完成。** 见[目录白名单](#目录白名单)。
5. **文档**：`docs/security.md` 新增一节，明写哪些能力等价于宿主机权限；README 的「还没做的」划掉这一条。

第 1 步合入后，2-5 都是填空。

## 未决问题

1. ~~**「开发」预设要不要默认带 `instance:launch`？**~~ **已定：给。** 见[出厂预设](#出厂预设)。
2. **代理连线（`panel:network`）算实例级还是面板级？** 它同时改代理端和子服的配置。按面板级更简单，
   但「只管代理端」的人就得要面板级能力。暂按面板级。
3. **面板级插件库是共享的。** 开发 A 升级了库里的插件，会影响开发 B 的实例吗（要等 B 主动安装）？
   需要确认现有的 pending 机制在多人下的表现。
4. **用户数量上限。** `users.json` 全量读写 + 内存持有，几十个用户完全没问题；要不要设个软上限拦住
   把它当业务用户表用的人？
