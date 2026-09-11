package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/jvmargs"
)

const forgeJVMArgs = `# Xmx and Xms set the maximum and minimum RAM usage, respectively.
# A good default for a modded server is 4GB.
# Uncomment the next line to set it.
# -Xmx4G
`

func (e *testEnv) jvmArgs(id string) jvmArgsResponse {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/instances/"+id+"/jvm-args", nil)
	var out jvmArgsResponse
	decodeBody(e.t, resp, &out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET jvm-args: %d", resp.StatusCode)
	}
	return out
}

// A script-launched server's heap lives in a file, not in the instance config,
// and the panel's memory control has to reach it there or mean nothing.
func TestTheMemorySettingLandsInUserJVMArgs(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("forge")
	env.writeScript(inst, jvmargs.FileName, forgeJVMArgs, 0o644)

	before := env.jvmArgs(inst.ID)
	if !before.Exists || before.MaxMemoryMB != 0 {
		t.Fatalf("stock file read as %+v, want it found with no heap set", before)
	}

	resp := env.do(http.MethodPut, "/api/instances/"+inst.ID+"/jvm-args",
		jvmArgsRequest{MinMemoryMB: 1024, MaxMemoryMB: 6144})
	var after jvmArgsResponse
	decodeBody(t, resp, &after)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT jvm-args: %d", resp.StatusCode)
	}
	if after.MaxMemoryMB != 6144 || after.MinMemoryMB != 1024 {
		t.Errorf("after the write: %+v", after)
	}

	// The file explains itself to whoever opens it next; a save that ate the
	// explanation would leave a worse file than it found.
	data, err := os.ReadFile(filepath.Join(inst.Directory, jvmargs.FileName))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if text := string(data); !strings.Contains(text, "# A good default for a modded server is 4GB.") {
		t.Errorf("the comments were lost:\n%s", text)
	}
}

// The whole reason for the file: an @argfile is expanded in place and the JVM
// lets the last -Xmx win, so the number in the instance config is not the heap
// ceiling, and the charts must stop drawing it as one.
func TestTheReportedHeapCeilingFollowsTheFileNotTheConfig(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("forge")
	env.writeScript(inst, jvmargs.FileName, "-Xmx6G\n", 0o644)

	// A config that says 2048 and a JVM that will never see it.
	resp := env.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:        inst.Name,
		Directory:   inst.Directory,
		MaxMemoryMB: 2048,
		ArgFiles:    []string{jvmargs.FileName},
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT instance: %d", resp.StatusCode)
	}

	if updated.MaxMemoryMB != 2048 {
		t.Errorf("the configured number was not kept: %d", updated.MaxMemoryMB)
	}
	if updated.EffectiveMaxMemoryMB != 6144 {
		t.Errorf("effectiveMaxMemoryMB = %d, want the 6G the JVM will actually get", updated.EffectiveMaxMemoryMB)
	}
}

// No file, no honest answer — and a zero tells the charts to draw no line at
// all rather than one nobody set.
func TestAnArgFileLaunchWithNoArgsFileReportsAnUnknownCeiling(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("forge")

	resp := env.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:        inst.Name,
		Directory:   inst.Directory,
		MaxMemoryMB: 4096,
		ArgFiles:    []string{jvmargs.FileName},
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if updated.EffectiveMaxMemoryMB != 0 {
		t.Errorf("effectiveMaxMemoryMB = %d, want 0 — nobody knows", updated.EffectiveMaxMemoryMB)
	}

	if got := env.jvmArgs(inst.ID); got.Exists {
		t.Errorf("a file that is not there was reported as present: %+v", got)
	}
	// Writing one would not help: only a launcher that passes
	// @user_jvm_args.txt reads it.
	write := env.do(http.MethodPut, "/api/instances/"+inst.ID+"/jvm-args", jvmArgsRequest{MaxMemoryMB: 2048})
	defer write.Body.Close()
	if write.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409 when there is no such file", write.StatusCode)
	}
}

// In jar mode the panel builds -Xmx itself, so the config is the answer.
func TestJarModeStillReportsTheConfiguredCeiling(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("paper")

	resp := env.do(http.MethodPut, "/api/instances/"+inst.ID, instanceRequest{
		Name:        inst.Name,
		Directory:   inst.Directory,
		Jar:         "server.jar",
		MaxMemoryMB: 3072,
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if updated.EffectiveMaxMemoryMB != 3072 {
		t.Errorf("effectiveMaxMemoryMB = %d, want the configured 3072", updated.EffectiveMaxMemoryMB)
	}
}
