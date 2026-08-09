package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/serverfiles"
)

// upload posts one file through the multipart endpoint.
func (e *testEnv) upload(instanceID, dir, filename string, content []byte) *http.Response {
	e.t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		e.t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		e.t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		e.t.Fatalf("close writer: %v", err)
	}

	target := e.server.URL + "/api/instances/" + instanceID +
		"/files/upload?path=" + url.QueryEscape(dir)
	req, err := http.NewRequest(http.MethodPost, target, &body)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(csrfHeader, "1")

	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	return resp
}

func TestUploadAndDownloadRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("files")

	resp := env.upload(created.ID, "", "paper-1.21.jar", []byte("fake jar bytes"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	onDisk := filepath.Join(created.Directory, "paper-1.21.jar")
	if data, err := os.ReadFile(onDisk); err != nil || string(data) != "fake jar bytes" {
		t.Fatalf("file not written correctly: %v / %q", err, data)
	}

	resp = env.do(http.MethodGet, "/api/instances/"+created.ID+
		"/files/download?path=paper-1.21.jar", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "fake jar bytes" {
		t.Errorf("download returned %q", body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "paper-1.21.jar") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// The uploaded jar should also show up for the launch-settings dropdown,
	// which reads the instance directory through the host browser.
	resp = env.do(http.MethodGet, "/api/fs?path="+url.QueryEscape(created.Directory), nil)
	var listing hostDirResponse
	decodeBody(t, resp, &listing)
	if len(listing.Jars) != 1 || listing.Jars[0].Name != "paper-1.21.jar" {
		t.Errorf("jar listing = %+v", listing.Jars)
	}
}

// A crafted client can put anything in a multipart filename. It must land
// inside the target directory regardless.
func TestUploadFilenameCannotEscape(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("escape")

	for _, name := range []string{"../evil.txt", "../../evil.txt", `..\evil.txt`, "/etc/evil.txt"} {
		resp := env.upload(created.ID, "", name, []byte("nope"))
		resp.Body.Close()

		parent := filepath.Dir(created.Directory)
		if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
			t.Fatalf("upload named %q escaped into the parent directory", name)
		}
	}

	// It should have been stored under its base name instead.
	if _, err := os.Stat(filepath.Join(created.Directory, "evil.txt")); err != nil {
		t.Errorf("expected the upload to land inside the instance: %v", err)
	}
}

func TestUploadRejectsOversizedFiles(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("big")

	// newTestEnv caps uploads at 1 MiB.
	resp := env.upload(created.ID, "", "huge.jar", bytes.Repeat([]byte("x"), (1<<20)+1))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", resp.StatusCode)
	}

	// A rejected upload must not leave a partial file behind.
	if _, err := os.Stat(filepath.Join(created.Directory, "huge.jar")); !os.IsNotExist(err) {
		t.Error("partial upload was left on disk")
	}
}

func TestListEditWriteAndDeleteFiles(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("edit")

	resp := env.upload(created.ID, "", "ops.json", []byte("[]"))
	resp.Body.Close()

	resp = env.do(http.MethodPost, "/api/instances/"+created.ID+"/files/mkdir",
		pathRequest{Path: "plugins"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("mkdir returned %d", resp.StatusCode)
	}

	resp = env.do(http.MethodGet, "/api/instances/"+created.ID+"/files", nil)
	var listing listFilesResponse
	decodeBody(t, resp, &listing)
	if len(listing.Entries) != 2 || !listing.Entries[0].IsDir {
		t.Fatalf("unexpected listing: %+v", listing.Entries)
	}
	if listing.MaxUploadBytes != 1<<20 {
		t.Errorf("maxUploadBytes = %d", listing.MaxUploadBytes)
	}

	// Edit through the text endpoint.
	resp = env.do(http.MethodPut, "/api/instances/"+created.ID+"/files/content",
		writeFileRequest{Path: "ops.json", Content: `[{"name":"玩家"}]`})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("write returned %d", resp.StatusCode)
	}

	resp = env.do(http.MethodGet, "/api/instances/"+created.ID+
		"/files/content?path=ops.json", nil)
	var content fileContentResponse
	decodeBody(t, resp, &content)
	if content.Content != `[{"name":"玩家"}]` {
		t.Errorf("content did not round trip: %q", content.Content)
	}

	// Rename, then delete.
	resp = env.do(http.MethodPost, "/api/instances/"+created.ID+"/files/rename",
		renameRequest{From: "ops.json", To: "plugins/ops.json"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename returned %d", resp.StatusCode)
	}

	resp = env.do(http.MethodDelete, "/api/instances/"+created.ID+
		"/files?path=plugins", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete returned %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(created.Directory, "plugins")); !os.IsNotExist(err) {
		t.Error("directory was not deleted")
	}
}

func TestFileEndpointsRefuseTraversal(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("jail")
	base := "/api/instances/" + created.ID

	cases := []struct {
		name string
		do   func() *http.Response
	}{
		{"list", func() *http.Response {
			return env.do(http.MethodGet, base+"/files?path="+url.QueryEscape("../.."), nil)
		}},
		{"read", func() *http.Response {
			return env.do(http.MethodGet, base+"/files/content?path="+url.QueryEscape("../../../etc/passwd"), nil)
		}},
		{"write", func() *http.Response {
			return env.do(http.MethodPut, base+"/files/content",
				writeFileRequest{Path: "../../pwned", Content: "x"})
		}},
		{"delete", func() *http.Response {
			return env.do(http.MethodDelete, base+"/files?path="+url.QueryEscape("../.."), nil)
		}},
		{"rename", func() *http.Response {
			return env.do(http.MethodPost, base+"/files/rename",
				renameRequest{From: "..", To: "../gone"})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := tc.do()
			defer resp.Body.Close()
			if resp.StatusCode < 400 {
				t.Errorf("traversal via %s returned %d", tc.name, resp.StatusCode)
			}
		})
	}

	// Nothing should have appeared next to the instance directory.
	if _, err := os.Stat(filepath.Join(filepath.Dir(created.Directory), "pwned")); err == nil {
		t.Error("a file escaped the instance directory")
	}
}

func TestReadRefusesOversizedFile(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("bigread")

	big := bytes.Repeat([]byte("x"), int(serverfiles.MaxEditableBytes())+1)
	if err := os.WriteFile(filepath.Join(created.Directory, "latest.log"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	resp := env.do(http.MethodGet, "/api/instances/"+created.ID+
		"/files/content?path=latest.log", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", resp.StatusCode)
	}
}

func TestSystemAndInstanceMetrics(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("metrics")

	resp := env.do(http.MethodGet, "/api/system", nil)
	var system systemResponse
	decodeBody(t, resp, &system)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if system.Host.CPUCores <= 0 {
		t.Errorf("expected a positive core count, got %d", system.Host.CPUCores)
	}
	if system.Host.MemoryTotal == 0 {
		t.Error("expected a non-zero total memory reading")
	}
	if system.Instances.Total != 1 || system.Instances.Running != 0 {
		t.Errorf("instance counts = %+v", system.Instances)
	}

	resp = env.do(http.MethodGet, "/api/instances/"+created.ID+"/metrics", nil)
	var series instanceMetricsResponse
	decodeBody(t, resp, &series)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if series.IntervalSeconds <= 0 {
		t.Errorf("intervalSeconds = %v", series.IntervalSeconds)
	}
	if series.Samples == nil {
		t.Error("samples should be an empty array, not null")
	}
}

// Instances must not be able to read each other's files by ID mix-up.
func TestFileAccessIsScopedToTheInstance(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	first := env.createInstance("alpha")
	second := env.createInstance("beta")

	resp := env.upload(first.ID, "", "secret.txt", []byte("alpha only"))
	resp.Body.Close()

	resp = env.do(http.MethodGet, "/api/instances/"+second.ID+
		"/files/content?path=secret.txt", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 reading another instance's file, got %d", resp.StatusCode)
	}
}
