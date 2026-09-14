package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/instance"
)

func (e *testEnv) launchCheck(id string) launchCheckResponse {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/instances/"+id+"/launch-check", nil)
	var out launchCheckResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET launch-check: %d", resp.StatusCode)
	}
	return out
}

// writeScript drops a file into an instance directory. Named for what it
// mostly carries — a start script the panel reads but never runs.
func (e *testEnv) writeScript(inst instance.Status, name, body string, mode os.FileMode) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(inst.Directory, name), []byte(body), mode); err != nil {
		e.t.Fatalf("write script: %v", err)
	}
}

func (e *testEnv) setArgFiles(inst instance.Status, files ...string) instance.Status {
	e.t.Helper()
	resp := e.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:      inst.Name,
		Directory: inst.Directory,
		Loader:    inst.Loader,
		ArgFiles:  files,
	})
	var out instance.Status
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("PUT instance: %d", resp.StatusCode)
	}
	return out
}

func issueByCode(issues []launchIssue, code string) *launchIssue {
	for i := range issues {
		if issues[i].Code == code {
			return &issues[i]
		}
	}
	return nil
}

// Jar mode has one way to go wrong, and the check has to catch it.
func TestJarModeReportsAMissingJar(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("jarless")

	out := env.launchCheck(inst.ID)
	if out.Mode != "jar" {
		t.Fatalf("mode = %q, want jar", out.Mode)
	}
	if issueByCode(out.Issues, "jar-unset") == nil {
		t.Errorf("an instance with no jar was not flagged: %#v", out.Issues)
	}
}

// The argfile form has its own target to lose, and losing it is not exotic: a
// modpack update rewrites libraries/ and renames the version directory the
// unix_args.txt lives under.
func TestArgFileModeReportsAMissingArgFile(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("forge")
	inst = env.setArgFiles(inst, "user_jvm_args.txt", "libraries/unix_args.txt")

	out := env.launchCheck(inst.ID)
	if out.Mode != "argfile" {
		t.Fatalf("mode = %q, want argfile", out.Mode)
	}
	if issueByCode(out.Issues, "argfile-missing") == nil {
		t.Errorf("a missing argfile was not flagged: %#v", out.Issues)
	}
	// And stops complaining once both are there.
	env.writeScript(inst, "user_jvm_args.txt", "-Xmx4G\n", 0o644)
	if err := os.MkdirAll(filepath.Join(inst.Directory, "libraries"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.writeScript(inst, "libraries/unix_args.txt", "-cp x\n", 0o644)

	if issueByCode(env.launchCheck(inst.ID).Issues, "argfile-missing") != nil {
		t.Errorf("still complaining about files that are there: %#v", env.launchCheck(inst.ID).Issues)
	}
}

// The silent one: no jar, no version_history.json, nothing to detect, and the
// consequence is mods landing in plugins/ rather than an error anyone sees.
func TestAnUnidentifiableServerIsCalledOutBecauseModsWouldGoToTheWrongPlace(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("mystery")
	inst = env.setArgFiles(inst, "user_jvm_args.txt")

	if issueByCode(env.launchCheck(inst.ID).Issues, "unknown-loader") == nil {
		t.Fatalf("an unidentifiable server was not flagged")
	}

	// Recording what it is settles it — that is the entire point of the field.
	resp := env.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:      inst.Name,
		Directory: inst.Directory,
		ArgFiles:  []string{"user_jvm_args.txt"},
		Loader:    "forge",
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if updated.Loader != "forge" {
		t.Fatalf("loader = %q, want forge", updated.Loader)
	}
	if issueByCode(env.launchCheck(inst.ID).Issues, "unknown-loader") != nil {
		t.Errorf("still unidentifiable after being told what it is")
	}
}

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

func (e *testEnv) launchPreview(id string, draft launchPreviewRequest) launchPreviewResponse {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/instances/"+id+"/launch/preview", draft)
	var out launchPreviewResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("POST launch/preview: %d", resp.StatusCode)
	}
	return out
}

// The page sends what it holds; everything it does not own — the console
// encoding, which lives on the other settings page now — comes off the stored
// config. A draft that blanked those would preview a command nobody asked for.
func TestLaunchPreviewOverlaysTheDraftOnTheStoredConfig(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("overlay")

	out := env.launchPreview(inst.ID, launchPreviewRequest{
		Java:        "java",
		Jar:         "paper.jar",
		MinMemoryMB: 2560,
		MaxMemoryMB: 2560,
		JVMArgs:     []string{"-XX:+UseG1GC"},
		ServerArgs:  []string{"--nogui"},
	})

	if out.Mode != "jar" {
		t.Errorf("mode = %q, want jar", out.Mode)
	}
	flat := instance.FlattenSegments(out.Segments)
	for _, want := range []string{"-Xms2560M", "-Xmx2560M", "-XX:+UseG1GC", "-jar", "paper.jar", "--nogui"} {
		if !slices.Contains(flat, want) {
			t.Errorf("preview is missing %q: %q", want, flat)
		}
	}

	// The instance still has the name and directory it had: previewing is not
	// a save, and the draft carries neither.
	var after instance.Status
	decodeBody(t, env.do(http.MethodGet, "/api/instances/"+inst.ID, nil), &after)
	if after.Name != "overlay" {
		t.Errorf("name = %q — a preview must not touch what is stored", after.Name)
	}
	if after.Jar == "paper.jar" {
		t.Errorf("the draft was saved; preview writes nothing")
	}
}

// The claim the page makes is that the command shown is the command run. That
// only holds if the flags the panel adds for its own console are in it.
func TestLaunchPreviewShowsThePanelsOwnInjectedFlags(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("injected")

	out := env.launchPreview(inst.ID, launchPreviewRequest{
		Java: "java",
		Jar:  "paper.jar",
	})

	var panel []string
	for _, seg := range out.Segments {
		if seg.Origin == instance.OriginPanel {
			panel = append(panel, seg.Args...)
		}
	}
	if len(panel) == 0 {
		t.Fatalf("no panel segment: the injected flags would be invisible, and the page claims the command is complete: %#v", out.Segments)
	}
	if !slices.Contains(panel, "-Dfile.encoding=UTF-8") {
		t.Errorf("panel segment = %q, want the encoding flags", panel)
	}
}

// argfile mode does not get -Xms/-Xmx on the command line, so the preview must
// not show them either — see the comment in Config.commandSegments.
func TestLaunchPreviewOmitsTheHeapInArgFileMode(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("forge-preview")

	out := env.launchPreview(inst.ID, launchPreviewRequest{
		Java:        "java",
		ArgFiles:    []string{"user_jvm_args.txt"},
		MinMemoryMB: 2048,
		MaxMemoryMB: 4096,
	})

	if out.Mode != "argfile" {
		t.Fatalf("mode = %q, want argfile", out.Mode)
	}
	for _, seg := range out.Segments {
		if seg.Origin == instance.OriginMemory {
			t.Errorf("argfile preview showed a heap the JVM will not get: %+v", seg)
		}
	}
	if !slices.Contains(instance.FlattenSegments(out.Segments), "@user_jvm_args.txt") {
		t.Errorf("argfile missing: %q", instance.FlattenSegments(out.Segments))
	}
}

// Aikar's flags size the heap up front; a server that starts at 1 GB and grows
// to 2.5 pauses doing it, which is the exact thing those flags were picked to
// avoid. So the check knows how to propose the fix, not just name the problem.
func TestHeapMismatchProposesRaisingXms(t *testing.T) {
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

// The sentence that used to sit under the jar dropdown, now a check: "找到 1 个
// jar" is the reader confirming the panel is looking where they think it is.
func TestJarCountIsReportedAsACheck(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("jarcount")

	if got := issueByCode(env.launchCheck(inst.ID).Issues, "jar-count"); got == nil || got.Level != launchLevelWarn {
		t.Errorf("an empty directory should warn, got %+v", got)
	}

	env.writeScript(inst, "paper.jar", "not really a jar", 0o644)
	got := issueByCode(env.launchCheck(inst.ID).Issues, "jar-count")
	if got == nil || got.Level != launchLevelOK {
		t.Fatalf("a directory with a jar should pass, got %+v", got)
	}
	if !strings.Contains(got.Message, "1 个") {
		t.Errorf("message = %q, want the count in it", got.Message)
	}
}

// Two servers on one port is a boot failure with a misleading message, so the
// panel says it first — even though the port itself is edited on another page.
func TestPortConflictIsReportedAcrossInstances(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	one := env.newTestInstance("port-one")
	two := env.newTestInstance("port-two")

	env.writeScript(one, "server.properties", "server-port=25565\n", 0o644)
	env.writeScript(two, "server.properties", "server-port=25566\n", 0o644)

	if got := issueByCode(env.launchCheck(one.ID).Issues, "port-conflict"); got == nil || got.Level != launchLevelOK {
		t.Errorf("distinct ports should pass, got %+v", got)
	}

	// Move the second one onto the first's port.
	env.writeScript(two, "server.properties", "server-port=25565\n", 0o644)
	got := issueByCode(env.launchCheck(one.ID).Issues, "port-conflict")
	if got == nil || got.Level != launchLevelWarn {
		t.Fatalf("a shared port should warn, got %+v", got)
	}
	if !strings.Contains(got.Message, "port-two") {
		t.Errorf("message = %q, want the other instance named — the reader has to know which one", got.Message)
	}
}

// An instance that has never run has no server.properties, so its port is not
// a fact yet. Guessing 25565 and flagging every pair of fresh instances would
// be noise dressed as a check.
func TestPortIsNotGuessedBeforeTheServerHasEverRun(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("never-run")

	if got := issueByCode(env.launchCheck(inst.ID).Issues, "port-conflict"); got != nil {
		t.Errorf("reported a port nobody has set: %+v", got)
	}
}
