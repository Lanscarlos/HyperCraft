# Java 环境统一登记（一期）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把实例的 Java 选择收进一张面板管理的登记表——只有登记过的 Java 才能被选，登记入口只有「Java 环境」页一处。

**Architecture:** 新增 `javaruntime.Registry`（外部 Java 路径的登记表，JSON 持久化、内存缓存、写穿透），与已有的 `javaruntime.Store`（扫 `<root>/java` 目录）合并成一份「可用 Java」列表。约束加在 API 写入实例配置的地方，**不加在启动路径上**——`cfg.Java` 仍然直接当 argv[0]，不给守护进程最不该脆弱的那条路径新增失败模式。

**Tech Stack:** Go 1.x（标准库，`log/slog`、`encoding/json`、`os/exec`）；React 18 + TypeScript + Vite；样式全在 `web/src/styles.css`。

**Spec:** `docs/superpowers/specs/2026-09-13-java-registry-and-core-gate-design.md`

## Global Constraints

- **代码注释用英文，文档用中文。** 沿用所处文件的语言，不要混。（CLAUDE.md）
- 注释解释**为什么**，不解释代码在做什么。
- Go 提交前必须 `gofmt`，CI 直接卡。检查命令：`make lint`（gofmt + go vet）、`make test`（`go test -race ./...`）。
- **不改启动路径。** `internal/instance` 包不得依赖 `internal/javaruntime`。`instance.go:507` 的 `cmd.Env = withJavaEnv(cmd.Env, cfg.Java)` 和 `config.go:353/371` 的 argv 构造一行不动。
- **白名单校验的是改动，不是状态**：只在 body 带了 `java`、trim 后非空、且与服务端当前存的值不同时才校验。服务端自己比对当前 config，**不得新增 `javaChanged` 之类的请求字段**。
- 前端所有样式写进 `web/src/styles.css`，只用文件开头令牌区的令牌，不写裸 hex。新增令牌必须 light / dark 两个块都加。
- 前端唯一的自动检查是 `npm --prefix web run build`（`tsc -b` + vite build + `check:ui`）。
- **涉及布局的任务（Task 7/8/9）动手前必须先调用 `frontend-design` skill**（`.claude/skills/frontend-design/SKILL.md`）。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节，**不要**把「未发布」改成版本号（那会触发发版）。
- 开发分支：`claude/modest-davinci-e58tca`。

---

### Task 1: `javaruntime.Registry` —— 登记表的持久化

**Files:**
- Create: `internal/javaruntime/registry.go`
- Create: `internal/javaruntime/registry_test.go`
- Modify: `internal/config/config.go`（在 `DatabasesFile()` 附近加 `JavaRegistryFile()`）

**Interfaces:**
- Consumes: 无
- Produces:
  - `type Entry struct { ID, JavaPath, Vendor, Version string; Major int; AddedBy string; AddedAt time.Time }`
  - `const AddedManual = "manual"`、`AddedDetected = "detected"`、`AddedMigrated = "migrated"`
  - `var ErrDuplicate error`
  - `func EntryID(javaPath string) string`
  - `func NewRegistry(path string, log *slog.Logger) *Registry`
  - `func (r *Registry) List() []Entry`
  - `func (r *Registry) Get(id string) (Entry, error)`
  - `func (r *Registry) Add(e Entry) (Entry, error)`
  - `func (r *Registry) Remove(id string) error`
  - `func (r *Registry) Replace(id string, e Entry) (Entry, error)`
  - `func (p Paths) JavaRegistryFile() string`

**设计要点（实现前读一遍）：**

内存缓存 + 写穿透。`NewRegistry` 构造时读一次盘，之后 `List()` 只读内存。**这是有意的**：登记表是一张白名单，如果每次 `List()` 都读盘，一次瞬时读失败就会让白名单变空，进而把合法的保存请求 400 掉。构造时读一次，损坏就当空表并记日志，面板照常启动。

- [ ] **Step 1: 写失败的测试**

创建 `internal/javaruntime/registry_test.go`：

```go
package javaruntime

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegistryAddListRemove(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	reg := NewRegistry(file, testLogger())

	if got := reg.List(); len(got) != 0 {
		t.Fatalf("a fresh registry should be empty, got %d", len(got))
	}

	added, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", Major: 21, Version: "21.0.12", AddedBy: AddedManual})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" {
		t.Fatal("Add should assign an ID")
	}
	if added.AddedAt.IsZero() {
		t.Fatal("Add should stamp AddedAt")
	}

	if got := reg.List(); len(got) != 1 || got[0].JavaPath != "/opt/jdk21/bin/java" {
		t.Fatalf("List after Add = %+v", got)
	}

	got, err := reg.Get(added.ID)
	if err != nil || got.Major != 21 {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	if err := reg.Remove(added.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("List after Remove = %+v", got)
	}
}

func TestRegistryPersistsAcrossReload(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	reg := NewRegistry(file, testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk17/bin/java", Major: 17, AddedBy: AddedDetected}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	reloaded := NewRegistry(file, testLogger())
	if got := reloaded.List(); len(got) != 1 || got[0].Major != 17 {
		t.Fatalf("reload lost the entry: %+v", got)
	}
}

func TestRegistryRejectsDuplicatePath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	reg := NewRegistry(file, testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err == nil {
		t.Fatal("a second Add of the same path should fail")
	}
}

// A registry the panel cannot parse must not stop the panel from starting: it
// is a list of conveniences, not a source of truth about anything running.
func TestRegistryTreatsCorruptFileAsEmpty(t *testing.T) {
	file := filepath.Join(t.TempDir(), "java-registry.json")
	if err := os.WriteFile(file, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(file, testLogger())
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("corrupt registry should read as empty, got %+v", got)
	}
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err != nil {
		t.Fatalf("a corrupt registry must still accept writes: %v", err)
	}
	if got := NewRegistry(file, testLogger()).List(); len(got) != 1 {
		t.Fatalf("the rewritten file should hold one entry, got %+v", got)
	}
}

func TestRegistryRemoveUnknownIsNotFound(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if err := reg.Remove("nope"); err == nil {
		t.Fatal("removing an unknown id should fail")
	}
}

func TestEntryIDIsStableForTheSamePath(t *testing.T) {
	if EntryID("/opt/jdk21/bin/java") != EntryID("/opt/jdk21/bin/java") {
		t.Fatal("EntryID should be deterministic")
	}
	if EntryID("/opt/jdk21/bin/java") == EntryID("/opt/jdk17/bin/java") {
		t.Fatal("different paths should get different ids")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/javaruntime/ -run 'TestRegistry|TestEntryID' -v`
Expected: FAIL，编译错误 `undefined: NewRegistry`、`undefined: Entry` 等。

- [ ] **Step 3: 写实现**

创建 `internal/javaruntime/registry.go`：

```go
package javaruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrDuplicate is returned when a path is already registered. The id is
// derived from the path, so a second Add would silently overwrite the first.
var ErrDuplicate = errors.New("java path already registered")

// How an entry got here, for the page to label it with.
const (
	AddedManual   = "manual"   // the operator typed the path
	AddedDetected = "detected" // probed off PATH or JAVA_HOME, then confirmed
	AddedMigrated = "migrated" // an instance was already launching with it
)

// Entry is a Java the panel did not install: a path the operator pointed at.
//
// The panel stores the probed version rather than re-probing on every read.
// Probing forks a JVM, and this list is read on every instance-settings save
// and every Java page poll.
type Entry struct {
	ID string `json:"id"`
	// JavaPath is the launcher. "java" is legal and means "follow PATH",
	// which is what an instance configured before this registry existed says.
	JavaPath string    `json:"javaPath"`
	Vendor   string    `json:"vendor"`
	Version  string    `json:"version"`
	Major    int       `json:"major"`
	AddedBy  string    `json:"addedBy"`
	AddedAt  time.Time `json:"addedAt"`
}

// EntryID derives a stable id from the path, so re-registering the same java
// is a duplicate rather than a second row that says the same thing.
func EntryID(javaPath string) string {
	sum := sha256.Sum256([]byte(javaPath))
	return "ext-" + hex.EncodeToString(sum[:6])
}

// Registry is the list of Java paths an instance is allowed to point at, on
// top of whatever Store finds in the runtimes directory.
//
// It is read into memory once and written through, rather than read per call.
// A whitelist that empties itself on a transient read error would start
// refusing saves that are perfectly valid, and the file is a few hundred bytes.
type Registry struct {
	path string
	log  *slog.Logger

	mu      sync.RWMutex
	entries []Entry
}

// NewRegistry loads the registry. A file that cannot be read or parsed is
// reported and treated as empty: this list is a set of conveniences, and none
// of it is needed to keep a running server running.
func NewRegistry(path string, log *slog.Logger) *Registry {
	r := &Registry{path: path, log: log, entries: []Entry{}}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error("could not read the java registry", "path", path, "err", err)
		}
		return r
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Error("the java registry is not readable json, starting empty", "path", path, "err", err)
		return r
	}
	r.entries = entries
	return r
}

// List returns every registered entry, in the order they were added.
func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Get returns one entry by id.
func (r *Registry) Get(id string) (Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.entries {
		if entry.ID == id {
			return entry, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Add registers a path. The caller supplies the probed version fields; this
// type does not fork JVMs.
func (r *Registry) Add(entry Entry) (Entry, error) {
	if entry.JavaPath == "" {
		return Entry{}, fmt.Errorf("%w: java path is required", ErrInvalidID)
	}
	entry.ID = EntryID(entry.JavaPath)
	if entry.AddedAt.IsZero() {
		entry.AddedAt = time.Now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.entries {
		if existing.JavaPath == entry.JavaPath {
			return Entry{}, fmt.Errorf("%w: %s", ErrDuplicate, entry.JavaPath)
		}
	}
	r.entries = append(r.entries, entry)
	if err := r.save(); err != nil {
		r.entries = r.entries[:len(r.entries)-1]
		return Entry{}, err
	}
	return entry, nil
}

// Replace overwrites one entry in place, keeping its id and position. It is
// how a re-probe records a new version without the row jumping around.
func (r *Registry) Replace(id string, entry Entry) (Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.entries {
		if existing.ID != id {
			continue
		}
		entry.ID, entry.JavaPath, entry.AddedBy, entry.AddedAt =
			existing.ID, existing.JavaPath, existing.AddedBy, existing.AddedAt
		previous := r.entries[i]
		r.entries[i] = entry
		if err := r.save(); err != nil {
			r.entries[i] = previous
			return Entry{}, err
		}
		return entry, nil
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Remove drops one entry.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, entry := range r.entries {
		if entry.ID != id {
			continue
		}
		removed := r.entries
		r.entries = append(append([]Entry{}, r.entries[:i]...), r.entries[i+1:]...)
		if err := r.save(); err != nil {
			r.entries = removed
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

// save writes the whole list. Callers hold the write lock.
func (r *Registry) save() error {
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	// Same rename-into-place the rest of the panel's state files use, so a
	// crash mid-write leaves the old list rather than half of the new one.
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
```

在 `internal/config/config.go` 的 `DatabasesFile()` 之后加：

```go
// JavaRegistryFile is the list of Java installations the operator pointed the
// panel at, as opposed to the ones it downloaded into JavaRoot.
//
// Beside the java directory rather than inside it, for the reason DatabasesFile
// sits beside the database root: an entry id could otherwise collide with a
// runtime directory name, and Store.List would have to learn to skip a file
// that is none of its business.
func (p Paths) JavaRegistryFile() string { return filepath.Join(p.Root, "java-registry.json") }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/javaruntime/ -run 'TestRegistry|TestEntryID' -v`
Expected: 全部 PASS。

Run: `gofmt -l internal/javaruntime/ internal/config/`
Expected: 无输出。

- [ ] **Step 5: 提交**

```bash
git add internal/javaruntime/registry.go internal/javaruntime/registry_test.go internal/config/config.go
git commit -m "feat(java): 登记表持久化，内存缓存写穿透

白名单每次读盘的话，一次瞬时读失败就会让它变空，把合法的保存 400 掉。
构造时读一次，损坏当空表并记日志，面板照常启动。"
```

---

### Task 2: 合并列表 —— Store ∪ Registry

**Files:**
- Create: `internal/javaruntime/available.go`
- Create: `internal/javaruntime/available_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Entry`、`Registry`、`Registry.List()`；已有的 `Store.List()`、`Runtime`
- Produces:
  - `const SourceManaged = "managed"`、`SourceExternal = "external"`
  - `type Available struct{ ID, JavaPath, Vendor, Version string; Major int; Source string; Valid bool; Path, ImageType string; Size int64; InstalledAt time.Time }`
  - `func Usable(javaPath string) bool`
  - `func AvailableList(store *Store, reg *Registry) ([]Available, error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/javaruntime/available_test.go`：

```go
package javaruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeRuntimeDir lays out just enough of a JDK for Store.inspect to accept it.
func fakeRuntimeDir(t *testing.T, root, id, version string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	bin := filepath.Join(dir, "bin")
	if runtime.GOOS == "darwin" {
		bin = filepath.Join(dir, "Contents", "Home", "bin")
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(bin, javaBinary())
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	release := "JAVA_VERSION=\"" + version + "\"\nIMPLEMENTOR=\"Test\"\n"
	if err := os.WriteFile(filepath.Join(dir, "release"), []byte(release), 0o644); err != nil {
		t.Fatal(err)
	}
	return launcher
}

func TestAvailableListMergesBothSources(t *testing.T) {
	root := t.TempDir()
	fakeRuntimeDir(t, root, "temurin-21", "21.0.12")
	store := NewStore(root)

	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/nowhere/jdk17/bin/java", Major: 17, Version: "17.0.1", AddedBy: AddedManual}); err != nil {
		t.Fatal(err)
	}

	list, err := AvailableList(store, reg)
	if err != nil {
		t.Fatalf("AvailableList: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(list), list)
	}

	bySource := map[string]Available{}
	for _, entry := range list {
		bySource[entry.Source] = entry
	}
	if bySource[SourceManaged].Major != 21 {
		t.Fatalf("managed entry = %+v", bySource[SourceManaged])
	}
	if bySource[SourceExternal].Major != 17 {
		t.Fatalf("external entry = %+v", bySource[SourceExternal])
	}
}

// A path that no longer exists stays in the list, marked. Dropping it would
// leave the instance pointing at it with nothing in the dropdown to show.
func TestAvailableListMarksMissingPathInvalid(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/nowhere/jdk17/bin/java", Major: 17, AddedBy: AddedManual}); err != nil {
		t.Fatal(err)
	}

	list, err := AvailableList(NewStore(t.TempDir()), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 entry, got %+v", list)
	}
	if list[0].Valid {
		t.Fatal("a path that is not there should not be Valid")
	}
}

// The same launcher registered by hand and found in the runtimes directory is
// one Java, and the managed row is the one that can be deleted or re-probed.
func TestAvailableListPrefersManagedOnDuplicatePath(t *testing.T) {
	root := t.TempDir()
	launcher := fakeRuntimeDir(t, root, "temurin-21", "21.0.12")

	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: launcher, Major: 21, AddedBy: AddedMigrated}); err != nil {
		t.Fatal(err)
	}

	list, err := AvailableList(NewStore(root), reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want the duplicate collapsed, got %+v", list)
	}
	if list[0].Source != SourceManaged {
		t.Fatalf("managed should win, got %+v", list[0])
	}
}

func TestUsableFollowsPathForBareJava(t *testing.T) {
	// "java" is legal and means PATH. Whether this machine has one is not the
	// point: Usable must not report true for a literal file named "java" in
	// the working directory, and must not panic.
	_ = Usable("java")

	if Usable(filepath.Join(t.TempDir(), "definitely-not-here")) {
		t.Fatal("a missing absolute path is not usable")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/javaruntime/ -run 'TestAvailable|TestUsable' -v`
Expected: FAIL，`undefined: AvailableList`。

- [ ] **Step 3: 写实现**

创建 `internal/javaruntime/available.go`：

```go
package javaruntime

import (
	"os"
	"os/exec"
	"sort"
	"time"
)

// Where an available Java came from.
const (
	SourceManaged  = "managed"  // unpacked under the runtimes root
	SourceExternal = "external" // a path the operator registered
)

// Available is one Java an instance is allowed to point at.
//
// The two sources are merged into one shape because every consumer — the
// dropdown, the whitelist check, the Java page — wants the same four facts
// (path, version, where it came from, is it still there) and none of them
// wants to branch on which list it was in.
type Available struct {
	ID       string `json:"id"`
	JavaPath string `json:"javaPath"`
	Vendor   string `json:"vendor"`
	Version  string `json:"version"`
	Major    int    `json:"major"`
	Source   string `json:"source"`
	// Valid is false for a path that is no longer there — a JDK uninstalled
	// behind the panel's back. Such an entry is kept and marked rather than
	// dropped: an instance still points at it, and a dropdown that silently
	// loses the selected option is worse than one that says why.
	Valid bool `json:"valid"`

	// Managed runtimes only; zero for a registered path.
	Path        string    `json:"path"`
	ImageType   string    `json:"imageType"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installedAt"`
}

// Usable reports whether the launcher is still where the entry says it is.
// A bare "java" means PATH, so it is resolved the way the JVM would be.
func Usable(javaPath string) bool {
	if javaPath == "" {
		return false
	}
	if javaPath == javaBinary() || javaPath == "java" {
		_, err := exec.LookPath(javaPath)
		return err == nil
	}
	info, err := os.Stat(javaPath)
	return err == nil && !info.IsDir()
}

// AvailableList merges the runtimes directory with the registry.
//
// A launcher present in both is one Java, and the managed row wins: it is the
// one that carries a size, an image type and a delete button that does
// something.
func AvailableList(store *Store, reg *Registry) ([]Available, error) {
	runtimes, err := store.List()
	if err != nil {
		return nil, err
	}

	out := make([]Available, 0, len(runtimes))
	seen := make(map[string]bool, len(runtimes))
	for _, rt := range runtimes {
		seen[rt.JavaPath] = true
		out = append(out, Available{
			ID:          rt.ID,
			JavaPath:    rt.JavaPath,
			Vendor:      rt.Vendor,
			Version:     rt.Version,
			Major:       rt.Major,
			Source:      SourceManaged,
			Valid:       Usable(rt.JavaPath),
			Path:        rt.Path,
			ImageType:   rt.ImageType,
			Size:        rt.Size,
			InstalledAt: rt.InstalledAt,
		})
	}

	if reg != nil {
		for _, entry := range reg.List() {
			if seen[entry.JavaPath] {
				continue
			}
			out = append(out, Available{
				ID:          entry.ID,
				JavaPath:    entry.JavaPath,
				Vendor:      entry.Vendor,
				Version:     entry.Version,
				Major:       entry.Major,
				Source:      SourceExternal,
				Valid:       Usable(entry.JavaPath),
				InstalledAt: entry.AddedAt,
			})
		}
	}

	// Newest major first, matching Store.List, so the dropdown's first option
	// is the one most servers want.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Major != out[b].Major {
			return out[a].Major > out[b].Major
		}
		if out[a].Source != out[b].Source {
			return out[a].Source == SourceManaged
		}
		return out[a].ID < out[b].ID
	})
	return out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/javaruntime/ -v`
Expected: 全部 PASS（包括已有的 store / source / installer 测试）。

Run: `gofmt -l internal/javaruntime/`
Expected: 无输出。

- [ ] **Step 5: 提交**

```bash
git add internal/javaruntime/available.go internal/javaruntime/available_test.go
git commit -m "feat(java): 把装的和登记的合成一份可用列表

失效条目保留并标记，不从列表里删掉——实例还指着它，
一个悄悄丢掉当前选项的下拉框比一个说明原因的更糟。"
```

---

### Task 3: 存量迁移 —— 把实例正在用的 Java 登记进来

**Files:**
- Create: `internal/javaruntime/migrate.go`
- Create: `internal/javaruntime/migrate_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Registry`、`Entry`、`AddedMigrated`；Task 2 的 `AvailableList`
- Produces: `func MigrateInstanceJava(ctx context.Context, store *Store, reg *Registry, javaPaths []string) int`

**设计要点：**

返回登记成功的条数，不返回 error——单条失败不该拖垮面板启动。探测不出版本的**照样写入**并保留原路径（版本字段留空，`AvailableList` 会把它标成失效）。这条沿用 `internal/instance/migrate.go` 开头立的规矩：拆不出来的就标记，不猜。

- [ ] **Step 1: 写失败的测试**

创建 `internal/javaruntime/migrate_test.go`：

```go
package javaruntime

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateRegistersUnknownPaths(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	store := NewStore(t.TempDir())

	added := MigrateInstanceJava(context.Background(), store, reg,
		[]string{"/opt/jdk21/bin/java", "/opt/jdk17/bin/java"})
	if added != 2 {
		t.Fatalf("want 2 registered, got %d", added)
	}
	if got := reg.List(); len(got) != 2 {
		t.Fatalf("registry = %+v", got)
	}
	for _, entry := range reg.List() {
		if entry.AddedBy != AddedMigrated {
			t.Fatalf("entry %+v should be marked migrated", entry)
		}
	}
}

// A path the panel cannot probe is still recorded. It is what an instance is
// actually launching with, which is more certain than any detection.
func TestMigrateKeepsUnprobeablePaths(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())

	MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg,
		[]string{"/nowhere/jdk8/bin/java"})

	list := reg.List()
	if len(list) != 1 {
		t.Fatalf("want the unprobeable path kept, got %+v", list)
	}
	if list[0].JavaPath != "/nowhere/jdk8/bin/java" {
		t.Fatalf("the original path must survive verbatim: %+v", list[0])
	}
	if list[0].Version != "" {
		t.Fatalf("a version the panel could not read must stay blank, got %q", list[0].Version)
	}
}

// "java" means "follow PATH", and an instance that says so said it on
// purpose. Pinning it to whatever absolute path PATH resolves to today would
// be the panel making a decision nobody asked it to make.
func TestMigrateKeepsBareJavaVerbatim(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())

	MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg, []string{"java"})

	list := reg.List()
	if len(list) != 1 || list[0].JavaPath != "java" {
		t.Fatalf(`want a single entry whose path is still "java", got %+v`, list)
	}
}

func TestMigrateDeduplicatesAndSkipsKnown(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if _, err := reg.Add(Entry{JavaPath: "/opt/jdk21/bin/java", AddedBy: AddedManual}); err != nil {
		t.Fatal(err)
	}

	added := MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg,
		[]string{"/opt/jdk21/bin/java", "/opt/jdk17/bin/java", "/opt/jdk17/bin/java"})
	if added != 1 {
		t.Fatalf("only the one new path should be registered, got %d", added)
	}
	if got := reg.List(); len(got) != 2 {
		t.Fatalf("registry = %+v", got)
	}
	// The pre-existing entry keeps the label it was added with.
	if got, _ := reg.Get(EntryID("/opt/jdk21/bin/java")); got.AddedBy != AddedManual {
		t.Fatalf("migration must not relabel an existing entry: %+v", got)
	}
}

// A runtime under the runtimes root is already available; registering it again
// would put the same Java in the list twice.
func TestMigrateSkipsManagedRuntimes(t *testing.T) {
	root := t.TempDir()
	launcher := fakeRuntimeDir(t, root, "temurin-21", "21.0.12")
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())

	added := MigrateInstanceJava(context.Background(), NewStore(root), reg, []string{launcher})
	if added != 0 {
		t.Fatalf("a managed runtime needs no registry entry, got %d", added)
	}
}

func TestMigrateIgnoresBlankPaths(t *testing.T) {
	reg := NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())
	if added := MigrateInstanceJava(context.Background(), NewStore(t.TempDir()), reg, []string{"", "   "}); added != 0 {
		t.Fatalf("blank paths are not a java, got %d", added)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/javaruntime/ -run TestMigrate -v`
Expected: FAIL，`undefined: MigrateInstanceJava`。

- [ ] **Step 3: 写实现**

创建 `internal/javaruntime/migrate.go`：

```go
package javaruntime

import (
	"context"
	"strings"
)

// MigrateInstanceJava registers every java path the instances are already
// launching with, so that tightening the instance form to "pick a registered
// one" does not invalidate every instance that existed before the registry.
//
// These paths are not guesses. Each one is what a server on this machine is
// configured to run right now, which is more certain than anything detection
// could tell us — so they are recorded without asking, unlike a java merely
// found on PATH, which the operator confirms before it joins the list.
//
// Returns how many were added. Errors on individual entries are swallowed on
// purpose: this runs during startup, and a registry that ends up one row short
// costs a dropdown entry, while a startup that fails costs every server.
func MigrateInstanceJava(ctx context.Context, store *Store, reg *Registry, javaPaths []string) int {
	if reg == nil {
		return 0
	}

	known := make(map[string]bool)
	if available, err := AvailableList(store, reg); err == nil {
		for _, entry := range available {
			known[entry.JavaPath] = true
		}
	} else {
		for _, entry := range reg.List() {
			known[entry.JavaPath] = true
		}
	}

	added := 0
	for _, path := range javaPaths {
		path = strings.TrimSpace(path)
		if path == "" || known[path] {
			continue
		}
		known[path] = true

		entry := Entry{JavaPath: path, AddedBy: AddedMigrated}
		// A path that will not answer `java -version` is still recorded, with
		// the version left blank. AvailableList marks it unusable, which is a
		// far better thing for the page to show than a version nobody read.
		if probed, ok := probe(ctx, path); ok {
			entry.Vendor, entry.Version, entry.Major = probed.Vendor, probed.Version, probed.Major
		}
		if _, err := reg.Add(entry); err == nil {
			added++
		}
	}
	return added
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/javaruntime/ -v`
Expected: 全部 PASS。

Run: `make lint`
Expected: 无输出，退出码 0。

- [ ] **Step 5: 提交**

```bash
git add internal/javaruntime/migrate.go internal/javaruntime/migrate_test.go
git commit -m "feat(java): 启动时把实例正在用的 Java 登记进来

这些路径不是猜的，是这台机器上的服务器此刻正在用的东西，
比任何探测都确凿——所以不问，直接记。探不出版本的照样记，
版本留空由列表标成失效，沿用 instance/migrate.go 的规矩。"
```

---

### Task 4: 接线 —— Registry 接进 Installer 和 main.go

**Files:**
- Modify: `internal/javaruntime/installer.go:73-90`（`Installer` 结构体、`NewInstaller`、加 `Registry()`）
- Modify: `cmd/hypercraft/main.go:150-155`（构造 Registry 并传入，迁移调用）
- Modify: `internal/javaruntime/installer_test.go`（`NewInstaller` 调用点补参数）
- Modify: `internal/api/server_test.go:113`（testEnv 的 `NewInstaller` 补参数，并加 `allowJava` 辅助）

**Interfaces:**
- Consumes: Task 1 的 `NewRegistry`；Task 3 的 `MigrateInstanceJava`；`config.Paths.JavaRegistryFile()`
- Produces: `func (i *Installer) Registry() *Registry`；测试辅助 `func (e *testEnv) allowJava(path string)`

**设计要点：**

Registry 挂在 `Installer` 上，与 `Store()` 并列。理由写进注释：`api.Server` 用 `s.java == nil` 作为「这个面板不开 Java 管理」的唯一判断（`handlers_java.go` 的 `javaAvailable`），登记表属于同一套功能，挂在同一个对象上就还是一个 nil 检查，不用在每个 handler 里多判一次。

迁移在 main.go 里、实例管理器建好之后调用一次。

- [ ] **Step 1: 改 Installer**

`internal/javaruntime/installer.go`，结构体里 `store` 字段旁边加：

```go
	// registry is the operator's own Java paths. It rides on the installer
	// rather than beside it so that the API keeps one nil check for "this
	// panel does Java management" instead of one per feature.
	registry *Registry
```

`NewInstaller` 签名改成：

```go
func NewInstaller(client *Client, store *Store, registry *Registry, logger *slog.Logger) *Installer {
```

并在构造体里填 `registry: registry`。在 `Store()` 旁边加：

```go
// Registry is the list of Java paths the operator registered by hand.
func (i *Installer) Registry() *Registry { return i.registry }
```

- [ ] **Step 2: 跑测试确认编译失败**

Run: `go build ./... && go test ./internal/javaruntime/ 2>&1 | head -20`
Expected: FAIL，`not enough arguments in call to NewInstaller`，指向 `installer_test.go` 和 `cmd/hypercraft/main.go`。

- [ ] **Step 3: 补上调用点**

`internal/javaruntime/installer_test.go` 里每个 `NewInstaller(...)` 调用补一个参数：`NewRegistry(filepath.Join(t.TempDir(), "java-registry.json"), testLogger())`。若某处没有 `t` 可用，传 `nil` —— `Registry()` 的消费者都做了 nil 判断（见 `AvailableList` 与 `MigrateInstanceJava`）。

`internal/api/server_test.go:113` 的 `Java: javaruntime.NewInstaller(...)` 同样补一个参数，这里要传**真的**登记表（后面三个任务的测试要往里写）：

```go
		Java: javaruntime.NewInstaller(
			javaruntime.NewClient("test", map[string]string{
				javaruntime.DistTemurin: adoptium.URL(),
				javaruntime.DistZulu:    azul.URL(),
			}),
			javaruntime.NewStore(paths.JavaRoot()),
			javaruntime.NewRegistry(paths.JavaRegistryFile(), logger),
			logger,
		),
```

并在 `server_test.go` 的辅助区（`decodeBody` 附近）加：

```go
// allowJava registers a path straight into the registry, skipping the probe
// the API endpoint does.
//
// The fake launchers these tests run are shell scripts that print Minecraft
// log lines; none of them answers `java -version`, so none could be registered
// through the endpoint. What the tests need is for the path to be on the
// whitelist, not for it to be a real JVM.
func (e *testEnv) allowJava(path string) {
	e.t.Helper()
	if _, err := e.api.java.Registry().Add(javaruntime.Entry{
		JavaPath: path, AddedBy: javaruntime.AddedManual,
	}); err != nil {
		e.t.Fatalf("allowJava(%q): %v", path, err)
	}
}
```

`cmd/hypercraft/main.go` 改成：

```go
	// Java runtimes live beside the servers, in the data directory, so a panel
	// that manages its own JDKs stays as movable as one that does not.
	javaRegistry := javaruntime.NewRegistry(paths.JavaRegistryFile(), logger)
	javaInstaller := javaruntime.NewInstaller(
		javaruntime.NewClient(userAgent, nil),
		javaruntime.NewStore(paths.JavaRoot()),
		javaRegistry,
		logger,
	)
	defer javaInstaller.Close()
```

紧接在 `defer javaInstaller.Close()` 之后加：

```go
	// Every instance that existed before the registry is launching with a java
	// path nobody registered. Record them, or tightening the instance form to
	// "pick a registered one" would invalidate the lot on the first upgrade.
	//
	// configs is what st.LoadInstances just read, which is the same list the
	// manager was loaded from — no need to ask it back through a lock.
	javaPaths := make([]string, 0, len(configs))
	for _, cfg := range configs {
		javaPaths = append(javaPaths, cfg.Java)
	}
	// Background rather than the signal context: that one is not built until
	// much further down, and probe caps itself at ten seconds per path.
	if added := javaruntime.MigrateInstanceJava(
		context.Background(), javaInstaller.Store(), javaRegistry, javaPaths); added > 0 {
		logger.Info("registered the java paths instances were already using", "count", added)
	}
```

`configs` 来自 `main.go:122` 的 `st.LoadInstances()`，在这个作用域里直接可用。`context` 若尚未 import 则补上。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./... && go test -race ./...`
Expected: 全部 PASS。

Run: `make lint`
Expected: 无输出。

- [ ] **Step 5: 提交**

```bash
git add internal/javaruntime/installer.go internal/javaruntime/installer_test.go cmd/hypercraft/main.go
git commit -m "feat(java): 登记表接进 Installer，启动时跑一次存量迁移

挂在 Installer 上而不是并排放：api 用 s.java == nil 作为
「这个面板不开 Java 管理」的唯一判断，登记表属于同一套功能。"
```

---

### Task 5: API —— overview 改成合并列表

**Files:**
- Modify: `internal/api/handlers_java.go`（`runtimeView`、`javaOverview.Runtimes`、`handleJavaOverview`、`usersOf`）
- Modify: `internal/api/handlers_java_test.go`（若不存在则 Create）

**Interfaces:**
- Consumes: Task 2 的 `AvailableList`、`Available`、`SourceManaged`、`SourceExternal`
- Produces: `runtimeView` 现在包 `javaruntime.Available`；`usersOf(instances []*instance.Instance, entry javaruntime.Available) []*instance.Instance`

**设计要点：**

JSON 的 key 保持 `runtimes` 不变——前端有四处读它（`LaunchSettings.tsx:198`、`NewInstanceWizard.tsx:260`、`JavaPage.tsx:131`、`Sidebar.tsx:725`），改名只是把一处改动摊成五处。元素形状变了，key 不变。

`usersOf` 现在按 `runtime.Path` 前缀匹配，因为 managed runtime 是一棵目录树。登记条目没有目录树，`Path` 为空，**前缀匹配必须跳过空 Path**，否则 `strings.HasPrefix(candidate, string(filepath.Separator))` 会把每个绝对路径都算成用户。

- [ ] **Step 1: 写失败的测试**

在 `internal/api/handlers_java_test.go` 追加。用仓库现成的测试台（`newTestEnv` / `env.login()` / `env.do` / `env.createInstance`，见 `internal/api/server_test.go:58` 与 `ws_test.go:56`）和 Task 4 加的 `env.allowJava`：

```go
// pointInstanceAt creates an instance and sets its java, whitelisting the path
// first so this helper keeps working once the instance form checks the list.
func (e *testEnv) pointInstanceAt(name, javaPath string) instance.Status {
	e.t.Helper()
	e.allowJava(javaPath)
	created := e.createInstance(name)
	resp := e.do(http.MethodPut, "/api/instances/"+created.ID, instanceRequest{
		Name: created.Name, Directory: created.Directory, Java: javaPath,
	})
	var updated instance.Status
	decodeBody(e.t, resp, &updated)
	if updated.Java != javaPath {
		e.t.Fatalf("instance java is %q, want %q", updated.Java, javaPath)
	}
	return updated
}

func TestUsersOfMatchesExternalEntryByExactPath(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// A registered path has no directory tree, so prefix matching must not
	// fall back to "every absolute path starts with a separator".
	env.pointInstanceAt("match", "/opt/jdk21/bin/java")
	env.pointInstanceAt("other", "/opt/jdk17/bin/java")

	entry := javaruntime.Available{
		JavaPath: "/opt/jdk21/bin/java",
		Source:   javaruntime.SourceExternal,
	}
	users := usersOf(env.mgr.List(), entry)
	if len(users) != 1 {
		t.Fatalf("want exactly the matching instance, got %d", len(users))
	}
	if got := users[0].Config().Java; got != "/opt/jdk21/bin/java" {
		t.Fatalf("matched the wrong instance, java = %q", got)
	}
}

func TestUsersOfIgnoresEntriesWithNoPath(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.pointInstanceAt("other", "/opt/jdk17/bin/java")

	entry := javaruntime.Available{JavaPath: "java", Source: javaruntime.SourceExternal}
	if users := usersOf(env.mgr.List(), entry); len(users) != 0 {
		t.Fatalf("a blank Path must not prefix-match everything, got %d", len(users))
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/api/ -run TestUsersOf -v`
Expected: FAIL，`cannot use entry (variable of type javaruntime.Available) as javaruntime.Runtime`。

- [ ] **Step 3: 写实现**

`internal/api/handlers_java.go`：

```go
// runtimeView is an available Java plus what the panel knows about how it is
// being used, which is what makes deleting one a safe decision.
type runtimeView struct {
	javaruntime.Available
	// UsedBy names the instances whose launch config points at this Java.
	UsedBy []string `json:"usedBy"`
	// Live is true while one of those instances is running on it.
	Live bool `json:"live"`
}
```

`usersOf` 改成：

```go
// usersOf returns the instances launched with this Java.
//
// A managed runtime is a directory tree and an instance may point anywhere
// under it, so that one matches by prefix. A registered path is a single
// launcher with no tree, and its Path is blank — which is exactly why the
// prefix branch is guarded: "" + separator is a prefix of every absolute path
// on the machine.
func usersOf(instances []*instance.Instance, entry javaruntime.Available) []*instance.Instance {
	var users []*instance.Instance
	for _, inst := range instances {
		candidate := inst.Config().Java
		matched := candidate != "" && candidate == entry.JavaPath
		if !matched && entry.Path != "" {
			matched = candidate == entry.Path ||
				strings.HasPrefix(candidate, entry.Path+string(filepath.Separator))
		}
		if matched {
			users = append(users, inst)
		}
	}
	return users
}
```

`handleJavaOverview` 里把

```go
	runtimes, err := s.java.Store().List()
```

换成

```go
	runtimes, err := javaruntime.AvailableList(s.java.Store(), s.java.Registry())
```

循环体里 `runtimeView{Runtime: runtime, ...}` 改成 `runtimeView{Available: runtime, ...}`。

`handleDeleteJava` 里 `usersOf(s.allInstances(), runtime)` 的 `runtime` 来自 `s.java.Store().Get(id)`，类型是 `Runtime` 不是 `Available`。在那里就地包一层：

```go
	entry := javaruntime.Available{
		ID: runtime.ID, JavaPath: runtime.JavaPath, Path: runtime.Path,
		Source: javaruntime.SourceManaged,
	}
	for _, inst := range usersOf(s.allInstances(), entry) {
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/ -v 2>&1 | tail -20`
Expected: 全部 PASS。

Run: `make lint && make test`
Expected: 全部通过。

- [ ] **Step 5: 提交**

```bash
git add internal/api/handlers_java.go internal/api/handlers_java_test.go
git commit -m "feat(api): Java overview 返回合并后的可用列表

usersOf 的前缀分支加了 Path 非空判断：登记条目没有目录树，
Path 为空时 \"\" + 分隔符是这台机器上每个绝对路径的前缀。"
```

---

### Task 6: API —— 登记 / 删除 / 重新检测 三个路由

**Files:**
- Modify: `internal/javaruntime/store.go`（导出 `Probe`）
- Create: `internal/api/handlers_java_registry.go`
- Modify: `internal/api/routes.go:274-278`
- Modify: `internal/api/handlers_java_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Registry`、`Entry`、`ErrDuplicate`、`AddedManual`、`AddedDetected`；Task 5 的 `usersOf`、`runtimeView`
- Produces:
  - `func javaruntime.Probe(ctx context.Context, javaPath string) (SystemJava, bool)`
  - `POST /api/java/registry`、`DELETE /api/java/registry/{id}`、`POST /api/java/registry/{id}/probe`

**设计要点：**

删除规则与 `handleDeleteJava` **完全一致**：正在运行则 409 指名实例；仅仅指向则允许，响应里带受影响的实例名。不给同一个动作编两套心智模型。

- [ ] **Step 1: 写失败的测试**

在 `internal/api/handlers_java_test.go` 追加。先加一个能通过 `probe` 的假 launcher —— `probe` 跑 `<path> -version` 并对 **stdout+stderr 的合并输出**匹配正则 `version "([^"]+)"`（`store.go:261`），所以脚本必须把版本行写到 stderr：

```go
// fakeJavaLauncher writes a script that answers `java -version` the way a real
// JVM does, which is the one thing the register endpoint insists on.
func fakeJavaLauncher(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the launcher is a posix shell script")
	}
	path := filepath.Join(t.TempDir(), "fake-java")
	body := "#!/bin/sh\n" +
		"echo 'openjdk version \"21.0.12\" 2026-07-21' >&2\n" +
		"echo 'OpenJDK Runtime Environment Test-21.0.12+8' >&2\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake java: %v", err)
	}
	return path
}

func TestRegisterJavaRejectsAPathThatIsNotJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/registry",
		registerJavaRequest{Path: filepath.Join(t.TempDir(), "not-java")})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unprobeable path, got %d", resp.StatusCode)
	}
	if got := env.api.java.Registry().List(); len(got) != 0 {
		t.Fatalf("a rejected path must not be written: %+v", got)
	}
}

func TestRegisterJavaRejectsABlankPath(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/java/registry", registerJavaRequest{Path: "   "})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestRegisterJavaRecordsTheProbedVersion(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	launcher := fakeJavaLauncher(t)

	resp := env.do(http.MethodPost, "/api/java/registry", registerJavaRequest{Path: launcher})
	var entry javaruntime.Entry
	decodeBody(t, resp, &entry)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if entry.Major != 21 {
		t.Fatalf("major = %d, want the probed 21 (entry: %+v)", entry.Major, entry)
	}
	if entry.AddedBy != javaruntime.AddedManual {
		t.Fatalf("addedBy = %q, want manual", entry.AddedBy)
	}
}

func TestRegisterJavaRejectsADuplicate(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	launcher := fakeJavaLauncher(t)

	first := env.do(http.MethodPost, "/api/java/registry", registerJavaRequest{Path: launcher})
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first register: %d", first.StatusCode)
	}

	second := env.do(http.MethodPost, "/api/java/registry", registerJavaRequest{Path: launcher})
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("want 409 for a duplicate, got %d", second.StatusCode)
	}
}

func TestUnregisterJavaRefusesWhileAnInstanceRuns(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// A launcher that stays up, so the instance is genuinely running.
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-java.sh")
	body := "#!/bin/sh\nwhile IFS= read -r line; do :; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake java: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatalf("write jar: %v", err)
	}
	env.allowJava(script)

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name: "live-server", Directory: dir, Java: script, Jar: "server.jar",
	})
	var created instance.Status
	decodeBody(t, resp, &created)

	started := env.do(http.MethodPost, "/api/instances/"+created.ID+"/start", nil)
	started.Body.Close()
	t.Cleanup(func() { _ = env.mgr.List()[0].Kill() })
	waitFor(t, func() bool { return env.mgr.List()[0].State().Running() })

	got := env.do(http.MethodDelete, "/api/java/registry/"+javaruntime.EntryID(script), nil)
	defer got.Body.Close()
	if got.StatusCode != http.StatusConflict {
		t.Fatalf("want 409 while an instance runs on it, got %d", got.StatusCode)
	}
	if !strings.Contains(readAll(t, got), "live-server") {
		t.Fatal("the refusal should name the instance")
	}
}

// Same rule as deleting an installed runtime: a stopped user does not block it,
// and the page is told who was pointing at it.
func TestUnregisterJavaAllowsStoppedUsers(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.pointInstanceAt("idle-server", "/opt/jdk21/bin/java")

	got := env.do(http.MethodDelete,
		"/api/java/registry/"+javaruntime.EntryID("/opt/jdk21/bin/java"), nil)
	var out deleteJavaResponse
	decodeBody(t, got, &out)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("a stopped user must not block the delete, got %d", got.StatusCode)
	}
	if len(out.UsedBy) != 1 || out.UsedBy[0] != "idle-server" {
		t.Fatalf("usedBy = %+v, want the instance that pointed at it", out.UsedBy)
	}
	if len(env.api.java.Registry().List()) != 0 {
		t.Fatal("the entry should be gone")
	}
}
```

> **实现者注意：** `readAll`（`server_test.go:428`）和 `waitFor`（`server_test.go:168`）是现成的，直接用。`pointInstanceAt` 来自 Task 5。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/api/ -run 'TestRegisterJava|TestUnregisterJava' -v`
Expected: FAIL，找不到路由（404）或编译不过。

- [ ] **Step 3: 写实现**

`internal/javaruntime/store.go`，在 `probe` 上方加导出包装：

```go
// Probe asks a java binary what version it is. Exported for the API, which
// registers a path only after the path answers.
func Probe(ctx context.Context, javaPath string) (SystemJava, bool) {
	return probe(ctx, javaPath)
}
```

创建 `internal/api/handlers_java_registry.go`：

```go
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/javaruntime"
)

type registerJavaRequest struct {
	Path string `json:"path"`
	// Detected marks a registration the panel proposed off PATH and the
	// operator accepted, as opposed to one they typed. Only a label.
	Detected bool `json:"detected"`
}

// deleteJavaResponse names who was pointing at the entry that just went away,
// so the page can say so rather than leaving it to be discovered at start-up.
type deleteJavaResponse struct {
	UsedBy []string `json:"usedBy"`
}

// handleRegisterJava records a Java the operator pointed the panel at.
//
// The path is probed before it is written: an entry whose version nobody read
// is an entry the launch check cannot reason about, and the whole point of the
// registry is that every option in the dropdown carries a major version.
func (s *Server) handleRegisterJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	var req registerJavaRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		writeError(w, http.StatusBadRequest, "要登记的 Java 路径不能为空")
		return
	}

	probed, ok := javaruntime.Probe(r.Context(), path)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"这个路径问不出 Java 版本：确认它指向 java 可执行文件本身（不是它所在的目录），"+
				"并且面板运行的账号有权执行它。")
		return
	}

	addedBy := javaruntime.AddedManual
	if req.Detected {
		addedBy = javaruntime.AddedDetected
	}
	entry, err := s.java.Registry().Add(javaruntime.Entry{
		JavaPath: path,
		Vendor:   probed.Vendor,
		Version:  probed.Version,
		Major:    probed.Major,
		AddedBy:  addedBy,
	})
	if err != nil {
		if errors.Is(err, javaruntime.ErrDuplicate) {
			writeError(w, http.StatusConflict, "这个 Java 已经登记过了")
			return
		}
		s.writeJavaError(w, err)
		return
	}

	s.log.Info("java registered", "path", entry.JavaPath, "major", entry.Major, "addedBy", entry.AddedBy)
	writeJSON(w, http.StatusCreated, entry)
}

// handleUnregisterJava drops a registered path.
//
// Same rule as deleting an installed runtime (see handleDeleteJava): refused
// while a server is running on it, allowed when the instances pointing at it
// are stopped. Those instances keep launching — the launch path reads the
// config, not this list — and the page is told which ones they were.
func (s *Server) handleUnregisterJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	id := r.PathValue("id")
	entry, err := s.java.Registry().Get(id)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	view := javaruntime.Available{
		ID: entry.ID, JavaPath: entry.JavaPath, Source: javaruntime.SourceExternal,
	}
	// Every instance, not just the visible ones: a server running on this Java
	// breaks whether or not the caller can see it. See allInstances.
	used := []string{}
	for _, inst := range usersOf(s.allInstances(), view) {
		if inst.State().Running() {
			writeError(w, http.StatusConflict,
				"实例「"+inst.Config().Name+"」正在用这个 Java 运行，先停掉它再删除")
			return
		}
		used = append(used, inst.Config().Name)
	}

	if err := s.java.Registry().Remove(id); err != nil {
		s.writeJavaError(w, err)
		return
	}
	s.log.Info("java unregistered", "path", entry.JavaPath, "usedBy", len(used))
	writeJSON(w, http.StatusOK, deleteJavaResponse{UsedBy: used})
}

// handleProbeJava re-reads the version of a registered path, for a JDK that
// was upgraded in place behind the panel's back.
func (s *Server) handleProbeJava(w http.ResponseWriter, r *http.Request) {
	if !s.javaAvailable(w) {
		return
	}

	id := r.PathValue("id")
	entry, err := s.java.Registry().Get(id)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}

	probed, ok := javaruntime.Probe(r.Context(), entry.JavaPath)
	if !ok {
		writeError(w, http.StatusBadRequest,
			"这个路径现在问不出 Java 版本，可能已经被卸载或移动了。")
		return
	}
	entry.Vendor, entry.Version, entry.Major = probed.Vendor, probed.Version, probed.Major

	updated, err := s.java.Registry().Replace(id, entry)
	if err != nil {
		s.writeJavaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
```

`internal/api/routes.go`，在现有 java 路由块里加三行（`DELETE /api/java/{id}` **之前**——`{id}` 会吃掉 `registry`，而 Go 1.22 的 mux 虽然按具体度排序，但把它们写在一起更好读）：

```go
		rt("POST /api/java/registry", s.handleRegisterJava, authz.CapPanelJava),
		rt("DELETE /api/java/registry/{id}", s.handleUnregisterJava, authz.CapPanelJava),
		rt("POST /api/java/registry/{id}/probe", s.handleProbeJava, authz.CapPanelJava),
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/api/ -run 'TestRegisterJava|TestUnregisterJava' -v`
Expected: 全部 PASS。

Run: `make lint && make test`
Expected: 全部通过。

- [ ] **Step 5: 提交**

```bash
git add internal/javaruntime/store.go internal/api/handlers_java_registry.go internal/api/routes.go internal/api/handlers_java_test.go
git commit -m "feat(api): Java 登记 / 删除 / 重新检测三个路由

登记前必须探得出版本——版本读不出来的条目，开服前检查就没法
对它讲道理，而登记表的全部意义就是每个选项都带版本号。
删除规则跟 managed runtime 完全一致：运行中拒绝，已停放行。"
```

---

### Task 7: API —— 实例写入的白名单校验

**Files:**
- Modify: `internal/api/handlers_instances.go`（`handleCreateInstance`、`handleUpdateInstance`）
- Create: `internal/api/handlers_instances_java_test.go`
- Modify: `internal/api/handlers_devices_test.go:141`（会被新校验打挂）
- Modify: `internal/api/ws_test.go:184`（同上）

**Interfaces:**
- Consumes: Task 2 的 `AvailableList`
- Produces: `func (s *Server) javaAllowed(current, next string) (bool, string)`

**设计要点 —— 这是整个一期最容易写错的一处：**

**校验的是改动，不是状态。** 设置页把整份 config 读进表单（`LaunchSettings.tsx:42`）再整份 PUT 回来。如果按「这个实例的 java 必须合法」来判，那么一个 java 不在白名单里的存量实例（迁移时探测失败的，或 managed runtime 被删之后的）**连改名字都做不到**。

服务端拿当前 config 自己比对。**不得**新增 `javaChanged` 之类的请求字段——那是让客户端自证，正是 `handlers_instances.go:203` 注释拒绝的东西。

- [ ] **Step 1: 写失败的测试**

新建 `internal/api/handlers_instances_java_test.go`（同目录已有的 `handlers_instances_fields_test.go` 是另一件事，别混进去）：

```go
package api

import (
	"net/http"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/instance"
)

func TestUpdateInstanceRejectsAnUnregisteredJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("srv")

	resp := env.do(http.MethodPut, "/api/instances/"+created.ID, instanceRequest{
		Name: created.Name, Directory: created.Directory, Java: "/opt/sneaky/bin/java",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unregistered java, got %d", resp.StatusCode)
	}
}

func TestUpdateInstanceAcceptsARegisteredJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	launcher := fakeJavaLauncher(t)

	registered := env.do(http.MethodPost, "/api/java/registry", registerJavaRequest{Path: launcher})
	registered.Body.Close()
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d", registered.StatusCode)
	}

	created := env.createInstance("srv")
	resp := env.do(http.MethodPut, "/api/instances/"+created.ID, instanceRequest{
		Name: created.Name, Directory: created.Directory, Java: launcher,
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if updated.Java != launcher {
		t.Fatalf("java = %q, want the registered launcher", updated.Java)
	}
}

// The whole point of checking the change rather than the state: an instance
// whose java predates the registry must stay editable, or a Java problem would
// block renaming the server.
func TestUpdateInstanceAllowsAnUnchangedLegacyJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// Registered so the instance can be pointed at it, then dropped, which is
	// exactly the state a deleted runtime or a failed migration leaves behind.
	legacy := env.pointInstanceAt("srv", "/opt/legacy/bin/java")
	if err := env.api.java.Registry().Remove(javaruntime.EntryID("/opt/legacy/bin/java")); err != nil {
		t.Fatalf("drop the entry: %v", err)
	}

	resp := env.do(http.MethodPut, "/api/instances/"+legacy.ID, instanceRequest{
		Name: "renamed", Directory: legacy.Directory, Java: "/opt/legacy/bin/java",
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an unchanged java must not block the save, got %d", resp.StatusCode)
	}
	if updated.Name != "renamed" {
		t.Fatalf("the rename should have landed, name = %q", updated.Name)
	}
}

func TestUpdateInstanceIgnoresAnOmittedJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	legacy := env.pointInstanceAt("srv", "/opt/legacy/bin/java")
	if err := env.api.java.Registry().Remove(javaruntime.EntryID("/opt/legacy/bin/java")); err != nil {
		t.Fatalf("drop the entry: %v", err)
	}

	resp := env.do(http.MethodPut, "/api/instances/"+legacy.ID, instanceRequest{
		Name: "renamed", Directory: legacy.Directory,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestCreateInstanceRejectsAnUnregisteredJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name: "new", Directory: t.TempDir(), Java: "/opt/sneaky/bin/java",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// Blank means "I did not choose", which applyDefaults turns into "java". That
// is the historical behaviour and breaking it buys nothing.
func TestCreateInstanceAllowsABlankJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name: "new", Directory: t.TempDir(),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
}
```

`javaruntime` 需要加进这个测试文件的 import。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/api/ -run 'TestUpdateInstance(Rejects|Accepts|Allows|Ignores)|TestCreateInstance(Rejects|Allows)' -v`
Expected: `TestUpdateInstanceRejectsAnUnregisteredJava` 和 `TestCreateInstanceRejectsAnUnregisteredJava` FAIL（返回 200 / 201 而非 400）。

- [ ] **Step 3: 写实现**

`internal/api/handlers_instances.go` 加：

```go
// javaAllowed reports whether an instance may be saved pointing at next.
//
// It checks the CHANGE, not the state. The settings page reads the whole
// config into a form and PUTs the whole thing back, so an instance whose
// java is not in the list — one the startup migration could not probe, or one
// whose runtime was deleted since — would otherwise be unable to have its name
// changed. A Java problem blocking a rename is an absurd failure.
//
// Unlike the capability check above it, "unchanged" here is not a claim the
// client makes: the server is holding the current config and compares it
// itself.
func (s *Server) javaAllowed(current, next string) (bool, string) {
	next = strings.TrimSpace(next)
	// Blank is "I did not choose"; applyDefaults turns it into "java".
	if next == "" || next == strings.TrimSpace(current) {
		return true, ""
	}
	if s.java == nil {
		// A panel with Java management switched off has no list to check
		// against, and refusing every path would make it unusable.
		return true, ""
	}

	available, err := javaruntime.AvailableList(s.java.Store(), s.java.Registry())
	if err != nil {
		s.log.Error("could not read the java list to validate an instance save", "err", err)
		return true, ""
	}
	for _, entry := range available {
		if entry.JavaPath == next {
			return true, ""
		}
	}
	return false, "这个 Java 没有登记在面板里。先到「资源库 → Java 环境」把它装上或登记进来，再回到这里选它。"
}
```

`handleCreateInstance`，在 `s.mgr.Create` 之前插入：

```go
	if ok, reason := s.javaAllowed("", req.Java); !ok {
		writeError(w, http.StatusBadRequest, reason)
		return
	}
```

`handleUpdateInstance`，在 `cfg := req.toConfig()` 之后、`s.mgr.Update` 之前插入：

```go
	// Compared against what is on record, not against what the client says
	// changed. See javaAllowed.
	currentJava := ""
	if current, err := s.mgr.Get(r.PathValue("id")); err == nil {
		currentJava = current.Config().Java
	}
	if ok, reason := s.javaAllowed(currentJava, cfg.Java); !ok {
		writeError(w, http.StatusBadRequest, reason)
		return
	}
```

> **实现者注意：** `handleUpdateInstance` 里已经有一处 `s.mgr.Get(r.PathValue("id"))`（补 `cfg.Kind` 的那段）。把两次查询合成一次，不要连查两遍。

`javaruntime` 需要加进 import。

- [ ] **Step 4: 修好被新校验打挂的两个现有测试**

这一步不是可选的：不做 `make test` 就是红的。两个测试在干净的 testEnv 里建实例时带了没登记过的 java，加上校验之后返回 400。

`internal/api/handlers_devices_test.go:140`，在 `env.bearer(...)` 之前加一行：

```go
	env.allowJava("java")
```

`internal/api/ws_test.go:181`，在 `env.do(http.MethodPost, "/api/instances", ...)` 之前加一行：

```go
	env.allowJava(script)
```

**注意这里为什么用 `allowJava` 而不是走登记接口**：`ws_test.go` 的 `fake-java.sh` 打印的是 Minecraft 日志行，不是 `version "..."`，`probe` 认不出它，走接口必然 400。测试要的是这个路径在白名单上，不是它真的是一个 JVM——`allowJava` 直接写登记表，正是为这件事准备的。

跑一遍确认只有这两处：

Run: `go test ./internal/api/ 2>&1 | grep -E "^\s+--- FAIL|^FAIL"`
Expected: 无输出。若还有别的 FAIL，同样是这个原因，用 `allowJava` 补上。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/api/ -v 2>&1 | tail -20`
Expected: 全部 PASS。

Run: `make lint && make test`
Expected: 全部通过。

- [ ] **Step 6: 提交**

```bash
git add internal/api/handlers_instances.go internal/api/handlers_instances_java_test.go internal/api/handlers_devices_test.go internal/api/ws_test.go
git commit -m "feat(api): 实例的 java 只能改成登记过的

校验的是改动不是状态：设置页整份 PUT 回来，按状态判会让一个
java 不在白名单里的存量实例连改名字都做不到。服务端手里有当前
config，自己比对——不像权限那条，这里不需要听客户端自证。"
```

---

### Task 8: 前端 —— 类型、api.ts、useJava

**Files:**
- Modify: `web/src/types.ts:664-688`（`JavaRuntime` 加 `source` / `valid`）、`:750`（`JavaOverview`）
- Modify: `web/src/api.ts:613-633`
- Modify: `web/src/useJava.ts`

**Interfaces:**
- Consumes: Task 5 / Task 6 的 JSON 形状
- Produces:
  - `JavaRuntime` 多出 `source: 'managed' | 'external'`、`valid: boolean`
  - `api.registerJava(path, detected)`、`api.unregisterJava(id)`、`api.probeJava(id)`
  - `JavaController` 多出 `register`、`unregister`、`reprobe`

- [ ] **Step 1: 改类型**

`web/src/types.ts`，`JavaRuntime` 里加两个字段：

```ts
  /** Where this Java came from: unpacked under the runtimes root, or a path
   *  the operator registered. */
  source: 'managed' | 'external'
  /** False when the launcher is no longer where the entry says it is. The
   *  entry is kept and marked rather than dropped — an instance still points
   *  at it. */
  valid: boolean
```

- [ ] **Step 2: 改 api.ts**

在 `deleteJavaRuntime` 旁边加：

```ts
  registerJava: (path: string, detected = false) =>
    request<JavaRuntime>('POST', '/api/java/registry', { path, detected }),
  unregisterJava: (id: string) =>
    request<{ usedBy: string[] }>('DELETE', `/api/java/registry/${encodeURIComponent(id)}`),
  probeJava: (id: string) =>
    request<JavaRuntime>('POST', `/api/java/registry/${encodeURIComponent(id)}/probe`),
```

> **实现者注意：** 照抄同文件里现有条目的 `request<T>(...)` 调用形状和缩进，包括第三个参数怎么传 body。

- [ ] **Step 3: 改 useJava.ts**

`JavaController` 接口加三个方法，实现照现有 `remove` 的 `act(...)` 包法：

```ts
  register: (path: string, detected?: boolean) => Promise<void>
  unregister: (id: string) => Promise<void>
  reprobe: (id: string) => Promise<void>
```

```ts
  const register = useCallback(
    (path: string, detected = false) =>
      act(async () => {
        await api.registerJava(path, detected)
        await refresh()
      }, '登记失败'),
    [act, refresh],
  )

  const unregister = useCallback(
    (id: string) =>
      act(async () => {
        await api.unregisterJava(id)
        await refresh()
      }, '删除失败'),
    [act, refresh],
  )

  const reprobe = useCallback(
    (id: string) =>
      act(async () => {
        await api.probeJava(id)
        await refresh()
      }, '重新检测失败'),
    [act, refresh],
  )
```

并加进 return 的对象里。

`remove` 已有的 `deleteJavaRuntime` 保留不动——managed runtime 的删除走的还是老路由。

- [ ] **Step 4: 跑构建确认通过**

Run: `npm --prefix web run build`
Expected: 构建成功，无 TS 报错。

- [ ] **Step 5: 提交**

```bash
git add web/src/types.ts web/src/api.ts web/src/useJava.ts
git commit -m "feat(web): Java 登记的类型、请求和 controller 方法"
```

---

### Task 9: 前端 —— Java 环境页

**Files:**
- Modify: `web/src/components/JavaPage.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: Task 8 的 `JavaRuntime.source` / `.valid`、`java.register` / `.unregister` / `.reprobe`、`overview.system`

**动手前：调用 `frontend-design` skill**（`.claude/skills/frontend-design/SKILL.md`），并读 `docs/design-system.md`。

**要实现的行为（不规定排版，排版按 skill 来）：**

1. 列表元素是合并后的 `overview.runtimes`。每条显示：版本（`Java 21 · 21.0.12`）、**来源**（面板安装 / 本机路径）、`usedBy.length > 0` 时显示「N 个实例在用」。
2. `valid === false` 的条目标成「路径已失效」。**只有这一种状态上色**（`docs/design-system.md` 的「只有异常才上色」），正常条目不上色。
3. 新增「添加本机 Java」：一个路径输入 + 提交。成功后列表刷新；失败把后端返回的消息原样显示（后端已经写好了中文的失败原因，不要在前端另编一套）。
4. `overview.system` 存在、且它的 `path` 不等于任何 `runtimes[].javaPath` 时，页面顶部显示一条：「面板在 PATH 上发现了 Java {system.major}，登记进来？」+ 一个按钮，点击调 `java.register(system.path, true)`。**探测归探测，进表要点一下**——不要做成自动登记。
5. `source === 'external'` 的条目：删除走 `java.unregister(id)`，另给一个「重新检测」调 `java.reprobe(id)`。
6. `source === 'managed'` 的条目：删除仍走 `java.remove(id)`（老路由），没有「重新检测」。
7. 删除成功后若响应的 `usedBy` 非空，提示哪些实例原来指向它。

- [ ] **Step 1: 调用 frontend-design skill，读 design-system.md，定排版**

- [ ] **Step 2: 实现上述 1-7**

- [ ] **Step 3: 跑构建**

Run: `npm --prefix web run build`
Expected: 成功。

- [ ] **Step 4: 人工验收**

启动 `npm --prefix web run dev` + `go run ./cmd/hypercraft`，逐项确认：

- 1440 / 1200 / 1024 / 768 / 390 五个宽度无横向溢出、无错位
- 明、暗两种模式都看过
- 登记一个真实路径 → 出现在列表，版本正确
- 登记一个瞎填的路径 → 显示后端返回的失败原因，列表不变
- 重复登记同一个路径 → 提示已登记过
- PATH 上有 java 且未登记 → 顶部提示出现；点了之后提示消失、列表多一条
- 折叠侧栏、打开抽屉两处没被波及

- [ ] **Step 5: 提交**

```bash
git add web/src/components/JavaPage.tsx web/src/styles.css
git commit -m "feat(web): Java 环境页管理登记的本机 Java

PATH 上探到的不自动进表，顶部给一条提示等人点一下——
列表里的每一条都该是人为决定过的。"
```

---

### Task 10: 前端 —— 实例设置去掉自定义路径

**Files:**
- Modify: `web/src/components/LaunchSettings.tsx`（`:60`、`:88-91`、`:196-202`、`:441-444`、`:559-605`）
- Modify: `web/src/styles.css`（如有需要）

**Interfaces:**
- Consumes: Task 8 的 `JavaRuntime.source` / `.valid`

**动手前：调用 `frontend-design` skill。**

**要做的：**

1. 删掉 `CUSTOM_JAVA` 哨兵（`:60`）、`customJava` state（`:91`）、`showCustomJava`（`:444`）、以及自定义路径的输入框分支（`:589-594`）。
2. 删掉 `knownJava` 那一行（`:443`）——它的存在就是为了判断要不要显示自定义框。
3. 下拉选项直接来自 `runtimes`（现在已经是合并列表，`系统 java（PATH）` 如果登记过就自然是其中一条，**不要再硬编码那个选项**）。每条的 `note` 显示来源。
4. **当前 `form.java` 不在 `runtimes` 里时**（存量实例、或登记条目被删了），在选项列表最前面补一条 `{ value: form.java, label: form.java, note: '未登记' }`，让它可选中、可显示。**这条是必须的**：没有它，下拉会显示空白，用户一保存就把 java 改成了列表里的第一项——一次静默的启动配置变更。
5. `valid === false` 的条目 `note` 标「路径已失效」。
6. 底部指路文案（`:599`）改成唯一入口的措辞，例如：「Java 只能在「资源库 → Java 环境」里添加，添加后这里就能选。」

- [ ] **Step 1: 调用 frontend-design skill**

- [ ] **Step 2: 实现上述 1-6**

- [ ] **Step 3: 跑构建**

Run: `npm --prefix web run build`
Expected: 成功，且没有 `CUSTOM_JAVA` / `customJava` 的残留（`grep -n "CUSTOM_JAVA\|customJava" web/src/components/LaunchSettings.tsx` 应无输出）。

- [ ] **Step 4: 人工验收**

- 一个 java 已登记的实例：下拉正常，能切换，能保存
- 一个 java **未**登记的实例（可手改 `<data>/instances.json` 造一个）：下拉显示该路径并标「未登记」，改名字能存下来（这是 Task 7 那条规则的前端对照），把 java 改成别的也能存
- 五个宽度 + 明暗两种模式
- 开着控制台的实例页没被波及

- [ ] **Step 5: 提交**

```bash
git add web/src/components/LaunchSettings.tsx web/src/styles.css
git commit -m "refactor(web): 实例设置的 Java 只能从登记列表里选

当前值不在列表里时补一条「未登记」的可选项：没有它下拉会显示空白，
用户一保存就把 java 换成了列表第一项——一次静默的启动配置变更。"
```

---

### Task 11: 前端 —— 创建向导

**Files:**
- Modify: `web/src/components/NewInstanceWizard.tsx`（`:260`、`:311`、`:384`、`:467`、`:985-986`）
- Modify: `web/src/styles.css`（如有需要）

**Interfaces:**
- Consumes: Task 8 的 `java.register`、`JavaRuntime.source` / `.valid`

**动手前：调用 `frontend-design` skill。**

**要做的：**

1. 删掉 `customJava` 相关的自由输入：`valid.java` 那条（`:384`）改成「选了一个」，提交时（`:467`）直接送选中的 `javaPath`。
2. **列表为空时的出路**——这是全新安装的真实场景，必须处理：
   - `java.overview.system` 存在时，显示一个按钮：「用这个（Java {major}），并登记进面板」。点击 → `await java.register(system.path, true)` → 刷新后自动选中它。**一步完成登记 + 选择**，不要求用户先跳去 Java 环境页再走回来。
   - `system` 不存在时，显示引导去「资源库 → Java 环境」的说明。
3. 选项列表（`:311`）跟 Task 10 一样带来源和失效标记。向导里**不需要**「未登记」那条补丁——新建实例没有历史值。

- [ ] **Step 1: 调用 frontend-design skill**

- [ ] **Step 2: 实现上述 1-3**

- [ ] **Step 3: 跑构建**

Run: `npm --prefix web run build`
Expected: 成功。

Run: `grep -n "customJava\|javaPath.trim()" web/src/components/NewInstanceWizard.tsx`
Expected: 无输出。

- [ ] **Step 4: 人工验收**

- 登记表非空：向导正常选 Java 建实例
- **登记表为空 + PATH 上有 java**：向导给出一键登记按钮；点完直接能往下走，不用离开向导
- **登记表为空 + PATH 上没有 java**：给出去 Java 环境页的说明，不是一个死掉的空下拉
- 五个宽度 + 明暗两种模式

- [ ] **Step 5: 提交**

```bash
git add web/src/components/NewInstanceWizard.tsx web/src/styles.css
git commit -m "feat(web): 向导在登记表为空时能一键登记 PATH 上的 Java

全新安装时登记表必然是空的。让人先跳去 Java 环境页装一个再回来
重走向导，是很差的第一印象。"
```

---

### Task 12: CHANGELOG 与整体验收

**Files:**
- Modify: `CHANGELOG.md`（「未发布」小节）

- [ ] **Step 1: 写 CHANGELOG**

在「未发布」小节下追加（**不要**动小节标题）：

```markdown
- 实例的 Java 环境改为只能从面板登记过的列表里选。「资源库 → Java 环境」
  现在除了下载安装，还能登记本机已有的 Java（填路径，面板探测版本后记下来）；
  PATH 上探到的 Java 会提示登记，确认后才进列表。
- 升级时会自动把各实例正在使用的 Java 路径登记进来，现有实例不受影响。
- 实例设置里的「自定义路径」输入框已移除。
```

- [ ] **Step 2: 跑全量检查**

Run: `make lint`
Expected: 无输出。

Run: `make test`
Expected: 全部 PASS。

Run: `npm --prefix web run build`
Expected: 成功。

Run: `make build`
Expected: 产出 `./hypercraft`。

- [ ] **Step 3: 端到端手工验收**

用一个**干净的 data 目录**跑 `./hypercraft -data /tmp/hc-fresh`：

- 全新安装：Java 环境页为空 + PATH 提示；向导里一键登记后能建实例
- 用一个**有存量实例的 data 目录**（复制一份现有的）跑：启动日志出现 `registered the java paths instances were already using`；Java 环境页里那几条标着来源；每个实例照常启动
- 登记表文件手动写坏（`echo 'x' > <data>/java-registry.json`）后重启：面板正常启动，日志有一条错误，列表里只剩 managed runtime

- [ ] **Step 4: 提交并推送**

```bash
git add CHANGELOG.md
git commit -m "docs: CHANGELOG 记 Java 统一登记"
git push -u origin claude/modest-davinci-e58tca
```

- [ ] **Step 5: 合并回 main**

按 CLAUDE.md 的工作流程：推功能分支 → 切 `main` → `git pull origin main` → 合并 → 推 `main` → 切回功能分支。若 `main` 已前进，先把 `main` 合进功能分支解决冲突，别把冲突带上 `main`。

---

## 二期预告（不在本计划内）

核心变更闸门：`Config.Core`（`CoreRef`）、`applyCore` 的三条硬拦与降级二次确认、换完更新 `Loader` / `GameVersion`、`InstanceCorePicker` 的禁用态，最后补 `java-too-old` 开服前检查。

`java-too-old` 需要一期的 `Available.Major` 和二期的 `CoreRef.MinJava` 同时在场，所以它是二期的最后一步，也是把两件事缝起来的那一针。二期在一期合并后单独出计划。
