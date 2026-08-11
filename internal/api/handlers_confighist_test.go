package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
)

// withConfigHistory turns the module on for one test. It is off in the default
// wiring so that every other test in this package keeps running without a
// background commit landing in its temporary directory.
func withConfigHistory(o *Options) {
	o.ConfigHistory = confighist.New(
		o.Paths.ConfigHistoryRoot(),
		o.Paths.ConfigHistoryFile(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func (e *testEnv) configHistory(id string) configHistoryOverview {
	e.t.Helper()
	resp := e.do(http.MethodGet, "/api/instances/"+id+"/config-history", nil)
	var out configHistoryOverview
	decodeBody(e.t, resp, &out)
	return out
}

func TestConfigHistoryStartsEmptyAndTakesASnapshot(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()
	created := env.createInstance("hist")

	overview := env.configHistory(created.ID)
	if !overview.Available || !overview.Enabled {
		t.Fatalf("module should be available on a fresh instance: %+v", overview)
	}

	// Writing a config file through the file manager and then snapshotting is
	// the shortest path through every layer: rules, gate, commit, timeline.
	resp := env.do(http.MethodPut, "/api/instances/"+created.ID+"/files/content",
		writeFileRequest{Path: "server.properties", Content: "motd=hi\nrcon.password=hunter2\n"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("write file = %d", resp.StatusCode)
	}

	// The save takes its own snapshot off the request, so the timeline gains a
	// row without anybody asking for one.
	waitFor(t, func() bool { return len(env.configHistory(created.ID).Timeline) > 0 })

	overview = env.configHistory(created.ID)
	newest := overview.Timeline[0]
	if newest.Message != "编辑 server.properties" {
		t.Fatalf("newest row = %q", newest.Message)
	}
	if newest.Trigger != confighist.TriggerUser || newest.Author != testUser {
		t.Fatalf("row attribution = %+v", newest)
	}
	if overview.Stats.Commits == 0 || overview.Stats.RepoBytes == 0 {
		t.Fatalf("footer stats = %+v", overview.Stats)
	}
	if len(overview.Pending) != 0 {
		t.Fatalf("nothing should be pending right after a snapshot: %+v", overview.Pending)
	}
}

func TestConfigHistoryDiffMasksCredentials(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()
	created := env.createInstance("hist")
	base := "/api/instances/" + created.ID

	env.do(http.MethodPut, base+"/files/content",
		writeFileRequest{Path: "server.properties", Content: "motd=hi\nrcon.password=hunter2\n"})
	waitFor(t, func() bool { return len(env.configHistory(created.ID).Timeline) > 0 })

	env.do(http.MethodPut, base+"/files/content",
		writeFileRequest{Path: "server.properties", Content: "motd=hi\nrcon.password=hunter3\n"})
	waitFor(t, func() bool { return len(env.configHistory(created.ID).Timeline) > 1 })

	ref := env.configHistory(created.ID).Timeline[0].Ref

	var changes []confighist.FileChange
	decodeBody(t, env.do(http.MethodGet, base+"/config-history/commits/"+ref, nil), &changes)
	if len(changes) != 1 || changes[0].Path != "server.properties" {
		t.Fatalf("changes = %+v", changes)
	}

	var diff confighist.FileDiff
	decodeBody(t, env.do(http.MethodGet,
		base+"/config-history/diff?ref="+ref+"&path=server.properties", nil), &diff)

	found := false
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == confighist.LineAdd && line.Sensitive {
				found = true
				if line.Masked == "" || line.Masked == line.Text {
					t.Errorf("sensitive line was not masked: %+v", line)
				}
			}
		}
	}
	if !found {
		t.Fatal("the changed password never came back marked sensitive")
	}
}

func TestConfigHistoryRestoreThroughTheAPI(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()
	created := env.createInstance("hist")
	base := "/api/instances/" + created.ID

	env.do(http.MethodPut, base+"/files/content",
		writeFileRequest{Path: "server.properties", Content: "motd=good\n"})
	waitFor(t, func() bool { return len(env.configHistory(created.ID).Timeline) > 0 })
	good := env.configHistory(created.ID).Timeline[0].Ref

	env.do(http.MethodPut, base+"/files/content",
		writeFileRequest{Path: "server.properties", Content: "motd=broken\n"})
	waitFor(t, func() bool { return len(env.configHistory(created.ID).Timeline) > 1 })

	var plan confighist.RestorePlan
	decodeBody(t, env.do(http.MethodPost, base+"/config-history/restore/preview",
		restoreRequest{Ref: good, Path: "server.properties"}), &plan)
	if plan.Whole || len(plan.Changes) != 1 {
		t.Fatalf("preview = %+v", plan)
	}

	resp := env.do(http.MethodPost, base+"/config-history/restore",
		restoreRequest{Ref: good, Path: "server.properties"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore = %d", resp.StatusCode)
	}

	var content fileContentResponse
	decodeBody(t, env.do(http.MethodGet,
		base+"/files/content?path=server.properties", nil), &content)
	if content.Content != "motd=good\n" {
		t.Fatalf("file after restore = %q", content.Content)
	}
}

// A history the operator can download whole is a history that can leave the
// machine with every credential on the server in it. There is no such route,
// and this is the test that says so out loud.
func TestConfigHistoryHasNoBulkExport(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()
	created := env.createInstance("hist")
	base := "/api/instances/" + created.ID + "/config-history"

	for _, path := range []string{
		base + "/archive",
		base + "/download",
		base + "/export",
		base + "/repo",
	} {
		resp := env.do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// Two instances on one directory cannot each own a history of it. See §10.
func TestConfigHistoryOffForASharedDirectory(t *testing.T) {
	env := newTestEnv(t, withConfigHistory)
	env.login()

	first := env.createInstance("first")
	resp := env.do(http.MethodPost, "/api/instances",
		instanceRequest{Name: "second", Directory: first.Directory + "/nested"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create nested instance = %d", resp.StatusCode)
	}
	var nested instance.Status
	decodeBody(t, resp, &nested)

	for _, id := range []string{first.ID, nested.ID} {
		overview := env.configHistory(id)
		if overview.Available || overview.Reason == "" {
			t.Errorf("instance %s should have the module switched off: %+v", id, overview)
		}
	}
}
