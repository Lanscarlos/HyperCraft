# 提案：Java 运行环境支持 Azul Zulu，并默认使用它

> **状态：方案已定，待实现。**
>
> 把 `internal/javaruntime` 里写死的「Adoptium」抽成一根**发行版**轴，Temurin 和 Zulu 并存，
> 默认 Zulu；下载源随发行版分表；顺带把 Alpine（musl）这个一直只能弹警告的场景做通。

- [要解决的问题](#要解决的问题)
- [先说清楚：Zulu 没有可用的国内镜像](#先说清楚zulu-没有可用的国内镜像)
- [Azul 的接口长什么样](#azul-的接口长什么样)
- [发行版这根轴](#发行版这根轴)
- [下载源](#下载源)
- [已装运行时与目录命名](#已装运行时与目录命名)
- [musl](#musl)
- [数据模型](#数据模型)
- [接口](#接口)
- [界面](#界面)
- [错误处理](#错误处理)
- [测试](#测试)
- [明确不做的事](#明确不做的事)
- [落地顺序](#落地顺序)
- [未决问题](#未决问题)

## 要解决的问题

面板只会装 **Eclipse Temurin**，而且是写死的：

```go
// internal/javaruntime/adoptium.go
const DefaultBaseURL = "https://api.adoptium.net/v3"

query := url.Values{
	"vendor": {"eclipse"},   // ← 写死
	...
}
endpoint := "/assets/latest/" + strconv.Itoa(major) + "/hotspot?" + query.Encode()
```

想换一个 OpenJDK 发行版，今天没有任何入口——不是「设置项藏得深」，是这个概念在代码里根本不存在。
`Release`、`Client`、`Sources()`、`installID`、API 响应、`JavaPage.tsx` 全都是 Adoptium 形状的：
`installID` 直接拼 `"temurin-" + version`，`source.go` 的五个下载源全部围绕 Adoptium 的发布树建，
页面文案里「Eclipse Temurin」是字符串常量。

这个提案要的是：**Temurin 和 Zulu 并存，默认 Zulu，将来再加第三个发行版时改一个文件而不是十个。**

顺带解决一件一直卡着的事——`platform.go` 对 Alpine 的处理是道歉：

```go
return "这台机器用的是 musl（Alpine 之类），Temurin 是 glibc 构建，装上也跑不起来。" +
	"请用系统包管理器装 OpenJDK，或换 Liberica 的 musl 版本，再在启动设置里手填路径。"
```

**Zulu 有 musl 构建。** 这根轴一旦有了，Alpine 上头一回能真装真跑。

## 先说清楚：Zulu 没有可用的国内镜像

这一节比功能本身重要，因为它决定了「下载源也优先选 Zulu 的」这个诉求能满足到什么程度。

现有五个源之所以能成立，是因为 Temurin 的包**躺在 GitHub release 上**，而 GitHub release 在国内
是灾难，于是国内教育网和商业云都做了 Adoptium 发布树的 rsync 副本，`ghfast.top` 也能给
`https://github.com/...` 套一层代理。

**Zulu 的包走 `cdn.azul.com`，不是 GitHub。** 于是：

- 三个镜像源（清华 / 南大 / 华为）是 **Adoptium 树**的副本，对 Zulu 一个字节都提供不了；
- `ghfast.top` 的代理只对 `https://github.com/` 开头的链接生效（`proxyLink` 明确判了前缀），
  对 `cdn.azul.com` 直接返回空串，等于不存在。

我逐个探了国内主流镜像站的 `/zulu/` 路径：**南大、阿里、北外、腾讯返回实打实的 404**；清华和中科大
在写方案的沙箱里根本连不通，判定不了；华为站点对**任何**路径都返回它自己的单页应用 HTML——包括它
确实在提供的 Adoptium 文件——所以从沙箱里也判定不了。

**结论：按「没有国内 Zulu 镜像」来设计**，并给出一个不需要我猜的出口（见下一节的自定义前缀）。

缓解因素有两个，都不是我能替运维证实的，得在真机上看：

1. `cdn.azul.com` 是商业 CDN，不是 GitHub 的 release 对象存储，国内直连通常好得多；
2. 实在不行，Temurin 连同它那五个镜像**一个都没删**，切回去就是两次点击。

## Azul 的接口长什么样

已在真实网络上验证过（2026-09-11）。一次请求就能拿齐 `Release` 需要的全部字段：

```
GET https://api.azul.com/metadata/v1/zulu/packages/
    ?java_version=21&os=linux&arch=x64&lib_c_type=glibc
    &archive_type=tar.gz&java_package_type=jre
    &javafx_bundled=false&crac_supported=false
    &release_status=ga&availability_type=ca&certifications=tck
    &latest=true&page_size=1
    &include_fields=sha256_hash,size,lib_c_type
```

```json
[{"download_url":"https://cdn.azul.com/zulu/bin/zulu21.52.203-ca-jre21.0.12.1-linux_x64.tar.gz",
  "java_version":[21,0,12,1],"name":"zulu21.52.203-ca-jre21.0.12.1-linux_x64.tar.gz",
  "sha256_hash":"170f31af...4e50","size":52435200,"lib_c_type":"glibc",...}]
```

与 Adoptium 对得上的地方：有 `download_url`、有 `sha256_hash`、有 `size`。**校验和这条安全底线
一点不打折**——`Installer.download` 那套「字节先落盘、比对通过才解压」的逻辑原样复用，校验和依旧
来自发行版官方的元数据接口而非供货的那个源。

三个必须写进代码的坑，都是实测踩出来的：

1. **必须显式 `crac_supported=false`。** 不带的话 CRaC 变体会混进结果——实测第二次查询就拿到了
   `zulu21.52.203-ca-crac-jre21.0.12.1-linux_x64.tar.gz`，那不是普通 JRE。
2. **必须显式 `javafx_bundled=false`**，否则会拿到捆了 JavaFX 的包（几十兆的无用负担）。
3. **返回里的 `arch` 字段不可信**：查 `arch=x64` 拿回来的条目写的是 `"arch":"x86"` 配
   `"hw_bitness":64`。请求参数是对的，响应字段只是 Azul 的历史命名。**不要拿它回填 `Release.Arch`，
   也不要拿它做结果校验**，用请求时的 `platform.Arch`。

可装的大版本（GA）：8 / 11 / 13-26。Adoptium 是 8 / 11 / 16-26，**Zulu 多出 13 / 14 / 15**，反过来
Adoptium 没有一个是 Zulu 缺的（2026-09-11 实测两边接口）。所以「可装版本列表」必须跟着发行版走，
不能两边共用一份。

拿真包验过的事（`zulu21.52.203-ca-jre21.0.12.1-linux_x64.tar.gz`，52 MB）：

- 下下来的 sha256 与接口声明的**完全一致**，整条校验链通；
- 包内是单层 `zulu21.../bin/java`，现有 `flatten()` 吃得下；
- `release` 文件里 `IMPLEMENTOR="Azul Systems, Inc."`、`JAVA_VERSION="21.0.12.1"`、`LIBC="gnu"`；
- **`release` 文件里没有 `IMAGE_TYPE`。** Temurin 有，Zulu 没有。见「已装运行时与目录命名」。

## 发行版这根轴

`internal/javaruntime` 里加一个包内私有的 `provider` 接口，两个实现：

```go
// Distribution ids, also the directory-name prefix of anything installed.
const (
	DistZulu    = "zulu"
	DistTemurin = "temurin"
)

// provider is one OpenJDK distribution: where to ask what exists, and where
// the bytes can come from.
type provider interface {
	Majors(ctx context.Context) ([]Major, error)
	LatestRelease(ctx context.Context, major int, imageType string, platform Platform) (Release, error)
	sources() []source
}
```

文件划分：

| 文件 | 内容 |
| --- | --- |
| `adoptium.go` | Temurin 的 provider（现有代码收缩进来） |
| `azul.go` | **新**，Zulu 的 provider |
| `fetch.go` | **新**，两边共用：HTTP 客户端、`getJSON`、`checkDownloadURL`、`safeFileName`、`isSupportedArchive` |
| `source.go` | 两张源表 + 自定义前缀 + `attempts` |
| `distribution.go` | **新**，`Distribution` 常量、`Distributions()`、`ResolveDistribution` |

`Client` 从「Adoptium 客户端」变成「按发行版分发的门面」：

```go
func (c *Client) Majors(ctx context.Context, dist string) ([]Major, error)
func (c *Client) LatestRelease(ctx context.Context, dist string, major int, imageType string, platform Platform) (Release, error)
func (c *Client) Fetch(ctx context.Context, release Release, sourceID string) (io.ReadCloser, string, error)
```

`Fetch` 的签名**不变**：`Release` 新增一个 `Distribution` 字段，它自己就知道该问哪张源表。同理
`installID`、`Job`、错误消息都从 release 上取，不必一路多传一个参数——这是把字段放进 `Release` 而
不是到处加形参的理由。

那份 `Majors` 的一小时缓存现在是 `Client` 上的单个字段，要变成 `map[string][]Major` 按发行版分键，
否则切一次发行版就会拿到另一边的版本列表，而且缓存一小时不会自己好。

## 下载源

`source.go` 的 `mirrors` 拆成两张表：

```go
// temurinSources: 现有五个原封不动（清华 / 南大 / 华为 / ghfast / 官方）

var zuluSources = []source{
	{
		Source: Source{ID: SourceOfficial, Name: "Azul 官方 CDN", Note: "cdn.azul.com 直连，商业 CDN，国内一般比 GitHub 好走"},
		link:   func(release Release) string { return release.URL },
	},
}
```

`SourceAuto` 和 `SourceOfficial` 两个 id 在两张表里都存在且语义一致（official = 上游直连），这样
面板记住的 `javaSource` 跨发行版仍然讲得通。

**自定义前缀**是 Zulu 这边唯一的加速出口。形状照抄 `internal/plugin/mirrors.go` 的 `ResolveMirror`
——仓库里已经有这个模式，`config.Panel.PluginMirror` 的注释写着「by the id of one of
plugin.Mirrors() or as a custom URL prefix」。运气好的是 **Zulu 的 CDN 路径是扁平的**
（`https://cdn.azul.com/zulu/bin/<文件名>`），所以「前缀 + 文件名」就够，不像 Adoptium 得拼
`<major>/<image>/<arch>/<os>/<文件名>`。

前缀一旦填了就排在官方前面，官方仍然兜底——沿用 `attempts` 里那条既有原则：

> 每一种选择最后都落到官方链接上，因为镜像是定时同步的，一小时前发布的版本压根不在上面。

签名变化：`Sources(dist)`、`ResolveSource(dist, id)`、`SourceName(dist, id)`。

## 已装运行时与目录命名

`installID` 里写死的前缀换成发行版：

```go
return release.Distribution + "-" + version + "-" + release.ImageType
// zulu-21.0.12.1-jre   /   temurin-21.0.12-jre
```

**已装的 `temurin-*` 目录不动、不迁移、不改名。** `store.inspect` 判 vendor / version 靠的是读目录里
的 `release` 文件（`applyReleaseFile`），与目录名前缀无关，所以老运行时照常识别、照常能用、照常能删。
不做迁移是有意的：重命名一个目录，会把所有指着旧路径的实例启动配置一起打断。

**但 `ImageType` 有个真实缺陷要补。** Zulu 的 `release` 文件没有 `IMAGE_TYPE`（实测确认），而
`applyReleaseFile` 只认这个键，于是 Zulu 运行时的 `ImageType` 会是空串，`JavaPage.tsx:473` 的
`runtime.imageType.toUpperCase()` 会渲染出一个空徽章。

补法是在 `inspect` 里加一条回落，**不依赖发行版也不依赖目录名**：

```go
// Zulu's release file carries no IMAGE_TYPE, and a hand-built runtime may
// carry no release file at all. javac is what actually separates the two.
if runtime.ImageType == "" && runtime.JavaPath != "" {
	if _, err := os.Stat(filepath.Join(filepath.Dir(runtime.JavaPath), javacBinary())); err == nil {
		runtime.ImageType = ImageJDK
	} else {
		runtime.ImageType = ImageJRE
	}
}
```

顺带把手工塞进运行时目录的那些包也修好了——这是选 `bin/javac` 而不是解析目录名后缀的理由。

## musl

`Platform` 加一个字段：

```go
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// LibC is "glibc" or "musl". Zulu builds for both; Temurin does not.
	LibC    string `json:"libc"`
	Warning string `json:"warning,omitempty"`
}
```

`LibC` 由现有那个 `/lib/ld-musl-*.so.1` 的探测填——逻辑已经在 `platformWarning` 里，抽成
`detectLibC()` 复用。Azul 的查询带上 `lib_c_type=<LibC>`，musl 机器上直接拿到 musl 构建，
**Alpine 上头一回能真装真跑**；Adoptium 忽略这个字段。

**警告文案的归属要跟着变，这里有个签名问题。** 「musl 上装不了」现在只对 Temurin 成立，可
`CurrentPlatform()` 不知道当前选的是哪个发行版，没法自己决定要不要出这句话。所以：

- `CurrentPlatform()` 只负责**事实**——填 `OS` / `Arch` / `LibC`，**不再填 `Warning`**；
- `Warning` 变成**按发行版算出来的**，由 `PlatformWarning(dist string, p Platform) string` 给，
  `handleJavaOverview` 用记住的那个发行版调它，填进响应；
- Temurin + musl 出「这台机器是 musl，Temurin 是 glibc 构建，装上跑不起来——**在下载设置里换成
  Zulu 就能装**」；Zulu + musl 不出警告，因为不再是问题。

这样「平台是什么」和「这个发行版在这个平台上行不行」是两件事，不挤在一个函数里。

## 数据模型

`config.Panel` 加一个字段，规矩与紧挨着它的 `JavaSource` 完全一致（空 = 默认，从上次安装记忆，
不是设置页上的开关）：

```go
// JavaDistribution is which OpenJDK distribution new installs come from, by
// the id of one of javaruntime's distributions. Empty means the default —
// which is both what a config written before this setting existed carries and
// what a fresh panel gets, so no pointer is needed to tell them apart.
JavaDistribution string `json:"javaDistribution,omitempty"`
```

**默认值是 Zulu**，对已有面板也一样：装过 Temurin 的面板下次装 Java 会拿到 Zulu，已装的 Temurin
不受影响。这是本提案要的行为，`CHANGELOG.md` 的「未发布」里写明。

## 接口

三处加字段，全是加法，已有字段的语义不动：

| 端点 | 变化 |
| --- | --- |
| `GET /api/java` | `javaOverview` 加 `distributions`（`[]javaruntime.Distribution`，前端 `JavaDistribution[]`）和 `distribution: string`（记住的那个）。`sources` 变成「当前发行版的源」，`source` 语义不变 |
| `GET /api/java/majors` | 加 `?distribution=` 查询参数，缺省用记住的。两个发行版的可装列表不一样（Zulu 多 13 / 14 / 15），必须跟着走 |
| `POST /api/java/install` | 请求体加 `distribution`，空 = 记住的那个 |

`POST` 的「空 = 记住的那个」沿用 `handlers_java.go:259-262` 已经写下的原则：

> 不指定源的请求拿到的是上次安装用的那个，而不是内置默认值：运维当初换掉它是有理由的。

`rememberJavaSource` 旁边加一个 `rememberJavaDistribution`，同样写回 `panel.json`。

## 界面

**发行版选择器放在「下载源」那一页，不放安装页。** 两个理由：

1. 源列表本身依赖发行版，分两页会让「下载源」页的内容被一个看不见的设置改掉；
2. `JavaPage.tsx` 里 `SourcePicker` 的注释已经定了调——这一页是「装面板那天，或者某个镜像挂了那天」
   才来一次的东西，而安装页是每周要走的。发行版正是同一类决定。

具体：

- 该页标题从「下载源」改成「下载设置」，发行版的 `choice-grid` 摆在源栅格**上面**；
- 两处都用现成的 `choice-grid` / `choice` 类，**不新增 CSS**；
- 选中 Zulu 时源栅格下面出一个自定义前缀输入框，形状照 `DatabasePage.tsx:773-800` 那个既有的
  「选项栅格 + 自定义输入」；
- 安装页顶上加一行 `将从 Azul Zulu 安装 ·〈改〉`，链到下载设置页；
- `TITLES` / `LEADS` 里写死的「Eclipse Temurin」「Adoptium 官方」改成随选择变的文案；
- 已装列表每一行显示 `runtime.vendor`——两个发行版混着装的时候必须能一眼分开，这个字段已经在
  `JavaRuntime` 里了，现在只在「系统 Java」那行露脸。

动手前先走 `frontend-design` skill，按 CLAUDE.md 的要求来。改完在明暗两种模式、1440 / 1200 / 1024 /
768 / 390 五个宽度下人工确认。

## 错误处理

- Azul 的失败复用现有哨兵错误（`ErrUnknownRelease` / `ErrUpstream` / `ErrUnsupported`），
  `writeJavaError` 的 HTTP 映射一个字不用改；
- 错误消息里写死的「Adoptium」跟着发行版走（`adoptium.go:174`、`platform.go:47-52`、
  `installer.go:29` 的 `ErrChecksum` 注释）；
- **跨发行版的源记忆**：面板记着 `tuna`，你切到 Zulu，`tuna` 不在 Zulu 的源表里。这时
  **不报错、不静默沿用，而是回落到该发行版的 `auto`**，并在响应里说明。

  最后这条是 `ResolveSource` 那句「不认识的一律拒绝，而不是悄悄变成默认值」的一个**有意例外**，
  实现时注释里要写清楚为什么：那条原则防的是「运维明确点了 A，系统悄悄下了 B」；而这里运维点的
  「清华」是在说 Temurin 的事，对 Zulu 无从解释，退回自动是唯一诚实的处理。两者的区别是**这次
  请求有没有明确指定过源**——显式传了一个本发行版不认识的 id，仍然照旧报错。

## 测试

后端走 TDD，先写测试。现有 `fixtures_test.go` 那套假 Adoptium API 旁边加一份假 Azul 的（响应形状
差别不小：`java_version` 是数组，`sha256_hash` 得靠 `include_fields` 才有）。

要覆盖的：

- Azul provider 能从固定响应解出 `Release`，且 **CRaC / JavaFX 变体被查询参数正确排除**、musl
  变体在 musl 平台下被正确选中——这三条是实测踩到的真坑，值得各来一条；
- `Release.Arch` 取自请求的 platform 而非响应字段（响应里那个是 `x86`）；
- `attempts()` 对两个发行版各自给出正确的源顺序；Zulu 自定义前缀拼出的 URL 正确，且官方仍在末尾兜底；
- `installID` 对 Zulu 给 `zulu-*`、对 Temurin 仍给 `temurin-*`；
- 记着一个 Temurin 专属源时切到 Zulu，落到 `auto` 而不报错；**显式**传一个不认识的 id 仍然报错；
- 装过的 `temurin-*` 目录在改动后仍被 `store.List()` 正常识别（防回归）；
- `IMAGE_TYPE` 缺席时 `inspect` 靠 `bin/javac` 判出 jre / jdk；
- musl 平台下 Zulu 拿到 musl 包、Temurin 仍出警告；
- `Majors` 的缓存按发行版分键，切换后不串。

命令：`make lint && make test`，前端 `npm --prefix web run build`。

## 明确不做的事

- **不做「Zulu 下不通就自动换 Temurin」。** 这会让面板在运维不知情的情况下装上另一个发行版的
  Java，正是 `ResolveSource` 那条原则要防的事。下不通就报错，切换是人的决定。
- **不提供 CRaC、JavaFX 捆绑版、Zulu Prime。** 它们对「开一个 MC 服」没有用，只会把选择器撑满。
- **不迁移已装的 `temurin-*` 目录。** 理由见上。
- **不猜国内 Zulu 镜像。** 没有实测过就不写进内置源表；自定义前缀是留给这件事的出口。
- **不做「发行版」的全局设置页。** 它跟着下载设置走，和 `JavaSource` 一样从上次安装记忆。

## 落地顺序

1. `fetch.go` 抽公共部分，`adoptium.go` 收缩成 provider——**行为零变化**，现有测试全绿；
2. `distribution.go` + `Release.Distribution` + `installID` 换前缀 + `inspect` 的 `javac` 回落；
3. `source.go` 拆两张源表 + 自定义前缀 + `ResolveSource(dist, id)`；
4. `azul.go`：Azul provider 与它的假 API 测试；
5. `Platform.LibC` 与 musl 选包；
6. API 三个端点加字段 + `config.Panel.JavaDistribution` + 记忆；
7. 前端：类型、`useJava`、下载设置页、安装页文案、已装列表的 vendor；
8. `CHANGELOG.md` 的「未发布」。

第 1 步单独成一个提交，因为它是纯重构：它的 diff 大而无害，混进后面几步会让真正有行为变化的改动
淹在里面。

## 未决问题

1. **国内到底有没有 Zulu 镜像？** 写方案的沙箱判定不了（见第二节）。请在能访问国内网络的机器上
   实测华为云的 `/zulu/bin/`；确认有，就把它加进 `zuluSources` 排在官方前面。
2. **`cdn.azul.com` 在国内的实际速度如何？** 如果直连就够快，自定义前缀基本用不上，那也是好结果。
   这个只能在真机上量。
