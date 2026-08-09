# HyperCraft

一个自托管的 Minecraft 服务器面板：Web 版的「小黑窗」控制台 + 基础服务器配置，编译产物是**一个单文件二进制**（前端已经嵌进去了），扔到服务器上就能跑。

## 为什么关掉网页不会影响服务器

这是本项目的核心设计，不是附带效果：

```
                 ┌─────────────────────────────────────────┐
   浏览器 ──ws──▶ │  hypercraft 守护进程                     │
   浏览器 ──ws──▶ │                                         │
   (随便开几个、  │   ┌──────────┐   stdin/stdout           │
    随便关)      │   │ 实例 A   │◀──────────▶ java -jar …   │
                 │   │ 环形缓冲 │                           │
                 │   └──────────┘                           │
                 │   ┌──────────┐                           │
                 │   │ 实例 B   │◀──────────▶ ./bedrock_… │
                 │   └──────────┘                           │
                 └─────────────────────────────────────────┘
```

服务器进程由**守护进程**持有，不属于任何一个 HTTP 请求或 WebSocket 连接：

- 关掉浏览器标签页、退出登录、断网、重启路由 —— 服务器照跑，输出继续写进环形缓冲区（每实例保留最近 2000 行）。
- 下次打开控制台，会先补齐这段时间的滚动历史，再接上实时输出。
- 连接掉线会自动重连并按行号补齐缺口，不会重复也不会漏。

唯一会关掉服务器的，是**停止面板本身** —— 而且是优雅停止：给每个实例发 `stop`、等世界存盘、超时才升级到信号。所以面板应该用 systemd 之类的方式常驻，而不是在一个你随手会关掉的终端里跑。

## 快速开始

```bash
git clone https://github.com/Lanscarlos/HyperCraft.git
cd HyperCraft

make deps      # 装前端依赖（只需一次）
make build     # 构建前端 + 编译单二进制 ./hypercraft

./hypercraft -data ./data
```

首次启动会生成一个随机管理员密码并打印在终端上（**只显示这一次**）：

```
==========================================================
  HyperCraft 面板登录凭据（仅显示这一次，请立即保存）

    用户名: admin
    密码:   pDpVo5BeJ4qhv7nzhJmx
==========================================================
```

然后打开 http://127.0.0.1:8080 。忘记密码就 `./hypercraft -reset-password`。

### 开新服的流程

1. 侧栏「+ 新建实例」，填个名字（中文没问题，目录名会跟着走）。
2. 「启动设置」→「下载服务端核心」，选 Paper（或 Velocity）和版本，点下载。
   面板会直接把 jar 下到实例目录（默认在 `data/servers/<名字>/`），并设为启动 jar。
   想用别的核心就自己把 jar 丢进那个目录，一样能跑。
3. 「启动设置」里选 Java 运行时、调内存 —— jar 下拉会自动列出目录里的文件。
   机器上没有合适的 Java 就去总览页「Java 运行时」一键装一个（1.20.5 起要 Java 21）。
4. 「服务器配置」里点「我已阅读并同意 EULA」，改改 MOTD、端口、难度。
5. 回「控制台」点启动。

## 命令行参数

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-data` | `HYPERCRAFT_DATA` | `./data` | 面板状态 + 服务器文件的根目录 |
| `-listen` | `HYPERCRAFT_LISTEN` | `127.0.0.1:8080` | 监听地址，会写进配置持久化 |
| `-username` | `HYPERCRAFT_USERNAME` | `admin` | 首次创建凭据时的用户名 |
| `-log-level` | `HYPERCRAFT_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `-reset-password` | | | 重置成一个新随机密码，打印后退出 |
| `-version` | | | 打印版本 |

默认只监听回环地址。面板能在你的机器上执行任意控制台命令，暴露到公网应该是个有意识的决定 —— 见下面的部署章节。

## 数据目录

```
data/
├── panel.json        # 面板配置 + 密码哈希 (PBKDF2-SHA256, 0600)
├── instances.json    # 实例注册表
├── java/             # 面板下载的 Java 运行时，一个版本一个目录
│   └── temurin-21.0.12-8-jre/
└── servers/
    ├── 生存服/        # 实例的工作目录：jar、存档、配置全在这
    └── 创造服/
```

都是普通 JSON，手改也行（改完重启面板）。实例目录也可以指向磁盘上任意已有的服务器，不一定要在 `servers/` 下面。

`panel.json` 里的 `maxUploadMb` 控制单个上传文件的大小上限，默认 2048。

## 部署（systemd）

`deploy/hypercraft.service` 是一份可用的示例，两个关键点：

- **`TimeoutStopSec=300`** —— 面板停止时要等所有世界存盘完，默认的 90 秒对大世界不够。
- **`KillMode=mixed`** —— 只给面板发信号，由它自己按顺序停子进程。否则 systemd 会直接 SIGTERM 掉 JVM，跳过优雅存盘。

```bash
sudo cp hypercraft /opt/hypercraft/
sudo cp deploy/hypercraft.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now hypercraft
sudo journalctl -u hypercraft -f      # 首次启动的初始密码在这里
```

要从外网访问，用 Nginx/Caddy 反代并配上 TLS。WebSocket 需要透传 Upgrade 头：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade    $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host       $host;
    proxy_set_header X-Forwarded-Proto $scheme;   # 让会话 Cookie 带上 Secure
    proxy_read_timeout 3600s;                     # 控制台是长连接
}
```

## 已经做了的

- **Java 运行时管理** —— 总览页可以一键下载 Eclipse Temurin 的 JRE / JDK（任意大版本，LTS 有标注），
  装进面板数据目录，不碰系统里的 Java；每个实例在「启动设置」里各自选一个，所以 1.12 的老服和
  1.21 的新服可以在同一台机器上共存。列表里会显示系统自带的 Java、每个运行时被哪些实例用着，
  正在跑的不让删。手动解压进 `data/java/` 的 JDK 也会被自动认出来。
- **面板内自动更新** —— 有新版本时侧栏会标出来，总览页点一下就更新，不用 SSH 上去换二进制。
  先下载并用 release 的 `SHA256SUMS.txt` 校验，这一步失败不动任何东西；校验通过后才停服、
  换二进制、用 `exec` 就地重启（PID 不变，systemd 察觉不到），然后自动把刚才在跑的服务器
  拉回来。确认弹窗会列出要停哪几个。旧二进制留作 `hypercraft.old` 方便回退。
- **一键下载服务端核心** —— 目前是 Paper 和 Velocity，数据来自 PaperMC 的 Fill API。选版本后
  面板自己去下（走服务器的网络，不经过你的浏览器），下载归守护进程管，关掉网页也会继续，
  重开页面能接上进度。落盘前校验 sha256 和体积，先写 `.part` 再改名 —— 失败、取消或断网都不会
  留下一个看起来能启动的半截 jar，也不会默默覆盖已有文件。
- **文件管理器** —— 浏览实例目录、上传（点选或拖拽，带进度条，单文件默认上限 2 GB）、下载（支持断点续传）、重命名、新建文件夹、递归删除，以及在线编辑文本配置（ops.json、bukkit.yml、插件配置等）。所有路径操作都走 Go 1.25 的 `os.Root`，由内核层面把访问关在实例目录里 —— `..`、绝对路径、指向外部的符号链接一律拒绝，不依赖字符串清洗。上传同名文件默认拒绝，会问一句再覆盖。
- **资源监控** —— 每个实例的 CPU 和内存曲线，5 秒采样、内存里保留 1 小时，面板守护进程采集，所以关掉网页也不断档。总览页还有整机的内存/磁盘水位和 CPU 曲线。
- **控制台** —— xterm.js 渲染，保留服务端自己的 ANSI 颜色；命令输入框支持 Tab 补全和 ↑↓ 翻历史；一条命令会回显给所有连着的客户端。用独立输入框而不是直接在终端里打字，是为了中文输入法能正常用。详见下面一节。
- **多实例** —— 一台机器上管任意多个服务器，侧栏实时状态。
- **进程管理** —— 启动 / 优雅停止 / 重启 / 强制结束；停服走 `stop` → SIGTERM → SIGKILL 三级升级，卡死的服务器不会卡住面板。可选崩溃自动重启（连续失败 5 次后放弃，退避 5→30 秒）。
- **状态机** —— 靠识别 `Done (x.xxs)!` 区分「启动中」和「运行中」，控制台输入在真正就绪前是禁用的。
- **启动配置** —— Java 路径、jar、`-Xms`/`-Xmx`、JVM 参数、服务端参数。也支持**完全自定义启动命令**，用来跑基岩版服务端、`start.sh` 或者 BungeeCord。
- **server.properties 编辑器** —— 常用项给了真正的表单控件，其余键按原样列出。三件容易踩坑的事都处理了：
  - 保留注释、空行和键顺序（你手改过的文件不会被打乱）；
  - 中文自动转成 `\uXXXX`（Minecraft 按 ISO-8859-1 读这个文件，直接写 UTF-8 会乱码）；
  - 只写你真正改过的键 —— 打开页面直接点保存不会把 `online-mode=false` 这种默认值悄悄写进去。
- **EULA** —— 一键写 `eula=true`（旁边就是 Mojang 协议链接）。
- **鉴权** —— PBKDF2-SHA256 密码、HttpOnly + SameSite=Strict 会话 Cookie、改密码后全部会话失效。所有改状态的接口要求自定义头 `X-HyperCraft`，WebSocket 校验 Origin。

## 控制台：颜色、编码和 Tab 补全

服务端的输出是通过**管道**读的，不是终端 —— 服务端能看出这一点，于是它会关掉颜色，
在 Windows 上还会按系统代码页而不是 UTF-8 输出。所以在 cmd 里好好的东西，到面板里
就变成了没颜色的中文乱码。面板默认帮你把这两件事掰回来：

- **颜色** —— 启动 jar 时自动加上 `-Dterminal.jline=false -Dterminal.ansi=true`。
  vanilla / Paper / Fabric 用的 TerminalConsoleAppender 认这两个参数，加上之后即使
  没有终端也照样输出 ANSI 颜色。不想要就在「启动设置 → 控制台」里关掉。
- **编码** —— 同时加上 `-Dfile.encoding=UTF-8` 和 `stdout/stderr/stdin` 那一组
  （Java 8~17 认前者，18+ 认后者），让 JVM 直接说 UTF-8；面板这边再按「输出编码」
  设置解码，默认「自动」：本来就是合法 UTF-8 的行原样放行，不是的按系统编码兜底
  （中文 Windows 上就是 GBK）。中文、`─ ┌ ➜ ✔` 这类符号和 emoji 因此都能正常显示。
  用自定义启动命令（`start.bat`、基岩版、BungeeCord）时面板加不了 JVM 参数，如果还是
  乱码，把「输出编码」直接选成 GBK —— 输入的命令也会按同一编码发回去。
- **Tab 补全** —— 输入框按 Tab 补全命令名，再按继续切换候选（Shift+Tab 往回切，
  Esc 关掉）。补全内容包括：原版和 Paper 的常用命令、`gamemode` / `difficulty` /
  `gamerule` 这类子参数、**在线玩家名**（从 `xxx joined the game` 和 `list` 的回复里
  学的），以及你在这个浏览器里发过的历史命令。JLine 的补全需要真终端，面板隔着管道
  拿不到，所以这份补全是面板自己的，不依赖服务端。

`-D` 参数加在你自己填的 JVM 参数**前面**，所以想覆盖哪个，自己再写一遍就行。

## 还没做的

按我觉得的优先级排：玩家列表和白名单/OP 管理（现在只能手改 JSON）、自动备份、多用户和权限、定时任务（定时重启/广播）、更多可下载的核心（Fabric / Forge / 基岩版）、更长时间的监控历史（现在只在内存里存 1 小时，重启面板就没了）。

Java 那块有个已知边界：Temurin 只有 glibc 构建，musl 系统（Alpine 之类）装了也跑不起来 —— 面板会检测到并直接说明，让你改用系统包管理器装。

## 开发

```bash
make test           # go test -race ./...
make lint           # gofmt + go vet

# 热重载开发：两个终端
go run ./cmd/hypercraft -data ./data     # 后端 :8080
npm --prefix web run dev                 # 前端 :5173，API 自动代理到 8080

make cross          # 交叉编译 linux/amd64, linux/arm64, windows, darwin/arm64
```

### 结构

```
cmd/hypercraft/      入口：flag、启动、优雅关闭
internal/instance/   核心：进程监管、状态机、控制台环形缓冲、事件广播
internal/api/        HTTP + WebSocket
internal/serverfiles/ 文件管理，全部经由 os.Root 限制在实例目录内
internal/serverjar/   服务端核心下载：PaperMC Fill API 客户端 + 后台下载任务
internal/javaruntime/ Java 运行时：Adoptium 客户端、安全解压、已装运行时注册表
internal/metrics/    CPU/内存采样，按进程树汇总
internal/mcprops/    server.properties 解析/写回（保留格式，Java 转义）
internal/store/      JSON 持久化（临时文件 + rename 原子写）
internal/auth/       PBKDF2 凭据 + 内存会话
internal/webui/      go:embed 前端产物
web/                 React + TypeScript + Vite + xterm.js（图表是手写 SVG，没引图表库）
```

跨平台：Linux/macOS 用进程组 + SIGTERM/SIGKILL，Windows 用 `taskkill /T`。

## 发布

发版动作就是抬 `CHANGELOG.md` 顶部的版本号。在 PR 里加上这一版的日志：

```markdown
## [1.0.1] - 2026-08-09

### 修复
- ...
```

合进 main 之后，GitHub Actions 发现这个版本还没有对应的 tag，就会跑完 lint 和
测试、交叉编译四个平台、打好包，然后给这个 commit 打上 `v1.0.1` 并发布 Release。
没动版本号的合并只会跑一个几秒钟的检查然后跳过，不会发版。

功能想先合进 main、攒几个再一起发的，把日志写在 `## [未发布]` 下面：这个标题不是
版本号，工作流读不到，也就不会发版。等要发的时候把它改成 `## [1.0.1] - 日期`
再合一次即可。

发布说明取自 `CHANGELOG.md` 里对应版本的那一节。带后缀的版本号（`1.1.0-rc.1`）
会发成 prerelease。手动推 `vX.Y.Z` 的 tag 也照样能发，走的是同一条流程。

传错了或者日志写漏了：改完在 Actions 页面手动重跑 Release 工作流，填上同一个
tag，产物和发布说明都会覆盖掉，不用另开一个版本号。

每个压缩包里是单文件二进制加 README、CHANGELOG、LICENSE，Linux 的还带一份
`hypercraft.service`。`SHA256SUMS.txt` 单独传，用 `sha256sum -c` 校验。

## 依赖与环境要求

需要 **Go 1.25+** 构建（文件管理器用到了 1.25 的 `os.Root.Rename` / `RemoveAll` 等；`GOTOOLCHAIN` 默认会自动下载，本机 Go 版本旧一些也不影响）。前端需要 Node 20+。

后端只有三个直接依赖：

- `gorilla/websocket` —— 控制台长连接
- `shirou/gopsutil` —— 跨平台读取进程和主机的 CPU / 内存
- `golang.org/x/text` —— 控制台编码转换（GBK / Big5 / Shift_JIS 等）

密码哈希用的是 Go 1.24 进标准库的 `crypto/pbkdf2`，没有引 `golang.org/x/crypto`。

面板自身很轻：实测常驻堆内存约 2 MB、十来个协程，跟它管理的 JVM 比可以忽略。

## License

MIT
