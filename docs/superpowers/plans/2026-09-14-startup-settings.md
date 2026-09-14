# 启动方式独立成页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `实例设置` 里的「启动方式」抽成独立导航页 `/i/<id>/startup`，重排成「左表单 + 右对照」两栏，右栏显示后端算出的真实命令行和启动前检查。

**Architecture:** Go 侧把 `commandLine` 内部改成先建带 origin 标签的分段、再压平——启动走压平、预览走分段，一份拼装逻辑。新增 `POST /api/instances/{id}/launch/preview` 收未保存草稿，同时返回命令分段和检查结果。前端拆出 `StartupSettings.tsx` 及四张卡片组件，右栏 `LaunchConsole.tsx` 消费这一个接口。

**Tech Stack:** Go 1.2x（标准库 + `net/http` ServeMux 路由）、React 18 + TypeScript + Vite、单一 `web/src/styles.css`。

**Spec:** `docs/superpowers/specs/2026-09-14-startup-settings-design.md`

## Global Constraints

这些是**每一个任务的隐含要求**，来自 `CLAUDE.md`、`.claude/skills/frontend-design/SKILL.md` 和 spec：

- **代码注释用英文，文档（README / CHANGELOG / CI 步骤名）用中文。** 沿用所处文件的语言，不要混。
- **注释解释「为什么」，不解释代码在做什么。** 本仓库大量注释记录的是踩过的坑；看起来能简化的写法先读注释再决定。
- **Go 提交前必须 `gofmt`**，CI 直接卡。
- **全部样式写在 `web/src/styles.css` 一个文件里。** 不引 CSS 框架、组件库、CSS-in-JS，不拆分文件，不加 `!important`，不写行内 `style`（除非是必须由 JS 计算的动态值）。
- **只用令牌**：颜色、圆角、阴影、时长、缓动从 `styles.css` 开头（约 150–240 行）的令牌区取，不写裸 hex。
- **不新增 `--term-*` 令牌**——它在 8 个主题块里都有定义，漏一个就是暗色下的空值。
- **不新增媒体查询断点。** 先用内在响应（`flex-wrap` / `auto-fill`）。
- **1024px 断点在两处**：`styles.css` 的媒体查询和 `App.tsx:59` 的 `DRAWER_QUERY`。本计划**不碰这一档**。
- **任何可能装长文本的 flex/grid 子项都要写 `min-width: 0`**（列方向 `min-height: 0`）。这是本仓库最高频的布局 bug。
- **`npm --prefix web run build` 里含 `check:ui`**，它会卡住这些：
  - 用了 `styles.css` 里没定义的类名 → 新类名必须**和 JSX 在同一个任务里**写进 CSS
  - 手写 `.panel--form` / `.panel__head` / `.panel__heading` / `.panel__body` / `.panel__tools` → 必须改用 `<Section>`
  - 一个文件超过 1 个 `variant="primary"`（`PRIMARY_ALLOWED` 里列出的文件除外，本计划涉及的文件都不在其中）
  - 图标按钮缺 `aria-label`、下拉没用 `<Select>`、徽章没用 `<Badge>`、空状态没用 `<EmptyState>`
  - `styles.css` 里同一个选择器出现两个定义块
- **用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节。** 除非明确要发版，只往「未发布」里加。
- 分支：`claude/epic-thompson-9nqx8y`。

## File Structure

| 文件 | 责任 | 状态 |
| --- | --- | --- |
| `internal/instance/config.go` | `Segment` 类型、`commandSegments()`、`commandLine()` 改为压平 | 改 |
| `internal/instance/config_test.go` | 分段 origin 与压平等价的黄金用例 | 改 |
| `internal/api/handlers_launch.go` | 预览端点、草稿覆盖、新增四条检查、`fix` 结构 | 改 |
| `internal/api/handlers_launch_test.go` | 预览与检查的用例 | 新建（若已存在则改） |
| `internal/api/routes.go` | 注册预览路由 | 改 |
| `web/src/routes.ts` | `InstanceSection` 加 `'startup'` | 改 |
| `web/src/components/Icon.tsx` | 新增 `bolt` 图标 | 改 |
| `web/src/components/Sidebar.tsx` | `INSTANCE_ICONS` 加一条 | 改 |
| `web/src/components/InstanceView.tsx` | 新增 `<Pane id="startup">` | 改 |
| `web/src/types.ts` | `LaunchIssue` 加 `ok` 级别与 `fix`；`StartupDraft`/`LaunchSegment`/`LaunchPreview` | 改 |
| `web/src/api.ts` | `launchPreview()`；`request` 支持 `AbortSignal` | 改 |
| `web/src/useLaunchPreview.ts` | 防抖 + 中断 + 保留上次结果的取数 hook | 新建 |
| `web/src/components/StartupSettings.tsx` | 新页：草稿状态、保存、两栏骨架 | 新建 |
| `web/src/components/JavaCoreCard.tsx` | 模式 segmented / Java / jar / 核心库入口 | 新建 |
| `web/src/components/MemoryCard.tsx` | Xms / Xmx / 锁定开关 / 宿主机分配条 | 新建 |
| `web/src/components/JvmArgsCard.tsx` | 预设三选一 + 现有 `JVMArgsEditor` | 新建 |
| `web/src/components/ServerArgsCard.tsx` | 卡片化的服务端参数 | 新建 |
| `web/src/components/ArgCards.tsx` | 从 `JVMArgsEditor` 抽出的共享卡片外壳 | 新建 |
| `web/src/serverFlags.ts` | 服务端参数词表 | 新建 |
| `web/src/components/LaunchConsole.tsx` | 右栏：命令预览 + 启动前检查 | 新建 |
| `web/src/components/LaunchSettings.tsx` → `InstanceSettings.tsx` | 留下的四段 + fatal 横幅 | 改名并瘦身 |
| `web/src/styles.css` | 新区块：`.startup*` / `.launchcmd*` / `.memorybar*` / `.originmark*` | 改 |

---

### Task 1: Go — `commandLine` 改成分段构建

**Files:**
- Modify: `internal/instance/config.go:335-371`
- Test: `internal/instance/config_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Segment struct { Origin string \`json:"origin"\`; Args []string \`json:"args"\` }`
  - 常量 `OriginPanel = "panel"` / `OriginMemory = "memory"` / `OriginJVM = "jvm"` / `OriginJar = "jar"` / `OriginServer = "server"`
  - `func (c *Config) commandSegments(tty bool) (string, []Segment, error)`
  - `func FlattenSegments(segments []Segment) []string`
  - `commandLine(tty bool) (string, []string, error)` 签名不变，行为不变

这是整份计划的地基：**它让「预览和真实 argv 漂移」在结构上不可能发生**，因为启动路径和预览路径读的是同一份分段。

- [ ] **Step 1: 写失败的测试**

加到 `internal/instance/config_test.go` 末尾：

```go
func TestCommandSegmentsCarryOriginsAndFlattenToTheCommandLine(t *testing.T) {
	cfg := launchConfig(t)
	cfg.Jar = "paper.jar"
	cfg.MinMemoryMB, cfg.MaxMemoryMB = 2048, 4096
	cfg.JVMArgs = []string{"-XX:+UseG1GC"}
	cfg.ServerArgs = []string{"--nogui"}

	bin, segments, err := cfg.commandSegments(true)
	if err != nil {
		t.Fatalf("commandSegments: %v", err)
	}
	if bin != "java" {
		t.Errorf("bin = %q", bin)
	}

	// The origins the UI colours by, in the order the JVM receives them.
	want := []Segment{
		{Origin: OriginMemory, Args: []string{"-Xms2048M", "-Xmx4096M"}},
		{Origin: OriginJVM, Args: []string{"-XX:+UseG1GC"}},
		{Origin: OriginJar, Args: []string{"-jar", "paper.jar"}},
		{Origin: OriginServer, Args: []string{"--nogui"}},
	}
	got := make([]Segment, 0, len(segments))
	for _, s := range segments {
		// tty=true emits no console flags beyond the encoding ones, which are
		// asserted in encoding_test.go; this test is about the other four.
		if s.Origin == OriginPanel {
			continue
		}
		got = append(got, s)
	}
	if len(got) != len(want) {
		t.Fatalf("segments = %+v, want %d non-panel segments", segments, len(want))
	}
	for at := range want {
		if got[at].Origin != want[at].Origin || !slices.Equal(got[at].Args, want[at].Args) {
			t.Errorf("segment %d = %+v, want %+v", at, got[at], want[at])
		}
	}

	// The whole point: what launches and what is previewed cannot drift,
	// because one is the other flattened.
	_, args, err := cfg.commandLine(true)
	if err != nil {
		t.Fatalf("commandLine: %v", err)
	}
	if !slices.Equal(FlattenSegments(segments), args) {
		t.Errorf("flatten(segments) = %q, commandLine = %q", FlattenSegments(segments), args)
	}
}

func TestCommandSegmentsOmitTheHeapInArgFileMode(t *testing.T) {
	// An @file is expanded in place and the last -Xmx wins, so the panel does
	// not put one in front of it. The preview has to show the same thing.
	cfg := launchConfig(t)
	cfg.ArgFiles = []string{"user_jvm_args.txt"}
	cfg.MinMemoryMB, cfg.MaxMemoryMB = 2048, 4096

	_, segments, err := cfg.commandSegments(true)
	if err != nil {
		t.Fatalf("commandSegments: %v", err)
	}
	for _, s := range segments {
		if s.Origin == OriginMemory {
			t.Fatalf("argfile mode emitted a memory segment: %+v", s)
		}
	}
	if !slices.Contains(FlattenSegments(segments), "@user_jvm_args.txt") {
		t.Errorf("argfile missing from %q", FlattenSegments(segments))
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/instance/ -run TestCommandSegments -v`
Expected: FAIL，`cfg.commandSegments undefined` / `FlattenSegments undefined`

- [ ] **Step 3: 实现分段构建**

在 `internal/instance/config.go` 里，**把 `commandLine` 整个替换成**下面这段（原来的 `commandLine` 主体成为 `commandSegments`）：

```go
// Segment is a stretch of the command line together with where it came from.
//
// The launch settings page shows the command it is about to run and claims it
// is the real one. That claim only survives if there is a single assembly:
// spawn flattens these, the preview endpoint serves them, and neither can grow
// a flag the other does not have.
type Segment struct {
	Origin string   `json:"origin"`
	Args   []string `json:"args"`
}

// Where a stretch of the command line comes from. The UI pairs each with the
// card that owns it — panel being the one nobody typed, so it is the one the
// page has to admit to rather than hide.
const (
	OriginPanel  = "panel"
	OriginMemory = "memory"
	OriginJVM    = "jvm"
	OriginJar    = "jar"
	OriginServer = "server"
)

// commandSegments builds the argv in labelled pieces. tty says which console
// transport the process is about to get, since some of the JVM flags exist
// only to paper over not having a terminal.
func (c *Config) commandSegments(tty bool) (string, []Segment, error) {
	segments := make([]Segment, 0, 5)
	add := func(origin string, args ...string) {
		if len(args) == 0 {
			return
		}
		segments = append(segments, Segment{Origin: origin, Args: args})
	}

	console := c.consoleJVMArgs(tty)

	if len(c.ArgFiles) > 0 {
		add(OriginPanel, console...)
		add(OriginJVM, c.JVMArgs...)
		// No -Xms/-Xmx here on purpose. An @file is expanded in place and the
		// JVM lets the last -Xmx win, so the one inside user_jvm_args.txt
		// would override anything put in front of it. A heap flag that loses
		// is worse than none: the panel would then report a ceiling the server
		// never ran with. In this mode the heap is edited in the argfile — see
		// EffectiveMaxMemoryMB and internal/jvmargs.
		files := make([]string, 0, len(c.ArgFiles))
		for _, file := range c.ArgFiles {
			files = append(files, "@"+file)
		}
		add(OriginJar, files...)
		add(OriginServer, c.ServerArgs...)
		return c.Java, segments, nil
	}

	if strings.TrimSpace(c.Jar) == "" {
		return "", nil, fmt.Errorf("%w: no launch target configured: set either jar or argFiles", ErrInvalidConfig)
	}

	memory := make([]string, 0, 2)
	if c.MinMemoryMB > 0 {
		memory = append(memory, fmt.Sprintf("-Xms%dM", c.MinMemoryMB))
	}
	if c.MaxMemoryMB > 0 {
		memory = append(memory, fmt.Sprintf("-Xmx%dM", c.MaxMemoryMB))
	}
	add(OriginMemory, memory...)
	add(OriginPanel, console...)
	add(OriginJVM, c.JVMArgs...)
	add(OriginJar, "-jar", c.Jar)
	add(OriginServer, c.ServerArgs...)
	return c.Java, segments, nil
}

// FlattenSegments is the argv as the kernel wants it.
func FlattenSegments(segments []Segment) []string {
	size := 0
	for _, s := range segments {
		size += len(s.Args)
	}
	args := make([]string, 0, size)
	for _, s := range segments {
		args = append(args, s.Args...)
	}
	return args
}

// commandLine builds the argv used to launch the server.
func (c *Config) commandLine(tty bool) (string, []string, error) {
	bin, segments, err := c.commandSegments(tty)
	if err != nil {
		return "", nil, err
	}
	return bin, FlattenSegments(segments), nil
}
```

- [ ] **Step 4: 跑全部 instance 测试确认通过**

Run: `go test ./internal/instance/ -v`
Expected: PASS。**既有的 `TestCommandLineBuildsTheJarForm` / `TestCommandLineBuildsTheArgFileForm` / `TestCommandLineForcesColourAndUTF8` 必须原样通过**——它们是这次重构「行为不变」的证据。

- [ ] **Step 5: 跑 lint**

Run: `make lint`
Expected: 无输出（gofmt + go vet 都过）

- [ ] **Step 6: 提交**

```bash
git add internal/instance/config.go internal/instance/config_test.go
git commit -m "启动命令改成先分段再压平

启动设置页要显示「实际执行的命令」并声称那就是真的。这个声称只有在
拼装逻辑只有一份时才成立，否则预览迟早会漏掉某个后加的 flag。

所以 commandLine 不再自己拼 argv，而是 commandSegments 建带 origin 的
分段、它压平。spawn 走压平结果，预览端点直接吃分段，两边不可能长出
对方没有的参数。既有的 commandLine 测试原样通过，行为没有变化。"
```

---

### Task 2: Go — `LaunchIssue` 加 `ok` 级别与结构化修复

**Files:**
- Modify: `internal/api/handlers_launch.go:31-46`
- Test: `internal/api/handlers_launch_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - 常量 `launchLevelOK = "ok"`
  - `type launchFix struct { Label string \`json:"label"\`; Patch map[string]any \`json:"patch"\` }`
  - `launchIssue` 新增字段 `Fix *launchFix \`json:"fix,omitempty"\``

`Patch` 用 `map[string]any` 而不是具体结构：它要能表达「把 `minMemoryMB` 改成 2560」这类任意字段的补丁，前端拿到就无脑合并进草稿，**前端不写任何按 `code` 分支的逻辑**。以后加检查项不用动前端。

- [ ] **Step 1: 写失败的测试**

新建 `internal/api/handlers_launch_test.go`（若文件已存在，把函数追加进去）：

```go
package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLaunchIssueOmitsFixWhenThereIsNothingToApply(t *testing.T) {
	// Most issues are "go and look at your disk" — they have no patch, and a
	// null fix key in every one of them would be noise on the wire.
	raw, err := json.Marshal(launchIssue{
		Level:   launchLevelFatal,
		Code:    "jar-missing",
		Message: "目录里没有 paper.jar。",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "fix") {
		t.Errorf("issue without a fix serialised it anyway: %s", raw)
	}
}

func TestLaunchIssueCarriesAFixTheFormCanApplyBlindly(t *testing.T) {
	raw, err := json.Marshal(launchIssue{
		Level:   launchLevelWarn,
		Code:    "heap-mismatch",
		Message: "这套参数的前提是最小内存和最大内存一样大。",
		Fix: &launchFix{
			Label: "把 Xms 改成 2560",
			Patch: map[string]any{"minMemoryMB": 2560},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back struct {
		Level string `json:"level"`
		Fix   *struct {
			Label string         `json:"label"`
			Patch map[string]any `json:"patch"`
		} `json:"fix"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Fix == nil {
		t.Fatalf("fix did not survive the round trip: %s", raw)
	}
	if back.Fix.Label != "把 Xms 改成 2560" {
		t.Errorf("label = %q", back.Fix.Label)
	}
	if got := back.Fix.Patch["minMemoryMB"]; got != float64(2560) {
		t.Errorf("patch minMemoryMB = %v (%T)", got, got)
	}
}

func TestLaunchLevelOKExists(t *testing.T) {
	// The check panel shows what passed as well as what failed: an empty panel
	// cannot say "I looked".
	if launchLevelOK != "ok" {
		t.Errorf("launchLevelOK = %q", launchLevelOK)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/ -run TestLaunch -v`
Expected: FAIL，`launchLevelOK undefined` / `unknown field Fix`

- [ ] **Step 3: 实现**

把 `internal/api/handlers_launch.go:31-39` 那段替换成：

```go
const (
	launchLevelFatal = "fatal"
	launchLevelWarn  = "warn"
	// What the panel checked and found in order. Shown rather than dropped:
	// a check panel that lists only problems is indistinguishable from one
	// that never ran, and "端口没被占用" is exactly the reassurance somebody
	// opens this page for.
	launchLevelOK = "ok"
)

// launchFix is a change the form can apply on the reader's behalf.
//
// Patch is a loose map so a new check can propose a new field without the
// browser learning anything about it: the page merges whatever arrives into
// its draft, which keeps every "and here is the button that fixes it" on this
// side of the wire.
type launchFix struct {
	Label string         `json:"label"`
	Patch map[string]any `json:"patch"`
}

type launchIssue struct {
	Level   string     `json:"level"`
	Code    string     `json:"code"`
	Message string     `json:"message"`
	Fix     *launchFix `json:"fix,omitempty"`
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/ -run TestLaunch -v`
Expected: PASS（三个用例全过）

- [ ] **Step 5: 提交**

```bash
git add internal/api/handlers_launch.go internal/api/handlers_launch_test.go
git commit -m "启动检查加上「通过」级别和可执行的修复

检查面板原来只列问题，于是「全都没问题」和「压根没检查」在界面上
长得一样。加一个 ok 级别，让它能把看过而没事的项也说出来。

修复动作做成 {label, patch} 而不是按 code 在前端分支：后端说「把
minMemoryMB 改成 2560」，前端无脑合并进草稿。以后加检查项不用动前端。"
```

---

### Task 3: Go — 预览端点

**Files:**
- Modify: `internal/api/handlers_launch.go`（新增 handler 与草稿覆盖）
- Modify: `internal/api/routes.go:122` 附近（注册路由）
- Test: `internal/api/handlers_launch_test.go`

**Interfaces:**
- Consumes: Task 1 的 `instance.Segment` / `commandSegments`；Task 2 的 `launchIssue.Fix`
- Produces:
  - `type launchPreviewRequest struct` —— 见下
  - `type launchPreviewResponse struct { Mode string; Program string; Segments []instance.Segment; Issues []launchIssue }`
  - 路由 `POST /api/instances/{id}/launch/preview`，权限 `authz.CapInstanceView`

**为什么是 `CapInstanceView` 而不是 `CapInstanceLaunch`**：这个端点**什么都不写**，它把请求里的草稿和已存配置拼一拼再回显。草稿里的 Java 路径是调用者自己传进来的，回显它不泄露任何调用者还不知道的东西。与既有的 `GET launch-check` 同级。

**`tty` 取值**：用 `cfg.wantsTTY()`——`instance.go:422` 的 `spawn(cfg, size, cfg.wantsTTY())` 用的就是它，所以正常情况下预览与真实启动一致。唯一的例外是 `instance.go:425-432` 那条路径：PTY 分配失败时守护进程会用 `tty=false` 重试。那是运行时故障不是配置，预览不表达它。

`commandSegments` 需要包外可见——`internal/instance` 与 `internal/api` 是两个包。**在 Task 1 的基础上加一个导出方法**（放在 `config.go` 的 `commandSegments` 下面）：

```go
// PreviewSegments is commandSegments for callers outside the package: the
// launch settings page, which shows this exact argv before anyone starts
// anything. tty is decided the way spawn decides it, so what is previewed is
// what will run — except where the PTY itself fails to allocate and the
// daemon retries on pipes (see Instance.start), which is a runtime failure
// rather than a setting and is not modelled here.
func (c Config) PreviewSegments() (string, []Segment, error) {
	return c.commandSegments(c.wantsTTY())
}
```

- [ ] **Step 1: 写失败的测试**

追加到 `internal/api/handlers_launch_test.go`：

```go
func TestLaunchPreviewOverlaysTheDraftOnTheStoredConfig(t *testing.T) {
	// The page sends what it holds; everything it does not own — the console
	// encoding, which lives on the other settings page now — comes off the
	// stored config. Otherwise the preview would answer with defaults for
	// settings the reader never touched.
	stored := instance.Config{
		Name:      "survival",
		Kind:      instance.KindServer,
		Directory: t.TempDir(),
		Java:      "java",
		Jar:       "old.jar",
		JVMArgs:   []string{"-XX:+UseSerialGC"},
	}

	draft := launchPreviewRequest{
		Java:        "java",
		Jar:         "paper.jar",
		MinMemoryMB: 2560,
		MaxMemoryMB: 2560,
		JVMArgs:     []string{"-XX:+UseG1GC"},
		ServerArgs:  []string{"--nogui"},
	}

	cfg := draft.overlay(stored)
	if cfg.Jar != "paper.jar" {
		t.Errorf("jar = %q, draft should win", cfg.Jar)
	}
	if cfg.Name != "survival" {
		t.Errorf("name = %q, the draft does not own it and must not blank it", cfg.Name)
	}
	if len(cfg.JVMArgs) != 1 || cfg.JVMArgs[0] != "-XX:+UseG1GC" {
		t.Errorf("jvmArgs = %q", cfg.JVMArgs)
	}
}

func TestLaunchPreviewSegmentsFlattenToTheRealCommandLine(t *testing.T) {
	cfg := instance.Config{
		Name:        "survival",
		Kind:        instance.KindServer,
		Directory:   t.TempDir(),
		Java:        "java",
		Jar:         "paper.jar",
		MinMemoryMB: 2560,
		MaxMemoryMB: 2560,
		ServerArgs:  []string{"--nogui"},
	}

	_, segments, err := cfg.PreviewSegments()
	if err != nil {
		t.Fatalf("PreviewSegments: %v", err)
	}
	flat := instance.FlattenSegments(segments)

	// Every arg belongs to exactly one segment, and the panel-injected ones
	// are present rather than quietly dropped — the page promises the command
	// is complete.
	if !slices.Contains(flat, "-Xms2560M") || !slices.Contains(flat, "-jar") {
		t.Errorf("flat = %q", flat)
	}
	var panel bool
	for _, s := range segments {
		if s.Origin == instance.OriginPanel {
			panel = true
		}
	}
	if !panel {
		t.Error("no panel segment: the injected encoding flags would be invisible")
	}
}
```

上面两个用例都**不调用 `applyDefaults`**：它是 `internal/instance` 的私有方法，跨包用不了。所以测试里显式填了 `Java: "java"`，`commandSegments` 的返回值不依赖任何默认值。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/ -run TestLaunchPreview -v`
Expected: FAIL，`launchPreviewRequest undefined`

- [ ] **Step 3: 实现 handler**

追加到 `internal/api/handlers_launch.go`：

```go
// launchPreviewRequest is the half of the config the 启动方式 page owns.
//
// Deliberately not instanceRequest: the console encoding and the process
// behaviour moved to the other settings page, and a page that cannot edit a
// field has no business asserting a value for it. Everything absent here is
// read off what is stored.
type launchPreviewRequest struct {
	Java        string   `json:"java"`
	Jar         string   `json:"jar"`
	ArgFiles    []string `json:"argFiles"`
	MinMemoryMB int      `json:"minMemoryMB"`
	MaxMemoryMB int      `json:"maxMemoryMB"`
	JVMArgs     []string `json:"jvmArgs"`
	ServerArgs  []string `json:"serverArgs"`
	Loader      string   `json:"loader"`
}

// overlay puts the draft on top of what is on record.
func (req launchPreviewRequest) overlay(stored instance.Config) instance.Config {
	cfg := stored
	cfg.Java = strings.TrimSpace(req.Java)
	cfg.Jar = strings.TrimSpace(req.Jar)
	cfg.ArgFiles = cleanArgs(req.ArgFiles)
	cfg.MinMemoryMB = req.MinMemoryMB
	cfg.MaxMemoryMB = req.MaxMemoryMB
	cfg.JVMArgs = cleanArgs(req.JVMArgs)
	cfg.ServerArgs = cleanArgs(req.ServerArgs)
	if loader := strings.TrimSpace(req.Loader); loader != "" {
		cfg.Loader = plugin.NormaliseLoader(loader)
	}
	return cfg
}

type launchPreviewResponse struct {
	Mode     string              `json:"mode"`
	Program  string              `json:"program"`
	Segments []instance.Segment  `json:"segments"`
	Issues   []launchIssue       `json:"issues"`
}

// handleLaunchPreview answers "what exactly will you run, and what do you
// already know is wrong with it" for settings nobody has saved yet.
//
// One endpoint for both because they are one answer: a preview built from the
// draft next to a check built from what is stored would disagree with itself
// on screen, which is worse than either alone.
func (s *Server) handleLaunchPreview(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	var req launchPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := req.overlay(inst.Config())
	resp := launchPreviewResponse{
		Mode:     launchMode(cfg),
		Segments: []instance.Segment{},
		Issues:   s.checkLaunchConfig(inst, cfg),
	}
	// A config with no launch target cannot produce a command line, and that
	// is not an error to report twice: the issues already say so.
	if program, segments, err := cfg.PreviewSegments(); err == nil {
		resp.Program = program
		resp.Segments = segments
	}
	writeJSON(w, http.StatusOK, resp)
}
```

同时把现有的 `checkLaunch(inst)` 改成薄封装，让草稿和已存两条路共用检查逻辑：

```go
func (s *Server) checkLaunch(inst *instance.Instance) launchCheckResponse {
	cfg := inst.Config()
	return launchCheckResponse{Mode: launchMode(cfg), Issues: s.checkLaunchConfig(inst, cfg)}
}

// checkLaunchConfig is the check against a config that may not be saved yet.
func (s *Server) checkLaunchConfig(inst *instance.Instance, cfg instance.Config) []launchIssue {
	if cfg.NeedsLaunchSetup {
		return []launchIssue{{
			Level: launchLevelFatal,
			Code:  "needs-setup",
			Message: "这个实例原来由它自己的启动脚本启动，面板已经不再执行脚本，也没能从那个脚本里" +
				"拆出启动参数。在上面指定核心和参数之后它才能开起来——原来的启动命令就列在下面，供对照。",
		}}
	}
	if len(cfg.ArgFiles) > 0 {
		return s.checkArgFileLaunch(inst, cfg)
	}
	return s.checkJarLaunch(inst, cfg)
}
```

`import` 区补上 `encoding/json` 和 `github.com/lanscarlos/hypercraft/internal/plugin`。

- [ ] **Step 4: 注册路由**

在 `internal/api/routes.go` 的 `rt("GET /api/instances/{id}/launch-check", …)` 那一行**下面**加：

```go
		// The same question asked of settings nobody has saved yet, plus the
		// argv they would produce. Writes nothing — it echoes the caller's own
		// draft back with the panel's additions made visible, which is the
		// whole reason the page can claim the command it shows is the real
		// one.
		rt("POST /api/instances/{id}/launch/preview", s.handleLaunchPreview, authz.CapInstanceView),
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/api/ ./internal/instance/ -v`
Expected: PASS

- [ ] **Step 6: lint 并提交**

```bash
make lint
git add internal/api/handlers_launch.go internal/api/handlers_launch_test.go internal/api/routes.go internal/instance/config.go
git commit -m "新增启动预览端点

启动方式页要在保存之前就显示「将会执行的那条命令」。做成端点而不是
前端自己拼，是因为面板会注入编码和终端参数（config.go 的
consoleJVMArgs），前端拼不出它们，拼出来的命令就是假的。

预览和检查合成一个端点：两者都是对同一份草稿的回答，分开做会在同一
屏上自相矛盾——一个讲草稿、一个讲已存的配置。

tty 取 cfg.wantsTTY()，和 spawn 用的是同一个判定。"
```

---

### Task 4: Go — 四条新检查

**Files:**
- Modify: `internal/api/handlers_launch.go`
- Test: `internal/api/handlers_launch_test.go`

**Interfaces:**
- Consumes: Task 2 的 `launchLevelOK` / `launchFix`；Task 3 的 `checkLaunchConfig`
- Produces: 检查码 `jar-count` / `heap-mismatch` / `java-version` / `port-conflict`

- [ ] **Step 1: 写失败的测试**

```go
func TestHeapMismatchProposesRaisingXms(t *testing.T) {
	// Aikar's flags size the heap up front; a server that starts at 1 GB and
	// grows to 2.5 pauses doing it, which is the exact thing those flags were
	// picked to avoid.
	cfg := instance.Config{
		MinMemoryMB: 1024,
		MaxMemoryMB: 2560,
		JVMArgs:     []string{"-XX:+UseG1GC", "-XX:G1NewSizePercent=30"},
	}
	issues := heapIssues(cfg)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one", issues)
	}
	if issues[0].Code != "heap-mismatch" || issues[0].Level != launchLevelWarn {
		t.Errorf("issue = %+v", issues[0])
	}
	if issues[0].Fix == nil || issues[0].Fix.Patch["minMemoryMB"] != 2560 {
		t.Errorf("fix = %+v, want a patch raising Xms to 2560", issues[0].Fix)
	}
}

func TestHeapMatchReportsOK(t *testing.T) {
	cfg := instance.Config{
		MinMemoryMB: 2560,
		MaxMemoryMB: 2560,
		JVMArgs:     []string{"-XX:G1NewSizePercent=30"},
	}
	issues := heapIssues(cfg)
	if len(issues) != 1 || issues[0].Level != launchLevelOK {
		t.Fatalf("issues = %+v, want one ok", issues)
	}
	if issues[0].Fix != nil {
		t.Error("nothing to fix, so no button")
	}
}

func TestHeapIsSilentWithoutAikarStyleFlags(t *testing.T) {
	// Plenty of servers run Xms below Xmx on purpose. Only the preset that
	// requires them equal gets to complain.
	cfg := instance.Config{MinMemoryMB: 1024, MaxMemoryMB: 4096}
	if issues := heapIssues(cfg); len(issues) != 0 {
		t.Errorf("issues = %+v, want none", issues)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/api/ -run TestHeap -v`
Expected: FAIL，`heapIssues undefined`

- [ ] **Step 3: 实现 `heapIssues`**

```go
// aikarStyle reports whether these flags are the preset that needs a fixed
// heap. G1NewSizePercent is the tell: it is in Aikar's set and in nothing a
// person types by hand.
func aikarStyle(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-XX:G1NewSizePercent") {
			return true
		}
	}
	return false
}

// heapIssues reports on -Xms against -Xmx, but only where the flags in use
// actually care.
func heapIssues(cfg instance.Config) []launchIssue {
	if !aikarStyle(cfg.JVMArgs) || cfg.MaxMemoryMB <= 0 {
		return nil
	}
	if cfg.MinMemoryMB == cfg.MaxMemoryMB {
		return []launchIssue{{
			Level:   launchLevelOK,
			Code:    "heap-mismatch",
			Message: fmt.Sprintf("最小和最大内存都是 %d MB，符合这套参数的前提。", cfg.MaxMemoryMB),
		}}
	}
	return []launchIssue{{
		Level: launchLevelWarn,
		Code:  "heap-mismatch",
		Message: fmt.Sprintf(
			"这套参数的前提是最小和最大内存一样大，现在是 %d / %d MB。堆区大小固定能避免运行中扩容造成的停顿。",
			cfg.MinMemoryMB, cfg.MaxMemoryMB),
		Fix: &launchFix{
			Label: fmt.Sprintf("把 Xms 改成 %d", cfg.MaxMemoryMB),
			Patch: map[string]any{"minMemoryMB": cfg.MaxMemoryMB},
		},
	}}
}
```

注意：测试里断言 `Patch["minMemoryMB"] != 2560` 比较的是 `any` 里的 `int`（未过 JSON），所以直接与 `2560`（int）比即可。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/ -run TestHeap -v`
Expected: PASS

- [ ] **Step 5: 实现 `jarCountIssue`（把左边的字段说明搬过来）**

```go
// jarCountIssue is the sentence that used to sit under the jar dropdown. It is
// a check and not a hint: "目录下找到 1 个 jar" is the reader confirming the
// panel is looking where they think it is.
func (s *Server) jarCountIssue(cfg instance.Config) []launchIssue {
	if cfg.Directory == "" {
		return nil
	}
	entries, err := unconfinedBrowser(cfg.Directory).List("")
	if err != nil {
		return nil
	}
	count := 0
	for _, entry := range entries {
		if !entry.Dir && strings.HasSuffix(strings.ToLower(entry.Name), ".jar") {
			count++
		}
	}
	if count == 0 {
		return []launchIssue{{
			Level:   launchLevelWarn,
			Code:    "jar-count",
			Message: "实例目录下没有 jar 文件。从核心库装一个，或者自己传一个进去。",
		}}
	}
	return []launchIssue{{
		Level:   launchLevelOK,
		Code:    "jar-count",
		Message: fmt.Sprintf("实例目录下找到 %d 个 jar 文件。", count),
	}}
}
```

**实现前先确认 `unconfinedBrowser(...).List("")` 的真实签名和返回类型**（`internal/serverfiles`），它在 `missingFileIssues` 里已经被用作 `.Stat(path)`。按实际签名调整字段名（`entry.Dir` / `entry.Name` 可能叫别的）。若没有合适的列目录方法，改用 `os.ReadDir(cfg.Directory)` 并过滤 `.jar`。

- [ ] **Step 6: 实现 `portIssue`（只读跨页核对）**

端口属于 `服务器配置` 页，这里**只读核对、不提供修改入口**，所以它没有 `Fix`。

```go
// portIssue cross-checks the port in server.properties against the other
// instances' — the one thing about this page that is not on this page.
//
// Read-only on purpose: the port lives in 服务器配置 and this page does not
// get an edit for it. Two servers on one port is a boot failure with a
// misleading message, so it is worth saying here even though the fix is
// elsewhere.
func (s *Server) portIssue(self *instance.Instance, cfg instance.Config) []launchIssue {
	port := serverPort(cfg)
	if port == "" {
		return nil
	}
	clashes := make([]string, 0)
	for _, other := range s.mgr.List() {
		if other.ID() == self.ID() {
			continue
		}
		if serverPort(other.Config()) == port {
			clashes = append(clashes, other.Config().Name)
		}
	}
	if len(clashes) > 0 {
		return []launchIssue{{
			Level:   launchLevelWarn,
			Code:    "port-conflict",
			Message: "端口 " + port + " 和这些实例撞了：" + strings.Join(clashes, "、") + "。在「服务器配置」里改端口。",
		}}
	}
	return []launchIssue{{
		Level:   launchLevelOK,
		Code:    "port-conflict",
		Message: "端口 " + port + " 没有和别的实例撞。",
	}}
}
```

`serverPort(cfg)` 读 `cfg.Directory/server.properties` 里的 `server-port=`，读不到返回空串。**实现时先在 `internal/` 里搜有没有现成的 properties 解析**（`ServerConfigPage` 背后必然有一个），有就用它，不要新写一个解析器。`s.mgr.List()` 与 `other.ID()` 的真实签名同样先核对再用。

- [ ] **Step 7: 实现 `javaVersionIssue`**

```go
// javaVersionIssue reports the runtime against what the core needs. A Paper
// 1.21 on Java 17 fails at class-load time with a message nobody reads as
// "wrong Java", so the panel says it first.
func (s *Server) javaVersionIssue(cfg instance.Config) []launchIssue {
	// Implementation note: the required major comes from the core the jar
	// belongs to. Reuse whatever the core library already knows — see
	// internal/serverjar — rather than hard-coding a version table here.
	return nil
}
```

**这一条的实现前提要先查**：`internal/serverjar` 或核心库里是否已经存有「某核心某版本要求的 Java 大版本」。**有就接上并补测试；没有就保持返回 `nil` 并在本任务的提交信息里写明「java-version 待核心库补齐版本要求后再接」**——不要为它硬编码一张版本表，那张表会立刻过时，而一个过时的「你的 Java 不对」比没有更糟。

- [ ] **Step 8: 把四条接进 `checkJarLaunch` / `checkArgFileLaunch`**

在两个函数 `return` 之前，把 `heapIssues(cfg)`、`s.jarCountIssue(cfg)`（仅 jar 模式）、`s.portIssue(inst, cfg)`、`s.javaVersionIssue(cfg)` 的结果 append 进去。

- [ ] **Step 9: 跑全部测试**

Run: `go test ./... && make lint`
Expected: PASS

- [ ] **Step 10: 提交**

```bash
git add internal/api/
git commit -m "启动检查补上内存、jar 数量与端口三条

这三条原来散在表单各处：jar 数量是下拉框底下一句说明，Xms/Xmx 是卡片
里一条常驻警告，端口根本没人查。作为说明它们常驻占地方却没人读，作为
检查项它们只在真的有问题时出现，还能带一个直接改好的按钮。

端口这条是只读的跨页核对：字段在「服务器配置」，这里只负责说出撞了，
不给修改入口，免得同一个字段有两个家。"
```

---

### Task 5: 前端 — 路由、图标、侧栏、空壳页

**Files:**
- Modify: `web/src/routes.ts:18-27`（类型）、`:189` 附近（`INSTANCE_SECTIONS`）
- Modify: `web/src/components/Icon.tsx`（新增 `bolt`）
- Modify: `web/src/components/Sidebar.tsx:457-464`（`INSTANCE_ICONS`）
- Modify: `web/src/components/InstanceView.tsx:230` 附近（新 `Pane`）
- Create: `web/src/components/StartupSettings.tsx`（本任务只放一个占位页头）

**Interfaces:**
- Consumes: 无
- Produces:
  - `InstanceSection` 多一个成员 `'startup'`
  - `export function StartupSettings({ instance }: { instance: InstanceStatus }): JSX.Element`

本任务的可验收成果：**侧栏出现「启动方式」，点进去 URL 变成 `/i/<id>/startup` 且页面有个标题**。功能是空的，但导航整条通了。

- [ ] **Step 1: 加路由成员**

`web/src/routes.ts` 的 `InstanceSection` 联合类型里，在 `'settings'` **之前**加一行 `| 'startup'`。

`INSTANCE_SECTIONS`（`:189` 那条 `settings` 之前）插入：

```ts
  // Before 实例设置 and not inside it: what a server runs is a different
  // question from what it is called, and the one you come back to while
  // tuning a heap is this one. They stay adjacent because the reader who
  // wants one often wants the other.
  { id: 'startup', label: '启动方式', cap: CAP.instanceSettings },
```

`routes.ts:443` 的解析用的是 `pick(INSTANCE_SECTIONS, …)`，**不需要改**——加进清单 URL 就认了。

- [ ] **Step 2: 加图标**

`web/src/components/Icon.tsx` 的图标表里加一条（沿用该文件现有条目的写法与 `viewBox`）：

```tsx
  bolt: <polygon points="13.5 2.5 4.5 13.5 11 13.5 10.5 21.5 19.5 10.5 13 10.5" />,
```

`web/src/components/Sidebar.tsx` 的 `INSTANCE_ICONS` 加：

```ts
  startup: 'bolt',
```

- [ ] **Step 3: 写占位页**

Create `web/src/components/StartupSettings.tsx`：

```tsx
import type { InstanceStatus } from '../types'
import { PageHead } from './Page'

/**
 * How this server is started: which Java, which jar, how much heap.
 *
 * Its own page rather than a section of 实例设置 because it is the only part
 * of that form anybody opens twice — and because it is the only part with a
 * right-hand answer to show (the command, and what is wrong with it), which
 * a single reading column has nowhere to put.
 */
export function StartupSettings({ instance }: { instance: InstanceStatus }) {
  return (
    <div className="stack">
      <PageHead
        title="启动方式"
        lead="用哪个 Java、跑哪个 jar、给多少内存，以及面板据此拼出的那条命令。"
      />
      <p className="muted">{instance.name}</p>
    </div>
  )
}
```

注意用 `.stack` 而**不是** `.stack--narrow`：实例 pane 里 `.stack` 就是 1440 的瓦片测量，两栏要用它。

- [ ] **Step 4: 接进 `InstanceView`**

`web/src/components/InstanceView.tsx`，在 `settings` 那个 `Pane` **之前**加（照抄现有 Pane 的懒挂载写法，`visited.has('startup') &&` 的守卫沿用同文件里其他 section 的形式）：

```tsx
        <Pane id="startup" active={section === 'startup'} leaving={leaving === 'startup'} scroll>
          <StartupSettings instance={instance} />
        </Pane>
```

并在文件顶部 `import { StartupSettings } from './StartupSettings'`。

- [ ] **Step 5: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过（`tsc -b` 无错、`check:ui: 通过`）

- [ ] **Step 6: 人工验证**

跑 `npm --prefix web run dev` 与 `go run ./cmd/hypercraft`，打开任意实例：
- 侧栏「实例设置」上面出现「启动方式」，图标是闪电
- 点它，地址栏变成 `/i/<id>/startup`
- 刷新页面，仍然停在「启动方式」（说明解析对了）

- [ ] **Step 7: 提交**

```bash
git add web/src/routes.ts web/src/components/Icon.tsx web/src/components/Sidebar.tsx web/src/components/InstanceView.tsx web/src/components/StartupSettings.tsx
git commit -m "启动方式：先把导航通上

只加路由、图标和一个空壳页，页面内容下个提交搬。分开是因为路由这层
改动波及侧栏、URL 解析和 pane 的懒挂载，跟表单搬家混在一起就没法单独
确认哪一半出的问题。

routes.ts 的解析走 pick(INSTANCE_SECTIONS)，所以加进清单 URL 就认了，
不用动解析。"
```

---

### Task 6: 前端 — 把启动字段搬进新页，两页各自保存

**Files:**
- Modify: `web/src/components/StartupSettings.tsx`（装上表单与保存）
- Rename + Modify: `web/src/components/LaunchSettings.tsx` → `web/src/components/InstanceSettings.tsx`
- Modify: `web/src/components/InstanceView.tsx`（改引用）
- Modify: `web/src/styles.css`（`.startup*` 骨架类）

**Interfaces:**
- Consumes: Task 5 的 `StartupSettings`
- Produces:
  - `export function InstanceSettings(props)` —— props 与原 `LaunchSettings` 相同
  - `StartupSettings` 新 props：`{ instance, cores, onSaved, onOpenLibrary }`

**本任务最重要的一条规则（spec「保存契约」）**：

> `toConfig()`（`internal/api/handlers_instances.go:72`）从请求构造**完整**的 `instance.Config`，缺席字段取零值。
> **所以两页都必须 PUT 全量**：`toInput(instance)` 读全量，各自只改自己那部分，保存时整份发回去。
> 只发自己那半 = 把另一页的字段清空（实例名变空串、`autoStart` 变 false）。

- [ ] **Step 1: 写失败的测试**

本仓库前端没有单测框架（`CLAUDE.md`：「前端没有单测和 lint，`tsc -b` 是唯一的自动检查」），**不要为此引入一个**。这一步改为**写下人工验收清单**，贴进本任务的提交信息：

```
两页互不覆盖的验收：
1. 启动方式页改 Xmx 为 3072 → 保存 → 打开实例设置：实例名、自启、
   停止方式、编码全部保持原值
2. 实例设置页改实例名 → 保存 → 打开启动方式：Xmx 仍是 3072、JVM 参数
   条数不变、服务端参数不变
3. 两步做完后刷新整页，两页的值都还在
```

- [ ] **Step 2: 改名并瘦身**

```bash
git mv web/src/components/LaunchSettings.tsx web/src/components/InstanceSettings.tsx
```

在 `InstanceSettings.tsx` 里：
- 把导出的函数名 `LaunchSettings` 改成 `InstanceSettings`
- **删掉** `启动方式` 那个 `<Section>`（原 `:563-762`）、`<LaunchCheckPanel …/>` 的渲染（原 `:493`）、`<InstanceCorePicker …/>`、`<ScriptImportDialog …/>`
- 连带删掉只服务于它们的 state 与函数：`jvmText` / `serverText` / `argFileText` / `argFileMode` / `importing` / `preset` / `jarsRev` / `jvm` / `jvmMin` / `jvmMax` / `jvmBusy` / `jvmStatus` / `jvmRows` / `setJvmView` / `applyPreset` / `applyDraft` / `toggleNogui` / `saveJVMArgs` / `aikarNeedsEqualHeap`，以及 `JVMPresets` / `ArgFileMemory` / `LaunchCheckPanel` 三个局部组件
- `PageHead` 的 `lead` 改成不再提启动：`lead="名称、目录、控制台编码，以及面板什么时候替你开关机。"`
- **保留** `form` / `toInput` / `dirty` / `revert` / `save` / `remove` 与 `基本信息`·`控制台`·`进程管理`·`危险操作` 四段
- `save()` 里发出的 body **保持发全量**（它本来就是 `form`，即 `toInput(instance)` 的全量拷贝），不要改成只发四段的字段

`InstanceView.tsx` 的 import 与 JSX 同步改名。

- [ ] **Step 3: 把启动字段搬进 `StartupSettings`**

`StartupSettings.tsx` 现在持有：

```tsx
const [form, setForm] = useState<InstanceInput>(() => toInput(instance))
const [jvmText, setJvmText] = useState(() => toLines(instance.jvmArgs ?? []))
const [serverText, setServerText] = useState(() => toLines(instance.serverArgs ?? []))
const [argFileText, setArgFileText] = useState(() => toLines(instance.argFiles ?? []))
const [argFileMode, setArgFileMode] = useState(() => (instance.argFiles?.length ?? 0) > 0)
```

`toInput` / `toLines` / `fromLines` 从 `InstanceSettings.tsx` 抽到一个两边都 import 的地方——**放进 `web/src/types.ts` 不合适**（那是类型文件），新建 `web/src/instanceForm.ts` 承载这三个函数，两个页面各自 import。

保存沿用原 `save()` 的形状（`api.updateInstance(instance.id, body)`），**body 是 `form` 全量**，其中 `jvmArgs` / `serverArgs` / `argFiles` 由三个 text state 经 `fromLines` 得出。

本任务先用**单栏**把四段字段原样搬过来（`<Section form>` ×4，内容与原来一致），两栏留到 Task 7。

- [ ] **Step 4: 加保存条**

两页都用现有 `.cfg__savebar`（`styles.css:8433`）。**不新增 CSS 类**，照 `ConfigLayout.tsx:142` 的结构写：

```tsx
{dirty && (
  <div className="cfg__savebar" role="status">
    <span className="cfg__savecount">
      <b>{changedCount}</b> 项更改待保存
    </span>
    <span className="cfg__savenote">
      {isLive(instance.state) ? '服务器正在运行，保存的设置下次启动生效。' : '下次启动即生效。'}
    </span>
    <span className="cfg__saveactions">
      <Button type="button" onClick={() => navigateToHistory()}>查看历史</Button>
      <Button type="button" onClick={revert}>放弃</Button>
      <Button variant="primary" type="submit" disabled={busy}>保存</Button>
    </span>
  </div>
)}
```

「查看差异」按 spec 改成跳 `配置历史`：`onOpenSection('config-history')`，所以 `StartupSettings` 的 props 要多一个 `onOpenSection: (section: InstanceSection) => void`，由 `InstanceView` 透传（它本来就有这个 prop）。

**`check-ui` 约束**：每个文件最多 1 个 `variant="primary"`。两页各自只有「保存」一个，符合。

- [ ] **Step 5: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过

- [ ] **Step 6: 跑 Step 1 的三条人工验收**

特别是**第 1、2 条——它们验的是「两页互不覆盖」，是本任务唯一可能出严重数据丢失的地方**。

- [ ] **Step 7: 提交**

```bash
git add -A web/src/
git commit -m "启动方式的字段搬进新页，实例设置留下四段

搬家时唯一要小心的是保存：handlers_instances.go 的 toConfig 从请求构造
完整 Config，缺席字段取零值。所以两页都得读全量、改自己那部分、发全量
——哪一页只发自己那半，都会把另一页的字段清空成零值。

这一版先单栏，把字段原样搬过来跑通保存，两栏和右栏下个提交做。

验收：启动方式页改 Xmx 保存后，实例设置的名字/自启/编码不变；反向
同理。"
```

---

### Task 7: 前端 — 两栏骨架与命令预览

**Files:**
- Create: `web/src/useLaunchPreview.ts`
- Create: `web/src/components/LaunchConsole.tsx`
- Modify: `web/src/api.ts`（`request` 支持 signal；新增 `launchPreview`）
- Modify: `web/src/types.ts`（`LaunchIssue.level` 加 `'ok'`、加 `fix`；新增三个类型）
- Modify: `web/src/components/StartupSettings.tsx`（两栏）
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 3 的 `POST /api/instances/{id}/launch/preview`
- Produces:
  - `export interface StartupDraft { java: string; jar: string; argFiles: string[]; minMemoryMB: number; maxMemoryMB: number; jvmArgs: string[]; serverArgs: string[]; loader: string }`
  - `export interface LaunchSegment { origin: 'panel' | 'memory' | 'jvm' | 'jar' | 'server'; args: string[] }`
  - `export interface LaunchPreview { mode: 'jar' | 'argfile'; program: string; segments: LaunchSegment[]; issues: LaunchIssue[] }`
  - `export function useLaunchPreview(id: string, draft: StartupDraft): { preview: LaunchPreview | null; stale: boolean; failed: boolean }`
  - `export function LaunchConsole({ preview, stale, failed, onApplyFix }: …)`

- [ ] **Step 1: 加类型**

`web/src/types.ts`，把 `LaunchIssue` 改成：

```ts
/** One thing the panel found wrong with how this instance would start — or
 *  one it looked at and found in order. */
export interface LaunchIssue {
  level: 'fatal' | 'warn' | 'info' | 'ok'
  code: string
  message: string
  /** A change the form can apply on the reader's behalf. `patch` is merged
   *  into the draft as-is: the page deliberately knows nothing about which
   *  code proposed what, so a new check needs no frontend change. */
  fix?: { label: string; patch: Partial<StartupDraft> }
}

/** The fields the 启动方式 page owns. Not `LaunchDraft` — that name is taken
 *  by the result of reading somebody's start script, which is a different
 *  thing entirely. */
export interface StartupDraft {
  java: string
  jar: string
  argFiles: string[]
  minMemoryMB: number
  maxMemoryMB: number
  jvmArgs: string[]
  serverArgs: string[]
  loader: string
}

export interface LaunchSegment {
  origin: 'panel' | 'memory' | 'jvm' | 'jar' | 'server'
  args: string[]
}

export interface LaunchPreview {
  mode: 'jar' | 'argfile'
  program: string
  segments: LaunchSegment[]
  issues: LaunchIssue[]
}
```

`LaunchSettings.tsx` 里的 `LEVEL_LABELS`（已随 Task 6 搬走或删除）在 `LaunchConsole.tsx` 里重建，多一条 `ok: '没问题'`。

- [ ] **Step 2: `request` 支持中断**

`web/src/api.ts:118`：

```ts
async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
```

并把 `signal` 传进 `fetch(path, { … , signal })`。加到 `api` 对象上：

```ts
  /** The argv this draft would produce, plus what the panel already knows is
   *  wrong with it. POST because it carries a draft, not because it writes:
   *  nothing on the server changes. */
  launchPreview: (id: string, draft: StartupDraft, signal?: AbortSignal) =>
    request<LaunchPreview>('POST', `/api/instances/${id}/launch/preview`, draft, signal),
```

- [ ] **Step 3: 写取数 hook**

Create `web/src/useLaunchPreview.ts`：

```ts
import { useEffect, useRef, useState } from 'react'

import { api } from './api'
import type { LaunchPreview, StartupDraft } from './types'

/** How long after the last keystroke to ask. Long enough that typing a
 *  four-digit heap is one request, short enough that the command still reads
 *  as following the form rather than lagging it. */
const SETTLE_MS = 300

/**
 * The command line and checks for a draft nobody has saved.
 *
 * Keeps the last good answer through a refresh and through a failure. A
 * command line that blinks out while you type is worse than one that is a
 * beat behind, and a preview that fails must never be able to stop you saving
 * — it is a mirror, not a gate.
 */
export function useLaunchPreview(id: string, draft: StartupDraft) {
  const [preview, setPreview] = useState<LaunchPreview | null>(null)
  const [stale, setStale] = useState(true)
  const [failed, setFailed] = useState(false)
  // Serialised so the effect compares by value: the draft is rebuilt on every
  // render and would otherwise refire on keystrokes that changed nothing.
  const key = JSON.stringify(draft)
  // The first ask is not a debounce — there is nothing to settle yet.
  const settled = useRef(false)

  useEffect(() => {
    const controller = new AbortController()
    setStale(true)
    const ask = () => {
      api
        .launchPreview(id, JSON.parse(key) as StartupDraft, controller.signal)
        .then((next) => {
          setPreview(next)
          setFailed(false)
          setStale(false)
        })
        .catch((err: unknown) => {
          if (err instanceof DOMException && err.name === 'AbortError') return
          setFailed(true)
          setStale(false)
        })
    }
    if (!settled.current) {
      settled.current = true
      ask()
      return () => controller.abort()
    }
    const timer = window.setTimeout(ask, SETTLE_MS)
    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [id, key])

  return { preview, stale, failed }
}
```

- [ ] **Step 4: 写 `LaunchConsole`**

Create `web/src/components/LaunchConsole.tsx`。要点：

- 命令按 `segments` 逐 origin 一行，行首 `<span className={\`originmark originmark--${s.origin}\`} aria-hidden="true" />`
- `program` 单独一行，origin 记作 `panel` 以外的中性色（用 `.launchcmd__program`）
- `panel` 那组套 `.launchcmd__line--muted`，并在图例里标「面板注入」
- 整块 `.launchcmd` 是深色画布，配色跟 `--term-*` 走，**不新增 `--term-*` 令牌**
- `stale` 时加 `.launchcmd--stale`（压暗），`failed` 时在下面加一句 `.muted` 提示，**上一次的结果继续显示**
- 检查列表沿用现有 `.launchcheck*` 类，`ok` 级别加 `.launchcheck__item--ok`
- 有 `fix` 的条目渲染一个 `<Button size="row">{issue.fix.label}</Button>`，点击调 `onApplyFix(issue.fix.patch)`。**不要用 `variant="primary"`**（check-ui 限 1 个/文件，且这里也不该是实心）

- [ ] **Step 5: 写 CSS**

`web/src/styles.css` 新增一个区块。**每个新类名都必须在这里定义**，否则 `check-ui` 的 `ruleNoUndefinedClasses` 会卡住构建。**不要与既有选择器重名**（`ruleNoSilentOverrides`）。

```css
/* --------------------------------------------------------------- 启动方式
   Two columns: the form, and the answer it produces. Wrapping rather than a
   breakpoint — the rail has a width it stops being readable below, and that
   width is the same on every screen, so the container is what should decide.
   Both sides need min-width: 0 or a long Java path pushes the grid open. */
.startup {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  align-items: flex-start;
}

.startup__form {
  flex: 1 1 420px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.startup__rail {
  flex: 1 1 340px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
  position: sticky;
  top: 0;
}

/* The legend key, in two places at once: the square on a card's heading and
   the square in front of that card's stretch of the command. One class so
   the pairing cannot drift apart. */
.originmark {
  width: 7px;
  height: 7px;
  border-radius: 2px;
  flex: none;
  display: inline-block;
  background: var(--text-faint);
}

.originmark--memory { background: var(--ok); }
.originmark--jvm { background: var(--code-key); }
.originmark--jar { background: var(--code-number); }
.originmark--server { background: var(--accent); }
.originmark--panel { background: var(--text-faint); }

/* The command, on the console's own dark ground. A third dark surface in a
   panel whose rule is that the two terminals must stay apart — allowed here
   because this one takes no input: there is nothing to type into the wrong
   one. It borrows --term-* rather than starting a palette, which also keeps
   it from being mistaken for the host shell. */
.launchcmd {
  background: var(--term-bg);
  color: var(--term-fg);
  border: 1px solid var(--term-edge);
  border-radius: var(--radius);
  padding: 12px 14px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.8;
  /* The one element allowed to be wider than the page: a Java path plus
     twenty Aikar flags has no wrap that reads well. */
  overflow-x: auto;
}

.launchcmd--stale {
  opacity: 0.55;
  transition: opacity var(--dur) var(--ease);
}

.launchcmd__line {
  display: flex;
  align-items: baseline;
  gap: 8px;
  white-space: pre;
}

/* Not yours to edit: these come from the console settings on the other page.
   Dimming says so without spending a hue. */
.launchcmd__line--muted {
  color: var(--term-bright-black);
}

.launchcmd__legend {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px solid var(--term-edge);
  color: var(--term-bright-black);
  font-size: 11px;
}
```

**先确认 `--font-mono`、`--code-key`、`--code-number`、`--term-edge`、`--term-bright-black` 这些令牌的真名**（`styles.css` 150–240 行的令牌区），名字对不上就换成实际存在的；**不要新增令牌**。

- [ ] **Step 6: 两栏接起来**

`StartupSettings.tsx` 的 return 改成：

```tsx
<form className="stack" onSubmit={save}>
  <PageHead title="启动方式" lead="…" />
  {error && <div className="alert alert--error">{error}</div>}
  <div className="startup">
    <div className="startup__form">{/* 四张卡 */}</div>
    <aside className="startup__rail">
      <LaunchConsole
        preview={preview}
        stale={stale}
        failed={failed}
        onApplyFix={(patch) => setForm((prev) => ({ ...prev, ...patch }))}
      />
    </aside>
  </div>
  {dirty && <div className="cfg__savebar">…</div>}
</form>
```

`onApplyFix` 就是「前端无脑合并」那条设计：**不按 `code` 分支**。

- [ ] **Step 7: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过。若报「未定义的类名」，说明 Step 5 漏了某个类。

- [ ] **Step 8: 人工验证**

- 改 Xmx，右边命令里的 `-Xmx` 跟着变，且**中途不闪空**
- 连续快速敲键，网络面板里只有一条请求（防抖生效）
- 命令里能看到 `-Dfile.encoding=UTF-8` 这组，且是压暗的
- 把后端停掉再改一个字段：命令**仍然显示上一次的结果**，出现一句提示，**保存按钮仍可用**
- 明暗两种模式都看；390px 宽度下右栏落到下面，命令块自己横向滚动，页面不横向溢出

- [ ] **Step 9: 提交**

```bash
git add -A web/src/
git commit -m "启动方式：右栏的命令预览

这一栏是整页的论点：左边每改一项，右边那条真命令立刻跟着变。命令来自
后端的分段接口，包括面板自己注入的编码参数——它们压暗显示并标成「面板
注入」，因为它们确实不是用户填的，藏起来才是撒谎。

上色只落在行首 7px 的色块上，和左边卡头共用一个 .originmark 类，所以
图例和卡片不可能对不上。命令文字保持单色：给它上五种颜色要新增 5 个
--term-* 令牌乘 8 个主题块，漏一处就是暗色下的空值。

预览失败或刷新中都保留上一次的结果，且从不拦住保存——它是镜子不是闸门。"
```

---

### Task 8: 前端 — 四张卡片重排

**Files:**
- Create: `web/src/components/JavaCoreCard.tsx` / `MemoryCard.tsx` / `JvmArgsCard.tsx`
- Modify: `web/src/components/StartupSettings.tsx`
- Modify: `web/src/styles.css`（`.memorybar*`）

**Interfaces:**
- Consumes: Task 7 的 `.originmark` 类
- Produces: 三个卡片组件，props 均为「当前值 + onChange」的受控形式

- [ ] **Step 1: `JavaCoreCard`**

`<Section form title={<><span className="originmark originmark--jar" aria-hidden="true" /> Java 与核心</>} tools={<模式 segmented />}>`

- 模式 segmented 从原来两个 96px 大卡（`InstanceSettings` 里已删）压成 `tools` 槽里的 32px 控件，**只有两项**：`核心 jar` / `参数文件`
- 卡身 `field-row`：`Java 环境` + `服务端 jar`
- `服务端 jar` 的标签行右侧放「从核心库安装…」，点开 `InstanceCorePicker`
- **删掉** jar 下拉底下那句「目录下找到 N 个 jar」——它已经是右栏的 `jar-count` 检查项

- [ ] **Step 2: `MemoryCard` 与宿主机分配条**

`tools` 槽放 `锁定 Xms = Xmx` 开关。锁定时 `onMin`/`onMax` 互相跟随。

分配条数据（**纯前端，不动后端**）：

```tsx
const hostTotalMB = metrics ? Math.round(metrics.memoryTotal / 1024 / 1024) : 0
const othersMB = instances
  .filter((one) => one.id !== instance.id)
  .reduce((sum, one) => sum + (one.maxMemoryMB ?? 0), 0)
```

- `metrics` 为 `null` 时**整条不渲染**，不占位、不显示 0
- 图例文字写「已分配」不写「已用」——它是各实例配置的 Xmx 之和，不是实时占用
- argfile 模式下这张卡换成原 `ArgFileMemory`，并写明「@file 模式面板不发 `-Xms/-Xmx`」

`metrics` 与 `instances` 已经是 `InstanceView` 的现成 props，透传下来即可。

新 CSS（同样必须写进 `styles.css`）：`.memorybar` / `.memorybar__track` / `.memorybar__fill` / `.memorybar__fill--others` / `.memorybar__fill--self` / `.memorybar__legend`。

- [ ] **Step 3: `JvmArgsCard`**

- `tools` 槽：`从启动脚本读入…` + `卡片/文本` 切换；`count` 槽放参数条数
- 三个预设（`jvmPresets.ts:77` 的 `JVM_PRESETS`，正好就是设计稿那三个）**做出明确选中态**：选中项加打勾图标与描边类
- 卡身仍用现有 `<JVMArgsEditor>`——**不换成胶囊**（spec 决定 4）
- 空状态用 `<EmptyState>` 的行内形态压成一行（`check-ui` 的 `ruleEmptyStatesAreComponents` 要求用组件）
- **删掉** 卡内那条 `aikarNeedsEqualHeap` 的 `.alert--warn`——它已经是右栏带按钮的 `heap-mismatch`

- [ ] **Step 4: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过

- [ ] **Step 5: 人工验证**

- 开「锁定 Xms = Xmx」，改 Xmx，Xms 跟着走，右栏命令两个值同步变
- 右栏 `heap-mismatch` 的「把 Xms 改成 N」按一下，左边 Xms 真的变了，且该条目变成绿色「没问题」
- 切到「参数文件」模式：内存卡换形态，右栏命令里**没有** `-Xms/-Xmx`，出现 `@user_jvm_args.txt`
- 1440 / 1200 / 1024 / 768 / 390 五档 × 明暗两种模式：无横向溢出、无错位

- [ ] **Step 6: 提交**

```bash
git add -A web/src/
git commit -m "启动方式：四张卡片按分组重排

启动方式那两个 96px 的大卡片压成卡头上一个 32px 的 segmented——它只是
个开关，不值那么多地方。「从核心库安装」从页面最底下挪到 jar 下拉框
旁边，它是 jar 的动作不是页脚的动作。

内存卡加了宿主机分配条：填 Xmx 的时候唯一真正缺的参考系是「这台机器
还剩多少」。数据是各实例配置的 Xmx 之和，所以图例写「已分配」而不是
「已用」，metrics 还没到就整条不渲染，不拿 0 冒充。

JVM 参数保留卡片编辑器没换成胶囊：卡片是行式版本上线被证伪之后的结果，
而且它保着每行原始文本，配置历史的 diff 才是一行而不是一堵墙。"
```

---

### Task 9: 前端 — 抽出共享卡片外壳

**Files:**
- Create: `web/src/components/ArgCards.tsx`
- Modify: `web/src/components/JVMArgsEditor.tsx`

**Interfaces:**
- Consumes: 无
- Produces:
  - `export interface ArgVocabulary { parse(line: string): { name: string; value?: string }; describe(name: string): { note?: string; kind: 'flag' | 'text' | 'number' | 'choice'; choices?: string[] }; suggest(prefix: string): string[] }`
  - `export function ArgCards({ value, onChange, vocabulary, ariaLabel }: …)`

`JVMArgsEditor` 改成 `ArgCards` + `jvmFlags.ts` 词表的薄封装，**外部行为与 props 完全不变**（`{ value, onChange }`），这样 Task 8 接进去的那个不用再动。

- [ ] **Step 1: 抽壳**

把 `JVMArgsEditor.tsx` 里与 JVM 语法无关的部分搬进 `ArgCards.tsx`：卡片网格、`editing` 单卡编辑态、`focusing` 焦点交接、「逐行保留原始文本、只有被编辑过的那张卡才按部件重建」这条（`JVMArgsEditor.tsx:34-37` 的注释**连同注释一起搬**，它记的是 `配置历史` diff 的约束）。

与 JVM 语法有关的部分（`parseFlag` / `parseFlags` / `formatFlags` / `knownFor` / `suggestFlags` / `wantsUnit` / `reflow`）留在原处，包成一个 `ArgVocabulary` 实现。

- [ ] **Step 2: `JVMArgsEditor` 改薄封装**

```tsx
export function JVMArgsEditor({ value, onChange }: { value: string; onChange: (text: string) => void }) {
  return <ArgCards value={value} onChange={onChange} vocabulary={jvmVocabulary} ariaLabel="JVM 参数" />
}
```

- [ ] **Step 3: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过

- [ ] **Step 4: 人工回归**

这是纯重构，**JVM 参数卡片的行为必须一模一样**：
- 加一个参数、删一个、点名字改写、切到文本再切回来
- 应用一个预设，卡片整批换掉
- 改完保存，去 `配置历史` 看 diff：**只有被改的那一行变化**，其余行的空格排布原样

- [ ] **Step 5: 提交**

```bash
git add -A web/src/
git commit -m "参数卡片的外壳抽成共享组件

服务端参数要做成和 JVM 参数一样的卡片，但两者语法不同（--key value
对 -XX:key=value），词表也不同。所以把与语法无关的部分——卡片网格、
单卡编辑态、焦点交接，以及「逐行保留原始文本、只重建被编辑过的那张」
这条——抽成 ArgCards，各自配一份词表。

JVMArgsEditor 变成薄封装，props 和行为都没变。保留原始文本那条注释
跟着搬了：它记的是配置历史 diff 的约束，不是实现细节。"
```

---

### Task 10: 前端 — 服务端参数卡片化

**Files:**
- Create: `web/src/serverFlags.ts`
- Create: `web/src/components/ServerArgsCard.tsx`
- Modify: `web/src/components/StartupSettings.tsx`

**Interfaces:**
- Consumes: Task 9 的 `ArgCards` / `ArgVocabulary`
- Produces: `export const serverVocabulary: ArgVocabulary`；`export function ServerArgsCard(…)`

- [ ] **Step 1: 写词表**

`web/src/serverFlags.ts`。服务端参数是 `--key value` 空格分隔，与 JVM 的 `-XX:key=value` 不同。

```ts
/** Server arguments the panel can say something useful about.
 *
 *  Deliberately short. A vocabulary that guesses is worse than one that
 *  admits it does not know: an argument with no entry here still round-trips
 *  exactly as typed, it just gets no note and no typed control. */
export const SERVER_FLAGS: Record<string, { note: string; kind: 'flag' | 'text' | 'number'; loaders?: string[] }> = {
  '--nogui': { note: '关掉服务端自带的那个 Swing 窗口。无头机器上基本都要。', kind: 'flag' },
  '--world-dir': { note: '存档目录，默认是实例目录本身。', kind: 'text' },
  '--port': { note: '覆盖 server.properties 里的端口。一般不用，改配置文件更清楚。', kind: 'number' },
  '--forceUpgrade': { note: '启动时把所有区块升级到当前版本。很慢，且不可逆——先备份。', kind: 'flag' },
  // Velocity exits on an argument it does not recognise, so a proxy's list is
  // effectively empty; the card says so rather than offering flags that kill it.
}
```

- [ ] **Step 2: 写 `ServerArgsCard`**

- 卡头 `<span className="originmark originmark--server" />` + 「服务端参数」+ `meta` 写「跟在 jar 之后」
- `tools` 槽：`卡片/文本` 切换。**两种视图互斥**——这是设计稿诊断第 2 条，原来 `--nogui` 胶囊和 textarea 同时显示，用户不知道改哪个
- 卡身用 `<ArgCards vocabulary={serverVocabulary} ariaLabel="服务端参数" />`
- 常用参数改成一行链接；**proxy 实例不显示**（Velocity 遇到不认识的参数直接退出）

- [ ] **Step 3: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过

- [ ] **Step 4: 人工验证**

- 卡片视图加一个 `--nogui`，切到文本视图能看到它；反过来也对
- **同一时刻只有一种视图可见**
- 右栏命令末尾跟着变
- proxy 类型的实例上，常用参数那行不出现

- [ ] **Step 5: 提交**

```bash
git add -A web/src/
git commit -m "服务端参数改成卡片，卡片与文本互斥

原来 --nogui 胶囊和 textarea 上下各显示一份同样的数据，改哪个都生效，
所以用户会停下来问「我该改哪个」。两种视图本来就是互斥的两种读法，
现在也就只显示一种。"
```

---

### Task 11: `实例设置` 的 fatal 横幅

**Files:**
- Modify: `web/src/components/InstanceSettings.tsx`
- Modify: `web/src/styles.css`（若需要新类）

**Interfaces:**
- Consumes: 现有 `GET /api/instances/{id}/launch-check`（`api.launchCheck`）
- Produces: 无对外接口

检查面板搬去 `启动方式` 之后，`实例设置` 一条检查都不显示了。而 `needs-setup`（旧脚本迁移过来、还没配好、根本开不了服）恰恰是用户会在 `实例设置` 找的。

- [ ] **Step 1: 实现**

在 `InstanceSettings.tsx` 的 `PageHead` 之后、第一个 `<Section>` 之前：

```tsx
{fatal && (
  <div className="alert alert--error">
    {fatal.message}
    <Button type="button" size="row" onClick={() => onOpenSection('startup')}>
      去启动方式
    </Button>
  </div>
)}
```

`fatal` 取 `check?.issues.find((issue) => issue.level === 'fatal') ?? null`，`check` 用现有 `api.launchCheck(instance.id)` 拉一次即可（**这一页不需要草稿预览**，它没有启动字段可改）。

**只显示第一条 fatal，不列全部**——整个面板是右栏的活，这里只负责把人引过去。

- [ ] **Step 2: 构建验证**

Run: `npm --prefix web run build`
Expected: 通过

- [ ] **Step 3: 人工验证**

造一个 `needsLaunchSetup` 为真的实例（或临时把 jar 名改成不存在的文件并保存），打开 `实例设置`：
- 顶部出现一条红色横幅和「去启动方式」按钮
- 点它跳到 `启动方式`，右栏检查面板里有完整的那条
- 把 jar 改对保存后，横幅消失

- [ ] **Step 4: 提交**

```bash
git add -A web/src/
git commit -m "实例设置留一条 fatal 横幅指向启动方式

检查面板整块搬去启动方式页之后，实例设置就一条检查都不显示了。而
needs-setup——旧脚本迁移过来、还没配好、根本开不了服——恰恰是用户会
在实例设置这一页找的。

只显示第一条 fatal 加一个跳转，不重复整个面板：那是右栏的活。"
```

---

### Task 12: 文档与全量验证

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/design-system.md`

- [ ] **Step 1: 写 CHANGELOG**

`CHANGELOG.md` 的「未发布」小节（**只往这里加**，不要改成版本号——那会触发自动发版）：

```markdown
### 新增

- 「启动方式」成为实例下的独立页面，从「实例设置」里分出来。页面右侧常驻显示面板将会执行的
  那条命令——包括面板自己注入的编码参数——以及启动前检查。左边改任何一项，右边立刻跟着变。
- 内存一栏新增「锁定 Xms = Xmx」开关和宿主机内存分配条（其他实例 / 本实例 / 剩余）。
- 启动前检查新增三项：内存上下限是否匹配所用的 JVM 预设、实例目录下 jar 的数量、端口是否与
  其他实例冲突。能自动改好的项直接给一个按钮。

### 变化

- 服务端参数改成与 JVM 参数一致的卡片编辑，卡片与文本两种视图互斥——原来同一份参数在胶囊和
  文本框里各显示一遍。
```

- [ ] **Step 2: 写 design-system**

`docs/design-system.md` 补两条：

1. **新的页面形态**：「左表单 + 右对照」。它用实例 pane 里 `.stack` 的瓦片测量（1440），**不是第四种测量**；两栏用 `flex-wrap` 而非断点。
2. **`.originmark` 的用法**：它是图例键，同一个类同时出现在卡头和命令行行首，保证两边不会对不上。**新增 origin 时两处自动同步，不要另写一套颜色。**

- [ ] **Step 3: 全量验证**

```bash
make lint
make test
npm --prefix web run build
make build
```

Expected: 四条全过。`make build` 是最后一关——它会把前端产物嵌进二进制，**前端没重新构建的话二进制里就还是旧界面**。

- [ ] **Step 4: 人工总验收**

| 项 | 检查 |
| --- | --- |
| 宽度 | 1440 / 1200 / 1024 / 768 / 390 五档无横向溢出、无错位 |
| 主题 | 明暗两种模式都看过，新令牌没有在暗色下变空值 |
| 三处高危 | 折叠侧栏、打开抽屉、开着控制台的实例页 |
| 长内容 | 六层深的路径、二十个 Aikar 参数、很长的实例名都不撑破容器 |
| 两页互不覆盖 | Task 6 Step 1 那三条再跑一遍 |
| 命令真实性 | 启一次服，把控制台里进程的实际命令行与预览对照 |

最后一条最重要：**它是整份计划唯一能证明「预览没有说谎」的端到端验证。**

- [ ] **Step 5: 提交并推送**

```bash
git add CHANGELOG.md docs/design-system.md
git commit -m "启动方式独立成页的文档

design-system 补两条：新的「左表单 + 右对照」页面形态用的是 .stack
已有的瓦片测量而不是第四种测量；.originmark 是图例键，同一个类同时用在
卡头和命令行行首，所以两边不可能对不上。"
git push -u origin claude/epic-thompson-9nqx8y
```

---

## Self-Review

**Spec 覆盖检查**（逐节对照 `2026-09-14-startup-settings-design.md`）：

| Spec 小节 | 对应任务 |
| --- | --- |
| 1. 路由与导航 | Task 5 |
| 2. 六段东西的去向 | Task 6（搬家）+ Task 11（fatal 横幅） |
| 3. 文件拆分 | Task 6 / 8 / 9 / 10 |
| 4. 左栏四张卡片 | Task 8（卡 1–3）+ Task 10（卡 4） |
| 5. 预览接口 | Task 3 |
| 6. 防漂移：Go 侧只留一份拼装 | Task 1 |
| 7. 上色：色块做图例 | Task 7 Step 5（`.originmark`） |
| 8. 启动前检查 | Task 2（级别与 fix）+ Task 4（四条检查）+ Task 7 Step 4（渲染） |
| 数据流 · 保存契约 | Task 6（全量 PUT 的规则与验收） |
| 数据流 · 请求节奏 | Task 7 Step 3（`useLaunchPreview`） |
| 错误处理 | Task 7 Step 3（失败保留上次）+ Step 8（人工验证不拦保存） |
| 布局与响应式 | Task 7 Step 5 + Task 12 Step 4 |
| 测试与验证 | Task 12 |
| 风险 · tty | Task 3（`PreviewSegments` 用 `wantsTTY`，注释写明 PTY 回退不建模） |

**已知留白，实现时必须先查证再落笔**（这些不是占位符，是「仓库里有现成的就用现成的」的指示）：

1. **Task 4 Step 5**：`unconfinedBrowser(...).List("")` 的真实签名与返回字段名。没有合适方法就换 `os.ReadDir`。
2. **Task 4 Step 6**：`server.properties` 的解析——`服务器配置` 页背后必然有现成的，**不要新写一个解析器**；`s.mgr.List()` / `other.ID()` 的真实签名。
3. **Task 4 Step 7**：`java-version` 依赖「某核心某版本要求哪个 Java 大版本」这份知识。`internal/serverjar` 里有就接上并补测试；**没有就保持返回 `nil`**，并在提交信息里写明待补——**绝不硬编码一张会过时的版本表**，一个过时的「你的 Java 不对」比没有更糟。
4. **Task 7 Step 5**：`--font-mono` / `--code-key` / `--code-number` / `--term-edge` / `--term-bright-black` 的真名要对着 `styles.css` 令牌区核一遍，对不上就换成实际存在的；**不新增令牌**。

**类型一致性**：`StartupDraft` 的七个字段在 Task 3（Go `launchPreviewRequest`）、Task 7（TS `StartupDraft`）、Task 7（`api.launchPreview`）三处同名同序；`LaunchIssue.fix.patch` 的类型在 Go 是 `map[string]any`、在 TS 是 `Partial<StartupDraft>`，语义一致（后端只发 `StartupDraft` 里有的键）。`origin` 的五个取值在 Go 常量、TS 联合类型、CSS 修饰类三处一致。
