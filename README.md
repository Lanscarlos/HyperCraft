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

## 快速开始（全新的 Debian）

发布产物是单文件二进制，前端已经嵌在里面，不需要 Go、Node 或者任何运行时依赖。部署就两件事：解压，交给 systemd。服务端要的 Java 也不用 `apt` —— 面板起来之后在「设置 → Java 运行时」页点一下就能装，所以下面这套在一台**什么都没装**的 Debian 上从头到尾能跑通。

### 1. 下载解压到 `/opt/hypercraft`

```bash
sudo mkdir -p /opt/hypercraft
cd /opt/hypercraft
sudo wget https://github.com/Lanscarlos/HyperCraft/releases/download/v1.2.0/hypercraft-1.2.0-linux-amd64.tar.gz
sudo tar -xzf hypercraft-1.2.0-linux-amd64.tar.gz --strip-components=1
```

包里是二进制、`hypercraft.service` 和几个文档文件。ARM 机器（`uname -m` 显示 `aarch64`）把 URL 里的
`amd64` 换成 `arm64`。上面这个 URL 固定指向 v1.2.0，更新的版本见
[Releases 页面](https://github.com/Lanscarlos/HyperCraft/releases/latest) —— 不过装好之后面板能自己升级，
这个链接一般只用一次。

国内的机器直连 GitHub 大概率慢到没法用，在 URL 前面加个镜像前缀就行 ——
`https://ghfast.top/https://github.com/...`，这也是面板自己更新时默认走的那个。

想验下载完整性的话，同一个 release 里有 `SHA256SUMS.txt`（这个建议直连 GitHub 取，
校验文件和压缩包都从同一个镜像拿的话，校验就没多大意义了）：

```bash
sudo wget https://github.com/Lanscarlos/HyperCraft/releases/download/v1.2.0/SHA256SUMS.txt
sha256sum -c SHA256SUMS.txt --ignore-missing
```

### 2. 建一个专用用户

```bash
sudo useradd -r -s /usr/sbin/nologin minecraft
sudo chown -R minecraft:minecraft /opt/hypercraft
```

`chown` 不能省：数据目录要写，面板内自动更新还要原地替换 `/opt/hypercraft/hypercraft` 并留一份
`hypercraft.old`，运行用户对这个目录没有写权限的话更新会失败。

### 3. 交给 systemd

面板是所有 Minecraft 进程的父进程，在 SSH 里直接跑，你一断线服务器就跟着停了，所以别跳过这步。

```bash
sudo cp /opt/hypercraft/hypercraft.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now hypercraft
sudo journalctl -u hypercraft -f
```

首次启动生成的随机管理员密码就打印在这段日志里（**只显示这一次**）：

```
==========================================================
  HyperCraft 面板登录凭据（仅显示这一次，请立即保存）

    用户名: admin
    密码:   pDpVo5BeJ4qhv7nzhJmx
==========================================================
```

忘了就停掉面板再重置：

```bash
sudo systemctl stop hypercraft
sudo -u minecraft /opt/hypercraft/hypercraft -data /opt/hypercraft/data -reset-password
sudo systemctl start hypercraft
```

### 4. 打开面板

默认监听 `0.0.0.0:19190`，也就是所有网卡都监听，装好之后直接访问 http://你的服务器IP:19190 就行
（别忘了在防火墙/安全组里放行 19190）。

面板讲的是明文 HTTP，而且能在这台机器上执行任意控制台命令，所以要长期挂在公网上，务必配反代加 TLS，
见下面的[部署细节](#部署细节systemd-与反代)。只想自己用、不想暴露出去的话，把 `-listen` 改回
`127.0.0.1:19190`，然后在**你自己的电脑上**开个 SSH 隧道：

```bash
ssh -L 19190:127.0.0.1:19190 you@your-server
```

### 开新服的流程

1. 「设置 → 服务端核心」，选 Paper（或 Velocity）和版本，下载到**核心库**。
   核心库是面板级的：一个核心只下一次，之后开多少个服都从这里复制，不用再联网。
   把自己的 jar（Fabric、Forge、整合包服务端）丢进 `data/cores/` 也会出现在库里。
2. 「设置 → Java 运行时」一键装一个 —— 刚装好的机器上一个 Java 都没有（1.20.5 起要
   Java 21，Paper 26.x 要 25）；系统里本来就有的 Java 也会一并列出来。
3. 侧栏「+ 新建实例」，填个名字（中文没问题，目录名会跟着走），从核心库里挑一个核心，
   点「创建」。目录留空就放在数据目录的 `servers/<名字>/` 下；也可以「浏览…」指到本机
   任意位置 —— 外挂硬盘、NAS 挂载点，或者一个你已经有服务端的目录（那里的 jar 会自动
   出现在「服务端 jar」的下拉里）。
4. 「启动设置」里选 Java 运行时、调内存。
5. 「服务器配置」里点「我已阅读并同意 EULA」，改改 MOTD、端口、难度。
6. 回「控制台」点启动。

## 命令行参数

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-data` | `HYPERCRAFT_DATA` | `./data` | 面板状态 + 服务器文件的根目录 |
| `-listen` | `HYPERCRAFT_LISTEN` | `0.0.0.0:19190` | 监听地址，会写进配置持久化 |
| `-username` | `HYPERCRAFT_USERNAME` | `admin` | 首次创建凭据时的用户名 |
| `-log-level` | `HYPERCRAFT_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `-reset-password` | | | 重置成一个新随机密码，打印后退出 |
| `-version` | | | 打印版本 |

默认监听所有网卡，外网可直接访问。面板能在你的机器上执行任意控制台命令，公网部署请务必加 TLS 反代，
或者把 `-listen` 改成 `127.0.0.1:19190` 只留本机 —— 见下面的部署章节。

## 数据目录

```
data/
├── panel.json        # 面板配置 + 密码哈希 (PBKDF2-SHA256, 0600)
├── instances.json    # 实例注册表
├── java/             # 面板下载的 Java 运行时，一个版本一个目录
│   └── temurin-21.0.12-8-jre/
├── cores/            # 服务端核心库：下载一次，复制给任意多个实例
│   ├── paper-1.21.11-132.jar
│   └── index.json    # 每个 jar 是哪个项目/版本/构建，丢了也只是少点信息
└── servers/
    ├── 生存服/        # 实例的工作目录：jar、存档、配置全在这
    └── 创造服/
```

都是普通 JSON，手改也行（改完重启面板）。实例目录也可以指向磁盘上任意已有的服务器，不一定要在 `servers/` 下面。

`panel.json` 里的 `maxUploadMb` 控制单个上传文件的大小上限，默认 2048。

`panel.json` 里的 `terminal` 控制本机终端：`{"enabled": false}` 是默认值，界面上的开关改的
就是它；`shell` 可选，填了就用指定的程序（不填按 `$SHELL` → `bash` → `sh` 找）。

## 部署细节（systemd 与反代）

`deploy/hypercraft.service`（也在每个 Linux 压缩包里）是一份可用的示例，三个关键点：

- **`TimeoutStopSec=300`** —— 面板停止时要等所有世界存盘完，默认的 90 秒对大世界不够。
- **`KillMode=mixed`** —— 只给面板发信号，由它自己按顺序停子进程。否则 systemd 会直接 SIGTERM 掉 JVM，跳过优雅存盘。
- **`ProtectSystem=full` + `ReadWritePaths=/opt/hypercraft`** —— 数据目录得在这个路径下面。想放别处（比如
  单独挂的数据盘），或者某个实例目录指向了 `/opt/hypercraft` 外面，记得把那个路径加进 `ReadWritePaths`，
  否则面板会遇到只读文件系统。

防火墙放行 Minecraft 端口和面板端口：

```bash
sudo ufw allow 25565/tcp
sudo ufw allow 19190/tcp
```

面板是明文 HTTP，直接开 19190 给公网等于把没有 TLS 的登录框挂上去。更稳妥的做法是只放行 443，
把 `-listen` 收回 `127.0.0.1:19190`，用 Nginx/Caddy 反代并配上 TLS。WebSocket 需要透传 Upgrade 头：

```nginx
location / {
    proxy_pass http://127.0.0.1:19190;
    proxy_http_version 1.1;
    proxy_set_header Upgrade    $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host       $host;
    proxy_set_header X-Forwarded-Proto $scheme;   # 让会话 Cookie 带上 Secure
    proxy_read_timeout 3600s;                     # 控制台是长连接
}
```

Caddy 省事一些，这三行就够了，证书和上面那几个头它自己会处理：

```caddyfile
panel.example.com {
    reverse_proxy 127.0.0.1:19190
}
```

## 原生客户端与设备令牌

浏览器之外的客户端（桌面端、Android）用**设备令牌**认证，而不是会话 Cookie。配对一台设备：

```bash
curl -X POST https://panel.example.com/api/auth/devices \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"你的密码","name":"我的手机"}'
```

返回里的 `token` **只出现这一次** —— 面板只保存它的摘要，丢了就重新配对。之后每个请求带上：

```
Authorization: Bearer hcd_xxxxxxxx...
```

控制台的 WebSocket 也走同一个头，不需要另外的握手方式。

设备令牌和浏览器会话是两套凭证，互不通用：

- 会话存在内存里，面板一重启（包括自更新）就全部失效。对坐在电脑前的人无所谓。
- 设备令牌写进 `panel.json`，重启照常能用 —— 否则面板每更新一次，手机就被登出一次。
- 每台设备可以单独吊销：面板里在「设置 → 已配对设备」看列表和解除配对，也可以走
  `GET /api/auth/devices` 和 `DELETE /api/auth/devices/{id}`。
  在 App 里退出登录也会解除当前这台的配对。
- **改密码会解除所有设备的配对**，需要重新配对。

> **配对之前先把 TLS 配好。** 设备令牌是长期凭证，不像会话那样过几天自己失效，在明文 HTTP 上
> 它会随每个请求原样过一遍网络。面板默认监听 `0.0.0.0`，手机开箱即可连上，也正因为如此，
> 反代加 TLS 不是建议而是前提。用明文 HTTP 配对时面板会在日志里警告，但不会阻止你。

Android 9 以上默认禁止明文 HTTP，所以客户端连非 HTTPS 的面板还需要额外配置
`networkSecurityConfig` —— 又一个直接上 TLS 更省事的理由。

## 已经做了的

- **全局仪表盘** —— 首页一眼看完：实例运行中/总数、已装 Java、核心库存量、面板版本，加上整机的
  内存/磁盘水位和 CPU 曲线；实例卡片上直接有启动/停止按钮，不用先进控制台。
- **Java 运行时管理** —— 「设置 → Java 运行时」可以一键下载 Eclipse Temurin 的 JRE / JDK（任意大版本，LTS 有标注），
  装进面板数据目录，不碰系统里的 Java；每个实例在「启动设置」里各自选一个，所以 1.12 的老服和
  1.21 的新服可以在同一台机器上共存。列表里会显示系统自带的 Java、每个运行时被哪些实例用着，
  正在跑的不让删。手动解压进 `data/java/` 的 JDK 也会被自动认出来。
- **面板内自动更新** —— 有新版本时侧栏会标出来，「设置 → 面板更新」点一下就更新，不用 SSH 上去换二进制。
  先下载并用 release 的 `SHA256SUMS.txt` 校验，这一步失败不动任何东西；校验通过后才停服、
  换二进制、用 `exec` 就地重启（PID 不变，systemd 察觉不到），然后自动把刚才在跑的服务器
  拉回来。确认弹窗会列出要停哪几个。旧二进制留作 `hypercraft.old` 方便回退。

  下载默认走 `https://ghfast.top/` 镜像（国内直连 GitHub 的下载速度基本没法用），界面上可以
  换成别的或直连，镜像挂了自动回退直连。**镜像只搬压缩包**：校验用的 `SHA256SUMS.txt` 优先
  从 GitHub 直接取，所以镜像换不掉二进制——它给的包对不上 GitHub 的哈希就会被拒绝。

  **更新通道**分「正式版」和「快照」，默认正式版。快照是 main 分支每个通过 CI 的提交自动出的
  一版（`1.2.1-snapshot.431` 这种，标着 prerelease），想提前试新功能可以在更新页切过去，
  更新流程和正式版完全一样。快照通道同时也看正式版——按版本号取最新的那个，所以
  `1.2.1` 一发布就会自动从 `1.2.1-snapshot.*` 更新过去。切回正式版通道时，如果当前跑的快照
  比最新正式版新，面板会提示装回最新正式版，那一步是往回装的，会明确说清楚。
  生产环境请留在正式版通道：快照只保证过了 CI。
- **服务端核心库** —— 面板级的 jar 货架，在「设置 → 服务端核心」里管。目前可下载的是 Paper 和
  Velocity，数据来自 PaperMC 的 Fill API；下载好的核心留在 `data/cores/`，新建实例或在「启动设置」
  里选一下就复制一份过去 —— 实例拿到的是自己的副本，所以删库里的核心不会动到正在跑的服。
  自己丢进 `data/cores/` 的 jar 也会被认出来，一样能复制。选版本后
  面板自己去下（走服务器的网络，不经过你的浏览器），下载归守护进程管，关掉网页也会继续，
  重开页面能接上进度。落盘前校验 sha256 和体积，先写 `.part` 再改名 —— 失败、取消或断网都不会
  留下一个看起来能启动的半截 jar，也不会默默覆盖已有文件。
- **本机终端（默认关闭）** —— 「设置 → 终端」打开开关后，侧栏会多一个「终端」页，里面是面板所在
  这台机器的一个真 shell（伪终端，`top`、`vim`、Ctrl-C、颜色和 Tab 补全都正常），装插件、看日志、
  改配置不用再单独 SSH 上来。它跑的**就是本机**、用的就是面板进程的身份，不是连到别的服务器，
  所以没有任何 SSH 密钥或密码要存在面板里。

  **这个开关默认是关的，升级面板也不会把它打开** —— 开了它，面板密码就等价于这台机器的 SSH
  密码：任何能登录面板的人都能以运行面板的那个用户执行任意命令。所以开之前先确认密码够强、
  面板没有裸奔在公网上。关掉开关会立刻挂断已经开着的会话，不只是拦住新连接。

  会话是跟着页面走的：关掉标签页就挂断，连同它启动的程序一起（先 SIGHUP，3 秒后 SIGKILL）——
  这一点和实例控制台正好相反，因为没人看着的 shell 只会把命令留在半路。要让命令活得比标签页久，
  请用 systemd，或者在终端里开 tmux / screen。同时最多 4 个会话。

  Windows 上不可用（需要 ConPTY，暂未实现），面板会直接说明，开关也点不动。
- **文件管理器** —— 浏览实例目录、上传（点选或拖拽，带进度条，单文件默认上限 2 GB）、下载（支持断点续传）、重命名、新建文件夹、递归删除，以及在线编辑文本配置（ops.json、bukkit.yml、插件配置等）。所有路径操作都走 Go 1.25 的 `os.Root`，由内核层面把访问关在实例目录里 —— `..`、绝对路径、指向外部的符号链接一律拒绝，不依赖字符串清洗。上传同名文件默认拒绝，会问一句再覆盖。
- **资源监控** —— 每个实例的 CPU 和内存曲线，5 秒采样、内存里保留 1 小时，面板守护进程采集，所以关掉网页也不断档。仪表盘还有整机的内存/磁盘水位和 CPU 曲线。
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

按我觉得的优先级排：玩家列表和白名单/OP 管理（现在只能手改 JSON）、自动备份、多用户和权限、定时任务（定时重启/广播）、更多可下载的核心（Fabric / Forge / 基岩版）、更长时间的监控历史（现在只在内存里存 1 小时，重启面板就没了）、Windows 上的终端（要接 ConPTY）。

Java 那块有个已知边界：Temurin 只有 glibc 构建，musl 系统（Alpine 之类）装了也跑不起来 —— 面板会检测到并直接说明，让你改用系统包管理器装。

## 从源码构建

不想用发布的二进制，或者要改代码：

```bash
git clone https://github.com/Lanscarlos/HyperCraft.git
cd HyperCraft

make deps      # 装前端依赖（只需一次）
make build     # 构建前端 + 编译单二进制 ./hypercraft

./hypercraft -data ./data
```

同样是首次启动打印一次随机密码，然后开 http://127.0.0.1:19190 。

## 开发

```bash
make test           # go test -race ./...
make lint           # gofmt + go vet

# 热重载开发：两个终端
go run ./cmd/hypercraft -data ./data     # 后端 :19190
npm --prefix web run dev                 # 前端 :5173，API 自动代理到 19190

make cross          # 交叉编译 linux/amd64, linux/arm64, windows, darwin/arm64
make package VERSION=v1.2.0   # 交叉编译 + 打包出 release/ 里的压缩包和 SHA256SUMS.txt
```

### 发布

`make package` 是发布的唯一入口，正式版和快照都走它，所以本地打的包和 CI 打的完全一样。

- **正式版**：把 CHANGELOG 顶上的「未发布」改成 `## [x.y.z] - 日期` 合进 main，
  `.github/workflows/release.yml` 会打 tag 并发布。
- **快照**：`.github/workflows/snapshot.yml` 挂在 CI 后面，main 上每个通过 CI 的提交都发一版
  prerelease，版本号是下一个补丁版加 `-snapshot.<提交数>`。这个提交本身就是某个正式版时会跳过，
  免得同一份代码有两个版本号。只保留最近 5 个，旧的连同 tag 一起删。

### 结构

```
cmd/hypercraft/      入口：flag、启动、优雅关闭
internal/instance/   核心：进程监管、状态机、控制台环形缓冲、事件广播
internal/api/        HTTP + WebSocket
internal/serverfiles/ 文件管理，全部经由 os.Root 限制在实例目录内
internal/serverjar/   服务端核心库：PaperMC Fill API 客户端 + 后台下载任务 + data/cores 货架
internal/hostfs/      只读地列出宿主机目录，给实例目录选择器用
internal/hostterm/   本机终端：伪终端上的 shell，会话数上限与挂断（Windows 上返回不支持）
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

抬版本号的那个 PR 里，顺手把「快速开始」里的下载 URL 也改成新版本 —— 压缩包名带版本号，
所以没法用 `releases/latest/download/` 那种永久链接，只能手动跟。把「下载解压」那一小节里出现的
旧版本号全换掉即可：两条 `wget`、一条 `tar`，加正文里提到的那一次。

## 依赖与环境要求

只是部署的话，这一节可以跳过 —— 发布的二进制是 `CGO_ENABLED=0` 静态编译的，服务端要的 Java 面板自己会装，所以目标机器上什么都不用先准备。

从源码构建需要 **Go 1.25+**（文件管理器用到了 1.25 的 `os.Root.Rename` / `RemoveAll` 等；`GOTOOLCHAIN` 默认会自动下载，本机 Go 版本旧一些也不影响）。前端需要 Node 20+。

后端只有三个直接依赖：

- `gorilla/websocket` —— 控制台长连接
- `shirou/gopsutil` —— 跨平台读取进程和主机的 CPU / 内存
- `golang.org/x/text` —— 控制台编码转换（GBK / Big5 / Shift_JIS 等）

密码哈希用的是 Go 1.24 进标准库的 `crypto/pbkdf2`，没有引 `golang.org/x/crypto`。

面板自身很轻：实测常驻堆内存约 2 MB、十来个协程，跟它管理的 JVM 比可以忽略。

## License

MIT
