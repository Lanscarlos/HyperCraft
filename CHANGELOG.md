# 更新日志

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [未发布]

### 新增

- **Java 运行时管理** —— 总览页多了「Java 运行时」：一键下载 Eclipse Temurin 的 JRE 或 JDK
  （任意大版本，LTS 有标注），装进 `data/java/`，不动系统里的 Java。每个实例在「启动设置」
  里单独选一个，所以 1.12 的老服（Java 8）和 1.21 的新服（Java 21）能在同一台机器上共存。
  列表里会显示系统自带的 Java、每个运行时被哪些实例用着；正在跑的实例所用的运行时不让删。
  自己解压进 `data/java/` 的 JDK 也会被认出来 —— 认的是每个 OpenJDK 都有的 `release` 文件。

  下载同样归守护进程管，先校验 Adoptium 公布的 sha256 与体积再解压，解压全程经由 `os.Root`
  限制在目标目录内（`..`、绝对路径、指向外部的符号链接一律拒绝），解压完确认有 `bin/java`
  才把目录改名到位 —— 失败或取消都不会留下一个半截的运行时。

### 修复

- **中文实例目录在没有 UTF-8 locale 的机器上起不来**。JVM 用 `sun.jnu.encoding` 解码文件路径，
  而 systemd 服务和精简容器镜像通常一个 locale 变量都没有，于是它退化成 ASCII，
  `data/servers/生存服/` 里的 jar 就打不开，报错是
  `Error: An unexpected error occurred while trying to open file paper.jar` —— 一个字都没提编码。
  现在启动服务端进程时，如果环境里没有任何 locale（或只是 `C` / `POSIX`），会补一个
  `LANG=C.UTF-8`；已经明确设了 locale 的（哪怕是 `zh_CN.GBK`，它照样编得了中文名）不动。

## [1.1.0] - 2026-08-09

### 新增

- **一键下载服务端核心** —— 「启动设置」里选 Paper 或 Velocity 加版本，面板直接把 jar
  下到实例目录，走服务器自己的网络而不是你的浏览器。下载由守护进程持有，关掉网页也会
  继续；重新打开页面能接上进度，可随时取消。落盘前校验 PaperMC 公布的 sha256 与体积，
  先写 `.part` 再改名，失败或取消都不会留下半个 jar，也不会覆盖已有文件（要覆盖会先问
  一句）。可勾选「下载完成后设为启动 jar」，下的是 Velocity 时会一并清空 `--nogui`
  这类服务端参数。数据来自 PaperMC Fill v3 API（旧的 v2 已下线）。

  版本下拉里标了每个版本的最低 Java 版本和官方支持状态 —— Paper 26.x 要 Java 25，
  1.21.11 要 Java 21，这个不匹配时服务端的报错跟 Java 一个字都不沾边。预览版、
  RC 和快照默认不显示。

## [1.0.0] - 2026-08-08

首个正式版。单文件二进制，前端已嵌入，扔到服务器上就能跑。

### 核心设计

服务器进程由面板守护进程持有，不属于任何 HTTP 请求或 WebSocket 连接。关掉浏览器、
退出登录、断网都不影响服务器运行；只有停止面板本身才会关掉它们，而且是优雅停止。

### 新增

- **Web 控制台** —— xterm.js 渲染，保留服务端 ANSI 颜色。每实例保留 2000 行滚动缓冲，
  重连时按行号补齐缺口。命令输入用独立输入框（中文输入法可用），支持 ↑↓ 翻历史，
  一条命令回显给所有连着的客户端。
- **多实例管理** —— 单机管理任意多个服务器，侧栏实时状态。
- **进程管理** —— 启动 / 优雅停止 / 重启 / 强制结束。停服走 `stop` → SIGTERM → SIGKILL
  三级升级，卡死的服务器不会卡住面板。可选崩溃自动重启（退避 5→30 秒，连续 5 次后放弃）。
- **启动配置** —— Java 路径、jar、`-Xms`/`-Xmx`、JVM 与服务端参数；也支持完全自定义
  启动命令，用于基岩版服务端、`start.sh` 或 BungeeCord。
- **server.properties 编辑器** —— 保留注释、空行与键顺序；中文自动转 `\uXXXX`
  （Minecraft 按 ISO-8859-1 读取，直接写 UTF-8 会乱码）；只写真正改过的键，避免把
  `online-mode=false` 这类默认值悄悄写进文件。
- **文件管理器** —— 浏览、上传（拖拽 + 进度条，流式写盘）、下载（支持断点续传）、
  重命名、新建文件夹、递归删除，以及文本配置在线编辑。所有路径操作经由 `os.Root`
  在内核层面限制在实例目录内。上传同名文件默认拒绝，确认后才覆盖。
- **资源监控** —— 每实例 CPU / 内存曲线，5 秒采样、保留 1 小时，采集在守护进程里，
  关掉网页也不断档。CPU 按进程树汇总、以单核百分比呈现。总览页含整机内存 / 磁盘水位
  和 CPU 曲线。
- **鉴权** —— PBKDF2-SHA256 密码、HttpOnly + SameSite=Strict 会话 Cookie、改密后全部
  会话失效。写操作要求自定义头 `X-HyperCraft`，WebSocket 校验 Origin。首次启动生成
  随机密码并打印一次，`-reset-password` 可重置。
- **部署** —— `deploy/hypercraft.service` systemd 示例，含 `TimeoutStopSec` 与
  `KillMode=mixed` 两处关键设置。

### 已知限制

- 监控历史只在内存中保留 1 小时，重启面板即清空。
- 单用户，无权限体系。
- 玩家 / 白名单 / OP 管理需要手动编辑对应 JSON。
- 无自动备份和定时任务。

### 环境要求

构建需要 Go 1.25+ 与 Node 20+；运行只需要目标平台的二进制文件本身。

[1.1.0]: https://github.com/Lanscarlos/HyperCraft/releases/tag/v1.1.0
[1.0.0]: https://github.com/Lanscarlos/HyperCraft/releases/tag/v1.0.0
