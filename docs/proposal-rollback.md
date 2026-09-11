# 提案：面板版本回退

> **状态：两期都已实现。**
>
> 更新页有「退回 `<上一版>`」（走本地 `<exe>.old`）和「其他版本」列表（走网络）；任何一次降级之前
> 都会备份面板自己的 JSON。

- [要解决的问题](#要解决的问题)
- [先说清楚：降级到底会丢什么](#先说清楚降级到底会丢什么)
- [两条机制](#两条机制)
- [降级前备份](#降级前备份)
- [数据模型](#数据模型)
- [接口](#接口)
- [界面](#界面)
- [明确不做的事](#明确不做的事)
- [落地顺序](#落地顺序)
- [未决问题](#未决问题)

## 要解决的问题

面板现在**没有回退**。唯一决定「更新按钮给不给按」的是 `internal/selfupdate/selfupdate.go` 的
`Offer`：

```go
case cmp > 0:
	return true, false                                                  // 更新
case cmp < 0 && u.channel == ChannelStable && !IsStableVersion(u.current): // 回退
	return true, true
```

于是：

- **正式版用户一条回退路径都没有。** `!IsStableVersion(u.current)` 就是专门挡这个的——跑着 `0.5.0`
  想退回 `0.4.0`，面板不给。
- **快照用户只有「切回正式版通道」这一种**，而且终点写死是**最新的**那个正式版。
- **退到更早的快照也不行。** `checkNewest` 只挑版本号最大的那一个，`Apply` 只装 `s.latest`，整套 API
  没有「装指定版本」的入口。

真出事的时候——新版有 bug、跟某个插件冲突、服务端起不来——唯一的出路是 SSH 上去手动换二进制。而这台
机器上**旧二进制其实就躺在旁边**：`Staged.Commit` 一直把它留成 `<exe>.old`，注释里写着「so a bad
release can be rolled back by hand」。只是没人知道它是哪一版，面板也没有按钮去用它。

## 先说清楚：降级到底会丢什么

这一节比功能本身重要。**「能退回任意版本」这句话是危险的**，因为旧版本不认识新字段，而 Go 的
`json.Marshal` 只写结构体里声明过的字段——**旧版本一旦回写配置文件，它读不懂的字段就永久消失了**。

仓库里已经有三处注释记着这件事：

| 位置 | 降级会发生什么 |
| --- | --- |
| `internal/config/config.go:124` | 登录凭据迁移进 `users.json` 后，`panel.json` 里的 `credential` 被清空。**退回迁移前的版本可能登不进面板**（`-reset-password` 能救）。 |
| `internal/config/config.go:73` | `githubToken` 折进 `githubTokens` 后清空，降级丢 token。 |
| `internal/authz/authz.go:146` | 旧版本不认识的 capability 一律当「不授权」，角色权限静默缩水。 |

所以真正的问题不是「能不能退」，而是**退完之后还能不能回来**。结论：面板不替操作者做主、不设版本
下限，但**每次向下走都先留一份备份**，并且在界面上说清楚可能丢什么。

## 两条机制

回退有两个完全不同的诉求，对应两套实现，不要混成一个。

### 一、退回上一版（本地，零下载）

更新时 `Staged.Commit` 已经把被换下去的二进制留成 `<exe>.old`，而且**从不清理**——它只在下一次更新
时被覆盖。所以「退回上一版」根本不用碰网络：把 `.old` 换回来重启就行。

- **先解决「它是哪一版」。** 现在这个信息是缺的。更新成功后把被替换掉的版本号记进 `panel.json`。
- **用之前先验它。** 执行 `<exe>.old -version`，确认它能跑、且输出跟记录的版本号一致。手动换过二进制、
  文件损坏、`.old` 是别的东西——这些情况下宁可不给按钮，也不能把面板换成一个起不来的文件。
- **交换是对称的。** 退回去之后 `.old` 就是刚才那个新版，所以还能再换回来，记录跟着一起换。

**为什么值得单独做一套**：离线可用、秒级完成、**不受 GitHub 清理影响**。快照只保留 3 份，想退的那一版
很可能已经从 releases 页上消失了，只有这条路救得了。

### 二、任选历史版本（走网络）

`Updater` 补两个方法，后面完全复用现有的下载 → 校验 → `Prepare` 链路：

- `List(ctx)`：拉一页 releases，**按当前通道过滤**（正式版通道只列正式版，快照通道两种都列），返回
  版本号、发布时间、是否 prerelease、本平台有没有产物。
- `Find(ctx, version)`：从**同一份列表**里取那一版。

> 实现时这里跟原设计不同：原本写的是 `ReleaseByTag(ctx, tag)` 走 `GET /releases/tags/<tag>`。
> 按 tag 直接取等于谁调 API 谁说了算装什么 —— 版本号是请求带进来的，通道就白设了。从 `List` 用的
> 那份列表里找，通道过滤是免费附带的，也少一个 GitHub 端点。

`Service.ApplyVersion(ctx, version)` 校验目标在列表里、有本平台产物，然后走跟 `Apply` 完全相同的那条路
（停服和下载并行 → 安装 → 重启），降级时同样先备份。

## 降级前备份

两条机制共用。**只在目标版本低于当前版本时做**，升级不做。

把面板自己的状态文件原样复制到 `data/rollback-<时间戳>-<from>-to-<to>/`：

`panel.json`、`users.json`、`instances.json`、`databases.json`、`instance-plugins.json`、
`pending-plugins.json`、`config-history.json`、`resume.json`（即 `config.Paths` 上那八个）。

不碰实例目录和世界存档：那是几十 GB 的东西，而且旧版本不会改写它们——会被改写的只有面板自己的
JSON。保留最近 3 份，再多没意义。

备份是**给「再升回来」用的**，不是给「回退失败」用的：回退本身失败不会动这些文件，真正的损失发生在
旧版本跑起来、回写、把它不认识的字段抹掉之后。

## 数据模型

`internal/config` 的 `Panel` 加一个字段：

```go
// PreviousVersion is the version that <exe>.old holds — the build this panel
// updated away from. Empty means there is nothing to roll back to: a fresh
// install, or a binary put in place by hand rather than by an update.
PreviousVersion string `json:"previousVersion,omitempty"`
```

写入时机是 `Commit()` 成功之后、重启之前，经 `Hooks` 回调落盘（`selfupdate` 包不认识 `config`，
这条边界不要打破）。

## 接口

`Status` 补四个字段，UI 只读状态、不做判断：

| 字段 | 含义 |
| --- | --- |
| `previousVersion` | `.old` 里是哪一版，空表示没有 |
| `rollbackAvailable` | `.old` 存在、版本号对得上、可执行 |
| `rollbackWhy` | 不可用时的人话原因 |
| `backupDir` | 这次降级把配置备份到了哪里 |

路由全部沿用 `authz.CapPanelUpdate`，不新增权限位：

| 路由 | 作用 |
| --- | --- |
| `GET /api/update/versions` | 当前通道下可装的版本列表 |
| `POST /api/update/rollback` | 退回上一版（本地 `.old`） |
| `POST /api/update/apply` | 加可选 `{"version": "..."}`，不带就是装最新的（现有行为） |

## 界面

更新页现有卡片下面加一节「回退」，用 `frontend-design` skill 做：

- 主按钮「退回 `<上一版>`」，只在 `rollbackAvailable` 为真时出现；否则显示 `rollbackWhy`。
- 下面一个折叠的「其他版本」列表，每行一个「装这一版」。
- 点下去弹确认框，必须写清三件事：**会停掉所有服务端并重启面板**、**可能丢登录凭据 / GitHub token /
  角色权限**（并指出 `-reset-password` 能救登录）、**已备份到哪个目录**。

## 明确不做的事

- **不做自动回滚。** 「新版起不来就自动退回去」需要面板判断自己是不是坏了，而一个坏掉的面板做不了这个
  判断。这是 systemd 和人的活。
- **不做数据迁移的反向迁移。** 把 `users.json` 里的凭据回填进 `panel.json` 这类事，等于要求每个迁移都
  写一份逆操作，并且永远维护下去。备份 + 说清楚，是这里合理的成本。
- **不设版本下限。** 想退到多旧是操作者的判断，面板负责说清风险和留好备份。
- **不做任意 commit 的构建。** 只能装已经发布出来的 release。

## 落地顺序

1. **第 1 期**：`previousVersion` 落盘 + `Service.Rollback` + 降级备份 + 更新页那个按钮。
   这是真出事时唯一靠得住的路，也最小。
2. **第 2 期**：`List` / `ReleaseByTag` / `ApplyVersion` + 版本列表界面。

两期都按 TDD 做，Go 侧至少覆盖：二进制交换、版本校验不通过时一个字节都不动、备份目录生成与保留数、
通道过滤、handler 的权限。

## 未决问题

- **Windows 上换二进制**。`Commit` 的注释说改名正在运行的可执行文件在两个平台都允许，回退走的是同一个
  改名操作，所以理论上一样。但没有 Windows 机器验证过，第 1 期实现时要把这条单独试出来。
- **`.old` 只有一份**。连退两版做不到，这是 `.old` 机制的固有限制；「其他版本」列表是它的补充 ——
  代价是那一版得还在 GitHub 上。
