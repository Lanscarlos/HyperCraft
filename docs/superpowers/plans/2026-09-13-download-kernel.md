# 下载内核与线路表 实施方案（一期 / 共二期）

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把散在五个包里的下载实现收成一个 `internal/download` 队列内核和一张线路表，并给服务端核心补上下载镜像。

**Architecture:** 内核不新写——从 `internal/plugin/downloader.go` 提取通用的一半（约 500 行：worker 池、去重、历史裁剪、带校验的字节搬运、按 id 取消），它是五份实现里唯一做对的。调用方通过 `Request.Install` 回调留住自己的解包与入库，通过 `Request.Attempts` 留住自己的取字节策略。线路表统一，但「哪类下载走哪条线路」的选择仍是四份独立配置。

**Tech Stack:** Go 1.x，标准库 + `log/slog`；测试用 `net/http/httptest`，无第三方断言库。

**Spec:** `docs/superpowers/specs/2026-09-13-unified-downloads-design.md`

## Global Constraints

- **代码注释用英文**，文档用中文。沿用所处文件的语言，不要混。（CLAUDE.md）
- 注释解释**为什么**，不解释代码在做什么。从 `plugin/downloader.go` 搬运时**注释一起搬，不做「清理」**——那些注释记的是踩过的坑。
- 提交前必须 `gofmt`，CI 直接卡。每个 Task 结束跑 `make lint && make test`。
- 先写测试再写实现（TDD）。
- 本期**不碰前端**、不碰 `internal/api/routes.go` 的对外路由形状。那是二期（`2026-09-13-downloads-ui.md`）。
- 本期**不动 `internal/schemlib`**（建筑是同步下载，没有 job）。
- 本期**不给数据库补镜像**（三个引擎三个 host，没有已验证的镜像）。
- **线路目录统一，选择不统一。** `config.JavaSource` / `JavaDistribution` / `PluginMirror` /
  `UpdateMirror` 原样保留各自的语义，只新增 `CoreSource`。不要顺手做成一个全局「本机网络线路」设置——
  `internal/config/config.go:86` 的注释写明了理由，它仍然成立：面板一年更新几次而插件每周下载，两者
  的代理值得分开选。
- 用户可见的行为变化写进 `CHANGELOG.md`「未发布」小节，**不要**把「未发布」改成版本号（会触发发版）。

## 一个必须先理解的接缝

Spec 里说「内核负责取字节」，但真实的边界比这细一层：线路 fallback 现在在 `internal/plugin/github.go:512 downloadOrder` / `:687 Fetch` 里，**并且掺着插件专属的私有仓库与 token 逻辑**（私有资产只有一条路由、绝不能经过代理）。

所以内核**不自己拼 URL**。它接受调用方给的一串「尝试」，自己负责走完、记下命中的那条、校验字节：

```go
// Attempt is one place the bytes might come from.
type Attempt struct {
	// Route names this attempt for Job.Route, so a finished job can say where
	// the bytes actually came from rather than leaving the automatic order a
	// black box.
	Route string
	Open  func(ctx context.Context) (io.ReadCloser, error)
}
```

这样 plugin 的 token 逻辑一行都不用离开 plugin 包，而 serverjar / javaruntime / dbruntime 用共享线路表生成 `Attempt`。

---

## File Structure

**新建**

| 文件 | 职责 |
| --- | --- |
| `internal/download/job.go` | `Kind` / `State` / `Job` 数据模型 |
| `internal/download/queue.go` | `Queue`：`Submit` / `Jobs` / 去重 / 历史裁剪 / 派发 / worker |
| `internal/download/cancel.go` | `Cancel` / `CancelAll` / `ClearFinished` / `Close` |
| `internal/download/transfer.go` | `Attempt` 走位、`transfer`、`verifyDigest`、`progressWriter` |
| `internal/download/route.go` | `RouteKind` / `Route` / `RouteSet` / `Resolve` / `Order` |
| `internal/download/routes_github.go` | github 线路表（ghfast / gh-proxy / moeyy / direct） |
| `internal/download/routes_java.go` | adoptium 与 azul 两张表 |
| `internal/download/routes_papermc.go` | papermc 线路表（fastmirror / official） |
| `internal/download/queue_test.go` | 队列行为测试（从 `plugin/downloader_test.go` 搬） |
| `internal/download/transfer_test.go` | 校验测试（从 `plugin/downloader_test.go` 搬） |
| `internal/download/route_test.go` | 线路解析 / fallback / `Serves` 过滤 |

**改造**

| 文件 | 改什么 |
| --- | --- |
| `internal/plugin/downloader.go` | 删掉通用的一半，改为持有 `*download.Queue` |
| `internal/plugin/mirrors.go` | 删掉线路表本体，保留 `plugin` 侧的 id 兼容层 |
| `internal/plugin/github.go` | `downloadOrder` 改为产出 `[]download.Attempt` |
| `internal/plugin/downloader_test.go` | 删掉已搬走的 17 个测试，保留插件专属的 |
| `internal/serverjar/downloader.go` | 删 `Job`/`progressWriter`，改用队列；接 papermc 线路 |
| `internal/javaruntime/installer.go` | 同上；`extracting` 通过 `Progress` 汇报 |
| `internal/javaruntime/source.go` | 表本体搬进 `download`，保留 `javaruntime` 侧适配 |
| `internal/dbruntime/installer.go` | 同上 |
| `internal/selfupdate/selfupdate.go` | `SetMirror` 改为接受 route id，走共享表 |
| `internal/config/config.go` | 新增 `CoreSource` |

---

## Task 1: `internal/download` 数据模型与队列骨架

**Files:**
- Create: `internal/download/job.go`, `internal/download/queue.go`, `internal/download/queue_test.go`
- Reference: `internal/plugin/downloader.go:20-142`（常量、`JobState`、`Job`、`job`、`Downloader`）

**Interfaces:**
- Consumes: 无
- Produces: `download.Kind`、`download.State`、`download.Job`、`download.Request`、`download.Queue`、`NewQueue(*slog.Logger) *Queue`、`(*Queue).Submit(Request) (Job, error)`、`(*Queue).Jobs() []Job`、`ErrBusy`、`ErrCancelled`

- [ ] **Step 1: 写 `job.go`**

把 `plugin/downloader.go:57-104` 的 `JobState` 与 `Job` 搬过来改名并加字段。**原注释全部保留**。

```go
package download

import (
	"context"
	"io"
	"time"
)

// Kind is which shelf a download belongs to. It is what the panel-wide list is
// filtered by, and — because each shelf has its own capability — what decides
// whether a given account may see the job at all.
type Kind string

const (
	KindCore     Kind = "core"
	KindJava     Kind = "java"
	KindDatabase Kind = "database"
	KindPlugin   Kind = "plugin"
)

// State is where a download has got to.
//
// The union of what the five separate implementations used to have between
// them: extracting was only ever Java's and the database's, queued was only
// ever the plugin queue's. A shelf that never reaches a state simply never
// reports it.
type State string

const (
	// StateQueued is waiting for one of the concurrency slots.
	StateQueued      State = "queued"
	StateDownloading State = "downloading"
	// StateExtracting is the install half: the bytes are on disk and the
	// caller's Install hook is unpacking them. Reported separately because a
	// 200 MB JDK spends real time here and a bar that sat at 100% would read
	// as a hang.
	StateExtracting State = "extracting"
	StateDone       State = "done"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
)

// Active reports whether a job is still going to do something.
func (s State) Active() bool {
	return s == StateQueued || s == StateDownloading || s == StateExtracting
}

// Job is a snapshot of one download, whatever shelf it belongs to.
//
// It survives the transfer: the finished job stays readable so an operator who
// closed the tab still sees how it went. Before this existed each shelf kept
// one slot, and a failure at 3am was overwritten by the next download.
type Job struct {
	// ID names this job for cancellation. Assigned by the panel and unique for
	// as long as the process lives — the queue is deliberately not persisted,
	// because a download that was interrupted by a panel restart is one that
	// has to be started again rather than resumed.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Title and Subtitle are what the panel shows. Built by the caller, which
	// is the only side that knows a build number from a major version.
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	FileName string `json:"fileName"`
	// Route is where the bytes actually came from, which with an automatic
	// order in play is not something the operator's setting can tell them.
	Route      string `json:"route,omitempty"`
	Total      int64  `json:"total"`
	Downloaded int64  `json:"downloaded"`
	State      State  `json:"state"`
	Error      string `json:"error,omitempty"`
	// Ref is what the finished download produced, by the id its own shelf knows
	// it as — a core, a runtime, an install, a plugin. It is how the UI offers
	// "go and look at it" without the panel having to guess.
	Ref      string    `json:"ref,omitempty"`
	QueuedAt time.Time `json:"queuedAt"`
	// StartedAt is when the job left the queue, so it is absent on one that
	// never has. Kept separate from QueuedAt rather than folded into it: "sat
	// in the queue for four minutes" and "took four minutes to download" are
	// different complaints with different causes.
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Attempt is one place the bytes might come from.
//
// The queue does not build URLs. Route selection lives with the caller because
// it is not uniformly mechanical: a private GitHub asset has exactly one route
// and must never see a proxy, and that rule has no business in a package that
// does not know what a token is. What the queue owns is walking the list,
// recording which entry answered, and verifying what came back.
type Attempt struct {
	Route string
	Open  func(ctx context.Context) (io.ReadCloser, error)
}

// Progress is the handle an Install hook reports through, so the extract half
// of a job keeps the same bar moving as the download half.
type Progress struct {
	q     *Queue
	entry *entry
}
```

- [ ] **Step 2: 写队列行为的失败测试**

新建 `internal/download/queue_test.go`。测试从 `internal/plugin/downloader_test.go` 搬，但 stub 简化——内核不认识 GitHub，只认识 `Attempt`：

```go
package download

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// held serves bytes but blocks inside Open until release is closed, which is
// the only way to observe a queue: everything else finishes too fast to
// overlap.
type held struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
}

func newHeld() *held { return &held{release: make(chan struct{})} }

func (h *held) attempt(body string) Attempt {
	return Attempt{Route: "test", Open: func(ctx context.Context) (io.ReadCloser, error) {
		now := h.inFlight.Add(1)
		for {
			peak := h.peak.Load()
			if now <= peak || h.peak.CompareAndSwap(peak, now) {
				break
			}
		}
		defer h.inFlight.Add(-1)
		select {
		case <-h.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return io.NopCloser(strings.NewReader(body)), nil
	}}
}

func req(q *Queue, h *held, kind Kind, title string) Request {
	return Request{
		Kind:      kind,
		Title:     title,
		FileName:  title + ".bin",
		DedupeKey: title,
		Attempts:  func(context.Context) ([]Attempt, error) { return []Attempt{h.attempt("ok!")}, nil },
		Install:   func(context.Context, string, *Progress) (string, error) { return title + "-ref", nil },
	}
}

func TestDownloadsOfOneKindRunSideBySideUpToItsLimit(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 3)

	for i := 0; i < 5; i++ {
		if _, err := q.Submit(req(q, h, KindPlugin, "p"+string(rune('a'+i)))); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	waitFor(t, func() bool { return h.inFlight.Load() == 3 })
	close(h.release)
	waitFor(t, func() bool { return activeCount(q) == 0 })

	if peak := h.peak.Load(); peak != 3 {
		t.Fatalf("peak concurrency = %d, want 3", peak)
	}
}

// The limit is per Kind because the reason for it is upstream's rate limit,
// not the panel's disk: three plugin jars must not keep a JDK waiting.
func TestOneKindsQueueDoesNotBlockAnother(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)
	q.SetLimit(KindJava, 1)

	if _, err := q.Submit(req(q, h, KindPlugin, "jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Submit(req(q, h, KindJava, "jdk")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.inFlight.Load() == 2 })
	close(h.release)
}

func TestAskingTwiceForTheSameThingReusesTheJob(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 1)

	first, err := q.Submit(req(q, h, KindPlugin, "same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := q.Submit(req(q, h, KindPlugin, "same"))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("second submit made a new job %s, want %s", second.ID, first.ID)
	}
	close(h.release)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 3s")
}

func activeCount(q *Queue) int {
	n := 0
	for _, job := range q.Jobs() {
		if job.State.Active() {
			n++
		}
	}
	return n
}

var _ = errors.Is
```

- [ ] **Step 3: 跑测试确认失败**

```bash
go test ./internal/download/ -run 'TestDownloads|TestOneKinds|TestAskingTwice' -v
```
预期：编译失败，`undefined: NewQueue` / `undefined: Request`。

- [ ] **Step 4: 写 `queue.go` 的最小实现**

从 `plugin/downloader.go` 搬 `Downloader` 结构、`Start`、`duplicate`、`prune`、`dispatch`、`work` 的骨架，改成按 Kind 计数。**这些函数的原注释全部保留**，只把「plugin」「jar」等字样改成中性说法。

```go
package download

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

var (
	// ErrBusy is returned when the queue is full.
	ErrBusy = errors.New("下载队列已经排满了，等几个下完再来")
	// ErrCancelled is recorded on a job the operator stopped.
	ErrCancelled = errors.New("download cancelled")
)

// maxQueued bounds jobs waiting for a slot. A backstop against a bulk action
// that fans out further than anybody intended, not a limit anyone should meet.
const maxQueued = 100

// maxHistory bounds the finished jobs kept around to be read.
//
// Finished jobs are the whole reason this is a list rather than a counter: a
// download that failed at 3am is only useful if it is still there in the
// morning, and before the queue existed the *next* download overwrote it.
const maxHistory = 30

// defaultLimits is how many of each kind come down at once.
//
// Per kind rather than one shared pool, and the numbers are upstream's rather
// than the disk's. Three plugin jars is what the GitHub API tolerates: every
// plugin job opens with a release lookup, an anonymous panel gets 60 calls an
// hour, and a burst is answered with a rate limit that then blocks the next
// *check* too. A JDK and a server core have no such lookup and no relation to
// that budget, so queueing them behind three jars would be a limit invented
// here rather than imposed from outside.
var defaultLimits = map[Kind]int{
	KindPlugin:   3,
	KindCore:     1,
	KindJava:     1,
	KindDatabase: 1,
}

// Request is what a caller submits.
type Request struct {
	Kind             Kind
	Title, Subtitle  string
	FileName         string
	// Total is the declared size, for the bar. Zero means unknown.
	Total int64
	// SHA256 is the digest the *upstream metadata* published, which is what
	// makes a mirror safe to use: whichever route serves the bytes, they are
	// checked against what the origin said they would be. Empty where upstream
	// publishes none (GitHub release assets), and then there is no content
	// check at all — see transfer.
	SHA256 string
	// DedupeKey collapses a repeat request onto the job already doing it. Two
	// workers writing the same part file is a corrupt download.
	DedupeKey string
	// TempDir is where the part file is written before Install is handed it.
	// Empty means os.TempDir(). Callers that want the bytes to land on the same
	// filesystem as their final home set it, so the move at the end is a rename
	// rather than a copy of 200 MB.
	TempDir string
	// Attempts is where to try, most preferred first. Called on the worker
	// rather than at submit time, because a queued job may be minutes from its
	// turn and the operator may have changed the route in between.
	Attempts func(ctx context.Context) ([]Attempt, error)
	// Install is what to do with the finished bytes: unpack, record, register.
	// It runs on the worker goroutine with the job in StateExtracting, and
	// returns the id its shelf knows the result by.
	Install func(ctx context.Context, temp string, pub *Progress) (ref string, err error)
}

// entry is one queue slot: the public snapshot plus what it takes to run it.
type entry struct {
	pub    *Job
	req    Request
	cancel context.CancelFunc
}

// Queue runs downloads for every shelf in the panel.
//
// It belongs to the daemon rather than to the request that started it, so
// closing the tab does not interrupt something already coming down, and a job
// that was still waiting for a slot does not lose its place.
type Queue struct {
	log *slog.Logger

	mu      sync.Mutex
	jobs    []*entry // oldest first, which is the order they run in
	active  map[Kind]int
	limits  map[Kind]int
	seq     int
	closed  bool

	wg sync.WaitGroup
}

func NewQueue(logger *slog.Logger) *Queue {
	limits := make(map[Kind]int, len(defaultLimits))
	for kind, n := range defaultLimits {
		limits[kind] = n
	}
	return &Queue{log: logger, active: map[Kind]int{}, limits: limits}
}

// SetLimit overrides one kind's concurrency. Tests use it; production takes
// the defaults.
func (q *Queue) SetLimit(kind Kind, n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limits[kind] = n
}

func (q *Queue) Submit(r Request) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return Job{}, ErrBusy
	}
	if existing := q.duplicate(r.Kind, r.DedupeKey); existing != nil {
		return *existing.pub, nil
	}
	queued := 0
	for _, e := range q.jobs {
		if e.pub.State == StateQueued {
			queued++
		}
	}
	if queued >= maxQueued {
		return Job{}, ErrBusy
	}

	q.seq++
	e := &entry{
		pub: &Job{
			ID:       strconv.Itoa(q.seq),
			Kind:     r.Kind,
			Title:    r.Title,
			Subtitle: r.Subtitle,
			FileName: r.FileName,
			Total:    r.Total,
			State:    StateQueued,
			QueuedAt: time.Now(),
		},
		req: r,
	}
	q.jobs = append(q.jobs, e)
	q.prune()
	q.dispatch()
	return *e.pub, nil
}

// duplicate finds an unfinished job for exactly this request. Called with the
// lock held. An empty key never matches: a caller that does not name its
// request is asking for a second one.
func (q *Queue) duplicate(kind Kind, key string) *entry {
	if key == "" {
		return nil
	}
	for _, e := range q.jobs {
		if e.pub.State.Active() && e.pub.Kind == kind && e.req.DedupeKey == key {
			return e
		}
	}
	return nil
}

// prune drops the oldest finished jobs once there are more than the history
// holds. Only finished ones: a queue longer than the history is still a queue,
// and forgetting a job that has not run yet would lose the download. Called
// with the lock held.
func (q *Queue) prune() {
	finished := 0
	for _, e := range q.jobs {
		if !e.pub.State.Active() {
			finished++
		}
	}
	if finished <= maxHistory {
		return
	}
	drop := finished - maxHistory
	kept := make([]*entry, 0, len(q.jobs)-drop)
	for _, e := range q.jobs {
		if drop > 0 && !e.pub.State.Active() {
			drop--
			continue
		}
		kept = append(kept, e)
	}
	q.jobs = kept
}

// dispatch starts queued jobs while their kind has a slot. Called with the
// lock held.
func (q *Queue) dispatch() {
	if q.closed {
		return
	}
	for _, e := range q.jobs {
		if e.pub.State != StateQueued {
			continue
		}
		kind := e.pub.Kind
		if q.active[kind] >= q.limitOf(kind) {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		now := time.Now()
		e.cancel = cancel
		e.pub.State = StateDownloading
		e.pub.StartedAt = &now
		q.active[kind]++
		q.wg.Add(1)
		go q.work(ctx, e)
	}
}

func (q *Queue) limitOf(kind Kind) int {
	if n, ok := q.limits[kind]; ok && n > 0 {
		return n
	}
	return 1
}

// Jobs returns the queue and the history, newest first.
func (q *Queue) Jobs() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for i := len(q.jobs) - 1; i >= 0; i-- {
		out = append(out, *q.jobs[i].pub)
	}
	return out
}
```

`work` 在 Task 4 才完整（它要 `transfer`）。本 Task 先写一个只跑 `Attempts` 第一条并调 `Install` 的版本，Task 4 再替换。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/download/ -race -v
```
预期：三个测试 PASS。

- [ ] **Step 6: 提交**

```bash
gofmt -w internal/download/
git add internal/download/
git commit -m "feat(download): 队列内核的数据模型与派发骨架

从 plugin/downloader.go 提取，注释一并搬运。与原实现的唯一行为差别是
并发额度按 Kind 分：插件限 3 的理由是 GitHub 每小时 60 次的查询预算，
那是上游的限制而非面板的，把 JDK 排在三个 jar 后面等是本地发明的限制。"
```

---

## Task 2: 取消、清理与关闭

**Files:**
- Create: `internal/download/cancel.go`
- Modify: `internal/download/queue_test.go`
- Reference: `internal/plugin/downloader.go:728-830`

**Interfaces:**
- Consumes: Task 1 的 `Queue`、`entry`、`State`
- Produces: `(*Queue).Cancel(id string) error`、`(*Queue).CancelAll(kinds ...Kind) int`、`(*Queue).ClearFinished() int`、`(*Queue).Close()`、`ErrNotFound`

- [ ] **Step 1: 写失败测试**

追加到 `internal/download/queue_test.go`。四个测试从 `plugin/downloader_test.go:229-400` 改写：

```go
func TestCancelStopsOneJobAndLeavesTheRest(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 3)

	one, _ := q.Submit(req(q, h, KindPlugin, "one"))
	two, _ := q.Submit(req(q, h, KindPlugin, "two"))
	waitFor(t, func() bool { return h.inFlight.Load() == 2 })

	if err := q.Cancel(one.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitFor(t, func() bool { return stateOf(q, one.ID) == StateCancelled })
	if got := stateOf(q, two.ID); got != StateDownloading {
		t.Fatalf("sibling state = %q, want downloading", got)
	}
	close(h.release)
}

// A failure has to outlive the download that follows it: before the queue
// existed the next job overwrote the only record of what went wrong.
func TestAFailedJobSurvivesTheNextDownload(t *testing.T) {
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindCore, 1)

	bad := Request{
		Kind: KindCore, Title: "bad", DedupeKey: "bad",
		Attempts: func(context.Context) ([]Attempt, error) { return nil, errors.New("boom") },
	}
	if _, err := q.Submit(bad); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })

	h := newHeld()
	close(h.release)
	if _, err := q.Submit(req(q, h, KindCore, "good")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })

	var failed int
	for _, job := range q.Jobs() {
		if job.State == StateFailed && job.Error != "" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed jobs in history = %d, want 1", failed)
	}
}

func TestClearFinishedKeepsWhatIsStillRunning(t *testing.T) {
	h := newHeld()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	q.SetLimit(KindPlugin, 2)

	done := newHeld()
	close(done.release)
	if _, err := q.Submit(req(q, done, KindPlugin, "over")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })

	live, _ := q.Submit(req(q, h, KindPlugin, "live"))
	waitFor(t, func() bool { return stateOf(q, live.ID) == StateDownloading })

	if n := q.ClearFinished(); n != 1 {
		t.Fatalf("cleared %d, want 1", n)
	}
	if got := stateOf(q, live.ID); got != StateDownloading {
		t.Fatalf("running job state = %q, want downloading", got)
	}
	close(h.release)
}

func stateOf(q *Queue, id string) State {
	for _, job := range q.Jobs() {
		if job.ID == id {
			return job.State
		}
	}
	return ""
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/download/ -run 'TestCancel|TestAFailed|TestClearFinished' -v
```
预期：`q.Cancel undefined` / `q.ClearFinished undefined`。

- [ ] **Step 3: 写 `cancel.go`**

搬 `plugin/downloader.go:728-830`，注释保留。`finish` 也在这里：

```go
package download

import (
	"errors"
	"fmt"
	"time"
)

// ErrNotFound rejects an id the queue does not hold.
var ErrNotFound = errors.New("没有这个下载任务")

// finish records how a job ended. The only place State leaves Active.
func (q *Queue) finish(e *entry, state State, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	e.pub.State = state
	e.pub.FinishedAt = &now
	if err != nil {
		e.pub.Error = err.Error()
	}
}

// Cancel stops one download by id. Cancelling a finished one is an error
// rather than a no-op: the operator is looking at a stale list, and saying so
// is more use than pretending it worked.
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	var target *entry
	for _, e := range q.jobs {
		if e.pub.ID == id {
			target = e
			break
		}
	}
	if target == nil {
		q.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if !target.pub.State.Active() {
		state := target.pub.State
		q.mu.Unlock()
		return fmt.Errorf("任务已经结束（%s），无法取消", state)
	}
	// A job still queued has no worker to interrupt, so it is finished here
	// and will never be dispatched.
	if target.pub.State == StateQueued {
		now := time.Now()
		target.pub.State = StateCancelled
		target.pub.FinishedAt = &now
		target.pub.Error = ErrCancelled.Error()
		q.mu.Unlock()
		return nil
	}
	cancel := target.cancel
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// CancelAll stops everything still queued or running for the given kinds, or
// for every kind when given none, and reports how many. The queue page's
// one-click way out of a bulk action that turned out to be a mistake.
func (q *Queue) CancelAll(kinds ...Kind) int {
	want := map[Kind]bool{}
	for _, kind := range kinds {
		want[kind] = true
	}
	q.mu.Lock()
	var ids []string
	for _, e := range q.jobs {
		if !e.pub.State.Active() {
			continue
		}
		if len(want) > 0 && !want[e.pub.Kind] {
			continue
		}
		ids = append(ids, e.pub.ID)
	}
	q.mu.Unlock()

	n := 0
	for _, id := range ids {
		if q.Cancel(id) == nil {
			n++
		}
	}
	return n
}

// ClearFinished forgets the history and reports how many rows went. What is
// still queued or running stays: this clears a record, it does not stop work.
func (q *Queue) ClearFinished() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := make([]*entry, 0, len(q.jobs))
	n := 0
	for _, e := range q.jobs {
		if e.pub.State.Active() {
			kept = append(kept, e)
			continue
		}
		n++
	}
	q.jobs = kept
	return n
}

// Close cancels everything in flight and waits briefly for the workers to
// notice, so a shutting-down panel does not leave part files behind.
func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	var cancels []func()
	for _, e := range q.jobs {
		if e.cancel != nil {
			cancels = append(cancels, e.cancel)
		}
	}
	q.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	q.wg.Wait()
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/download/ -race -v
```
预期：全部 PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/download/
git add internal/download/
git commit -m "feat(download): 取消、清空历史与关闭

CancelAll 接受 Kind 过滤，因为聚合页面上「全部停止」要能只停一类。
取消一个已结束的任务仍然报错而不是静默成功——操作员看的是一份过期
列表，说出来比假装成功有用。"
```

---

## Task 3: 字节搬运与校验

**Files:**
- Create: `internal/download/transfer.go`, `internal/download/transfer_test.go`
- Modify: `internal/download/queue.go`（`work` 的完整版）
- Reference: `internal/plugin/downloader.go:578-703, 860-871`

**Interfaces:**
- Consumes: Task 1 的 `Attempt`、`Request`、`entry`；Task 2 的 `finish`
- Produces: `(*Progress).Extracting()`、`(*Progress).Set(downloaded, total int64)`、`ErrChecksum`

- [ ] **Step 1: 写失败测试**

`internal/download/transfer_test.go`。六个测试从 `plugin/downloader_test.go:454-556` 搬，它们测的是校验策略，是这次最值得保住的资产：

```go
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func runOne(t *testing.T, r Request) Job {
	t.Helper()
	q := NewQueue(slog.New(slog.DiscardHandler))
	t.Cleanup(q.Close)
	if _, err := q.Submit(r); err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitFor(t, func() bool { return activeCount(q) == 0 })
	jobs := q.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	return jobs[0]
}

func serve(body string) func(context.Context) ([]Attempt, error) {
	return func(context.Context) ([]Attempt, error) {
		return []Attempt{{Route: "test", Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(body)), nil
		}}}, nil
	}
}

// Azul's metadata under-reports a package by 9 bytes while publishing the right
// SHA-256 for it. A size check that outranked the digest turned a perfectly
// good JDK into a failed install.
func TestMisdeclaredSizeIsAcceptedWhenTheChecksumMatches(t *testing.T) {
	body := "the actual bytes"
	job := runOne(t, Request{
		Kind: KindJava, Title: "jdk", DedupeKey: "jdk",
		Total: int64(len(body)) + 9, SHA256: sum(body),
		Attempts: serve(body),
		Install:  func(context.Context, string, *Progress) (string, error) { return "rt-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
}

func TestPublishedChecksumMismatchIsRejected(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		SHA256:   sum("what upstream promised"),
		Attempts: serve("something else entirely"),
		Install:  func(context.Context, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	if !strings.Contains(job.Error, "SHA-256") {
		t.Fatalf("error = %q, want it to name SHA-256", job.Error)
	}
}

// Without a digest the declared size is the only check there is, so it has to
// be exact — every GitHub release asset lands here.
func TestMisdeclaredSizeIsRejectedWithoutAChecksum(t *testing.T) {
	body := "four"
	job := runOne(t, Request{
		Kind: KindPlugin, Title: "jar", DedupeKey: "jar",
		Total: int64(len(body)) + 10, SHA256: "",
		Attempts: serve(body),
		Install:  func(context.Context, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
}

// "The connection dropped, run it again" is very different advice from "this
// source is serving the wrong file", so a short body says which it was.
func TestATruncatedBodyIsReportedAsTruncated(t *testing.T) {
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		Total: 1024, SHA256: sum("full body"),
		Attempts: serve("short"),
		Install:  func(context.Context, string, *Progress) (string, error) { return "", nil },
	})
	if !strings.Contains(job.Error, "下载中断") {
		t.Fatalf("error = %q, want it to say the transfer was cut short", job.Error)
	}
}

// A route that is down costs a retry rather than the install. This is why
// every route list ends at the origin.
func TestAFailingRouteFallsThroughToTheNextOne(t *testing.T) {
	body := "served by the second"
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		SHA256: sum(body),
		Attempts: func(context.Context) ([]Attempt, error) {
			return []Attempt{
				{Route: "mirror", Open: func(context.Context) (io.ReadCloser, error) {
					return nil, errors.New("mirror is down")
				}},
				{Route: "official", Open: func(context.Context) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(body)), nil
				}},
			}, nil
		},
		Install: func(context.Context, string, *Progress) (string, error) { return "core-1", nil },
	})
	if job.State != StateDone {
		t.Fatalf("state = %q err = %q, want done", job.State, job.Error)
	}
	if job.Route != "official" {
		t.Fatalf("route = %q, want the one that actually answered", job.Route)
	}
}

// The temp file is what Install is handed, and a failed job must not leave one
// behind that looks installable.
func TestAFailedDownloadLeavesNoFileBehind(t *testing.T) {
	dir := t.TempDir()
	job := runOne(t, Request{
		Kind: KindCore, Title: "paper", DedupeKey: "paper",
		TempDir: dir,
		SHA256:  sum("promised"),
		Attempts: serve("delivered"),
		Install:  func(context.Context, string, *Progress) (string, error) { return "", nil },
	})
	if job.State != StateFailed {
		t.Fatalf("state = %q, want failed", job.State)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("temp dir still holds %v", left)
	}
	_ = filepath.Join
}
```

`Request.TempDir` 在 Task 1 已定义（空值取 `os.TempDir()`）。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/download/ -run 'TestMisdeclared|TestPublished|TestATruncated|TestAFailing|TestAFailed' -v
```
预期：`unknown field TempDir` 等编译错误。

- [ ] **Step 3: 写 `transfer.go` 并补完 `work`**

`verifyDigest` 与 `progressWriter` 从 `plugin/downloader.go:667-683, 860-871` 搬，**注释保留**（尤其是 Azul 那段和「短包」那段）。

`Progress` 的两个方法（`job.go` 里已有结构体，方法写在 `transfer.go`）：

```go
// Extracting moves the job into its install phase. Called by the Install hook
// before it starts unpacking: a 200 MB JDK spends real time there and a bar
// that sat at 100% would read as a hang.
func (p *Progress) Extracting() {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	p.entry.pub.State = StateExtracting
	// The download half's numbers would otherwise be read as the unpack's.
	p.entry.pub.Downloaded, p.entry.pub.Total = 0, 0
}

// Set reports how far the install phase has got. Zero total means "working,
// length unknown", which is what an archive with no entry count looks like.
func (p *Progress) Set(downloaded, total int64) {
	p.q.mu.Lock()
	defer p.q.mu.Unlock()
	p.entry.pub.Downloaded, p.entry.pub.Total = downloaded, total
}
```

要点：
- `transfer` 走 `Attempts` 列表，第一条打开成功就用它，把 `Route` 写进 job
- 取消时不要沿着 fallback 往下走（`ctx.Err() != nil` 直接返回），这是原 `Fetch` 的行为，必须保住
- `work` 的流程：`Attempts` → `transfer`（写 `<TempDir>/<id>.part`）→ 校验 → `State = StateExtracting` → `Install` → `finish`
- 无论成败，`work` 都 `os.Remove(temp)`；成功路径由 `Install` 决定把它搬去哪（`Install` 里 `os.Rename` 走了就删不到，`Remove` 的错误忽略）

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/download/ -race -v
```
预期：全部 PASS。

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/download/ && go vet ./internal/download/
git add internal/download/
git commit -m "feat(download): 字节搬运、线路回落与校验策略

校验策略原样保留：有上游校验和时它压过声明长度（Azul 的元数据把包
少报 9 字节却给对了 SHA-256），没有校验和时长度就是唯一的检查因而必须
精确，短包单独报「下载中断」而不是「校验不符」——两者的处置完全不同。

取消时不沿 fallback 继续往下试，否则一个被停掉的任务会装作是镜像的错。"
```

---

## Task 4: 线路表内核与 github 表

**Files:**
- Create: `internal/download/route.go`, `internal/download/routes_github.go`, `internal/download/route_test.go`
- Reference: `internal/plugin/mirrors.go`（整份）

**Interfaces:**
- Consumes: 无
- Produces: `download.RouteKind`（`RouteProxy` / `RouteCopy` / `RouteDirect`）、`download.Route`、
  `download.Upstream`、`Origin(url string) Upstream`、`download.RouteSet`、`RouteSets` map、
  `ResolveRoute(set, id string) (string, error)`、`RouteOrder(set, pref string, up Upstream) []Route`、
  `RouteAuto`、`ErrUnknownRoute`

`Upstream` 在这个 Task 就要定义好，因为 `Route.Link` 收的是它：

```go
// Upstream is the origin download plus whatever a copy needs to rebuild the
// path on its own tree.
//
// A proxy only ever reads URL — it fronts the origin verbatim. A copy of a
// tree laid out differently needs the parts: FastMirror serves
// /download/{project}/{version}/build{n} against PaperMC's content-addressed
// /v1/objects/{sha256}/{name}, and no amount of prefixing turns one into the
// other.
type Upstream struct {
	URL     string
	Host    string
	Project string
	Version string
	Build   int
}

// Origin is an Upstream for a route set whose copies only need the URL, with
// Host filled in from it. Serves is matched against Host, so a caller that
// builds an Upstream by hand must set it.
func Origin(rawURL string) Upstream {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Upstream{URL: rawURL}
	}
	return Upstream{URL: rawURL, Host: parsed.Host}
}
```

- [ ] **Step 1: 写失败测试**

```go
package download

import (
	"strings"
	"testing"
)

// Every set ends at its origin. A proxy that is down, blocked or rate-limiting
// would otherwise turn a working install into a failure, and unlike a copy
// there is nothing a proxy has that the origin does not.
func TestEveryRouteSetEndsAtItsOrigin(t *testing.T) {
	for name, set := range RouteSets {
		if len(set.Routes) == 0 {
			t.Fatalf("%s has no routes", name)
		}
		last := set.Routes[len(set.Routes)-1]
		if last.Kind != RouteDirect {
			t.Fatalf("%s ends at %q (%v), want a direct route", name, last.ID, last.Kind)
		}
	}
}

// A route only appears for an upstream it can actually serve: ghfast fronts
// github.com and nothing else, so offering it for cdn.azul.com would produce
// a 404 with the operator's name on it.
func TestARouteIsSkippedForAnUpstreamItCannotServe(t *testing.T) {
	order := RouteOrder("github", RouteAuto, Origin("https://cdn.azul.com/zulu/bin/x.tar.gz"))
	if len(order) != 1 || order[0].Kind != RouteDirect {
		t.Fatalf("order = %v, want just the direct route", ids(order))
	}
}

func TestAutoWalksEveryProxyThenTheOrigin(t *testing.T) {
	order := RouteOrder("github", RouteAuto, Origin("https://github.com/o/r/releases/download/v1/x.jar"))
	got := ids(order)
	want := []string{"ghfast", "ghproxy", "moeyy", "direct"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// Naming one route still ends at the origin, for the same reason auto does.
func TestNamingOneRouteStillFallsBackToTheOrigin(t *testing.T) {
	order := RouteOrder("github", "moeyy", Origin("https://github.com/o/r/releases/download/v1/x.jar"))
	if got := ids(order); strings.Join(got, ",") != "moeyy,direct" {
		t.Fatalf("order = %v, want moeyy,direct", got)
	}
}

// These proxies come and go; an operator running their own is exactly the
// person this should not stand in the way of.
func TestACustomPrefixIsAccepted(t *testing.T) {
	id, err := ResolveRoute("github", "https://my-proxy.example/")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id != "https://my-proxy.example/" {
		t.Fatalf("id = %q", id)
	}
}

// Anything else is refused rather than quietly turned into the default —
// silently downloading from somewhere other than what was asked for is the
// surprise this whole feature removes.
func TestAnUnknownRouteIsRefusedRatherThanDefaulted(t *testing.T) {
	if _, err := ResolveRoute("github", "not-a-route"); err == nil {
		t.Fatal("resolve accepted an unknown route id")
	}
}

func ids(routes []Route) []string {
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.ID)
	}
	return out
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/download/ -run 'TestEveryRouteSet|TestARoute|TestAuto|TestNaming|TestACustom|TestAnUnknown' -v
```
预期：`undefined: RouteSets`。

- [ ] **Step 3: 写 `route.go` 与 `routes_github.go`**

`route.go` 定义类型与 `ResolveRoute` / `RouteOrder`；解析行为照抄 `plugin.ResolveMirror`（接受已知 id、接受 `https://…/` 自定义前缀并补尾斜杠、其余拒绝）。

`routes_github.go` 把 `plugin/mirrors.go` 的四条搬过来，`Serves: []string{"github.com"}`，`Link` 为前缀拼接。**`mirrors.go` 顶部那段「代理拿不到校验和，选代理等于扩大信任范围」的注释搬到这里**——它是这张表的安全前提。

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/download/ -race -v
```

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/download/ && go vet ./internal/download/
git add internal/download/
git commit -m "feat(download): 线路表内核与 github 线路

ghfast 此前被独立实现了三遍（plugin/mirrors.go、javaruntime 的 ghproxy
源、config 的 DefaultUpdateMirror），这里合成一份。

Route.Serves 是新的：一条线路能不能服务某个下载取决于上游 host，
ghfast 包不住 cdn.azul.com。此前 javaruntime 靠 temurinSources /
zuluSources 两份列表硬扛这件事。"
```

---

## Task 5: adoptium / azul / papermc 三张表

**Files:**
- Create: `internal/download/routes_java.go`, `internal/download/routes_papermc.go`
- Modify: `internal/download/route_test.go`
- Reference: `internal/javaruntime/source.go:55-95`

**Interfaces:**
- Consumes: Task 4 的 `Route`、`RouteSet`、`RouteSets`
- Produces: `RouteSets["adoptium"]`、`RouteSets["azul"]`、`RouteSets["papermc"]`

- [ ] **Step 1: 写失败测试**

```go
// FastMirror serves Paper and Velocity byte-for-byte: build 232 of Paper
// 1.21.4 came back with the same SHA-256 and the same 51437498 bytes as the
// official CDN. That is what makes it safe — the panel verifies against the
// checksum fill.papermc.io published, not against anything the mirror says.
func TestPaperMCRouteSetOffersTheMirrorThenTheOrigin(t *testing.T) {
	upstream := "https://fill-data.papermc.io/v1/objects/abc/paper-1.21.4-232.jar"
	order := RouteOrder("papermc", RouteAuto, Origin(upstream))
	if got := strings.Join(ids(order), ","); got != "fastmirror,official" {
		t.Fatalf("order = %q, want fastmirror,official", got)
	}
}

// Zulu's archives come off cdn.azul.com, which none of the Adoptium mirrors
// carries and the GitHub proxy has nothing to wrap. Shipping a guess that 404s
// on every install is worse than shipping one route.
func TestAzulOffersOnlyItsOwnCDN(t *testing.T) {
	order := RouteOrder("azul", RouteAuto, Origin("https://cdn.azul.com/zulu/bin/x.tar.gz"))
	if got := ids(order); len(got) != 1 || got[0] != "official" {
		t.Fatalf("order = %v, want just official", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/download/ -run 'TestPaperMC|TestAzul' -v
```

- [ ] **Step 3: 写两个文件**

`routes_java.go`：`temurinSources` / `zuluSources` 搬过来改成 `Route`，`mirrorLink(base)` 与 `proxyLink(prefix)` 一并搬，**开头那段「元数据只走官方、字节走镜像、靠 SHA-256 兜底」的注释搬过来**。

`routes_papermc.go`：

```go
// PaperMC download routes.
//
// The official URL is content-addressed —
// https://fill-data.papermc.io/v1/objects/<sha256>/<name>.jar — and the build
// metadata publishes that same SHA-256, which is what makes a copy safe to
// use: whichever route serves the bytes, they are checked against what
// fill.papermc.io said they would be.
//
// FastMirror was verified byte-for-byte before it was added: Paper 1.21.4
// build 232 came back with the same digest and the same 51437498 bytes as the
// origin. It publishes a SHA-1 of its own, which is not used — the origin's
// SHA-256 is the stronger claim and the one already in hand.
//
// The university mirrors that carry Adoptium do not carry PaperMC (TUNA and
// NJU both 404), and Huawei's mirror portal answers 200 for every path
// including ones that do not exist, so its apparent /papermc/ tree is not one.
var paperMCRoutes = []Route{
	{
		ID: "fastmirror", Name: "FastMirror", Note: "国内镜像，与官方字节一致",
		Kind: RouteCopy, Serves: []string{"fill-data.papermc.io"},
		Link: fastMirrorLink,
	},
	{
		ID: "official", Name: "PaperMC 官方", Note: "直连官方 CDN，境外机器选它",
		Kind: RouteDirect, Serves: []string{"fill-data.papermc.io"},
		Link: func(u Upstream) string { return u.URL },
	},
}
```

`fastMirrorLink` 需要 project / version / build，这些不在 URL 里——这正是 Task 4 的 `Upstream`
带这三个字段的原因。serverjar（Task 7）构造 `Upstream` 时要把它们填上，不能用 `Origin()`：

```go
// fastMirrorLink rebuilds the path on FastMirror's own tree. It returns "" for
// anything it cannot address — a caller that did not fill in the build number
// gets the origin rather than a URL that 404s.
func fastMirrorLink(u Upstream) string {
	if u.Project == "" || u.Version == "" || u.Build == 0 {
		return ""
	}
	return fmt.Sprintf("https://download.fastmirror.net/download/%s/%s/build%d",
		strings.Title(u.Project), u.Version, u.Build)
}
```

（`strings.Title` 已废弃，实现时用 `golang.org/x/text/cases` 或手写首字母大写——FastMirror 的
project 段是 `Paper` / `Velocity`，而 serverjar 的 id 是小写的 `paper` / `velocity`。这一行别照抄。）

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/download/ -race -v
```

- [ ] **Step 5: 提交**

```bash
gofmt -w internal/download/ && go vet ./internal/download/
git add internal/download/
git commit -m "feat(download): adoptium、azul 与 papermc 三张线路表

papermc 是新的。验证记录写在 routes_papermc.go 的注释里：清华与南大
都不carry，华为云的 200 是门户 SPA 对任意路径的回应而不是一棵镜像树，
FastMirror 经过字节比对。"
```

---

## Task 6: plugin 接入内核

**Files:**
- Modify: `internal/plugin/downloader.go`, `internal/plugin/mirrors.go`, `internal/plugin/github.go:512-556`, `internal/plugin/downloader_test.go`
- Test: `internal/plugin/downloader_test.go`（删掉已搬走的 17 个，保留插件专属的）

**Interfaces:**
- Consumes: Task 1-5 的全部
- Produces: `plugin.NewDownloader(client, library, queue, logger)`；`(*Downloader).Jobs()` 改为委托给队列并按 `KindPlugin` 过滤

- [ ] **Step 1: 先跑一遍现有测试存档**

```bash
go test ./internal/plugin/ -race 2>&1 | tail -5
```
记下当前是绿的。这是接下来判断「有没有改坏」的基线。

- [ ] **Step 2: 删掉搬走的测试**

从 `internal/plugin/downloader_test.go` 删掉 `TestDownloadsRunSideBySideUpToTheLimit`、`TestQueuedDownloadsRunWhenASlotOpens`、`TestAskingTwiceForTheSameJarReusesTheJob`、`TestCancelStopsOneJobAndLeavesTheRest`、`TestCancellingAQueuedJobStopsItBeforeItRuns`、`TestAFailedJobSurvivesTheNextDownload`、`TestClearFinishedKeepsWhatIsStillRunning`、`TestHistoryIsBoundedButTheQueueIsNot`、`TestTheQueueIsNeverPrunedToMakeRoomForHistory`、`TestConcurrentStartsAreSerialised`，以及 `TestTransfer*` 六个。

**保留** `TestARepeatOfNewestIsNotADifferentRequestOnceItResolves` —— 它测的是插件专属的去重语义（空 tag 是「最新」，解析成 v5.5.71 之后再来一次「最新」仍是同一个请求），内核的 `DedupeKey` 不懂这件事。它变成 plugin 侧怎么构造 `DedupeKey` 的测试。

- [ ] **Step 3: 改 `downloadOrder` 产出 `[]download.Attempt`**

`internal/plugin/github.go:512` 的三条分支语义不变：
- registry → 一条 `Attempt{Route: "direct"}`
- 公开 GitHub → `download.RouteOrder("github", c.Mirror(), download.Origin(asset.URL))` 逐条转成 `Attempt`
- 私有 → 一条 `Attempt{Route: "direct"}` 带 token

**「私有资产绝不经过代理」这条不变量必须有测试守着**，若现有测试没覆盖就补一个。

- [ ] **Step 4: 改 `Downloader` 持有队列**

`Start` 改为构造 `download.Request` 并 `q.Submit`：`DedupeKey` 用 `pluginID + "\x00" + wantTag + "\x00" + strings.ToLower(wantAsset)`；`Install` 里放原 `run()` 中「改名到 final、读 jar、写 library」那一段。

`mirrors.go` 只留 `plugin` 侧需要的 id 兼容（`MirrorAuto` / `MirrorDirect` 常量、`Mirrors()` 转发到 `download.RouteSets["github"]`），表本体删掉。

- [ ] **Step 5: 跑全量测试**

```bash
make lint && make test
```
预期：全绿。若 `internal/api` 的测试挂了，是 `NewDownloader` 签名变了，同步改 `internal/api/server.go` 的构造处。

- [ ] **Step 6: 提交**

```bash
git add -A internal/plugin internal/api
git commit -m "refactor(plugin): 下载队列改由 internal/download 承担

插件专属的部分留在本包：私有仓库的可见性同步、token、从 release 里挑
asset、写入插件库。私有资产只有一条路由且绝不经过代理这条不变量随
downloadOrder 一起留在这里——它不该出现在一个不知道 token 为何物的包里。

去重键由本包构造，因为「最新」和它解析出的 tag 是同一个请求这件事只有
插件这一侧知道。"
```

---

## Task 7: serverjar 接入并补上镜像

**Files:**
- Modify: `internal/serverjar/downloader.go`, `internal/serverjar/client.go`, `internal/config/config.go`
- Test: `internal/serverjar/downloader_test.go`

**Interfaces:**
- Consumes: Task 1-5
- Produces: `serverjar.NewDownloader(client, library, queue, logger)`；`config.Panel.CoreSource`

- [ ] **Step 1: 写失败测试**

```go
// coreStub stands in for both halves of PaperMC: the metadata API that
// publishes the digest, and a mirror that serves bytes. They are separate
// hosts in production and the split is the point — the digest comes from the
// origin, the bytes may not.
type coreStub struct {
	server *httptest.Server
	jar    []byte
	// mirrorHits counts requests the mirror route served, so a test can assert
	// which route actually answered rather than trusting the label.
	mirrorHits atomic.Int32
}

func newCoreStub(t *testing.T, jar []byte, mirrorServes []byte) *coreStub {
	t.Helper()
	stub := &coreStub{jar: jar}
	sum := sha256.Sum256(jar)
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/builds"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `[{"id":232,"time":"2025-06-09T10:18:55.778Z","channel":"STABLE",
			  "downloads":{"server:default":{"name":"paper-1.21.4-232.jar",
			    "checksums":{"sha256":"%s"},"size":%d,"url":"%s/origin/paper.jar"}}}]`,
				hex.EncodeToString(sum[:]), len(jar), stub.server.URL)
		case strings.HasPrefix(r.URL.Path, "/mirror/"):
			stub.mirrorHits.Add(1)
			w.Write(mirrorServes)
		default:
			w.Write(jar)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// A mirror is offered at all only because the bytes can be checked against the
// digest the *origin* published. A mirror serving something else must fail the
// job, not install a different jar under the right name.
func TestACoreFromTheMirrorIsCheckedAgainstTheOfficialDigest(t *testing.T) {
	stub := newCoreStub(t, []byte("the real paper jar"), []byte("not the paper jar"))
	dl, q := newCoreDownloader(t, stub, "mirror")
	t.Cleanup(q.Close)

	job, err := dl.Start("paper", "1.21.4")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForJob(t, q, job.ID, func(j download.Job) bool { return !j.State.Active() })

	got := jobByID(t, q, job.ID)
	if got.State != download.StateFailed {
		t.Fatalf("state = %q, want failed — the mirror served the wrong bytes", got.State)
	}
	if !strings.Contains(got.Error, "SHA-256") {
		t.Fatalf("error = %q, want it to name the checksum", got.Error)
	}
	if stub.mirrorHits.Load() == 0 {
		t.Fatal("the mirror was never tried, so this proves nothing")
	}
}

// Core downloads used to hold one slot and answer the second request with 409.
// Installing two versions back to back is an ordinary thing to want.
func TestASecondCoreDownloadQueuesRatherThanBeingRefused(t *testing.T) {
	jar := []byte("the real paper jar")
	stub := newCoreStub(t, jar, jar)
	dl, q := newCoreDownloader(t, stub, "official")
	t.Cleanup(q.Close)

	if _, err := dl.Start("paper", "1.21.4"); err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := dl.Start("paper", "1.21.3")
	if err != nil {
		t.Fatalf("second download was refused: %v", err)
	}
	if second.State != download.StateQueued && second.State != download.StateDownloading {
		t.Fatalf("second state = %q, want it queued or running", second.State)
	}
}
```

`newCoreDownloader(t, stub, route)` 建一个指向 stub 的 `Client`、一个 `t.TempDir()` 的 `Library`、一个
`download.NewQueue`，并把 route 表里的 `fastmirror` 的 `Link` 指到 `stub.server.URL + "/mirror/..."`。
`waitForJob` / `jobByID` 是本包的小辅助，照 `internal/download/queue_test.go` 里 `waitFor` / `stateOf`
的写法抄。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/serverjar/ -run 'TestACoreFromTheMirror|TestASecondCore' -v
```

- [ ] **Step 3: 实现**

- `config.Panel` 加 `CoreSource string`，注释照 `JavaSource` 的写法（**从上次下载记住，不是设置页上的一项**）。
- `serverjar.Downloader` 删 `Job` / `JobState` / `progressWriter` / `Status`，改为持有队列。
- `Build` 已有 `SHA256`（`client.go:280`），直接进 `Request.SHA256`。
- `Attempts` 用 `download.RouteOrder("papermc", cfg.CoreSource, Upstream{URL: build.URL, Host: "fill-data.papermc.io", Project: projectID, Version: version, Build: build.ID})`。
- `ErrBusy` / `ErrExists` 的 409 分支：`ErrBusy` 不再由单槽产生，但队列满时仍可能回它，`writeJarError` 保持不动。

- [ ] **Step 4: 跑测试确认通过**

```bash
make lint && make test
```

- [ ] **Step 5: 写 CHANGELOG**

在 `CHANGELOG.md`「未发布」下追加：

```markdown
- 服务端核心新增下载镜像（FastMirror），下载时可选；镜像的字节仍按 PaperMC 官方发布的 SHA-256 校验
- 服务端核心、Java 环境、数据库引擎的下载改为排队，不再「上一个没下完就拒绝下一个」
```

- [ ] **Step 6: 提交**

```bash
git add -A internal/serverjar internal/config CHANGELOG.md
git commit -m "feat(serverjar): 核心下载接入统一队列，并补上下载镜像

核心是此前唯一一个连加速选项都没有的下载，而 Paper 的 jar 有 50MB。
FastMirror 经过字节比对后加入，校验仍用官方元数据里的 SHA-256——镜像
自己发布的是 SHA-1，不使用。

单槽随之消失：第二个下载排队而不是 409。"
```

---

## Task 8: javaruntime 接入

**Files:**
- Modify: `internal/javaruntime/installer.go`, `internal/javaruntime/source.go`
- Test: `internal/javaruntime/installer_test.go`

**Interfaces:**
- Consumes: Task 1-5
- Produces: `javaruntime.NewInstaller(client, store, queue, logger)`

- [ ] **Step 1: 写失败测试**：解包阶段要能汇报进度

`extracting` 是个中间状态，等任务结束再看就已经错过了，所以断言靠一个采样 goroutine：

```go
// A 200 MB JDK spends real time unpacking, and a bar that sat at 100% would
// read as a hang. The install hook reports through the same job, so the state
// has to pass through extracting on its way to done.
func TestTheExtractPhaseKeepsTheJobMoving(t *testing.T) {
	stub := newJavaStub(t)
	inst, q := newTestInstaller(t, stub)
	t.Cleanup(q.Close)

	job, err := inst.Start("temurin", 21, "jdk")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Sampled rather than checked at the end: extracting is a state the job
	// passes through, and by the time it is done it is gone.
	seen := make(chan struct{})
	go func() {
		for {
			if jobByID(t, q, job.ID).State == download.StateExtracting {
				close(seen)
				return
			}
			if !jobByID(t, q, job.ID).State.Active() {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	waitForJob(t, q, job.ID, func(j download.Job) bool { return !j.State.Active() })
	if got := jobByID(t, q, job.ID); got.State != download.StateDone {
		t.Fatalf("state = %q err = %q, want done", got.State, got.Error)
	}
	select {
	case <-seen:
	default:
		t.Fatal("the job never reported extracting, so the unpack shows as a stalled bar")
	}
}
```

`newJavaStub` 服务一个真的 tar.gz（用 `archive/tar` + `compress/gzip` 在测试里现造一个含两三个文件的
包，够慢到能被采样到），`newTestInstaller` 照 `internal/javaruntime/installer_test.go` 现有的搭建方式写。

- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现**：`source.go` 的表删掉改为转发 `download.RouteSets["adoptium"]` / `["azul"]`；`Installer` 删 `Job` / `progressWriter`；解包放进 `Install` 回调，开头调 `pub.Extracting()`。
- [ ] **Step 4: `make lint && make test`**
- [ ] **Step 5: 提交**

---

## Task 9: dbruntime 接入

**Files:**
- Modify: `internal/dbruntime/installer.go`
- Test: `internal/dbruntime/installer_test.go`

**不接线路表**：MySQL / PostgreSQL / MongoDB 的下载 URL 由各自上游的元数据给出，三个引擎三个 host，
与 GitHub 和 PaperMC 都不重叠，没有一条已验证的镜像可接。`Attempts` 只产出一条直连。

- [ ] **Step 1: 写失败测试**

```go
// The database installer loses its own Job type and progressWriter here. What
// must not change is that a finished install still hands back the id the
// database shelf knows it by — everything downstream addresses an engine by
// its install id.
func TestAFinishedInstallReportsItsInstallID(t *testing.T) {
	stub := newEngineStub(t)
	inst, q := newTestDBInstaller(t, stub)
	t.Cleanup(q.Close)

	job, err := inst.Start("mysql", "8.0.28")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForJob(t, q, job.ID, func(j download.Job) bool { return !j.State.Active() })

	got := jobByID(t, q, job.ID)
	if got.State != download.StateDone {
		t.Fatalf("state = %q err = %q, want done", got.State, got.Error)
	}
	if got.Ref == "" {
		t.Fatal("Ref is empty, so the UI has no way to point at what was installed")
	}
	if _, err := inst.Store().Get(got.Ref); err != nil {
		t.Fatalf("Ref %q is not an install this store knows: %v", got.Ref, err)
	}
}

// A database download has exactly one route. Offering the GitHub proxies here
// would produce a 404 with the operator's name on it.
func TestADatabaseDownloadHasNoMirrorRoutes(t *testing.T) {
	stub := newEngineStub(t)
	inst, q := newTestDBInstaller(t, stub)
	t.Cleanup(q.Close)

	job, _ := inst.Start("mysql", "8.0.28")
	waitForJob(t, q, job.ID, func(j download.Job) bool { return !j.State.Active() })

	if route := jobByID(t, q, job.ID).Route; route != "direct" {
		t.Fatalf("route = %q, want direct — this shelf has no mirrors", route)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/dbruntime/ -run 'TestAFinishedInstall|TestADatabaseDownload' -v
```

- [ ] **Step 3: 实现**：`Installer` 删 `Job` / `JobState` / `progressWriter` / `Status`，改为持有队列；
  解包放进 `Install` 回调，开头调 `pub.Extracting()`；`Attempts` 返回单条 `{Route: "direct"}`。

- [ ] **Step 4: `make lint && make test`**

- [ ] **Step 5: 提交**

```bash
git add -A internal/dbruntime
git commit -m "refactor(dbruntime): 数据库引擎安装接入统一队列

本期不给这一格补镜像：三个引擎的下载 URL 由各自上游的元数据给出，
三个 host 与 GitHub 和 PaperMC 都不重叠，没有一条能验证过的镜像可接。
线路表的结构容得下它——将来验到了加一张 RouteSet 即可，不必改内核。"
```

---

## Task 10: selfupdate 接线路表

**Files:**
- Modify: `internal/selfupdate/selfupdate.go:146-180`, `internal/config/config.go:24-30`
- Test: `internal/selfupdate/mirror_test.go`

**不接队列**——面板自更新要停掉所有实例再替换自己，与「并行下载三个东西」语义冲突，`Phase` 保留。

- [ ] **Step 1: 写失败测试**：`SetMirror("ghfast")` 这个 **id** 能解析成前缀（现在只吃裸前缀）
- [ ] **Step 2: 跑测试确认失败**
- [ ] **Step 3: 实现**：`SetMirror` 先过 `download.ResolveRoute("github", id)`；`DefaultUpdateMirror` 改为常量 `"ghfast"`，并保留把旧配置里的裸前缀读成自定义线路的兼容路径（`ApplyDefaults` 里处理）。
- [ ] **Step 4: `make lint && make test`**
- [ ] **Step 5: 提交**

---

## Task 11: 收口

- [ ] **Step 1: 确认五份 Job 都没了**

```bash
grep -rn 'progressWriter' internal/ --include='*.go' | grep -v '^internal/download/'
```
预期：无输出。

```bash
grep -rn 'ghfast.top' internal/ --include='*.go' | grep -v '^internal/download/'
```
预期：无输出（ghfast 只应出现在线路表里）。

- [ ] **Step 2: 全量验证**

```bash
make lint && make test && make build
```

- [ ] **Step 3: 提交并推送**

```bash
git push -u origin claude/brave-cray-nxdlue
```

一期到此结束。二期见 `docs/superpowers/plans/2026-09-13-downloads-ui.md`。
