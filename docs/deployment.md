# 部署指南

从一台什么都没装的 Debian 到一个能对外提供服务的面板。发布产物是单文件二进制，前端已经嵌在里面，
不需要 Go、Node 或者任何运行时依赖；服务端要的 Java 也不用 `apt`，面板起来之后在「Java 环境」页点
一下就能装。

- [1. 下载解压](#1-下载解压)
- [2. 建一个专用用户](#2-建一个专用用户)
- [3. 交给 systemd](#3-交给-systemd)
- [4. 打开面板](#4-打开面板)
- [命令行参数](#命令行参数)
- [数据目录](#数据目录)
- [systemd 单元里的三个关键点](#systemd-单元里的三个关键点)
- [防火墙](#防火墙)
- [反向代理与 TLS](#反向代理与-tls)
- [直接跑 HTTPS](#直接跑-https)
- [升级与备份](#升级与备份)

## 1. 下载解压

```bash
sudo mkdir -p /opt/hypercraft
cd /opt/hypercraft
sudo wget https://github.com/Lanscarlos/HyperCraft/releases/download/v0.4.0/hypercraft-0.4.0-linux-amd64.tar.gz
sudo tar -xzf hypercraft-0.4.0-linux-amd64.tar.gz --strip-components=1
```

包里是二进制、`hypercraft.service` 和几个文档文件。ARM 机器（`uname -m` 显示 `aarch64`）把 URL 里的
`amd64` 换成 `arm64`。上面这个 URL 固定指向 v0.4.0，更新的版本见
[Releases 页面](https://github.com/Lanscarlos/HyperCraft/releases/latest) —— 不过装好之后面板能自己
升级，这个链接一般只用一次。

国内的机器直连 GitHub 大概率慢到没法用，在 URL 前面加个镜像前缀就行 ——
`https://ghfast.top/https://github.com/...`，这也是面板自己更新时默认走的那个。

想验下载完整性的话，同一个 release 里有 `SHA256SUMS.txt`（这个建议直连 GitHub 取，校验文件和压缩包
都从同一个镜像拿的话，校验就没多大意义了）：

```bash
sudo wget https://github.com/Lanscarlos/HyperCraft/releases/download/v0.4.0/SHA256SUMS.txt
sha256sum -c SHA256SUMS.txt --ignore-missing
```

## 2. 建一个专用用户

```bash
sudo useradd -r -s /usr/sbin/nologin minecraft
sudo chown -R minecraft:minecraft /opt/hypercraft
```

`chown` 不能省：数据目录要写，面板内自动更新还要原地替换 `/opt/hypercraft/hypercraft` 并留一份
`hypercraft.old`，运行用户对这个目录没有写权限的话更新会失败。

## 3. 交给 systemd

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

## 4. 打开面板

默认监听 `0.0.0.0:19190`，也就是所有网卡都监听，装好之后直接访问 `http://你的服务器IP:19190` 就行
（别忘了在防火墙 / 安全组里放行 19190）。

面板讲的是明文 HTTP，而且能在这台机器上执行任意控制台命令，所以要长期挂在公网上，务必配反代加 TLS，
见[下面](#反向代理与-tls)。只想自己用、不想暴露出去的话，把 `-listen` 改回 `127.0.0.1:19190`，然后在
**你自己的电脑上**开个 SSH 隧道：

```bash
ssh -L 19190:127.0.0.1:19190 you@your-server
```

接下来怎么开第一个服，见[上手指南](getting-started.md)。

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
├── db/               # 数据库环境：引擎二进制和各服务的数据目录
└── servers/
    ├── 生存服/        # 实例的工作目录：jar、存档、配置全在这
    └── 创造服/
```

都是普通 JSON，手改也行（改完重启面板）。实例目录也可以指向磁盘上任意已有的服务器，不一定要在
`servers/` 下面。

`panel.json` 里几个值得知道的键：

- `maxUploadMb` —— 单个上传文件的大小上限，默认 2048。
- `terminal` —— 本机终端，`{"enabled": false}` 是默认值，界面上的开关改的就是它；`shell` 可选，
  填了就用指定的程序（不填按 `$SHELL` → `bash` → `sh` 找）。
- `trustedProxies` —— 只在面板挂在反代或加速器后面时才需要，见
  [安全与访问控制](security.md#在反代--加速器后面)。

## systemd 单元里的三个关键点

`deploy/hypercraft.service`（也在每个 Linux 压缩包里）是一份可用的示例：

- **`TimeoutStopSec=300`** —— 面板停止时要等所有世界存盘完，默认的 90 秒对大世界不够。
- **`KillMode=mixed`** —— 只给面板发信号，由它自己按顺序停子进程。否则 systemd 会直接 SIGTERM 掉
  JVM，跳过优雅存盘。
- **`ProtectSystem=full` + `ReadWritePaths=/opt/hypercraft`** —— 数据目录得在这个路径下面。想放别处
  （比如单独挂的数据盘），或者某个实例目录指向了 `/opt/hypercraft` 外面，记得把那个路径加进
  `ReadWritePaths`，否则面板会遇到只读文件系统。

## 防火墙

放行 Minecraft 端口和面板端口：

```bash
sudo ufw allow 25565/tcp
sudo ufw allow 19190/tcp
```

## 反向代理与 TLS

面板默认是明文 HTTP，直接开 19190 给公网等于把没有 TLS 的登录框挂上去。两条路都行：手上已经有证书就
直接用 [`-tls-cert` / `-tls-key`](#直接跑-https)；没有的话只放行 443，把 `-listen` 收回
`127.0.0.1:19190`，用 Nginx / Caddy 反代并配上 TLS（有域名时这条更省事，证书能自动续期）。
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

挂上反代之后记得配 `trustedProxies`，否则限速会把所有人算成同一个客户端 ——
见[安全与访问控制](security.md#在反代--加速器后面)。

## 直接跑 HTTPS

手上已经有证书（acme.sh 签的、Caddy 顺手签的、公司内部 CA 发的、云厂商送的）就不用再套一层反代：

```bash
hypercraft -data ./data -listen 0.0.0.0:19190 \
  -tls-cert /etc/ssl/hypercraft/fullchain.pem \
  -tls-key  /etc/ssl/hypercraft/privkey.pem
```

两个参数必须成对出现，只给一个会直接报错退出 —— 半套配置多半是打错了，静悄悄退回明文 HTTP 比报错
糟糕得多。证书在绑端口之前就加载，路径写错会立刻失败并指出是哪个文件，不会等到服务器都起来了才发现。
最低协议版本是 TLS 1.2。

证书续期后需要重启面板才会重新加载。**没有域名、只能用 IP 访问**的话，正规证书签不出来，优先考虑上面
的 SSH 隧道方案而不是自签证书。

## 升级与备份

**升级**：面板能自己更新 —— 有新版本时侧栏会标出来，「面板设置 → 面板更新」点一下就换二进制并原地
重启，不用 SSH 上去。细节（两步流程、镜像、更新通道）见[功能详解](features.md#面板自更新)。

**备份**：整个数据目录就是全部状态。服务器停下来之后打包即可：

```bash
sudo systemctl stop hypercraft
sudo tar -czf hypercraft-backup-$(date +%F).tar.gz -C /opt/hypercraft data
sudo systemctl start hypercraft
```

不想停服的话，至少要保证世界文件是一致的：先在控制台里 `save-off` + `save-all`，打包完再 `save-on`。
面板暂时还没有内置的自动备份。
