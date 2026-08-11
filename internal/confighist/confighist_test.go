package confighist

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	service := New(
		filepath.Join(root, "instances"),
		filepath.Join(root, "config-history.json"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	dir := filepath.Join(root, "server")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return service, dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snap(t *testing.T, s *Service, dir, message string) CommitResult {
	t.Helper()
	result, err := s.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: message,
		Trigger: TriggerUser, Actor: "lanscarlos",
	})
	if err != nil {
		t.Fatalf("commit %q: %v", message, err)
	}
	return result
}

// TestCollectIsAWhitelist is the rule the whole module rests on: a file is not
// recorded because it looks like config, it is recorded because a rule says so.
func TestCollectIsAWhitelist(t *testing.T) {
	dir := t.TempDir()

	// Recorded.
	write(t, dir, "server.properties", "motd=hi\n")
	write(t, dir, "bukkit.yml", "settings:\n")
	write(t, dir, "ops.json", "[]\n")
	write(t, dir, "eula.txt", "eula=true\n")
	write(t, dir, "start.sh", "#!/bin/sh\n")
	write(t, dir, "config/paper-global.yml", "proxies: {}\n")
	write(t, dir, "plugins/LuckPerms/config.yml", "server: global\n")
	write(t, dir, "plugins/Skript/scripts/deep/quest.sk", "on join:\n")
	write(t, dir, "plugins/WorldGuard/worlds/survival/regions.yml", "regions: {}\n")

	// Not recorded, and each one is a way a blacklist would have failed.
	write(t, dir, "usercache.json", "[]\n")
	write(t, dir, "plugins/Essentials/userdata/notch.yml", "money: 1\n")
	write(t, dir, "plugins/Essentials/userdata/lang/x.yml", "a: 1\n")
	write(t, dir, "plugins/LuckPerms/yaml-storage/users/notch.yml", "x: 1\n")
	write(t, dir, "plugins/Towny/data/residents/notch.txt", "x\n")
	write(t, dir, "plugins/dynmap/web/tiles/t.png", "\x89PNG\n")
	write(t, dir, "plugins/CoreProtect/database.db", "binary\n")
	write(t, dir, "logs/latest.log", "line\n")
	write(t, dir, "crash-reports/crash.txt", "boom\n")
	write(t, dir, "server.jar", "PK\n")
	// A world that does not match world*, detected by its contents.
	write(t, dir, "survival/level.dat", "\x1f\x8b\n")
	write(t, dir, "survival/paper-world.yml", "chunks: {}\n")
	write(t, dir, "survival/region/r.0.0.mca", "binary\n")
	write(t, dir, "survival/playerdata/notch.dat", "binary\n")

	scan, err := Collect(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, file := range scan.Files {
		got[file.Path] = true
	}

	for _, path := range []string{
		"server.properties", "bukkit.yml", "ops.json", "eula.txt", "start.sh",
		"config/paper-global.yml", "plugins/LuckPerms/config.yml",
		"plugins/Skript/scripts/deep/quest.sk",
		"plugins/WorldGuard/worlds/survival/regions.yml",
		// The one file kept out of a world: its per-world config.
		"survival/paper-world.yml",
	} {
		if !got[path] {
			t.Errorf("%s should have been collected", path)
		}
	}
	for _, path := range []string{
		"usercache.json",
		"plugins/Essentials/userdata/notch.yml",
		"plugins/Essentials/userdata/lang/x.yml",
		"plugins/LuckPerms/yaml-storage/users/notch.yml",
		"plugins/Towny/data/residents/notch.txt",
		"plugins/dynmap/web/tiles/t.png",
		"plugins/CoreProtect/database.db",
		"logs/latest.log", "crash-reports/crash.txt", "server.jar",
		"survival/region/r.0.0.mca", "survival/playerdata/notch.dat",
	} {
		if got[path] {
			t.Errorf("%s should not have been collected", path)
		}
	}

	if len(scan.Worlds) != 1 || scan.Worlds[0] != "survival" {
		t.Errorf("world detection = %v, want [survival]", scan.Worlds)
	}
}

func TestCollectHonoursExclusions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "server.properties", "motd=hi\n")
	write(t, dir, "plugins/WorldGuard/worlds/survival/regions.yml", "regions: {}\n")

	scan, err := Collect(dir, []string{"/plugins/WorldGuard/worlds/survival/regions.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != 1 || scan.Files[0].Path != "server.properties" {
		t.Fatalf("exclusion ignored: %+v", scan.Files)
	}
}

func TestCommitSkipsWhenNothingChanged(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")

	first := snap(t, service, dir, "出厂状态")
	if first.Skipped || first.Ref == "" {
		t.Fatalf("first commit was skipped: %+v", first)
	}

	// The design's §4: a start/stop pair that changed nothing must not put an
	// empty commit on the timeline.
	second := snap(t, service, dir, "启动前快照")
	if !second.Skipped {
		t.Fatalf("expected the unchanged commit to be skipped: %+v", second)
	}

	history, err := service.History("abc123", dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("timeline has %d rows, want 1", len(history))
	}
}

func TestCommitRecordsTriggerAndAuthor(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")

	if _, err := service.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: "启动前快照",
		Trigger: TriggerLifecycle, Actor: ActorLifecycle,
	}); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "server.properties", "motd=hello\n")
	if _, err := service.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: "运行中快照",
		Trigger: TriggerUser, Actor: "lanscarlos", Running: true,
	}); err != nil {
		t.Fatal(err)
	}

	history, err := service.History("abc123", dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("timeline has %d rows", len(history))
	}
	if history[0].Trigger != TriggerUser || history[0].Author != "lanscarlos" || !history[0].Running {
		t.Errorf("newest row = %+v", history[0])
	}
	if history[1].Trigger != TriggerLifecycle || history[1].Author != ActorLifecycle {
		t.Errorf("oldest row = %+v", history[1])
	}
	if history[0].Stats.Insertions != 1 || history[0].Stats.Deletions != 1 || history[0].Stats.Files != 1 {
		t.Errorf("stats = %+v, want 1 file +1 -1", history[0].Stats)
	}
}

func TestOversizedFileBlocksTheCommit(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")
	write(t, dir, "plugins/WorldGuard/worlds/w/regions.yml", strings.Repeat("region: x\n", 100000))

	_, err := service.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: "出厂状态",
		Trigger: TriggerUser, Actor: "lanscarlos",
	})
	gate, blocked := AsGateError(err)
	if !blocked {
		t.Fatalf("expected the size gate to block, got %v", err)
	}
	if gate.Kind != GateFileSize || len(gate.Oversized) != 1 {
		t.Fatalf("gate = %+v", gate)
	}

	// Nothing may have been written: a blocked commit that still grew the
	// repository would defeat the gate it just tripped.
	history, err := service.History("abc123", dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("a blocked commit left %d rows behind", len(history))
	}
	if stats, err := service.Stats("abc123", dir); err != nil || stats.RepoBytes != 0 {
		t.Fatalf("a blocked commit wrote %d bytes of objects (err %v)", stats.RepoBytes, err)
	}

	// Allowing the file by hand is the operator's other option, and it works.
	if _, err := service.UpdateSettings("abc123", func(entry *InstanceSettings) {
		entry.Allow = append(entry.Allow, "plugins/WorldGuard/worlds/w/regions.yml")
	}); err != nil {
		t.Fatal(err)
	}
	if result := snap(t, service, dir, "出厂状态"); result.Skipped {
		t.Fatal("commit still skipped after allowing the file")
	}
}

// An oversized file is left out of the tree, so it contributes nothing to the
// change set. If it is the *only* thing that changed, a commit that checked
// "did anything change" before checking the gates would answer 没有配置变更 —
// and the operator would never learn their file was refused. That is the silent
// skip §2 exists to prevent, and it is worth its own test because the two
// checks read as though their order does not matter.
func TestOversizedFileAloneStillTripsTheGate(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")
	snap(t, service, dir, "出厂状态")

	write(t, dir, "plugins/Foo/huge.yml", strings.Repeat("a: 1\n", 200000))

	_, err := service.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: "启动前快照",
		Trigger: TriggerLifecycle, Actor: ActorLifecycle,
	})
	gate, blocked := AsGateError(err)
	if !blocked {
		t.Fatalf("expected the size gate to block, got %v", err)
	}
	if gate.Kind != GateFileSize {
		t.Fatalf("gate = %+v", gate)
	}
}

func TestExcludingSkipsTheGate(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")
	write(t, dir, "big.yml", strings.Repeat("a: 1\n", 200000))

	if _, err := service.UpdateSettings("abc123", func(entry *InstanceSettings) {
		entry.Exclude = append(entry.Exclude, "big.yml")
	}); err != nil {
		t.Fatal(err)
	}
	if result := snap(t, service, dir, "出厂状态"); result.Skipped {
		t.Fatal("excluding the oversized file did not unblock the commit")
	}
}

func TestFileCountGate(t *testing.T) {
	service, dir := newService(t)
	if _, err := service.UpdateSettings("abc123", func(entry *InstanceSettings) {
		entry.Limits.FileCount = 3
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		write(t, dir, filepath.Join("config", string(rune('a'+i))+".yml"), "a: 1\n")
	}

	_, err := service.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: "出厂状态", Trigger: TriggerUser,
	})
	gate, blocked := AsGateError(err)
	if !blocked || gate.Kind != GateFileCount {
		t.Fatalf("expected the file-count gate, got %v", err)
	}
}

func TestDiffAndMasking(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\nrcon.password=hunter2\n")
	snap(t, service, dir, "出厂状态")

	write(t, dir, "server.properties", "motd=hello\nrcon.password=hunter3\n")
	second := snap(t, service, dir, "编辑 server.properties")

	changes, err := service.Changes("abc123", dir, second.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "server.properties" || changes[0].Status != "modified" {
		t.Fatalf("changes = %+v", changes)
	}

	diff, err := service.Diff("abc123", dir, second.Ref, "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Insertions != 2 || diff.Deletions != 2 {
		t.Fatalf("diff counts = +%d -%d", diff.Insertions, diff.Deletions)
	}

	var sawSecret bool
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			if !strings.Contains(line.Text, "rcon.password") {
				continue
			}
			sawSecret = true
			if !line.Sensitive {
				t.Errorf("the rcon password was not marked sensitive: %+v", line)
			}
			if strings.Contains(line.Masked, "hunter") {
				t.Errorf("the masked form still holds the password: %q", line.Masked)
			}
		}
	}
	if !sawSecret {
		t.Fatal("the changed password line never appeared in the diff")
	}
}

func TestDiffKeepsUnchangedLinesOutOfTheCounts(t *testing.T) {
	service, dir := newService(t)
	var before strings.Builder
	for i := 0; i < 200; i++ {
		before.WriteString("key-")
		before.WriteString(string(rune('a' + i%26)))
		before.WriteString(": value\n")
	}
	write(t, dir, "config/big.yml", before.String())
	snap(t, service, dir, "出厂状态")

	changed := strings.Replace(before.String(), "key-a: value\n", "key-a: other\n", 1)
	write(t, dir, "config/big.yml", changed)
	ref := snap(t, service, dir, "一行改动").Ref

	diff, err := service.Diff("abc123", dir, ref, "config/big.yml")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Truncated {
		t.Fatal("a one-line change should not fall back to a whole-file replace")
	}
	if diff.Insertions != 1 || diff.Deletions != 1 {
		t.Fatalf("counts = +%d -%d, want +1 -1", diff.Insertions, diff.Deletions)
	}
	if len(diff.Hunks) != 1 {
		t.Fatalf("expected one hunk, got %d", len(diff.Hunks))
	}
	if len(diff.Hunks[0].Lines) > 2+2*diffContext {
		t.Fatalf("hunk carries %d lines, more context than asked for", len(diff.Hunks[0].Lines))
	}
}

func TestRestoreOneFileMovesForward(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=good\n")
	write(t, dir, "bukkit.yml", "settings:\n")
	good := snap(t, service, dir, "出厂状态").Ref

	write(t, dir, "server.properties", "motd=broken\n")
	write(t, dir, "spigot.yml", "settings:\n")
	snap(t, service, dir, "编辑 server.properties")

	result, plan, err := service.Restore(RestoreRequest{
		InstanceID: "abc123", Directory: dir, Ref: good,
		Path: "server.properties", Actor: "lanscarlos",
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if plan.Whole {
		t.Fatal("a path was given, so this should not be a tree restore")
	}
	if result.Skipped {
		t.Fatal("the restore was skipped")
	}

	content, err := os.ReadFile(filepath.Join(dir, "server.properties"))
	if err != nil || string(content) != "motd=good\n" {
		t.Fatalf("file was not restored: %q %v", content, err)
	}
	// A single-file restore touches nothing else, which is the whole reason it
	// is the default.
	if _, err := os.Stat(filepath.Join(dir, "spigot.yml")); err != nil {
		t.Fatal("a single-file restore removed an unrelated file")
	}

	// Only forward: the pre-restore snapshot and the restore itself are two new
	// commits on top, and nothing was rewritten.
	history, err := service.History("abc123", dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("timeline has %d rows, want 3", len(history))
	}
	if history[0].Trigger != TriggerRestore || !strings.HasPrefix(history[0].Message, "还原 server.properties") {
		t.Fatalf("newest row = %+v", history[0])
	}
	if history[len(history)-1].Ref != good {
		t.Fatal("the original commit id changed, so history was rewritten")
	}
}

func TestTreeRestoreIsRefusedWhileRunning(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=good\n")
	first := snap(t, service, dir, "出厂状态").Ref
	write(t, dir, "server.properties", "motd=broken\n")
	snap(t, service, dir, "编辑")

	_, _, err := service.Restore(RestoreRequest{
		InstanceID: "abc123", Directory: dir, Ref: first, Running: true,
	})
	if err == nil {
		t.Fatal("a tree restore under a running server should be refused")
	}

	// The same restore of a single file is allowed, with a warning.
	plan, err := service.PlanRestore(RestoreRequest{
		InstanceID: "abc123", Directory: dir, Ref: first,
		Path: "server.properties", Running: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BlockedBy != "" || plan.Warning == "" {
		t.Fatalf("single-file plan = %+v", plan)
	}
}

func TestTreeRestoreRemovesFilesAddedSince(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=good\n")
	first := snap(t, service, dir, "出厂状态").Ref

	write(t, dir, "spigot.yml", "settings:\n")
	snap(t, service, dir, "加了一个文件")

	plan, err := service.PlanRestore(RestoreRequest{InstanceID: "abc123", Directory: dir, Ref: first})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removals) != 1 || plan.Removals[0] != "spigot.yml" {
		t.Fatalf("removals = %v, want [spigot.yml]", plan.Removals)
	}

	if _, _, err := service.Restore(RestoreRequest{
		InstanceID: "abc123", Directory: dir, Ref: first, Actor: "lanscarlos",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spigot.yml")); !os.IsNotExist(err) {
		t.Fatal("the tree restore did not remove the file added since")
	}
}

func TestRestoreWarnsOnVersionDrift(t *testing.T) {
	service, dir := newService(t)
	versions := []string{"LuckPerms 5.5.70"}
	service.SetManifest(func(string) Manifest {
		return Manifest{Core: "Paper 1.21.1", Plugins: versions}
	})

	write(t, dir, "plugins/LuckPerms/config.yml", "server: global\n")
	old := snap(t, service, dir, "升级 LuckPerms 前").Ref

	versions = []string{"LuckPerms 5.5.71"}
	write(t, dir, "plugins/LuckPerms/config.yml", "server: survival\n")
	snap(t, service, dir, "升级 LuckPerms 后")

	_, plan, err := service.Restore(RestoreRequest{
		InstanceID: "abc123", Directory: dir, Ref: old,
		Path: "plugins/LuckPerms/config.yml", Actor: "lanscarlos",
	})
	if err == nil {
		t.Fatal("a version mismatch should stop the first attempt")
	}
	if !IsVersionMismatch(err) {
		t.Fatalf("wrong refusal: %v", err)
	}
	if plan.Mismatch == nil || len(plan.Mismatch.Plugins) != 1 {
		t.Fatalf("mismatch = %+v", plan.Mismatch)
	}

	if _, _, err := service.Restore(RestoreRequest{
		InstanceID: "abc123", Directory: dir, Ref: old,
		Path: "plugins/LuckPerms/config.yml", Actor: "lanscarlos", Confirmed: true,
	}); err != nil {
		t.Fatalf("confirming should let it through: %v", err)
	}
}

func TestCompactKeepsTransactionSnapshots(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=0\n")
	snap(t, service, dir, "出厂状态")

	var transaction string
	for i := 1; i <= 12; i++ {
		write(t, dir, "server.properties", "motd="+string(rune('a'+i))+"\n")
		trigger, actor := TriggerUser, "lanscarlos"
		if i == 2 {
			trigger, actor = TriggerTransaction, ActorTransaction
		}
		result, err := service.Commit(CommitRequest{
			InstanceID: "abc123", Directory: dir, Message: "改动 " + string(rune('a'+i)),
			Trigger: trigger, Actor: actor,
		})
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			transaction = result.Ref
		}
	}

	result, err := service.Compact("abc123", dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if result.Before != 13 {
		t.Fatalf("before = %d, want 13", result.Before)
	}
	if result.After >= result.Before {
		t.Fatalf("nothing was dropped: %d -> %d", result.Before, result.After)
	}
	if _, kept := result.Remap[transaction]; !kept {
		t.Fatal("the transaction snapshot was dropped, which breaks plugin rollback")
	}

	history, err := service.History("abc123", dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != result.After {
		t.Fatalf("history has %d rows, compaction reported %d", len(history), result.After)
	}
	var sawTransaction bool
	for _, row := range history {
		if row.Trigger == TriggerTransaction {
			sawTransaction = true
		}
	}
	if !sawTransaction {
		t.Fatal("no transaction snapshot survived")
	}
	// 出厂状态 is the base of everything and must still be the oldest row.
	if history[len(history)-1].Message != "出厂状态" {
		t.Fatalf("oldest row = %q", history[len(history)-1].Message)
	}
}

func TestPendingShowsUncommittedEdits(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")
	snap(t, service, dir, "出厂状态")

	pending, err := service.Pending("abc123", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected a clean tree, got %+v", pending)
	}

	write(t, dir, "server.properties", "motd=hello\n")
	write(t, dir, "spigot.yml", "settings:\n")
	if err := os.Remove(filepath.Join(dir, "server.properties")); err == nil {
		// removed and re-added below; this checks the deletion path too
		write(t, dir, "spigot.yml", "settings:\n")
	}

	pending, err = service.Pending("abc123", dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, change := range pending {
		byPath[change.Path] = change.Status
	}
	if byPath["spigot.yml"] != "added" {
		t.Errorf("spigot.yml = %q, want added", byPath["spigot.yml"])
	}
	if byPath["server.properties"] != "deleted" {
		t.Errorf("server.properties = %q, want deleted", byPath["server.properties"])
	}
}

func TestMaskLine(t *testing.T) {
	cases := []struct {
		line      string
		sensitive bool
	}{
		{"rcon.password=hunter2", true},
		{"  password: 'hunter2'", true},
		{`  "token": "abc123",`, true},
		{"bot-token = \"xyz\"", true},
		{"database.secret: s3cr3t", true},
		{"motd=A Minecraft Server", false},
		{"# password: hunter2", false},
		{"rcon.password=", false},
		{"level-name: world", false},
	}
	for _, tc := range cases {
		masked, ok := maskLine(tc.line)
		if ok != tc.sensitive {
			t.Errorf("maskLine(%q) sensitive = %v, want %v", tc.line, ok, tc.sensitive)
			continue
		}
		if ok && strings.Contains(masked, "hunter2") {
			t.Errorf("maskLine(%q) leaked the value: %q", tc.line, masked)
		}
	}
}

func TestDisabledInstanceRecordsNothing(t *testing.T) {
	service, dir := newService(t)
	write(t, dir, "server.properties", "motd=hi\n")
	if err := service.SetEnabled("abc123", false); err != nil {
		t.Fatal(err)
	}

	_, err := service.Commit(CommitRequest{
		InstanceID: "abc123", Directory: dir, Message: "出厂状态", Trigger: TriggerUser,
	})
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if _, err := os.Stat(service.ExportPath("abc123")); !os.IsNotExist(err) {
		t.Fatal("a disabled instance still got a repository")
	}
}
