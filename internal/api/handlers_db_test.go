package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseOverviewIsUsableWhenNothingIsInstalled(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	var overview struct {
		Root     string `json:"root"`
		Platform struct {
			OS      string `json:"os"`
			Warning string `json:"warning"`
		} `json:"platform"`
		Engines []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DefaultPort int    `json:"defaultPort"`
		} `json:"engines"`
		Installs []any `json:"installs"`
		Services []any `json:"services"`
	}
	decodeBody(t, env.do(http.MethodGet, "/api/databases", nil), &overview)

	// The three engines are a property of the build, not of what is installed,
	// so the page can offer them before anything has been downloaded.
	if len(overview.Engines) != 3 {
		t.Fatalf("got %d engines, want 3: %+v", len(overview.Engines), overview.Engines)
	}
	seen := map[string]bool{}
	for _, engine := range overview.Engines {
		if engine.Name == "" || engine.DefaultPort == 0 {
			t.Errorf("engine %s is not fully described: %+v", engine.ID, engine)
		}
		seen[engine.ID] = true
	}
	for _, want := range []string{"mysql", "postgresql", "mongodb"} {
		if !seen[want] {
			t.Errorf("%s is missing from the engine list", want)
		}
	}
	// Empty rather than null: the page maps over both without a guard.
	if overview.Installs == nil || overview.Services == nil {
		t.Error("installs and services should be empty arrays, not null")
	}
}

func TestDatabaseRoutesNeedASession(t *testing.T) {
	env := newTestEnv(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/databases"},
		{http.MethodPost, "/api/databases/engines/install"},
		{http.MethodPost, "/api/databases/services"},
		{http.MethodDelete, "/api/databases/services/mysql"},
	} {
		resp := env.do(tc.method, tc.path, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestCreateDatabaseRejectsAnUnknownEngineInstall(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/databases/services", map[string]any{
		"installId": "mysql-9.9.9",
		"database":  "survival",
		"user":      "hypercraft",
		"password":  "long-enough-password",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.StatusCode)
	}
}

// The names go into generated SQL and into a directory name, so the API has to
// refuse the ones the engine layer would not know how to quote.
func TestCreateDatabaseRejectsUnsafeNames(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// An install has to exist for validation to be reached at all; a directory
	// with a fake server binary in it is enough for the store to list it.
	root := env.paths.DatabaseEnginesRoot()
	binDir := filepath.Join(root, "mysql-8.0.45", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "mysqld"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake mysqld: %v", err)
	}

	for _, bad := range []map[string]any{
		{"database": "drop; --", "user": "hypercraft", "password": "long-enough-pass"},
		{"database": "survival", "user": "a'b", "password": "long-enough-pass"},
		{"database": "survival", "user": "hypercraft", "password": "short"},
		{"database": "survival", "user": "hypercraft", "password": "has'a'quote1"},
	} {
		bad["installId"] = "mysql-8.0.45"
		resp := env.do(http.MethodPost, "/api/databases/services", bad)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%v: got %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestDeleteUnknownDatabaseIs404(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodDelete, "/api/databases/services/nope", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.StatusCode)
	}
}

func TestInstallDatabaseRejectsAnUnknownEngine(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/databases/engines/install", map[string]any{
		"engine": "mariadb", "version": "11.4.0",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}
