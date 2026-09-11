# 开发指南

## 环境要求

从源码构建需要 **Go 1.25+**（文件管理器用到了 1.25 的 `os.Root.Rename` / `RemoveAll` 等；
`GOTOOLCHAIN` 默认会自动下载，本机 Go 版本旧一些也不影响）和 **Node 20+**。

只是部署的话这些都不需要 —— 发布的二进制是 `CGO_ENABLED=0` 静态编译的，服务端要的 Java 面板自己会装。

后端只有三个直接依赖：

- `gorilla/websocket` —— 控制台长连接
- `shirou/gopsutil` —— 跨平台读取进程和主机的 CPU / 内存
- `golang.org/x/text` —— 控制台编码转换（GBK / Big5 / Shift_JIS 等）

密码哈希用的是 Go 1.24 进标准库的 `crypto/pbkdf2`，没有引 `golang.org/x/crypto`。前端是 React 18 +
TypeScript + Vite + xterm.js，不引 UI 框架，图表是手写 SVG。

面板自身很轻：实测常驻堆内存约 2 MB、十来个协程，跟它管理的 JVM 比可以忽略。

## 构建与开发

```bash
git clone https://github.com/Lanscarlos/HyperCraft.git && cd HyperCraft

make deps      # 装前端依赖（只需一次）
make build     # 构建前端 + 编译单二进制 ./hypercraft
make build-go  # 只编译后端，复用已有的前端产物

./hypercraft -data ./data
```

同样是首次启动打印一次随机密码，然后开 http://127.0.0.1:19190 。

热重载开发，两个终端：

```bash
go run ./cmd/hypercraft -data ./data     # 后端 :19190
npm --prefix web run dev                 # 前端 :5173，API 自动代理到 19190
```

## 检查

```bash
make lint    # gofmt + go vet
make test    # go test -race ./...
npm --prefix web run build   # tsc -b + vite build，前端唯一的自动检查
```

CI（`.github/workflows/ci.yml`）跑的是 `make lint` → `make test` → `make build`。前端没有单测和
lint，`tsc -b` 是唯一的自动检查，所以样式改动要人工在多个宽度和明暗两种模式下确认。

交叉编译和打包：

```bash
make cross                    # linux/amd64, linux/arm64, windows/amd64, darwin/arm64
make package VERSION=v0.4.0   # 交叉编译 + 打出 release/ 里的压缩包和 SHA256SUMS.txt
```

## 项目结构

```
cmd/hypercraft/       入口：flag、启动、优雅关闭
internal/instance/    核心：进程监管、状态机、控制台环形缓冲、事件广播
internal/api/         HTTP + WebSocket
internal/serverfiles/ 文件管理，全部经由 os.Root 限制在实例目录内
internal/serverjar/   服务端核心库：PaperMC Fill API 客户端 + 后台下载任务 + data/cores 货架
internal/plugin/      插件库：市场 / GitHub Release 客户端 + 后台下载任务 + 版本货架 + 实例装载
internal/dbruntime/   数据库环境：引擎下载、自检、服务进程与数据目录
internal/hostfs/      只读地列出宿主机目录，给实例目录选择器用
internal/hostterm/    本机终端：伪终端上的 shell，会话数上限与挂断（Windows 上返回不支持）
internal/javaruntime/ Java 运行时：Adoptium 客户端、安全解压、已装运行时注册表
internal/metrics/     CPU / 内存 / 网络采样，按进程树汇总
internal/mcprops/     server.properties 解析 / 写回（保留格式，Java 转义）
internal/confighist/  配置历史：收录规则、体积闸门、提交 / diff / 还原 / 压缩
internal/gitlite/     最小 Git 对象层：松散对象、tree、单分支线性历史。不依赖 git 二进制
internal/selfupdate/  面板自更新：下载校验、就地替换、exec 重启
internal/store/       JSON 持久化（临时文件 + rename 原子写）
internal/auth/        PBKDF2 凭据 + 内存会话 + 设备令牌
internal/authz/       权限词表：能力常量与元数据，路由和角色都引用它
internal/users/       账号与角色：注册表、校验、出厂预设
internal/webui/       go:embed 前端产物
web/src/              前端源码；全部样式在 web/src/styles.css 一个文件里
deploy/               systemd unit
```

构建产物是**单文件二进制**：前端 `npm run build` 的结果被 `//go:embed` 进 `internal/webui/dist`，
所以改了前端不重新构建前端，二进制里就还是旧界面。

跨平台：Linux / macOS 用进程组 + SIGTERM/SIGKILL，Windows 用 `taskkill /T`。

## 发布

`make package` 是发布的唯一入口，正式版和快照都走它，所以本地打的包和 CI 打的完全一样。

**发版动作就是抬 `CHANGELOG.md` 顶部的版本号。** 在 PR 里加上这一版的日志：

```markdown
## [0.5.0] - 2026-09-01

### 修复
- ...
```

合进 main 之后，GitHub Actions 发现这个版本还没有对应的 tag，就会跑完 lint 和测试、交叉编译四个平台、
打好包，然后给这个 commit 打上 `v0.5.0` 并发布 Release。没动版本号的合并只会跑一个几秒钟的检查然后
跳过，不会发版。

功能想先合进 main、攒几个再一起发的，把日志写在 `## [未发布]` 下面：这个标题不是版本号，工作流读不到，
也就不会发版。

发布说明取自 `CHANGELOG.md` 里对应版本的那一节。带后缀的版本号（`0.5.0-rc.1`）会发成 prerelease。
手动推 `vX.Y.Z` 的 tag 也照样能发，走的是同一条流程。传错了或者日志写漏了：改完在 Actions 页面手动
重跑 Release 工作流，填上同一个 tag，产物和发布说明都会覆盖掉，不用另开一个版本号。

**快照**：`.github/workflows/snapshot.yml` 挂在 CI 后面，main 上每个通过 CI 的提交都发一版
prerelease，tag 就是 `snapshot-<提交数>`（`snapshot-213`）。**不带 `v` 前缀，也不是语义化版本** ——
快照不是冲着某个版本去的，它就是 main 在那个提交的样子，提交数是它唯一有的数字。排序规则在
`internal/selfupdate/version.go`：两个快照按提交数比，快照整体排在所有正式版之上（正式版是从 main 上
切出去的，快照里已经包含了）。所以正式版发布不会把快照面板拉回正式版轨道，切通道才会。发布提交也照发
一版快照，这样快照通道拿得到跟正式版一模一样的代码。只保留最近 3 个，旧的连同 tag 一起删；清理的正则
同时认旧的 `vX.Y-snapshot.N`，否则改名前发的那些没人会去删。

**改版本号格式必须先发解析器。** 这个命名改过两次：第一次直接改了工作流，结果所有在用的面板全都卡住 ——
做版本比较的是**已经装在机器上的那个二进制**，它解析不了新格式就会把那个 tag 整个跳过，然后一直显示
「已是最新」。正确顺序是：先发一版能读懂新格式的二进制（仍用旧命名），等它装进大家的面板，再改工作流。
以后再动版本号长相，照这个顺序走。

这个命名以后大概率要换成 `snapshot-<提交数>`——快照不是冲着某个版本去的，`0.5-snapshot.86`
读起来却像「0.5 的预览版」，而 0.5 里有什么在打这个快照时根本还没定。**换名字之前，认识新格式的
二进制必须先发出去。** 做版本比较的是已经装在机器上的那个面板：它的 `IsReleaseVersion` 只要解析不了
`snapshot-191`，`checkNewest` 就会把这个 tag 整个跳过，于是永远「已是最新」。这个顺序搞反过一次，
快照通道对所有在用的面板直接断掉，只能手动换二进制。

现在 `internal/selfupdate/version.go` 两种格式都认：`snapshot-N` 之间按提交数比，整体排在所有正式版
和 `0.5-snapshot.*` 之上（正式版是从 main 上切出去的，快照里已经有那些代码）。所以改名要等到在用的
面板都更新过了再动工作流，而且改的时候只改 `.github/workflows/snapshot.yml`，Go 这边不用再碰。

每个压缩包里是单文件二进制加 README、CHANGELOG、LICENSE，Linux 的还带一份 `hypercraft.service`。
`SHA256SUMS.txt` 单独传，用 `sha256sum -c` 校验。

抬版本号的那个 PR 里，顺手把 [`docs/deployment.md`](deployment.md) 和 README 快速开始里的下载 URL
也改成新版本 —— 压缩包名带版本号，所以没法用 `releases/latest/download/` 那种永久链接，只能手动跟。

## 代码风格

- **代码注释用英文，文档（README / CHANGELOG / CI 步骤名）用中文。** 沿用所处文件的语言，不要混。
- 注释解释**为什么**，不解释代码在做什么。仓库里大量注释记录的是踩过的坑和仍然成立的约束 —— 看起来
  可以简化的写法，先读注释再决定。
- Go 代码提交前必须 `gofmt`（CI 直接卡）。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节。
- 前端所有样式在 `web/src/styles.css` 一个文件里，颜色 / 圆角 / 阴影 / 时长都从开头的令牌区取，
  新增令牌必须 light / dark 两个块都加。
