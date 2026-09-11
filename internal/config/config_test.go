package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A panel upgraded from the days of one token has its credential in the old
// field and a library full of plugins that name no token at all. Those plugins
// resolve to the head of the list, so that is where the old token has to land.
func TestTheSingleTokenBecomesTheDefaultOfTheList(t *testing.T) {
	panel := Panel{GitHubToken: "ghp_old"}
	panel.ApplyDefaults()

	if len(panel.GitHubTokens) != 1 {
		t.Fatalf("expected the token to be migrated: %+v", panel.GitHubTokens)
	}
	if panel.GitHubTokens[0].Token != "ghp_old" || panel.GitHubTokens[0].ID != LegacyTokenID {
		t.Fatalf("unexpected migrated token: %+v", panel.GitHubTokens[0])
	}
	// Emptied, so reading and writing the config again cannot produce a second
	// copy of the same credential.
	if panel.GitHubToken != "" {
		t.Errorf("the old field should have been cleared: %q", panel.GitHubToken)
	}

	// Idempotent: a config that still carries the old field beside a list it has
	// already been folded into gains nothing on the next load.
	again := Panel{GitHubToken: "ghp_old", GitHubTokens: panel.GitHubTokens}
	again.ApplyDefaults()
	if len(again.GitHubTokens) != 1 {
		t.Fatalf("the migration ran twice: %+v", again.GitHubTokens)
	}
}

func TestApplyDefaultsLeavesAnExistingTokenListAlone(t *testing.T) {
	panel := Panel{GitHubTokens: []GitHubToken{
		{ID: "a", Name: "我的私库", Token: "ghp_a"},
		{ID: "b", Name: "公司 org", Token: "ghp_b"},
	}}
	panel.ApplyDefaults()

	if len(panel.GitHubTokens) != 2 || panel.GitHubTokens[0].ID != "a" {
		t.Fatalf("the stored order is the default order: %+v", panel.GitHubTokens)
	}
}

// A downgrade is the one direction that loses data: an older build does not
// know this build's fields, and writing the file back drops every one it
// cannot see. The backup is what makes coming back up possible, so it has to
// be a byte-for-byte copy taken before the old binary ever runs.
func TestBackupStateCopiesTheStateFiles(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(root)
	if err := os.WriteFile(paths.PanelFile(), []byte(`{"listen":":19190"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.UsersFile(), []byte(`{"users":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// instances.json is deliberately absent: a panel that has never had an
	// instance has no such file, and that is not a reason to refuse a backup.

	dir, err := BackupState(paths, "0.5.0", "0.4.0", 3)
	if err != nil {
		t.Fatalf("BackupState: %v", err)
	}

	name := filepath.Base(dir)
	if !strings.HasPrefix(name, "rollback-") || !strings.HasSuffix(name, "-0.5.0-to-0.4.0") {
		t.Errorf("backup directory named %q, want rollback-<时间戳>-0.5.0-to-0.4.0", name)
	}
	for _, f := range []string{"panel.json", "users.json"} {
		got, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("%s was not backed up: %v", f, err)
		}
		want, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s backed up as %q, want %q", f, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "instances.json")); !os.IsNotExist(err) {
		t.Errorf("a file that does not exist was backed up anyway: %v", err)
	}
}

// Backups accumulate one per downgrade and nobody prunes them by hand.
func TestBackupStateKeepsOnlyTheNewest(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(root)
	if err := os.WriteFile(paths.PanelFile(), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var made []string
	for i := 0; i < 5; i++ {
		dir, err := BackupState(paths, "0.5.0", "0.4.0", 3)
		if err != nil {
			t.Fatalf("BackupState: %v", err)
		}
		made = append(made, dir)
	}

	entries, err := filepath.Glob(filepath.Join(root, "rollback-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("kept %d backups, want 3: %v", len(entries), entries)
	}
	// The three that survive are the three most recent ones.
	for _, dir := range made[2:] {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("a recent backup was pruned: %v", err)
		}
	}
}

// Every file the panel writes for itself has to be in the list, or a downgrade
// silently loses whichever one was forgotten.
func TestStateFilesCoversEveryPanelOwnedFile(t *testing.T) {
	paths := NewPaths("/data")
	want := []string{
		paths.PanelFile(), paths.InstancesFile(), paths.UsersFile(), paths.DatabasesFile(),
		paths.InstancePluginsFile(), paths.PendingPluginsFile(), paths.ConfigHistoryFile(),
		paths.ResumeFile(),
	}
	got := paths.StateFiles()
	if len(got) != len(want) {
		t.Fatalf("StateFiles() has %d entries, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("StateFiles()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
