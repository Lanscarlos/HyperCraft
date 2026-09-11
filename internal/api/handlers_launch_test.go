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
