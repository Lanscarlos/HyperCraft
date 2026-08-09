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

发布产物是单文件二进制，前端已经嵌在里面，不需要 Go、Node 或者任何运行时依赖。部署就两件事：解压，交给 systemd。服务端要的 Java 也不用 `apt` —— 面板起来之后在「Java 环境」页点一下就能装，所以下面这套在一台**什么都没装**的 Debian 上从头到尾能跑通。

### 1. 下载解压到 `/opt/hypercraft`

```bash
sudo mkdir -p /opt/hypercraft
cd /opt/hypercraft
sudo wget https://github.com/Lanscarlos/HyperCraft/releases/download/v1.3.0/hypercraft-1.3.0-linux-amd64.tar.gz
sudo tar -xzf hypercraft-1.3.0-linux-amd64.tar.gz --strip-components=1
```

包里是二进制、`hypercraft.service` 和几个文档文件。ARM 机器（`uname -m` 显示 `aarch64`）把 URL 里的
`amd64` 换成 `arm64`。上面这个 URL 固定指向 v1.3.0，更新的版本见
[Releases 页面](https://github.com/Lanscarlos/HyperCraft/releases/latest) —— 不过装好之后面板能自己升级，
这个链接一般只用一次。

国内的机器直连 GitHub 大概率慢到没法用，在 URL 前面加个镜像前缀就行 ——
`https://ghfast.top/https://github.com/...`，这也是面板自己更新时默认走的那个。

想验下载完整性的话，同一个 release 里有 `SHA256SUMS.txt`（这个建议直连 GitHub 取，
校验文件和压缩包都从同一个镜像拿的话，校验就没多大意义了）：

```bash
sudo wget https://github.com/Lanscarlos/HyperCraft/releases/download/v1.3.0/SHA256SUMS.txt
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

1. 「资源库 → 服务端核心 → 下载核心」，选 Paper（或 Velocity）和版本，下载到**核心库**。
   核心库是面板级的：一个核心只下一次，之后开多少个服都从这里复制，不用再联网。
   把自己的 jar（Fabric、Forge、整合包服务端）丢进 `data/cores/` 也会出现在库里。
2. 「Java 环境」一键装一个 —— 刚装好的机器上一个 Java 都没有（1.20.5 起要
   Java 21，Paper 26.x 要 25）；系统里本来就有的 Java 也会一并列出来。
3. 侧栏「+ 新建实例」，填个名字（中文没问题，目录名会跟着走），从核心库里挑一个核心，
   点「创建」。目录留空就放在数据目录的 `servers/<名字>/` 下；也可以「浏览…」指到本机
   任意位置 —— 外挂硬盘、NAS 挂载点，或者一个你已经有服务端的目录（那里的 jar 会自动
   出现在「服务端 jar」的下拉里）。
4. 「启动设置」里选 Java 环境、调内存。
5. 「服务器配置」里点「我已阅读并同意 EULA」，改改 MOTD、端口、难度。
6. 要装插件的话，先去侧栏「插件库」添加插件（填 GitHub 仓库）并下一个版本，
   再回实例的「插件」标签点安装。插件在全局统一管理，实例这边只负责用哪个、用哪个版本。
7. 回「控制台」点启动。

**机器上已经有服务端了？**「所有实例 → 导入现有目录」直接接管它，跳过上面第 1、3 步。选中目录后
面板会就地读一遍并把看到的东西摆出来 —— 哪个 jar 像服务端、有哪些世界、多少插件、端口和 MOTD、
EULA 同意了没有 —— 确认之后只往面板自己的实例表里写一条记录，目录里的东西一个字节都不动。
已经被别的实例占着的目录会被拒绝：两个实例指同一个世界，一起开服会写坏存档。

## 命令行参数

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-data` | `HYPERCRAFT_DATA` | `./data` | 面板状态 + 服务器文件的根目录 |
| `-listen` | `HYPERCRAFT_LISTEN` | `0.0.0.0:19190` | 监听地址，会写进配置持久化 |
| `-username` | `HYPERCRAFT_USERNAME` | `admin` | 首次创建凭据时的用户名 |
| `-tls-cert` | `HYPERCRAFT_TLS_CERT` | | PEM 证书链，和 `-tls-key` 一起给就直接跑 HTTPS |
| `-tls-key` | `HYPERCRAFT_TLS_KEY` | | `-tls-cert` 对应的私钥 |
| `-log-level` | `HYPERCRAFT_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `-reset-password` | | | 重置成一个新随机密码，打印后退出 |
| `-version` | | | 打印版本 |

默认监听所有网卡，外网可直接访问。面板能在你的机器上执行任意控制台命令，公网部署请务必加 TLS，
或者把 `-listen` 改成 `127.0.0.1:19190` 只留本机 —— 见下面的部署章节。

### 直接跑 HTTPS

手上已经有证书（acme.sh 签的、Caddy 顺手签的、公司内部 CA 发的、云厂商送的）就不用再套一层反代：

```bash
hypercraft -data ./data -listen 0.0.0.0:19190 \
  -tls-cert /etc/ssl/hypercraft/fullchain.pem \
  -tls-key  /etc/ssl/hypercraft/privkey.pem
```

两个参数必须成对出现，只给一个会直接报错退出——半套配置多半是打错了，静悄悄退回明文
HTTP 比报错糟糕得多。证书在绑端口之前就加载，路径写错会立刻失败并指出是哪个文件，不会等到
服务器都起来了才发现。最低协议版本是 TLS 1.2。

证书续期后需要重启面板才会重新加载。**没有域名、只能用 IP 访问**的话，正规证书签不出来，
优先考虑下面的隧道方案而不是自签证书。

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
├── plugins/          # 插件库：一个插件一个目录，每个版本再一个目录
│   ├── registry.json # 插件的来源、下载过的版本、最近一次检查更新的结果
│   └── essentials/
│       ├── v2.20.1/EssentialsX-2.20.1.jar
│       └── v2.21.0/EssentialsX-2.21.0.jar
├── instance-plugins.json  # 哪个实例装了哪些插件、各自钉在哪个版本
└── servers/
    ├── 生存服/        # 实例的工作目录：jar、存档、配置全在这
    └── 创造服/
```

都是普通 JSON，手改也行（改完重启面板）。实例目录也可以指向磁盘上任意已有的服务器，不一定要在 `servers/` 下面。

`panel.json` 里的 `maxUploadMb` 控制单个上传文件的大小上限，默认 2048。

`panel.json` 里的 `terminal` 控制本机终端：`{"enabled": false}` 是默认值，界面上的开关改的
就是它；`shell` 可选，填了就用指定的程序（不填按 `$SHELL` → `bash` → `sh` 找）。

`panel.json` 里的 `trustedProxies` 只在面板挂在反代或加速器后面时才需要，见
[登录保护与真实客户端 IP](#登录保护与真实客户端-ip)。

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

面板默认是明文 HTTP，直接开 19190 给公网等于把没有 TLS 的登录框挂上去。两条路都行：手上已经有证书就
直接用 [`-tls-cert` / `-tls-key`](#直接跑-https)；没有的话只放行 443，把 `-listen` 收回
`127.0.0.1:19190`，用 Nginx/Caddy 反代并配上 TLS（有域名时这条更省事，证书能自动续期）。
WebSocket 需要透传 Upgrade 头：

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

## 登录保护与真实客户端 IP

面板校验密码用的是 210k 轮 PBKDF2，一次约 0.1 秒的单核 CPU。这对存储密码是正确的成本，但
`/api/auth/login` 和 `/api/auth/devices` 都是公开的——不限制的话，猜密码和把 CPU 从 Minecraft
服务端手里抢走这两件事，任何人都能做，而且后者连密码都不用猜对。

两道限制各管一件事，谁也替代不了谁：

- **按客户端地址限速** —— 连续 5 次失败后开始限流，每 30 秒回一次机会，返回 `429` 并带
  `Retry-After`。只有**失败**扣次数，登录成功会把该地址的记录清空，所以连打几个错字再输对不会被罚。
  被限流的请求约 4ms 返回，不再跑 PBKDF2。
- **并发派生上限** —— 同时进行的密码校验数被限制在约 1/4 的核数（下限 2），排不上就返回 `503`。
  这一道才是真正兜住 CPU 的：换多少个源地址都绕不过去。

配对接口和登录共用同一份额度，不然把登录打满之后换一扇门继续敲就行了。

### 在反代 / 加速器后面（`trustedProxies`）

限速按"客户端地址"计数，而面板默认只认 TCP 对端地址。**直连时这是对的**：`X-Forwarded-For`
是任何客户端都能随手写的头，无条件相信它等于让攻击者每个请求换一个计数桶，限速直接失效。

但在 Nginx 反代、CDN 或者阿里云 GA 这类加速器后面，所有请求都从少数几个回源地址进来，全算作
同一个客户端的话，一个攻击者就能把所有人一起锁在门外。这时在 `panel.json` 里列出那些地址：

```json
{
  "trustedProxies": ["127.0.0.1", "10.0.0.0/8", "203.0.113.7"]
}
```

支持 CIDR，也支持写单个 IP（按 /32、/128 处理）。只有当 TCP 对端命中这个列表时，面板才会去读
`X-Forwarded-For`，并且取的是**最右侧那个不属于可信代理的地址**——每一跳都往右追加自己看到的
地址，所以左边的部分是客户端自己写的，取左边就等于把计数桶交给攻击者挑。条目写错不会导致面板
起不来，只会在启动日志里告警并跳过那一条。

> 阿里云 GA 的回源地址在 **控制台 → 实例 → 监听详情 → 终端节点出公网 IP** 里能查到。注意那是**一批**
> 地址而不是一个（每个加速地域都有），并且增删加速地域时会变化，所以别只填你抓包看到的那一个。

### 排查：到底是哪个地址在访问面板

**在面板里看：「设置 → 登录记录」。** 上半页是当前这次连接的两个地址，下半页是面板启动以来的
登录、配对和限流事件，每条都带客户端地址和 TCP 对端：

```
当前连接
  客户端地址  203.0.113.9      ← trustedProxies 生效时，代理转告的真实客户端
  TCP 对端    10.1.2.3         ← 代理回源到面板的地址，防火墙要放行的是这个

最近事件
  10:45:10  已限流 ×2    —      198.51.100.7
  10:45:09  密码错误 ×5  root   198.51.100.7
  10:45:09  登录成功     admin  203.0.113.9
```

连续重复的同类事件会折叠成一行并标次数，否则一次限流就能把其他记录挤没。这份列表**只存在内存里**，
面板一重启（包括自动更新）就清空。

**需要长期留存的记录看系统日志**，那才是权威来源：

```bash
journalctl -u hypercraft | grep -E 'signed in|failed login'
```

```
level=INFO msg="signed in"    username=admin remote=10.1.2.3:41522 client=203.0.113.9
level=WARN msg="failed login" username=admin remote=10.1.2.3:41530 client=203.0.113.9
```

没配 `trustedProxies` 时 `client` 就等于 `remote` 的地址部分。

> 用加速器时注意：这里显示的是**已经连过**的地址，而回源节点通常有一批。防火墙白名单要以服务商
> 控制台给出的完整清单为准（GA 见上一节），这一页适合用来**验证**清单对不对，不适合用来生成清单。

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

- **全局仪表盘** —— 首页一眼看完：实例运行中/总数、已装 Java、核心库存量、插件更新、面板版本，加上整机的
  内存/磁盘水位和 CPU 曲线；实例卡片上直接有启动/停止按钮，不用先进控制台。
- **Java 环境管理** —— 「Java 环境」页可以一键下载 Eclipse Temurin 的 JRE / JDK（任意大版本，LTS 有标注），
  装进面板数据目录，不碰系统里的 Java；每个实例在「启动设置」里各自选一个，所以 1.12 的老服和
  1.21 的新服可以在同一台机器上共存。列表里会显示系统自带的 Java、每个运行时被哪些实例用着，
  正在跑的不让删。手动解压进 `data/java/` 的 JDK 也会被自动认出来。
  下载源可选：默认「自动」依次试清华 TUNA、南京大学、华为云、GitHub 加速，都不通才回 Adoptium
  官方（国内机器直连 GitHub 常年几十 KB/s，这一步能省掉大半个小时）；也可以指定某一个。
  版本信息和 SHA-256 始终取自 Adoptium 官方 API，镜像只负责传压缩包，对不上一律不装。
- **面板内自动更新** —— 有新版本时侧栏会标出来，「面板设置 → 面板更新」点一下就更新，不用 SSH 上去换二进制。
  更新分两步：**第一步下载校验新版本，同时优雅停止所有服务器**（两件事互不依赖，一个几十兆的包和一个
  大世界的存盘没必要排队）；**第二步在服务器全部停稳之后**才换二进制、用 `exec` 就地重启（PID 不变，
  systemd 察觉不到），然后自动把刚才在跑的服务器拉回来。任何一步失败都不动二进制，并且把这次停掉的
  服务器重新拉起来。更新期间不能启动服务器。确认弹窗会列出要停哪几个，更新页会实时显示下载进度和还在
  等哪台服存档。旧二进制留作 `hypercraft.old` 方便回退。

  下载默认走 `https://ghfast.top/` 镜像（国内直连 GitHub 的下载速度基本没法用），界面上可以
  换成别的或直连，镜像挂了自动回退直连。**镜像只搬压缩包**：校验用的 `SHA256SUMS.txt` 优先
  从 GitHub 直接取，所以镜像换不掉二进制——它给的包对不上 GitHub 的哈希就会被拒绝。

  **更新通道**分「正式版」和「快照」，默认正式版。快照是 main 分支每个通过 CI 的提交自动出的
  一版（`1.2.1-snapshot.431` 这种，标着 prerelease），想提前试新功能可以在更新页切过去，
  更新流程和正式版完全一样。快照通道同时也看正式版——按版本号取最新的那个，所以
  `1.2.1` 一发布就会自动从 `1.2.1-snapshot.*` 更新过去。切回正式版通道时，如果当前跑的快照
  比最新正式版新，面板会提示装回最新正式版，那一步是往回装的，会明确说清楚。
  生产环境请留在正式版通道：快照只保证过了 CI。
- **服务端核心库** —— 面板级的 jar 货架，在「资源库 → 服务端核心」里管（分「核心库」和「下载核心」两页）。目前可下载的是 Paper 和
  Velocity，数据来自 PaperMC 的 Fill API；下载好的核心留在 `data/cores/`，新建实例或在「启动设置」
  里选一下就复制一份过去 —— 实例拿到的是自己的副本，所以删库里的核心不会动到正在跑的服。
  自己丢进 `data/cores/` 的 jar 也会被认出来，一样能复制。选版本后
  面板自己去下（走服务器的网络，不经过你的浏览器），下载归守护进程管，关掉网页也会继续，
  重开页面能接上进度。落盘前校验 sha256 和体积，先写 `.part` 再改名 —— 失败、取消或断网都不会
  留下一个看起来能启动的半截 jar，也不会默默覆盖已有文件。
- **插件管理（全局 + 实例两层）** —— 插件的**添加、来源、版本和更新统一在侧栏的「插件库」里管**，
  实例那边只能「拿来用、换版本、停用」。这是有意的分工：如果每个服都能自己下载，同一个插件很快
  就会有六份说不清区别的副本，而最想回滚到的那个旧版永远是被就地覆盖掉的那个。

  **全局（插件库）**：填一个 GitHub 仓库（`EssentialsX/Essentials`，或者直接把仓库地址粘进去），
  面板就从它的 Release 里拉 jar —— 和面板自己更新走的是同一条路（默认 `https://ghfast.top/`，
  镜像失败自动回落 GitHub 直连）。一个 Release 里挂了
  好几个 jar 时，默认会跳过 `-sources`、`-javadoc`、`-dev` 这类附带包，在剩下的里挑最大的；
  不满意可以填个文件名通配（`EssentialsX-*.jar`）自己指定。预发布版默认不列，要的话勾一下。

  **私有仓库**：自己写的插件发在私有仓库里也能管。在插件库页面填一个 GitHub 访问令牌
  （fine-grained 令牌给目标仓库 `Contents: Read-only`，classic 令牌勾 `repo`）就行 —— 仓库是不是
  私有的面板会自己问 GitHub，检查更新和下载前各核一次，检查和下载都带认证走 API。
  私有仓库的 jar 只能从 API 取，所以
  **不经过下载镜像** —— 镜像是第三方，没有你的令牌，也不该知道这个仓库存在。令牌只发给
  `api.github.com`，存在 `panel.json`（`0600`）里，接口只回答「配没配」和末尾四位，读不回原文。
  顺带一提，公开仓库配了令牌也有用：匿名 API 每小时 60 次会变成 5000 次。

  **下载源可选**：ghfast.top、gh-proxy.com、github.moeyy.xyz、GitHub 直连，或者自己搭的代理前缀，
  在「插件库 → 插件源」里选。默认自动，按顺序挨个试；指定了某一个，它不通也会回落到直连。镜像只搬
  jar —— 版本列表和更新检查始终直连 api.github.com，私有仓库的 jar 只走认证过的 API。

  **页面分两层**：「插件库」是列表（谁有更新、谁没人用、一共多少磁盘），点进去是单个插件的页面
  （版本、上游发布、来源设置、删除）。挑版本和看更新说明本来就是一次对着一个插件做的事。

  **每个版本单独存**：`data/plugins/<插件>/<版本>/`。很多插件每次发布都叫同一个 `Foo.jar`，
  按文件名存的话新版会直接盖掉旧版，回滚时就没东西可回了。检查更新是手动点的（整体或单个）——
  匿名 GitHub API 一小时只有 60 次，页面自动刷新会把配额花在没人看的地方，所以结果是缓存下来的，
  卡片上会写清上次检查是什么时候。

  **实例（「插件」标签）**：从库里挑一个插件和版本装进去 —— 拿到的是**自己的副本**，所以删掉库里的
  插件不会动到已经装上的服。换版本是一次操作：新 jar 落地后旧的才删，不会出现同一个插件两个版本
  都在 `plugins/` 里害得服务器起不来。停用是把 jar 改名成 `.jar.disabled`（Bukkit 系只加载
  `*.jar`），插件自己的配置目录原封不动，重新启用就是改回来。**这些都要重启服务器才生效**，
  正在跑的时候界面会提醒。

  不是面板装的 jar 也会列出来（自己传的、从备份恢复的），并且会**自动认出它是什么**：面板读 jar
  自己的描述文件（plugin.yml / paper-plugin.yml / bungee.yml / velocity-plugin.json /
  fabric.mod.json），按服务器启动时读到的那个名字和版本显示，而不是文件名 ——
  `build-42-shaded.jar` 是谁，光看文件名没人答得上来。如果它和插件库里某个版本**逐字节一致**
  （按下载时记的 SHA-256 比对），还会多一个「接管」按钮：点了它就变成普通的受管插件，能换版本、
  能跟更新，磁盘上什么都不动。只认内容不认文件名 —— 靠名字猜等于让面板声称它知道这台服务器在跑
  哪个版本。Fabric / Forge 的模组把「安装目录」填成 `mods` 就行，同一套机制。

  一点实话：GitHub Release 没有官方校验和可对，所以这里只能校验体积、记录下载到的 SHA-256，
  信任链落在 GitHub 的 TLS 和你填的那个仓库上 —— 这和 Java 环境、服务端核心（都有上游发布的
  哈希可比对）不一样。
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
- **控制台** —— xterm.js 渲染。默认把服务端跑在**伪终端**上，所以你在网页里得到的和 SSH 上去开服务器是同一个控制台：Tab 补全由正在运行的服务端回答、进度条不等换行就能看到、颜色不用强制。可以关掉换回管道模式。详见下面一节。
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

## 控制台：终端模式、颜色和编码

**默认是终端模式**：面板给服务端开一个伪终端（pty），而不是拿管道读它的输出。
服务端因此认为自己跑在真终端里，于是：

- **Tab 补全来自服务端本身** —— JLine 只有在看得见终端时才工作。你按的 Tab 是**正在
  运行的那个服务端**在回答，所以插件注册的命令、命令方块参数、真实在线玩家名、不同
  版本的差异全都自动正确，不需要面板维护一份命令表。
- **没换行的输出也看得到** —— 按行读意味着一段只有 `\r` 的输出（区块预生成、Forge /
  NeoForge 安装器、各种 mod loader 的进度条，以及 `Continue? [y/N] ` 这类提示）在
  面板上是一片空白，看着像卡死了。终端模式下来多少显示多少。
- **颜色是天生的** —— TerminalConsoleAppender 检测到终端就自己上色，不用再强制。
- **窗口宽度是真的** —— 服务端按你浏览器窗口的宽度折行，而不是默认的 80 列。多个
  浏览器同时看同一个服务器时，取所有窗口里最窄的那个，谁都不会看到错位的文字。

代价是终端只有一条流，**stderr 不再单独标红**（面板仍然完整记录它，只是无法区分）。
需要这个区分的实例，可以在「启动设置 → 控制台」里关掉终端模式换回管道；关掉后原来的
命令输入框、面板自带的 Tab 补全和 ↑↓ 历史也会一并回来。

Windows 上没有可用的伪终端（需要 ConPTY，暂未实现），所有实例都以管道模式运行，开关
是灰的。Linux / macOS 上万一开不出 pty，面板会回落到管道并在控制台里说明，而不是拒绝
启动服务器。

### 编码

启动 jar 时面板会加上 `-Dfile.encoding=UTF-8` 和 `stdout/stderr/stdin` 那一组
（Java 8~17 认前者，18+ 认后者），让 JVM 直接说 UTF-8；面板这边再按「输出编码」设置
解码。中文、`─ ┌ ➜ ✔` 这类符号和 emoji 因此都能正常显示。

用自定义启动命令（`start.bat`、基岩版、BungeeCord）时面板加不了 JVM 参数，如果还是
乱码，把「输出编码」直接选成 GBK —— 输入的命令也会按同一编码发回去。

「自动」在两种模式下含义略有不同：管道模式逐行嗅探（合法 UTF-8 原样放行，否则按系统
编码兜底），而终端模式下没有行边界可供重新判断，一次性按系统编码解析整条流（现代
Linux 上就是 UTF-8）—— 解码器读到一半改主意会毁掉已经半消费的字符。所以如果服务端
坚持输出别的编码，把「输出编码」直接选定，那条路径是完整有状态的，跨读边界的多字节
字符不会被切断。

### 管道模式下的颜色和补全

关掉终端模式后，面板会补上管道拿不到的那两样：

- **颜色** —— 自动加上 `-Dterminal.jline=false -Dterminal.ansi=true`，
  TerminalConsoleAppender 认这两个参数，加上之后即使没有终端也照样输出 ANSI 颜色。
- **Tab 补全** —— 输入框按 Tab 补全命令名，再按继续切换候选（Shift+Tab 往回切，
  Esc 关掉）。补全内容包括：原版和 Paper 的常用命令、`gamemode` / `difficulty` /
  `gamerule` 这类子参数、**在线玩家名**（从 `xxx joined the game` 和 `list` 的回复里
  学的），以及你在这个浏览器里发过的历史命令。这份补全是面板自己的，不依赖服务端 ——
  隔着管道问不到它。

这两个 `-D` 参数在终端模式下**不会**被加上：`terminal.jline=false` 恰好会关掉终端
模式最想要的那个补全。它们加在你自己填的 JVM 参数**前面**，所以想覆盖哪个，自己再
写一遍就行。

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
make package VERSION=v1.3.0   # 交叉编译 + 打包出 release/ 里的压缩包和 SHA256SUMS.txt
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
internal/plugin/      插件库：GitHub Release 客户端 + 后台下载任务 + 版本货架 + 实例装载/停用
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
