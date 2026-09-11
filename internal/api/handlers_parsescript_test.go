package api

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func (e *testEnv) parseHostScript(path string) (parseScriptResponse, int) {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/fs/parse-script", parseScriptRequest{Path: path})
	var out parseScriptResponse
	decodeBody(e.t, resp, &out)
	return out, resp.StatusCode
}

// hostScript writes one script into a fresh directory and returns its absolute
// path, which is what the import dialog has to work with: no instance exists
// yet, so there is nothing to resolve a relative path against.
func hostScript(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParsingAHostScriptDraftsTheLaunchSettings(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	got, status := env.parseHostScript(hostScript(t, "启动.sh",
		"#!/bin/sh\njava -Xms2G -Xmx8G -XX:+UseG1GC -jar paper-1.20.4.jar --nogui\n"))
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !got.OK {
		t.Fatalf("refused a script it should have read: %+v", got.Refusals)
	}
	if got.Draft.Jar != "paper-1.20.4.jar" {
		t.Errorf("Jar = %q", got.Draft.Jar)
	}
	if got.Draft.MinMemoryMB != 2048 || got.Draft.MaxMemoryMB != 8192 {
		t.Errorf("memory = %d/%d", got.Draft.MinMemoryMB, got.Draft.MaxMemoryMB)
	}
	if !slices.Equal(got.Draft.JVMArgs, []string{"-XX:+UseG1GC"}) {
		t.Errorf("JVMArgs = %q", got.Draft.JVMArgs)
	}
	if !slices.Equal(got.Draft.ServerArgs, []string{"--nogui"}) {
		t.Errorf("ServerArgs = %q", got.Draft.ServerArgs)
	}
}

func TestParsingAForgeRunScriptDraftsArgFiles(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	got, _ := env.parseHostScript(hostScript(t, "run.sh",
		"#!/usr/bin/env sh\njava @user_jvm_args.txt @libraries/unix_args.txt \"$@\"\n"))
	if !got.OK {
		t.Fatalf("refused: %+v", got.Refusals)
	}
	if !slices.Equal(got.Draft.ArgFiles, []string{"user_jvm_args.txt", "libraries/unix_args.txt"}) {
		t.Errorf("ArgFiles = %q", got.Draft.ArgFiles)
	}
	if got.Draft.Jar != "" {
		t.Errorf("Jar = %q, want empty", got.Draft.Jar)
	}
}

// A refusal is a 200 with a reason, not an error: the dialog has something to
// show either way, and the operator needs to read why.
func TestARefusalComesBackWithItsReason(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	got, status := env.parseHostScript(hostScript(t, "start.sh",
		"#!/bin/sh\nwhile true; do\n  java -Xmx4G -jar s.jar\n  sleep 5\ndone\n"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: a refusal is an answer", status)
	}
	if got.OK {
		t.Fatal("a restart loop was accepted")
	}
	if len(got.Refusals) == 0 || got.Refusals[0].Code != "wrapped-in-loop" {
		t.Fatalf("refusals = %+v", got.Refusals)
	}
	if got.Refusals[0].Line != 3 || got.Refusals[0].Reason == "" {
		t.Errorf("refusal = %+v, want the line and a reason the operator can act on", got.Refusals[0])
	}
}

// The panel reads these scripts; it does not run them. So the wrappers it had
// to strip are reported rather than quietly dropped — "your server will no
// longer be inside screen" is news to whoever set it up that way.
func TestStrippedWrappersAreReported(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	got, _ := env.parseHostScript(hostScript(t, "start.sh",
		"#!/bin/sh\nscreen -dmS mc java -Xmx4G -jar s.jar\n"))
	if !got.OK {
		t.Fatalf("refused: %+v", got.Refusals)
	}
	if !slices.Contains(got.Draft.Wrappers, "screen") {
		t.Errorf("Wrappers = %q, want screen named", got.Draft.Wrappers)
	}
}

func TestABatchScriptIsReadAsBatch(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	got, _ := env.parseHostScript(hostScript(t, "启动.bat",
		"@echo off\r\nset MEM=6G\r\njava -Xmx%MEM% -jar server.jar nogui\r\npause\r\n"))
	if !got.OK {
		t.Fatalf("refused: %+v", got.Refusals)
	}
	if got.Draft.MaxMemoryMB != 6144 {
		t.Errorf("MaxMemoryMB = %d, want the %%MEM%% expansion", got.Draft.MaxMemoryMB)
	}
}

func TestAHostPathMustBeAbsolute(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	if _, status := env.parseHostScript("run.sh"); status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func TestAMissingHostScriptIsReported(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	_, status := env.parseHostScript(filepath.Join(t.TempDir(), "not-there.sh"))
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

func TestParsingAScriptInsideAnInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("survival")
	env.writeScript(inst, "启动.sh", "#!/bin/sh\nexec java -Xmx4G -jar paper.jar --nogui\n", 0o755)

	resp := env.do(http.MethodPost, "/api/instances/"+inst.ID+"/parse-script",
		parseScriptRequest{Path: "启动.sh"})
	var got parseScriptResponse
	decodeBody(t, resp, &got)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !got.OK || got.Draft.Jar != "paper.jar" {
		t.Fatalf("got %+v %+v", got.Draft, got.Refusals)
	}
	if !slices.Contains(got.Draft.Wrappers, "exec") {
		t.Errorf("Wrappers = %q", got.Draft.Wrappers)
	}
}

// The instance route reads inside one instance and nowhere else, the same rule
// the file manager follows.
func TestAnInstanceScriptPathCannotEscape(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	inst := env.newTestInstance("survival")

	for _, bad := range []string{"../../etc/passwd", "/etc/passwd"} {
		resp := env.do(http.MethodPost, "/api/instances/"+inst.ID+"/parse-script",
			parseScriptRequest{Path: bad})
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%q was read", bad)
		}
	}
}
