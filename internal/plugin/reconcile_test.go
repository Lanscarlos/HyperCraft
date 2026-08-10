package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// storeDeclaredJar puts a real jar in the library, descriptor and all, the way
// a finished download does — including the plugin.yml read that everything in
// here keys off.
func storeDeclaredJar(t *testing.T, library *Library, id, tag, fileName, declared, declaredVer string) Artifact {
	t.Helper()
	slug, err := versionSlug(tag)
	if err != nil {
		t.Fatalf("versionSlug: %v", err)
	}
	dir := filepath.Join(library.Root(), id, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := jarBytes(t, "plugin.yml", "name: "+declared+"\nversion: "+declaredVer+"\n")
	if err := os.WriteFile(filepath.Join(dir, fileName), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	digest := sha256.Sum256(body)
	artifact := Artifact{
		SHA256:     hex.EncodeToString(digest[:]),
		FileName:   fileName,
		Size:       int64(len(body)),
		PluginName: declared,
		PluginVer:  declaredVer,
		Platform:   "bukkit",
		AddedAt:    time.Now(),
	}
	err = library.record(id, Version{
		Tag: tag, Version: VersionOf(tag),
		Artifacts:   []Artifact{artifact},
		PublishedAt: time.Now(), AddedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	return artifact
}

// jarFixture is a library holding two releases of one plugin, both declaring
// the same plugin.yml name under different file names — the shape that breaks
// a panel which upgrades by copying the new jar in.
func jarFixture(t *testing.T) (*Instances, *Library, string, Plugin) {
	t.Helper()
	library := newLibrary(t)
	item := addPlugin(t, library, "LuckPerms", "LuckPerms/LuckPerms")
	storeDeclaredJar(t, library, item.ID, "v5.4.0", "LuckPerms-Bukkit-5.4.0.jar", "LuckPerms", "5.4.0")
	storeDeclaredJar(t, library, item.ID, "v5.5.0", "LuckPerms-Bukkit-5.5.0.jar", "LuckPerms", "5.5.0")

	dir := t.TempDir()
	instances := NewInstances(library, filepath.Join(t.TempDir(), "instance-plugins.json"))
	return instances, library, dir, item
}

func TestUpgradeDeletesEveryJarDeclaringTheSamePluginName(t *testing.T) {
	instances, _, dir, item := jarFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v5.4.0"); err != nil {
		t.Fatalf("install: %v", err)
	}
	// A second copy of the same plugin under a name the panel never chose:
	// somebody's manual upload, or a restored backup. It declares the same
	// plugin.yml name, which is the only thing that makes it a duplicate.
	stray := filepath.Join(dir, "plugins", "luckperms.jar")
	if err := os.WriteFile(stray, jarBytes(t, "plugin.yml", "name: LuckPerms\nversion: 5.3.0\n"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	if _, err := instances.Install("inst", dir, item.ID, "v5.5.0"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	// The whole point. Two jars declaring one plugin name is a server that
	// loads the plugin twice and reports a spray of errors naming neither.
	if exists(t, dir, "plugins/LuckPerms-Bukkit-5.4.0.jar") {
		t.Error("the previous version was left in the directory")
	}
	if exists(t, dir, "plugins/luckperms.jar") {
		t.Error("a differently-named jar of the same plugin was left in the directory")
	}
	if !exists(t, dir, "plugins/LuckPerms-Bukkit-5.5.0.jar") {
		t.Fatal("the new jar is missing")
	}
}

func TestUpgradeKeepsABackupTheRollbackCanRead(t *testing.T) {
	instances, _, dir, item := jarFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v5.4.0"); err != nil {
		t.Fatalf("install: %v", err)
	}
	// Config the plugin wrote, which a rollback should be able to bring back.
	configDir := filepath.Join(dir, "plugins", "LuckPerms")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("old: true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := instances.Install("inst", dir, item.ID, "v5.5.0"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("new: true\n"), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	entry, err := instances.Rollback("inst", dir, item.ID, true)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if entry.Version != "5.4.0" {
		t.Errorf("rolled back to %q, want 5.4.0", entry.Version)
	}
	if !exists(t, dir, "plugins/LuckPerms-Bukkit-5.4.0.jar") {
		t.Error("the old jar did not come back")
	}
	if exists(t, dir, "plugins/LuckPerms-Bukkit-5.5.0.jar") {
		t.Error("the newer jar was left beside the restored one")
	}
	body, err := os.ReadFile(filepath.Join(configDir, "config.yml"))
	if err != nil || string(body) != "old: true\n" {
		t.Errorf("config = %q (%v), want the backed-up copy", body, err)
	}
}

func TestRollbackLeavesTheConfigAloneUnlessAsked(t *testing.T) {
	instances, _, dir, item := jarFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v5.4.0"); err != nil {
		t.Fatalf("install: %v", err)
	}
	configDir := filepath.Join(dir, "plugins", "LuckPerms")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("old: true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := instances.Install("inst", dir, item.ID, "v5.5.0"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("written since\n"), 0o644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}

	if _, err := instances.Rollback("inst", dir, item.ID, false); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// A plugin that has been running since the upgrade has written data, and
	// throwing it away is a separate decision from going back to the old jar.
	body, _ := os.ReadFile(filepath.Join(configDir, "config.yml"))
	if string(body) != "written since\n" {
		t.Errorf("config = %q, want it untouched", body)
	}
}

func TestReconcileReportsDriftMissingAndForeign(t *testing.T) {
	instances, library, dir, item := jarFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v5.5.0"); err != nil {
		t.Fatalf("install: %v", err)
	}

	// A second plugin, installed and then deleted by hand.
	gone := addPlugin(t, library, "Vault", "MilkBowl/Vault")
	storeDeclaredJar(t, library, gone.ID, "v1.7.3", "Vault-1.7.3.jar", "Vault", "1.7.3")
	if _, err := instances.Install("inst", dir, gone.ID, "v1.7.3"); err != nil {
		t.Fatalf("install vault: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "plugins", "Vault-1.7.3.jar")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// The first plugin rewrites its own jar, the way a self-updating build does.
	target := filepath.Join(dir, "plugins", "LuckPerms-Bukkit-5.5.0.jar")
	if err := os.WriteFile(target, jarBytes(t, "plugin.yml", "name: LuckPerms\nversion: 5.5.1\n"), 0o644); err != nil {
		t.Fatalf("rewrite jar: %v", err)
	}

	// And somebody SFTPs a jar in that the panel has never seen.
	uploaded := filepath.Join(dir, "plugins", "mystery.jar")
	if err := os.WriteFile(uploaded, jarBytes(t, "plugin.yml", "name: WorldEdit\nversion: 7.2.0\n"), 0o644); err != nil {
		t.Fatalf("write uploaded: %v", err)
	}

	report, err := instances.Reconcile("inst", dir)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.Drift != 1 || report.Missing != 1 || report.Foreign != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Clean() {
		t.Error("a report with three findings claimed to be clean")
	}

	byState := map[string]Finding{}
	for _, finding := range report.Findings {
		byState[finding.State] = finding
	}
	// A foreign jar has to be named by what it declares, not by its file name:
	// "mystery.jar" tells the operator nothing they can act on.
	if got := byState[ReconForeign].Name; got != "WorldEdit" {
		t.Errorf("foreign jar named %q, want the name out of its plugin.yml", got)
	}
	drift := byState[ReconDrift]
	if drift.Expected == "" || drift.Found == "" || drift.Expected == drift.Found {
		t.Errorf("drift finding should carry both digests: %+v", drift)
	}
	if drift.Allowed {
		t.Error("drift was allowed without 允许自更新 being set")
	}
}

func TestAllowSelfUpdateStopsDriftCountingAsAProblem(t *testing.T) {
	instances, library, dir, item := jarFixture(t)

	if _, err := instances.Install("inst", dir, item.ID, "v5.5.0"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := library.SetPolicy(item.ID, Policy{AllowSelfUpdate: true}); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	target := filepath.Join(dir, "plugins", "LuckPerms-Bukkit-5.5.0.jar")
	if err := os.WriteFile(target, jarBytes(t, "plugin.yml", "name: LuckPerms\nversion: 5.5.1\n"), 0o644); err != nil {
		t.Fatalf("rewrite jar: %v", err)
	}

	report, err := instances.Reconcile("inst", dir)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// Still found, still recorded, still shown — and not counted, because a
	// plugin that legitimately rewrites itself would otherwise raise the alarm
	// every week until nobody reads it.
	if report.Drift != 0 {
		t.Errorf("drift counted = %d, want 0 with 允许自更新 on", report.Drift)
	}
	if len(report.Findings) != 1 || !report.Findings[0].Allowed {
		t.Fatalf("the drift should still be reported, marked allowed: %+v", report.Findings)
	}
}

func TestReconcileAdoptsADigestForRecordsWrittenBeforeThereWereAny(t *testing.T) {
	instances, _, dir, item := jarFixture(t)
	if _, err := instances.Install("inst", dir, item.ID, "v5.5.0"); err != nil {
		t.Fatalf("install: %v", err)
	}

	// An install record from an older panel: no digest to compare against.
	instances.mu.Lock()
	instances.load()["inst"].Plugins[0].SHA256 = ""
	instances.mu.Unlock()

	report, err := instances.Reconcile("inst", dir)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// Reporting drift here would put a warning on every plugin installed by a
	// previous release, which is every plugin on an upgraded panel.
	if report.Drift != 0 || report.OK != 1 {
		t.Fatalf("report = %+v", report)
	}
	if instances.recordsFor("inst")[0].SHA256 == "" {
		t.Error("the baseline digest was not adopted")
	}
}

func TestOneReleaseCanHoldSeveralJars(t *testing.T) {
	library := newLibrary(t)
	item := addPlugin(t, library, "LuckPerms", "LuckPerms/LuckPerms")
	storeDeclaredJar(t, library, item.ID, "v5.5.0", "LuckPerms-Bukkit-5.5.0.jar", "LuckPerms", "5.5.0")
	storeDeclaredJar(t, library, item.ID, "v5.5.0", "LuckPerms-Velocity-5.5.0.jar", "luckperms", "5.5.0")

	stored, err := library.Get(item.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// One release, two jars. Counting these as two versions is what made the
	// old page report "版本不一致" about a plugin that had shipped one release.
	if len(stored.Versions) != 1 {
		t.Fatalf("versions = %d, want 1 release: %+v", len(stored.Versions), stored.Versions)
	}
	if len(stored.Versions[0].Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", stored.Versions[0].Artifacts)
	}

	// And either one is installable by digest, which is what a plugin shipping
	// one build per platform needs.
	velocity := stored.Versions[0].Artifacts[1]
	file, _, _, artifact, err := library.OpenArtifact(item.ID, "v5.5.0", velocity.SHA256)
	if err != nil {
		t.Fatalf("OpenArtifact: %v", err)
	}
	file.Close()
	if artifact.FileName != "LuckPerms-Velocity-5.5.0.jar" {
		t.Errorf("opened %q, want the velocity jar", artifact.FileName)
	}
}
