package api

import (
	"net/http"
	"os"
	"path/filepath"
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

// writeScript drops a start script into an instance directory with the mode a
// caller asks for, so the exec-bit tests can arrive at the state an uploaded
// zip leaves behind.
func (e *testEnv) writeScript(inst instance.Status, name, body string, mode os.FileMode) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(inst.Directory, name), []byte(body), mode); err != nil {
		e.t.Fatalf("write script: %v", err)
	}
}

func (e *testEnv) setCommand(inst instance.Status, command ...string) instance.Status {
	e.t.Helper()
	resp := e.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:      inst.Name,
		Directory: inst.Directory,
		Loader:    inst.Loader,
		Command:   command,
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

// The failure this whole endpoint exists for: the server starts, the panel
// loses it, and every symptom points somewhere else.
func TestABackgroundingScriptIsNamedBeforeItIsStarted(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("forge")

	env.writeScript(inst, "run.sh", "#!/bin/sh\nnohup java -jar server.jar &\n", 0o755)
	inst = env.setCommand(inst, "./run.sh")

	out := env.launchCheck(inst.ID)
	if out.Mode != "script" {
		t.Fatalf("mode = %q, want script", out.Mode)
	}
	if out.Script != "run.sh" {
		t.Errorf("script = %q, want the file that was read", out.Script)
	}
	found := issueByCode(out.Issues, "background-nohup")
	if found == nil {
		t.Fatalf("nohup went unmentioned: %#v", out.Issues)
	}
	// The operator has to be able to find the line in their own file.
	if found.Detail != "nohup java -jar server.jar &" || found.Line != 2 {
		t.Errorf("issue does not point at the line: %+v", *found)
	}
}

// A comment is not a command, and && is not a trailing &. Both would be easy
// false positives, and a warning nobody can act on trains people to ignore the
// ones that matter.
func TestTheScanDoesNotCryWolf(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("quiet")

	env.writeScript(inst, "run.sh", `#!/bin/sh
# nohup is not used here, and neither is screen
cd "$(dirname "$0")" && exec java @user_jvm_args.txt -jar server.jar nogui
`, 0o755)
	inst = env.setCommand(inst, "./run.sh")

	for _, issue := range env.launchCheck(inst.ID).Issues {
		switch issue.Code {
		case "background-nohup", "background-screen", "background-amp", "no-exec":
			t.Errorf("false positive: %+v", issue)
		}
	}
}

// A script that never execs still works — the process group and the metrics
// tree both handle the extra shell — so this is advice, not an error.
func TestAScriptWithoutExecIsOnlyAdvice(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("noexec")

	env.writeScript(inst, "run.sh", "#!/bin/sh\nJAVA_HOME=/opt/jdk\njava -jar server.jar\n", 0o755)
	inst = env.setCommand(inst, "./run.sh")

	found := issueByCode(env.launchCheck(inst.ID).Issues, "no-exec")
	if found == nil {
		t.Fatalf("no-exec was not reported")
	}
	if found.Level != launchLevelInfo {
		t.Errorf("level = %q, want info — the script does work", found.Level)
	}
	if found.Line != 3 {
		t.Errorf("line = %d, want the java call and not the assignment above it", found.Line)
	}
}

// The mode an uploaded zip leaves behind, and the one repair the panel offers.
func TestAMissingExecuteBitIsFoundAndFixed(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("unzipped")

	env.writeScript(inst, "run.sh", "#!/bin/sh\nexec java -jar server.jar\n", 0o644)
	inst = env.setCommand(inst, "./run.sh")

	found := issueByCode(env.launchCheck(inst.ID).Issues, "not-executable")
	if found == nil {
		t.Fatalf("a non-executable script was not reported")
	}
	if found.Fix != launchFixChmod {
		t.Errorf("fix = %q, want the panel to offer the repair", found.Fix)
	}

	resp := env.do(http.MethodPost, "/api/instances/"+inst.ID+"/launch-check/fix",
		launchFixRequest{Action: launchFixChmod})
	var after launchCheckResponse
	decodeBody(t, resp, &after)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fix: %d", resp.StatusCode)
	}
	if issueByCode(after.Issues, "not-executable") != nil {
		t.Errorf("still not executable after the fix: %#v", after.Issues)
	}

	info, err := os.Stat(filepath.Join(inst.Directory, "run.sh"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("mode = %v, want the owner able to run it", info.Mode().Perm())
	}
}

// The panel only ever changes permissions inside the instance it belongs to.
func TestTheFixRefusesAnythingOutsideTheInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("hostbin")
	inst = env.setCommand(inst, "/bin/sh", "start.sh")

	resp := env.do(http.MethodPost, "/api/instances/"+inst.ID+"/launch-check/fix",
		launchFixRequest{Action: launchFixChmod})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a path outside the instance", resp.StatusCode)
	}
}

// "run.sh" and "./run.sh" look the same and are not: the first is a PATH
// lookup, and the error it produces says "not found in $PATH" about a file
// sitting right there in the directory.
func TestABareScriptNameIsDiagnosedAsTheMissingDotSlash(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("dotslash")

	env.writeScript(inst, "run.sh", "#!/bin/sh\nexec java -jar server.jar\n", 0o755)
	inst = env.setCommand(inst, "run.sh")

	if issueByCode(env.launchCheck(inst.ID).Issues, "needs-dot-slash") == nil {
		t.Errorf("the missing ./ was not diagnosed: %#v", env.launchCheck(inst.ID).Issues)
	}
}

// "/bin/sh start.sh" puts the interpreter first, so the file to read is the
// second argument.
func TestTheScannedFileIsNotAlwaysTheExecutable(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("interpreted")

	env.writeScript(inst, "start.sh", "#!/bin/sh\nscreen -dmS mc java -jar server.jar\n", 0o644)
	inst = env.setCommand(inst, "/bin/sh", "start.sh")

	out := env.launchCheck(inst.ID)
	if out.Script != "start.sh" {
		t.Fatalf("script = %q, want the script rather than /bin/sh", out.Script)
	}
	if issueByCode(out.Issues, "background-screen") == nil {
		t.Errorf("screen went unmentioned: %#v", out.Issues)
	}
	// /bin/sh is not ours to chmod, so no repair is offered for it.
	if found := issueByCode(out.Issues, "not-executable"); found != nil {
		t.Errorf("offered to chmod something outside the instance: %+v", *found)
	}
}

// The silent one: no jar, no version_history.json, nothing to detect, and the
// consequence is mods landing in plugins/ rather than an error anyone sees.
func TestAnUnidentifiableServerIsCalledOutBecauseModsWouldGoToTheWrongPlace(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("mystery")

	env.writeScript(inst, "run.sh", "#!/bin/sh\nexec java -jar server.jar\n", 0o755)
	inst = env.setCommand(inst, "./run.sh")

	if issueByCode(env.launchCheck(inst.ID).Issues, "unknown-loader") == nil {
		t.Fatalf("an unidentifiable server was not flagged")
	}

	// Recording what it is settles it — that is the entire point of the field.
	resp := env.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:      inst.Name,
		Directory: inst.Directory,
		Command:   []string{"./run.sh"},
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

// Jar mode has one way to go wrong, and the check still has to catch it.
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
