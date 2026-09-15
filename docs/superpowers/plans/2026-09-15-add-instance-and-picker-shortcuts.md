# 添加实例入口合并、Java 文件选择、目录快捷位置自定义 实施方案

> **For agentic workers:** REQUIRED SUB-SKILL: 用 executing-plans 逐条实现（本仓库默认 Inline Execution）。步骤用 `- [ ]` 跟踪。

**Goal:** 一次处理三个功能需求 issue：目录选择器支持用户自定义快捷位置并改名（#5）、添加 Java 时可以浏览目录选文件（#3）、「新建实例」与「导入现有目录」合并为一个「添加实例」入口（#4）。

**Architecture:** 三件事有一个公共的落点 —— `web/src/components/PathPicker.tsx`。#5 在选择器里加一行「收藏 / 管理」的操作，数据落在 `panel.json`（`config.Panel.HostShortcuts`），经 `/api/fs/shortcuts` 三条路由读写，`hostfs.Shortcuts` 负责把内置位置和自定义位置合成一张去重后的列表；#3 给 `PathPicker` 加一个「选文件」模式，Java 页的登记表单多一个「浏览…」按钮；#4 新增一个只做分流的 `AddInstanceDialog`，把概览、侧栏、所有实例三处入口统一成「添加实例」，向导和导入对话框本身不动。

**Tech Stack:** Go 1.2x（`net/http` + `log/slog`，无框架）、React 18 + TypeScript + Vite、单一 `web/src/styles.css`。

**Spec:** GitHub issues Lanscarlos/HyperCraft#3、#4、#5（正文抄录在各任务的「需求」里）。

## Global Constraints

- 代码注释用英文，用户可见文案与 CHANGELOG 用中文；沿用所处文件的语言，不混写。
- 注释解释**为什么**，不解释代码在做什么。
- 样式只能改 `web/src/styles.css`，颜色/圆角/阴影/时长/缓动只取令牌区已有的令牌；本方案不新增令牌。
- 不引入 CSS 框架、组件库、CSS-in-JS，不拆样式文件。
- 页面框只用 `components/Page.tsx`，段头只用 `Section`，空状态只用 `EmptyState`，下拉只用 `Select`，徽标只用 `Badge`。
- 消息面：页面级加载失败用 `.alert`；条件说明用 `<Note tone>`；一次性结果用 `toast()` / `toastError()`。判据：能用 `if (条件)` 渲染的不是消息。
- 一个组件文件默认最多一个 `variant="primary"`；要多于一个必须在 `web/scripts/check-ui.mjs` 的 `PRIMARY_ALLOWED` 里登记并写明理由。
- 任何可能装长文本（路径）的 flex/grid 子项必须有 `min-width: 0`。
- Go 提交前 `gofmt`；后端检查 `make lint && make test`，前端检查 `npm --prefix web run build`（含 `check:ui`）。
- 用户可见的行为变化写进 `CHANGELOG.md` 的「未发布」小节，不动版本号。
- 开发分支：`claude/zen-faraday-81rtlv`。

---

### Task 1: 自定义快捷位置的存储与接口（issue #5 后端）

**需求（#5）：** 服务端通常集中放在某一两个目录下，现版本只有四个内置快捷位置，不在其中的话每次添加实例都要切好几层目录。希望能添加自定义快捷位置并编辑展示名。

**Files:**
- Modify: `internal/config/config.go`（`Panel` 加 `HostShortcuts` 字段，新增 `HostShortcut` 类型）
- Modify: `internal/hostfs/hostfs.go`（`Shortcut` 加 `ID`/`Custom`；新增 `ShortcutID`、`CleanShortcutPath`；`Shortcuts` 保留传入项的 ID 与 Custom）
- Create: `internal/api/handlers_hostfs_shortcuts.go`（三个处理器 + 合并与落盘）
- Modify: `internal/api/handlers_hostfs.go`（`hostShortcuts()` 折进自定义项）
- Modify: `internal/api/routes.go`（三条路由，`authz.CapPanelHostFS`）
- Test: `internal/hostfs/shortcuts_test.go`（新建）
- Test: `internal/api/handlers_hostfs_test.go`（追加）

**Interfaces:**
- Produces:
  - `config.HostShortcut{ID, Label, Path string}`，`config.Panel.HostShortcuts []HostShortcut`（json `hostShortcuts,omitempty`）
  - `hostfs.Shortcut{ID string; Label string; Path string; Custom bool}`（json `id,omitempty` / `label` / `path` / `custom,omitempty`）
  - `hostfs.ShortcutID(path string) string`
  - `hostfs.CleanShortcutPath(path string) (string, error)`（非绝对路径或含 NUL 返回包装了 `ErrInvalidPath` 的错误）
  - `hostfs.Shortcuts(named []Shortcut) []Shortcut`（签名不变，行为：保留 `ID`/`Custom`）
  - HTTP：`POST /api/fs/shortcuts`（body `{label, path}`）、`PUT /api/fs/shortcuts/{id}`（body `{label}`）、`DELETE /api/fs/shortcuts/{id}`，三者都回 `{"shortcuts": [...]}`（`hostShortcutsResponse`）
  - 常量 `maxHostShortcuts = 12`、`maxShortcutLabelRunes = 24`

- [ ] **Step 1: 写失败的 hostfs 测试**

新建 `internal/hostfs/shortcuts_test.go`：

```go
package hostfs

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCleanShortcutPathRejectsRelative(t *testing.T) {
	for _, bad := range []string{"", "  ", "servers", "./servers"} {
		if _, err := CleanShortcutPath(bad); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("CleanShortcutPath(%q) = %v, want ErrInvalidPath", bad, err)
		}
	}
	if _, err := CleanShortcutPath("/opt/mc\x00"); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("a null byte in a path has to be refused")
	}
}

func TestCleanShortcutPathCleans(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix path shapes")
	}
	got, err := CleanShortcutPath("/opt/minecraft/../minecraft/survival/")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Clean("/opt/minecraft/survival"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The id has to follow the path and nothing else: renaming a shortcut must not
// move it, and adding the same path twice must collide rather than make a
// second row saying the same thing.
func TestShortcutIDFollowsPathOnly(t *testing.T) {
	a := ShortcutID("/opt/mc")
	if a != ShortcutID("/opt/mc") {
		t.Fatal("the same path has to give the same id")
	}
	if a == ShortcutID("/opt/mc2") {
		t.Fatal("two paths must not share an id")
	}
}

// A custom shortcut keeps its id and its Custom flag through the merge, which
// is what lets the picker offer to rename that one and not the built-ins.
func TestShortcutsKeepsCustomIdentity(t *testing.T) {
	root := string(filepath.Separator)
	out := Shortcuts([]Shortcut{
		{Label: "面板服务器目录", Path: root},
		{ID: "fav-abc", Label: "我的服务端", Path: filepath.Join(root, "opt", "mc"), Custom: true},
	})

	var found *Shortcut
	for i := range out {
		if out[i].Path == filepath.Join(root, "opt", "mc") {
			found = &out[i]
		}
	}
	if found == nil {
		t.Fatalf("the custom shortcut is missing from %+v", out)
	}
	if found.ID != "fav-abc" || !found.Custom || found.Label != "我的服务端" {
		t.Fatalf("the merge dropped the custom identity: %+v", *found)
	}
	// The built-ins that follow must not come back marked custom.
	for _, entry := range out {
		if entry.Path == root && entry.Custom {
			t.Fatal("a built-in shortcut must not be marked custom")
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/hostfs/ -run 'Shortcut' -v`
Expected: 编译失败 —— `undefined: CleanShortcutPath`、`undefined: ShortcutID`、`unknown field ID in struct literal`。

- [ ] **Step 3: 改 `internal/hostfs/hostfs.go`**

`Shortcut` 换成：

```go
// Shortcut is a starting point offered beside the listing.
type Shortcut struct {
	// ID is set only on the operator's own shortcuts, and is derived from the
	// path — so renaming one does not move it, and re-adding a path it already
	// holds collides instead of making a second row that says the same thing.
	ID    string `json:"id,omitempty"`
	Label string `json:"label"`
	Path  string `json:"path"`
	// Custom marks a shortcut the operator added. The built-in ones are the
	// panel's own directories and the filesystem roots, which are not theirs
	// to rename or remove — so the picker needs to tell them apart.
	Custom bool `json:"custom,omitempty"`
}
```

`Shortcuts` 的 `add` 改成按整个 `Shortcut` 收：

```go
// Shortcuts are the starting points the picker offers: the panel's own
// directories first, since that is where most servers live, then the
// operator's own saved locations, then their home directory and the
// filesystem roots.
//
// Deduplication is by path and first-one-wins, so the caller's order decides
// which label a path is shown under. Custom entries therefore have to come
// after the panel's own two and before home: a shortcut the operator saved on
// top of the servers directory would otherwise rename a built-in, and one
// saved on their home directory would be unremovable.
func Shortcuts(named []Shortcut) []Shortcut {
	out := make([]Shortcut, 0, len(named)+4)
	seen := make(map[string]bool)
	add := func(shortcut Shortcut) {
		if shortcut.Path == "" || seen[shortcut.Path] {
			return
		}
		seen[shortcut.Path] = true
		out = append(out, shortcut)
	}

	for _, shortcut := range named {
		if abs, err := filepath.Abs(shortcut.Path); err == nil {
			shortcut.Path = abs
			add(shortcut)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(Shortcut{Label: "用户主目录", Path: home})
	}
	for _, root := range roots() {
		add(Shortcut{Label: root, Path: root})
	}
	return out
}

// ShortcutID derives a shortcut's id from its path. Stable across a rename,
// which is the only thing about a saved shortcut that can change, and equal for
// two attempts to save the same directory — which is what makes the second one
// a duplicate rather than a second chip.
func ShortcutID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "fav-" + hex.EncodeToString(sum[:6])
}

// CleanShortcutPath is List's path check without the listing: a saved shortcut
// has to be absolute, but it does not have to exist — a NAS that is not
// mounted yet is a directory worth keeping a chip for.
func CleanShortcutPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not an absolute path", ErrInvalidPath, path)
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: path contains a null byte", ErrInvalidPath)
	}
	return filepath.Clean(path), nil
}
```

import 里补 `crypto/sha256` 和 `encoding/hex`。

- [ ] **Step 4: 跑测试确认通过**

Run: `gofmt -l internal/hostfs && go test ./internal/hostfs/ -run 'Shortcut' -v`
Expected: PASS，`gofmt -l` 无输出。

- [ ] **Step 5: 加 `config.HostShortcut`**

`internal/config/config.go`，在 `Panel` 的 `Terminal` 字段之前插入：

```go
	// HostShortcuts are the operator's own starting points for the directory
	// picker, on top of the panel's own directories and the filesystem roots.
	//
	// Minecraft servers on a given machine nearly always live under one or two
	// directories, and those are rarely the panel's own — so without this every
	// instance added costs the same three or four clicks down the same tree.
	//
	// No secrets in here, and nothing that grants reach: the directory field has
	// always accepted any absolute path, so a saved shortcut only spares the
	// operator the walk. That is why the browse capability governs it rather
	// than the settings one — see the routes in routes.go.
	HostShortcuts []HostShortcut `json:"hostShortcuts,omitempty"`
```

`GitHubToken` 类型下方新增：

```go
// HostShortcut is one directory the operator pinned in the path picker.
type HostShortcut struct {
	// ID is derived from the path by hostfs.ShortcutID, so it survives a rename
	// and collides on a re-add of the same directory.
	ID string `json:"id"`
	// Label is the operator's own name for the place — 「我的服务端」, 「备份盘」.
	Label string `json:"label"`
	Path  string `json:"path"`
}
```

不要动 `Defaults()` 和 `ApplyDefaults()`：空列表就是「没收藏过」，nil 与 `[]` 在这里同义。

- [ ] **Step 6: 写失败的接口测试**

`internal/api/handlers_hostfs_test.go` 末尾追加：

```go
func (e *testEnv) shortcuts() []hostfs.Shortcut {
	e.t.Helper()
	return e.browse(e.t.TempDir()).Shortcuts
}

func labelOfShortcut(list []hostfs.Shortcut, path string) (hostfs.Shortcut, bool) {
	for _, entry := range list {
		if entry.Path == path {
			return entry, true
		}
	}
	return hostfs.Shortcut{}, false
}

func TestHostShortcutAddRenameRemove(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	dir := t.TempDir()

	resp := env.do(http.MethodPost, "/api/fs/shortcuts",
		map[string]string{"label": "我的服务端", "path": dir})
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("adding a shortcut: expected 201, got %d", resp.StatusCode)
	}
	var added hostShortcutsResponse
	decodeBody(t, resp, &added)
	saved, ok := labelOfShortcut(added.Shortcuts, dir)
	if !ok || saved.Label != "我的服务端" || !saved.Custom || saved.ID == "" {
		t.Fatalf("unexpected shortcut list %+v", added.Shortcuts)
	}

	// It has to show up in the listing too — that is where the picker reads it.
	if _, ok := labelOfShortcut(env.shortcuts(), dir); !ok {
		t.Fatal("the saved shortcut is missing from a listing")
	}

	// Renaming keeps the id, which is what stops a rename detaching the chip.
	resp = env.do(http.MethodPut, "/api/fs/shortcuts/"+saved.ID,
		map[string]string{"label": "生存服的家"})
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("renaming: expected 200, got %d", resp.StatusCode)
	}
	var renamed hostShortcutsResponse
	decodeBody(t, resp, &renamed)
	after, ok := labelOfShortcut(renamed.Shortcuts, dir)
	if !ok || after.Label != "生存服的家" || after.ID != saved.ID {
		t.Fatalf("rename changed more than the label: %+v", after)
	}

	resp = env.do(http.MethodDelete, "/api/fs/shortcuts/"+saved.ID, nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("removing: expected 200, got %d", resp.StatusCode)
	}
	var removed hostShortcutsResponse
	decodeBody(t, resp, &removed)
	if _, ok := labelOfShortcut(removed.Shortcuts, dir); ok {
		t.Fatalf("the shortcut survived its own deletion: %+v", removed.Shortcuts)
	}
}

func TestHostShortcutRefusesDuplicateAndRelative(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	dir := t.TempDir()
	resp := env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The same directory again would be dropped by the merge's dedup and the
	// operator would see their click do nothing, so it is refused out loud.
	resp = env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusConflict {
		resp.Body.Close()
		t.Fatalf("a duplicate path: expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": "servers"})
	if resp.StatusCode != http.StatusBadRequest {
		resp.Body.Close()
		t.Fatalf("a relative path: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// An empty label is the common case — the operator clicks 收藏 and does not
// type anything — so it falls back to the directory's own name rather than
// leaving a nameless chip.
func TestHostShortcutLabelDefaultsToDirectoryName(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	dir := filepath.Join(t.TempDir(), "survival")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	resp := env.do(http.MethodPost, "/api/fs/shortcuts", map[string]string{"path": dir})
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var added hostShortcutsResponse
	decodeBody(t, resp, &added)
	saved, ok := labelOfShortcut(added.Shortcuts, dir)
	if !ok || saved.Label != "survival" {
		t.Fatalf("expected the directory name as the label, got %+v", added.Shortcuts)
	}
}
```

import 里补 `github.com/lanscarlos/hypercraft/internal/hostfs`。

- [ ] **Step 7: 跑测试确认失败**

Run: `go test ./internal/api/ -run 'HostShortcut' -v`
Expected: 编译失败 —— `undefined: hostShortcutsResponse`。

- [ ] **Step 8: 写处理器**

新建 `internal/api/handlers_hostfs_shortcuts.go`：

```go
package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/hostfs"
)

// maxHostShortcuts caps the saved list. The shortcuts are a row of chips above
// a 280px listing: past a dozen the row is taller than the thing it is meant to
// save you scrolling through.
const maxHostShortcuts = 12

// maxShortcutLabelRunes caps a label. A chip is one line and does not wrap, so
// a long label is one that gets clipped — better to refuse it while the
// operator is still looking at the box they typed it in.
const maxShortcutLabelRunes = 24

type hostShortcutsResponse struct {
	Shortcuts []hostfs.Shortcut `json:"shortcuts"`
}

type hostShortcutRequest struct {
	// Label is the operator's own name for the place. Empty on an add means
	// "call it whatever the directory is called"; empty on a rename means
	// "leave the label alone".
	Label string `json:"label"`
	// Path is only read when adding. A shortcut's id follows its path, so
	// moving one would be a different shortcut — the picker deletes and re-adds
	// instead.
	Path string `json:"path"`
}

// handleAddHostShortcut saves a directory as a starting point in the picker.
//
// The directory is not required to exist: an unmounted NAS is exactly the kind
// of place worth keeping a chip for, and the picker already draws a path that
// does not exist yet without complaining.
func (s *Server) handleAddHostShortcut(w http.ResponseWriter, r *http.Request) {
	var req hostShortcutRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	path, err := hostfs.CleanShortcutPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "快捷位置要填绝对路径")
		return
	}
	label, err := shortcutLabel(req.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if label == "" {
		// filepath.Base of a filesystem root is the separator, which is what
		// the root's own chip is already called.
		if base := filepath.Base(path); base != "" && base != string(filepath.Separator) {
			label = base
		} else {
			label = path
		}
	}

	saved := s.customShortcuts()
	if len(saved) >= maxHostShortcuts {
		writeError(w, http.StatusConflict,
			"快捷位置最多 "+itoa(maxHostShortcuts)+" 个，先删掉一个再加")
		return
	}
	// Dedup is against the merged list, not just the saved one: a chip on a
	// path a built-in already holds would be silently dropped by the merge, and
	// the operator would see their click do nothing.
	if existing, ok := findShortcut(s.hostShortcuts(), path); ok {
		writeError(w, http.StatusConflict,
			"这个目录已经在快捷位置里了，叫「"+existing.Label+"」")
		return
	}

	entry := config.HostShortcut{ID: hostfs.ShortcutID(path), Label: label, Path: path}
	if !s.applyHostShortcuts(w, append(saved, entry)) {
		return
	}
	s.log.Info("host shortcut saved", "shortcut", entry.ID, "label", entry.Label)
	writeJSON(w, http.StatusCreated, hostShortcutsResponse{Shortcuts: s.hostShortcuts()})
}

// handleRenameHostShortcut retitles a saved shortcut.
//
// The label is the only thing about one that can change: the id follows the
// path, so a shortcut that moved would be a different shortcut — the picker
// removes and re-adds for that.
func (s *Server) handleRenameHostShortcut(w http.ResponseWriter, r *http.Request) {
	var req hostShortcutRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	label, err := shortcutLabel(req.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if label == "" {
		writeError(w, http.StatusBadRequest, "快捷位置的名字不能是空的")
		return
	}

	id := r.PathValue("id")
	saved := s.customShortcuts()
	at := indexOfHostShortcut(saved, id)
	if at < 0 {
		writeError(w, http.StatusNotFound, "没有这个快捷位置")
		return
	}
	saved[at].Label = label
	if !s.applyHostShortcuts(w, saved) {
		return
	}
	s.log.Info("host shortcut renamed", "shortcut", id, "label", label)
	writeJSON(w, http.StatusOK, hostShortcutsResponse{Shortcuts: s.hostShortcuts()})
}

// handleRemoveHostShortcut forgets a saved shortcut. Nothing points at one —
// an instance stores its directory, not the chip it was picked through — so
// this cannot orphan anything.
func (s *Server) handleRemoveHostShortcut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	saved := s.customShortcuts()
	at := indexOfHostShortcut(saved, id)
	if at < 0 {
		writeError(w, http.StatusNotFound, "没有这个快捷位置")
		return
	}
	if !s.applyHostShortcuts(w, append(saved[:at], saved[at+1:]...)) {
		return
	}
	s.log.Info("host shortcut removed", "shortcut", id)
	writeJSON(w, http.StatusOK, hostShortcutsResponse{Shortcuts: s.hostShortcuts()})
}

// shortcutLabel trims a label and refuses one too long to fit a chip. An empty
// result means "not given", which each caller reads its own way.
func shortcutLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if len([]rune(label)) > maxShortcutLabelRunes {
		return "", errors.New("快捷位置的名字最多 " + itoa(maxShortcutLabelRunes) + " 个字")
	}
	if strings.ContainsAny(label, "\n\r\t") {
		return "", errors.New("快捷位置的名字里不能有换行")
	}
	return label, nil
}

func findShortcut(list []hostfs.Shortcut, path string) (hostfs.Shortcut, bool) {
	for _, entry := range list {
		if sameDirectory(entry.Path, path) {
			return entry, true
		}
	}
	return hostfs.Shortcut{}, false
}

func indexOfHostShortcut(list []config.HostShortcut, id string) int {
	for i, entry := range list {
		if entry.ID == id {
			return i
		}
	}
	return -1
}

// customShortcuts copies the saved list out from under the lock, so a caller
// that appends to it cannot write into the config the rest of the panel reads.
func (s *Server) customShortcuts() []config.HostShortcut {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return append([]config.HostShortcut(nil), s.panel.HostShortcuts...)
}

// applyHostShortcuts stores the list and writes panel.json.
//
// Same shape as applyGitHubTokens: in memory first so the picker sees the
// change immediately, then to disk, and a failed write is reported as
// "it worked but will not survive a restart" rather than silently.
func (s *Server) applyHostShortcuts(w http.ResponseWriter, list []config.HostShortcut) bool {
	s.panelMu.Lock()
	panel := s.panel
	panel.HostShortcuts = list
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.persistPanel(); err != nil {
		s.log.Error("could not persist the host shortcuts", "err", err)
		writeError(w, http.StatusInternalServerError, "快捷位置已生效，但保存失败，重启面板后会丢失")
		return false
	}
	return true
}
```

如果 `itoa` 在 `internal/api` 里不存在，用 `strconv.Itoa` 并去掉 `itoa` 的引用（先 `grep -rn "func itoa" internal/api` 确认）。

- [ ] **Step 9: 改 `hostShortcuts()`**

`internal/api/handlers_hostfs.go` 末尾那个函数换成：

```go
// hostShortcuts are the starting points the picker offers first: where the
// panel puts servers by default, where it keeps its own data, then whatever
// the operator has pinned. Order matters — see hostfs.Shortcuts, which
// deduplicates by path and keeps the first label it sees.
func (s *Server) hostShortcuts() []hostfs.Shortcut {
	named := []hostfs.Shortcut{
		{Label: "面板服务器目录", Path: s.paths.ServersRoot()},
		{Label: "面板数据目录", Path: s.paths.Root},
	}
	for _, saved := range s.customShortcuts() {
		named = append(named, hostfs.Shortcut{
			ID: saved.ID, Label: saved.Label, Path: saved.Path, Custom: true,
		})
	}
	return hostfs.Shortcuts(named)
}
```

- [ ] **Step 10: 加路由**

`internal/api/routes.go` 里 `GET /api/fs/inspect` 那一行之后插入：

```go
		// The operator's own starting points for that picker. Governed by the
		// browse capability rather than the settings one: the list holds no
		// secrets and buys no reach — the directory field has always taken any
		// absolute path — so somebody allowed to browse the host is exactly
		// who has any use for it.
		rt("POST /api/fs/shortcuts", s.handleAddHostShortcut, authz.CapPanelHostFS),
		rt("PUT /api/fs/shortcuts/{id}", s.handleRenameHostShortcut, authz.CapPanelHostFS),
		rt("DELETE /api/fs/shortcuts/{id}", s.handleRemoveHostShortcut, authz.CapPanelHostFS),
```

- [ ] **Step 11: 跑测试确认通过**

Run: `make lint && go test ./internal/hostfs/ ./internal/api/ ./internal/config/`
Expected: PASS。`routes_test.go` 可能断言路由表，失败就照它的要求补登记。

- [ ] **Step 12: 提交**

```bash
git add internal/config/config.go internal/hostfs internal/api
git commit -m "$(cat <<'MSG'
快捷位置: 允许操作者自己钉目录

内置的四个快捷位置覆盖不到服务端实际的存放位置，于是每次添加实例都要
沿同一棵树点三四层（issue #5）。目录字段本来就接受任意绝对路径，钉一
个目录只是省掉这段路，不多给任何权限——所以归浏览权限管，而不是面板
设置权限。

id 由路径派生：改名不会让它换位置，重复钉同一个目录会撞上而不是多出一
个说同样话的 chip。去重是拿合并后的整张表比的，否则钉在内置位置上的那
一下会被静默丢掉。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ReR9FKdS3pqeALnoySXAQn
MSG
)"
```

---

### Task 2: 选择器里的收藏与改名（issue #5 前端）

**Files:**
- Modify: `web/src/types.ts`（`HostShortcut` 加 `id` / `custom`）
- Modify: `web/src/api.ts`（三个调用）
- Modify: `web/src/components/PathPicker.tsx`（收藏 / 管理）
- Modify: `web/src/styles.css`（`.picker__favs` 一小段）

**Interfaces:**
- Consumes: Task 1 的 `/api/fs/shortcuts` 三条路由与 `{shortcuts}` 响应体。
- Produces:
  - `HostShortcut { label: string; path: string; id?: string; custom?: boolean }`
  - `api.addHostShortcut(path: string, label?: string): Promise<{ shortcuts: HostShortcut[] }>`
  - `api.renameHostShortcut(id: string, label: string): Promise<{ shortcuts: HostShortcut[] }>`
  - `api.removeHostShortcut(id: string): Promise<{ shortcuts: HostShortcut[] }>`

- [ ] **Step 1: 改类型**

`web/src/types.ts` 的 `HostShortcut`：

```ts
export interface HostShortcut {
  label: string
  path: string
  /** Set only on the operator's own shortcuts; derived from the path. */
  id?: string
  /** The operator's own, which is the only kind that can be renamed or removed. */
  custom?: boolean
}
```

- [ ] **Step 2: 加 api 调用**

`web/src/api.ts` 里 `inspectHost` 之后（照该文件既有的写法，先 `grep -n "inspectHost" -A 6 web/src/api.ts` 抄一遍形状），加：

```ts
  addHostShortcut: (path: string, label = '') =>
    request<{ shortcuts: HostShortcut[] }>('/api/fs/shortcuts', {
      method: 'POST',
      body: JSON.stringify({ path, label }),
    }),
  renameHostShortcut: (id: string, label: string) =>
    request<{ shortcuts: HostShortcut[] }>(`/api/fs/shortcuts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify({ label }),
    }),
  removeHostShortcut: (id: string) =>
    request<{ shortcuts: HostShortcut[] }>(`/api/fs/shortcuts/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    }),
```

`HostShortcut` 要加进这个文件的 type import。

- [ ] **Step 3: 选择器里接上**

`web/src/components/PathPicker.tsx`：

1. 新增 state（放在 `typed` 之后）：

```tsx
  // The shortcut row is kept apart from the listing that delivered it: saving
  // one returns the new list, and re-listing the directory just to redraw a
  // chip would flash the whole picker.
  const [shortcuts, setShortcuts] = useState<HostShortcut[]>([])
  // Whether the row is in edit mode. Off by default: renaming is a thing you
  // do once, and a row of text boxes is not what you came here to look at.
  const [managing, setManaging] = useState(false)
  const [savingFav, setSavingFav] = useState(false)
```

2. 列目录成功时同步 `setShortcuts(fetched.shortcuts)`（在 `setListing(fetched)` 旁边）。

3. `current` 之后：

```tsx
  const pinned = shortcuts.find((entry) => entry.custom && entry.path === current)
  const custom = shortcuts.filter((entry) => entry.custom)
```

4. 三个动作（`toastError` 从 `./Toast` 引，照 `grep -n "toastError" web/src/components` 里的用法）：

```tsx
  const pin = async () => {
    setSavingFav(true)
    try {
      const { shortcuts: next } = await api.addHostShortcut(current)
      setShortcuts(next)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '收藏失败')
    } finally {
      setSavingFav(false)
    }
  }

  const unpin = async (id: string) => {
    try {
      const { shortcuts: next } = await api.removeHostShortcut(id)
      setShortcuts(next)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '删除失败')
    }
  }

  // Committed on blur and on Enter rather than per keystroke: every letter
  // typed would otherwise be a write to panel.json.
  const rename = async (id: string, label: string) => {
    const entry = custom.find((item) => item.id === id)
    if (!entry || label.trim() === '' || label.trim() === entry.label) {
      setShortcuts((prev) => [...prev]) // put the box back to the stored label
      return
    }
    try {
      const { shortcuts: next } = await api.renameHostShortcut(id, label.trim())
      setShortcuts(next)
    } catch (err) {
      toastError(err instanceof Error ? err.message : '改名失败')
      setShortcuts((prev) => [...prev])
    }
  }
```

5. 快捷位置那段 `Toolbar` 换成（chips 用 `shortcuts` 而不是 `listing.shortcuts`，并在末尾加两个 `.link`）：

```tsx
        {shortcuts.length > 0 && (
          <Toolbar>
            <span className="toolbar__label">快捷位置</span>
            <div className="toolbar__chips">
              {shortcuts.map((shortcut) => (
                <button
                  key={shortcut.path}
                  type="button"
                  className={`chip${shortcut.path === current ? ' chip--on' : ''}`}
                  onClick={() => setPath(shortcut.path)}
                  title={shortcut.path}
                >
                  {shortcut.label}
                </button>
              ))}
            </div>
            {pinned ? (
              <button className="link" type="button" onClick={() => void unpin(pinned.id!)}>
                取消收藏
              </button>
            ) : (
              <button className="link" type="button" disabled={savingFav} onClick={() => void pin()}>
                收藏这个目录
              </button>
            )}
            {custom.length > 0 && (
              <button className="link" type="button" onClick={() => setManaging((on) => !on)}>
                {managing ? '完成' : '改名'}
              </button>
            )}
          </Toolbar>
        )}

        {managing && custom.length > 0 && (
          <div className="picker__favs">
            {custom.map((entry) => (
              <div className="picker__fav" key={entry.id}>
                <input
                  className="input-slim"
                  defaultValue={entry.label}
                  maxLength={24}
                  aria-label={`${entry.path} 的名字`}
                  onBlur={(event) => void rename(entry.id!, event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      event.preventDefault()
                      event.currentTarget.blur()
                    }
                  }}
                />
                <span className="picker__favpath" title={entry.path}>
                  {entry.path}
                </span>
                <Button type="button" onClick={() => void unpin(entry.id!)}>
                  删除
                </Button>
              </div>
            ))}
          </div>
        )}
```

`defaultValue` 是刻意的：受控输入会在每次 `setShortcuts` 后把光标顶到末尾。`custom` 里的 `id` 一定有值（后端只给自定义项发 id），`!` 只是让 TS 认这一点。

`Toolbar` 若不接受 chips 之外的子元素（先读 `web/src/components/Toolbar.tsx`），就把两个 `.link` 放进 `toolbar__chips` 同级的位置，按 Toolbar 实际的结构调整。

6. import 补 `HostShortcut` 类型和 `toastError`。

- [ ] **Step 4: 加样式**

`web/src/styles.css` 的 `.picker__list .muted { … }` 之后插入：

```css
/* The rename row, shown only while the shortcut row is in edit mode. Two
   intrinsic columns and one that takes the rest: the path is the long unbroken
   string here, so it is the one that gets min-width: 0 and an ellipsis. */
.picker__favs {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.picker__fav {
  display: flex;
  align-items: center;
  gap: 8px;
}

.picker__fav > .input-slim {
  flex: 0 1 180px;
  min-width: 0;
}

.picker__favpath {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-dim);
  font-family: var(--font-mono);
  font-size: 11.5px;
}
```

- [ ] **Step 5: 跑检查**

Run: `npm --prefix web run build`
Expected: `tsc -b` 与 `check:ui` 都过。`.picker__favs` / `.picker__fav` / `.picker__favpath` 三个类名都已在上一步定义，`ruleNoUndefinedClasses` 不会报。

- [ ] **Step 6: 提交**

```bash
git add web/src/types.ts web/src/api.ts web/src/components/PathPicker.tsx web/src/styles.css
git commit -m "$(cat <<'MSG'
选择目录: 快捷位置可以自己加、自己改名

issue #5。chip 行末尾多了「收藏这个目录」，当前目录已经收藏过就变成
「取消收藏」；有自定义位置时再多一个「改名」，展开一组「名字 + 路径 +
删除」的行。

改名走 blur 和回车，不走每次按键——每敲一个字母都是一次 panel.json 的
写入。名字输入框用 defaultValue 而不是受控值：列表每次刷新都会把受控输
入的光标顶到末尾。

chip 行读的是单独一份 shortcuts state 而不是 listing 里那份，这样保存一
个收藏只重画 chip，不用把整个目录重列一遍。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ReR9FKdS3pqeALnoySXAQn
MSG
)"
```

---

### Task 3: 选择器的「选文件」模式与 Java 页的浏览按钮（issue #3）

**需求（#3）：** 添加本机 Java 现在只能手输 java 可执行文件的绝对路径。希望能像添加实例那样「浏览目录并选择文件」。

**Files:**
- Modify: `web/src/components/PathPicker.tsx`（`mode` / `title` / `lead` / `confirmLabel`）
- Modify: `web/src/components/JavaPage.tsx`（登记表单加「浏览…」）
- Modify: `web/src/styles.css`（`.picker__row--on` 一小段）

**Interfaces:**
- Consumes: Task 2 之后的 `PathPicker`。
- Produces: `PathPicker` 的 props 增加 `mode?: 'dir' | 'file'`、`title?: string`、`lead?: React.ReactNode`、`confirmLabel?: string`；`onPick` 在 `mode="file"` 下回的是文件的完整路径。

- [ ] **Step 1: 扩 `PathPicker` 的 props**

```tsx
interface Props {
  /** Where to open. Empty starts at the panel's own servers directory. */
  initialPath: string
  onPick: (path: string) => void
  onCancel: () => void
  /** What comes back: the directory being browsed, or a file clicked in it.
   *  A file picker still browses directories — only the answer differs. */
  mode?: 'dir' | 'file'
  title?: string
  lead?: React.ReactNode
  confirmLabel?: string
}
```

签名改成带默认值：

```tsx
export function PathPicker({
  initialPath,
  onPick,
  onCancel,
  mode = 'dir',
  title = mode === 'file' ? '选择文件' : '选择目录',
  lead,
  confirmLabel = mode === 'file' ? '选择这个文件' : '选择这个目录',
}: Props) {
```

`initialPath` 在文件模式下可能是一个文件的路径（Java 页传的就是输入框里的内容）。目录列表接受不了文件，所以要先退一层：

```tsx
  // In file mode the field holds a file, and listing a file is an error — so
  // the picker opens in the directory holding it and preselects it.
  const [path, setPath] = useState(() =>
    mode === 'file' ? parentDirectoryOf(initialPath) : initialPath,
  )
  const [picked, setPicked] = useState(() => (mode === 'file' ? initialPath.trim() : ''))
```

文件末尾加一个小工具（不要用 `separator`，初始值来得比第一次 listing 早）：

```tsx
/** The directory holding a path, guessed from whichever separator it uses.
 *  Cheap on purpose: it only has to be good enough to open the picker in the
 *  right place, and the operator can navigate from wherever it lands. */
function parentDirectoryOf(path: string): string {
  const trimmed = path.trim()
  const cut = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'))
  return cut > 0 ? trimmed.slice(0, cut) : trimmed
}
```

- [ ] **Step 2: 文件行可点**

`title`、`lead` 接上：

```tsx
        <h2 className="modal__title">{title}</h2>
        {lead && <p className="modal__lead">{lead}</p>}
```

目录切换时清掉选中的文件（`setPath` 的每个调用点都清一次容易漏，所以放进列目录的 effect 里，紧接着 `setListing(fetched)`）：

```tsx
        // A file picked in one directory is not an answer about another.
        setPicked((current) =>
          current !== '' && parentDirectoryOf(current) === fetched.path ? current : '',
        )
```

文件行分两种写法：

```tsx
          {files.map((entry) =>
            mode === 'file' ? (
              <button
                key={entry.path}
                className={`picker__row${entry.path === picked ? ' picker__row--on' : ''}`}
                type="button"
                aria-pressed={entry.path === picked}
                onClick={() => setPicked(entry.path)}
                onDoubleClick={() => onPick(entry.path)}
              >
                <span className="file-icon">📄</span>
                <span className="picker__name">{entry.name}</span>
              </button>
            ) : (
              <div key={entry.path} className="picker__row picker__row--plain">
                <span className="file-icon">📄</span>
                <span className="picker__name">{entry.name}</span>
              </div>
            ),
          )}
```

「这个目录还不存在」那条 `Note` 的文案是给创建实例写的，文件模式下换掉：

```tsx
        {listing && !listing.exists && (
          <Note tone={mode === 'file' ? 'warn' : 'ok'}>
            {mode === 'file'
              ? '这个目录不存在。'
              : '这个目录还不存在，选它会在创建实例时一并建好。'}
          </Note>
        )}
```

底部按钮：

```tsx
          <Button
            variant="primary"
            type="button"
            disabled={mode === 'file' && picked === ''}
            onClick={() => onPick(mode === 'file' ? picked : current)}
          >
            {confirmLabel}
          </Button>
```

`Note` 的 `tone` 取值先用 `grep -n "tone" web/src/components/Note.tsx` 核一遍，没有 `warn` 就用它实际有的那一档。

- [ ] **Step 3: 选中行的样式**

`web/src/styles.css` 的 `.picker__row--plain:hover { … }` 之后插入：

```css
/* The file picked in file mode. The accent edge is what .row--pick uses for
   the same job, so a picked row reads the same way wherever it appears. */
.picker__row--on {
  background: var(--accent-soft);
  box-shadow: inset 2px 0 0 var(--accent);
}
```

`--accent-soft` 与 `--accent` 先确认在令牌区的 light / dark 两块里都有（`grep -n "accent-soft" web/src/styles.css | head`），有就直接用，不新增令牌。

- [ ] **Step 4: Java 页接上**

`web/src/components/JavaPage.tsx` 的 `.java-add` 表单里，`登记` 按钮之前插入一个浏览按钮，并在组件里加 state：

```tsx
  const [browsing, setBrowsing] = useState(false)
```

```tsx
            <Button type="button" onClick={() => setBrowsing(true)}>
              浏览…
            </Button>
            <Button type="submit" disabled={busy || newPath.trim() === ''}>
              登记
            </Button>
```

表单之后（仍在 `adding &&` 的块里）：

```tsx
            {browsing && (
              <PathPicker
                mode="file"
                title="选择 java 可执行文件"
                lead="进到 JDK 的 bin 目录，选里面的 java（Windows 上是 java.exe）。"
                initialPath={newPath.trim()}
                onCancel={() => setBrowsing(false)}
                onPick={(file) => {
                  setNewPath(file)
                  setBrowsing(false)
                }}
              />
            )}
```

登记表单的说明文字保留 —— 选文件之后「填 java 本身而不是目录」这句仍然成立。import 补 `PathPicker`。

后端不用改：`handleRegisterJava` 已经 probe 一次，指错了会回一条说清楚的 400。

- [ ] **Step 5: 跑检查**

Run: `npm --prefix web run build`
Expected: 通过。`JavaPage.tsx` 里 `variant="primary"` 的个数没变（新加的两个按钮都是描边），`rulePrimaryButtons` 不会报。

- [ ] **Step 6: 提交**

```bash
git add web/src/components/PathPicker.tsx web/src/components/JavaPage.tsx web/src/styles.css
git commit -m "$(cat <<'MSG'
Java: 登记本机 Java 可以浏览目录选文件

issue #3。选择器多一个「选文件」模式：照样浏览目录，只是文件行变成可点
的，回的是文件路径而不是所在目录。Java 页的登记表单因此多一个「浏览…」。

文件模式下 initialPath 拿到的是一个文件（输入框里已有的路径），而列一个
文件是错误，所以开局先退到它所在的那一层并预选它。切目录时清掉选中项：
在一个目录里点的文件不是关于另一个目录的答案。

后端没动——handleRegisterJava 本来就会 probe 一次，指错了有现成的说明。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ReR9FKdS3pqeALnoySXAQn
MSG
)"
```

---

### Task 4: 「添加实例」合并入口（issue #4）

**需求（#4）：** 装面板的机器往往已经跑了很久，从现有目录导入才是常态，但现版本「新建实例」优先级更高：概览页和侧栏的按钮都是「新建实例」，要导入必须先进「所有实例」页。改进方案是这三处一并改成「添加实例」，点了之后弹对话框让用户选新建还是导入。

**Files:**
- Create: `web/src/components/AddInstanceDialog.tsx`
- Modify: `web/src/App.tsx`（`showAdd` state，三处 `onCreate` 改指分流对话框）
- Modify: `web/src/components/Dashboard.tsx`（按钮与空状态文案）
- Modify: `web/src/components/Sidebar.tsx`（按钮文案）
- Modify: `web/src/components/InstanceList.tsx`（两个按钮并成一个）
- Modify: `web/src/components/InstanceSettings.tsx:159`（指路文案）

**Interfaces:**
- Produces: `AddInstanceDialog({ onCreate, onImport, onCancel }: { onCreate: () => void; onImport: () => void; onCancel: () => void })`
- 向导（`NewInstanceWizard`，路由 `{ kind: 'new-instance' }`）与 `ImportInstanceDialog` 本身不改，命令面板里的两条直达条目也不改 —— 在面板里是打字直达，没有「点错了才想起来」的问题。

- [ ] **Step 1: 写分流对话框**

新建 `web/src/components/AddInstanceDialog.tsx`：

```tsx
import { Modal } from './Modal'
import { Button } from './Button'

interface Props {
  onCreate: () => void
  onImport: () => void
  onCancel: () => void
}

/**
 * Asks which of the two ways to add an instance this is.
 *
 * The two used to be separate entrances at different ranks: 新建实例 was the
 * button on 概览 and in the sidebar, and 导入现有目录 was reachable only from
 * 所有实例. On a machine that has been running servers for a year that ranking
 * is backwards — adopting is the normal case — and the failure it caused was
 * pressing 新建实例 and only then remembering it was the wrong one.
 *
 * So neither is the default here and neither is filled: this dialog is a fork,
 * not a call to action, and the thing it is buying is the half-second in which
 * you read which one you are about to take.
 */
export function AddInstanceDialog({ onCreate, onImport, onCancel }: Props) {
  return (
    <Modal onClose={onCancel}>
      <div className="modal__card">
        <h2 className="modal__title">添加实例</h2>
        <p className="modal__lead">机器上已经有服务端目录的话，走「导入」，一个文件都不会动。</p>

        <div className="choice-grid choice-grid--wide">
          <button type="button" className="choice" onClick={onImport}>
            <span className="choice__label">导入现有目录</span>
            <span className="choice__note">
              接管这台机器上已有的服务端 —— 手动跑了很久的服，或者从别的面板搬过来的。世界、
              插件、配置原样保留。
            </span>
          </button>
          <button type="button" className="choice" onClick={onCreate}>
            <span className="choice__label">新建实例</span>
            <span className="choice__note">
              从头开一个新服：选核心和 Java，面板下好核心、建好目录、写好配置。
            </span>
          </button>
        </div>

        <div className="modal__actions">
          <Button type="button" onClick={onCancel}>
            取消
          </Button>
        </div>
      </div>
    </Modal>
  )
}
```

导入排在前面是有意的：这条 issue 说的就是它本该是默认那条。

- [ ] **Step 2: App 里接上**

`web/src/App.tsx`：

1. import `AddInstanceDialog`。
2. `showImport` 旁边加 `const [showAdd, setShowAdd] = useState(false)`（照 `grep -n "showImport" web/src/App.tsx` 找到声明处）。
3. 三处传给页面/侧栏的 `onCreate={() => navigate({ kind: 'new-instance' })}` 改成 `onCreate={() => setShowAdd(true)}` —— 具体是侧栏（约 587 行）、Dashboard、InstanceList 以及 InstanceList 的空状态那几处。`InstanceList` 的 `onImport` prop 随按钮一起删掉。
   **不要改**的两处：`NewInstanceWizard` 自己的回调，和命令面板的 `onCreate` / `onImport`（面板里是直达，保持直达）。
4. `ImportInstanceDialog` 的渲染块之前插入：

```tsx
        {showAdd && (
          <AddInstanceDialog
            onCancel={() => setShowAdd(false)}
            onCreate={() => {
              setShowAdd(false)
              navigate({ kind: 'new-instance' })
            }}
            onImport={() => {
              setShowAdd(false)
              setShowImport(true)
            }}
          />
        )}
```

- [ ] **Step 3: 改三处按钮**

`Dashboard.tsx`：按钮文案 `+ 新建实例` → `+ 添加实例`；上方那段注释里的 `新建` 一并改成 `添加`（注释描述的约束没变，只是名字变了）。空状态里的号召文案如果写着「新建实例」，改成「添加实例」（`grep -n "新建" web/src/components/Dashboard.tsx` 扫一遍）。

`Sidebar.tsx`：`title="新建实例"` 与 `<span className="sidebar__name">新建实例</span>` 都改成 `添加实例`。

`InstanceList.tsx`：`actions` 里两个按钮并成一个，注释换掉 —— 原注释解释的是「为什么导入按钮摆在新建旁边」，这个理由已经不存在了：

```tsx
      actions={
        /* One entrance, because the two ways in are not ranked: on a machine
           that has been running servers for a year, adopting one is the normal
           case. Which it is gets asked in the dialog — see AddInstanceDialog. */
        can(CAP.panelCreate) && (
          <Button variant="primary" onClick={onCreate}>
            + 添加实例
          </Button>
        )
      }
```

`onImport` prop 与它的类型声明一并删掉；`grep -n "onImport" web/src/components/InstanceList.tsx` 确认没有残留（空状态里若也用了 `onImport`，改成 `onCreate`）。

`InstanceSettings.tsx:159` 的指路文案改成：「之后可以用「添加实例 → 导入现有目录」再加回来。」

- [ ] **Step 4: 跑检查**

Run: `npm --prefix web run build`
Expected: 通过。`InstanceList.tsx` 从两个按钮变一个，实心按钮数从 1 变 1（原本只有「+ 新建实例」是实心），`AddInstanceDialog.tsx` 一个实心都没有 —— `PRIMARY_ALLOWED` 不用动。若 `check:ui` 报 `.choice-grid` 之类未定义，说明类名拼错了（这几个都是既有类）。

- [ ] **Step 5: 提交**

```bash
git add web/src/components/AddInstanceDialog.tsx web/src/App.tsx web/src/components/Dashboard.tsx web/src/components/Sidebar.tsx web/src/components/InstanceList.tsx web/src/components/InstanceSettings.tsx
git commit -m "$(cat <<'MSG'
添加实例: 新建与导入并成一个入口

issue #4。概览、侧栏、所有实例三处的入口原先按「新建实例」排位，导入现
有目录只在所有实例页有。装面板的机器往往已经跑了很久，那个排位是反的——
造成的失误是点完「新建实例」才想起来点错了。

三处一并改成「添加实例」，点开是一个只做分流的对话框。两条路都不是实心
按钮：这是一个岔路口，不是一个号召，它买的是「读一眼自己要走哪条」的那
半秒。导入排在前面，因为这条 issue 说的就是它本该是默认那条。

命令面板里的两条直达条目没动——在那里是打字直达，不存在点错。

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ReR9FKdS3pqeALnoySXAQn
MSG
)"
```

---

### Task 5: CHANGELOG 与整体验证

**Files:**
- Modify: `CHANGELOG.md`（「未发布 → 新增」三条）

- [ ] **Step 1: 写 CHANGELOG**

`CHANGELOG.md` 的 `## 未发布` → `### 新增` 小节顶部插入三条，沿用该小节既有的「粗体一句结论 + 为什么」的写法：

```markdown
- **「新建实例」和「导入现有目录」并成一个「添加实例」。** 概览、侧栏、所有实例三处入口原先都按
  「新建实例」排位，要导入必须先进「所有实例」页——装面板的机器往往已经跑了很久，那个排位是反的。
  现在点「添加实例」弹一个岔路口，两条路平级，导入排在前面。

- **添加本机 Java 可以浏览目录选文件。** 不用再手抄 java 可执行文件的绝对路径；选择器多了「选
  文件」模式，进 JDK 的 bin 目录点 java 就行。

- **目录选择器的快捷位置可以自己加、自己改名。** 服务端集中放在哪一两个目录下只有机主知道，原先
  四个内置位置覆盖不到的话，每次添加实例都要沿同一棵树点三四层。现在在选择器里「收藏这个目录」，
  chip 行末尾的「改名」能改展示名，最多 12 个，记在 panel.json 里。
```

- [ ] **Step 2: 跑全量检查**

```bash
gofmt -l ./cmd ./internal
make lint && make test
npm --prefix web run build
```
Expected: `gofmt -l` 无输出，三条命令都通过。

- [ ] **Step 3: 人工自查（样式改不了靠类型兜底）**

- 明暗两种模式都看过；本方案没新增令牌，只用了既有的 `--accent` / `--accent-soft` / `--text-dim` / `--font-mono`。
- 1440 / 1200 / 1024 / 768 / 390 宽度下：选择器的 chip 行（收藏满 12 个时）、改名行的长路径、`.choice-grid--wide` 的两张卡 —— 都不横向溢出。
- 折叠侧栏（「添加实例」只剩 `+`）、打开抽屉、开着控制台的实例页三处没被波及。

- [ ] **Step 4: 提交并按仓库流程合回 main**

```bash
git add CHANGELOG.md
git commit -m "$(cat <<'MSG'
文档: 记下三个 issue 的行为变化

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ReR9FKdS3pqeALnoySXAQn
MSG
)"
git push -u origin claude/zen-faraday-81rtlv
git checkout main && git pull origin main
git merge claude/zen-faraday-81rtlv && git push origin main
git checkout claude/zen-faraday-81rtlv
```

---

## Self-Review

**需求覆盖：** #5 → Task 1（存储与接口）+ Task 2（收藏、改名、删除的界面）；#3 → Task 3（选文件模式 + Java 页浏览按钮）；#4 → Task 4（分流对话框 + 三处入口）。三条都进了 CHANGELOG（Task 5）。

**类型一致：** 后端 `hostfs.Shortcut{ID, Label, Path, Custom}` 与前端 `HostShortcut { label, path, id?, custom? }` 字段名对齐（json tag 全小写驼峰）；`config.HostShortcut` 只在后端出现，不过 API。`hostShortcutsResponse.Shortcuts` 与前端三个调用的 `{ shortcuts }` 对齐。`PathPicker` 的 `mode` 在 Task 3 定义、Task 3 使用，Task 2 的改动都在 `mode` 之前生效且不碰这几个 prop。

**已知待核实项**（执行时用 grep 当场确认，别猜）：`internal/api` 里 `itoa` 是否存在；`Note` 的 `tone` 有哪几档；`Toolbar` 能不能接 chips 之外的子元素；`--accent-soft` 是否 light/dark 两块都有；`api.ts` 里 `request` 的确切写法。
