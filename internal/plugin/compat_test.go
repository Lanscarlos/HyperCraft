package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestASpigotPluginRunsOnAPaperServer(t *testing.T) {
	// The single most common case in the whole panel: Paper loads Bukkit and
	// Spigot plugins unchanged, so a plugin that only claims "spigot" must not
	// come back greyed out.
	verdict := Judge(
		Target{MCVersion: "1.20.4", Loader: "paper"},
		[]string{"spigot", "bukkit"},
		[]string{"1.20.4"},
	)
	if verdict.State != CompatOK {
		t.Errorf("state = %q (%s), want ok", verdict.State, verdict.Detail)
	}
	if verdict.Label != "兼容 1.20.4" {
		t.Errorf("label = %q", verdict.Label)
	}
}

func TestAPaperPluginDoesNotRunOnASpigotServer(t *testing.T) {
	// Not symmetric with the case above, and deliberately so.
	verdict := Judge(Target{MCVersion: "1.20.4", Loader: "spigot"}, []string{"paper"}, []string{"1.20.4"})
	if verdict.State != CompatBad {
		t.Errorf("state = %q, want bad", verdict.State)
	}
}

func TestAWrongLoaderIsNamedAsSuchRatherThanAsAVersionProblem(t *testing.T) {
	verdict := Judge(Target{MCVersion: "1.20.4", Loader: "paper"}, []string{"velocity"}, []string{"1.20.4"})
	if verdict.State != CompatBad {
		t.Fatalf("state = %q, want bad", verdict.State)
	}
	if verdict.Label != "不支持 Paper" {
		t.Errorf("label = %q, want the loader named", verdict.Label)
	}
}

func TestAnAbandonedPluginSaysHowFarItGot(t *testing.T) {
	verdict := Judge(
		Target{MCVersion: "1.20.4", Loader: "paper"},
		[]string{"spigot"},
		[]string{"1.13", "1.14.4", "1.16.5"},
	)
	if verdict.State != CompatBad {
		t.Fatalf("state = %q, want bad", verdict.State)
	}
	if verdict.Label != "最高支持 1.16.5" {
		t.Errorf("label = %q", verdict.Label)
	}
}

func TestADeclaredPatchStandsForItsWholeLine(t *testing.T) {
	// Authors list one patch per minor line and mean the line. EssentialsX's
	// real Modrinth metadata is 1.19.4 / 1.20.6 / 1.21.11 — matching patches
	// exactly would tell a 1.20.4 operator that EssentialsX does not support
	// their server, which is wrong and is the most visible mistake this badge
	// could make.
	essentials := []string{"1.16.5", "1.17.1", "1.18.2", "1.19.4", "1.20.6", "1.21.11"}
	for _, version := range []string{"1.20.4", "1.20.6", "1.21.1", "1.19.2", "1.20"} {
		verdict := Judge(Target{MCVersion: version, Loader: "paper"}, []string{"paper"}, essentials)
		if verdict.State != CompatOK {
			t.Errorf("%s: state = %q (%s), want ok", version, verdict.State, verdict.Detail)
		}
	}

	// The looseness stops at the line. A break between minor versions is a
	// real break, and the yellow badge exists for exactly this case.
	verdict := Judge(Target{MCVersion: "1.22.1", Loader: "paper"}, []string{"paper"}, essentials)
	if verdict.State != CompatBad {
		t.Errorf("state = %q, want bad", verdict.State)
	}
}

func TestSilenceIsUnknownAndNeverCompatible(t *testing.T) {
	// The whole reason there are three states. A source that said nothing must
	// never produce a green badge.
	for _, test := range []struct {
		name         string
		target       Target
		loaders      []string
		gameVersions []string
	}{
		{"no metadata at all", Target{MCVersion: "1.20.4", Loader: "paper"}, nil, nil},
		{"no game versions", Target{MCVersion: "1.20.4", Loader: "paper"}, []string{"paper"}, nil},
		{"unknown server", Target{}, []string{"paper"}, []string{"1.20.4"}},
		{"server version unknown", Target{Loader: "paper"}, []string{"paper"}, []string{"1.20.4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			verdict := Judge(test.target, test.loaders, test.gameVersions)
			if verdict.State != CompatUnknown {
				t.Errorf("state = %q, want unknown", verdict.State)
			}
		})
	}
}

func TestTargetIsReadFromWhatTheServerWroteDown(t *testing.T) {
	dir := t.TempDir()
	history := `{"oldVersion":null,"currentVersion":"git-Paper-496 (MC: 1.20.4)"}`
	if err := os.WriteFile(filepath.Join(dir, "version_history.json"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	// The jar is deliberately misleading: the file the server actually wrote
	// has to win over anything guessed from a name.
	target := DetectTarget(dir, "server.jar", nil)
	if target.MCVersion != "1.20.4" || target.Loader != "paper" {
		t.Fatalf("target = %+v", target)
	}
	if target.Source != "version-history" {
		t.Errorf("source = %q", target.Source)
	}
}

func TestTargetFallsBackToTheCoreLibraryThenTheJarName(t *testing.T) {
	dir := t.TempDir()

	core := func(fileName string) (string, string, bool) {
		if fileName == "paper-1.21.1-40.jar" {
			return "paper", "1.21.1", true
		}
		return "", "", false
	}
	if target := DetectTarget(dir, "paper-1.21.1-40.jar", core); target.MCVersion != "1.21.1" {
		t.Errorf("core library lookup: %+v", target)
	}

	// Nothing in the library, so the name is all there is — and it is marked
	// as a guess.
	target := DetectTarget(dir, "velocity-3.3.0.jar", core)
	if target.Loader != "velocity" || target.MCVersion != "3.3.0" || target.Source != "jar-name" {
		t.Errorf("jar name fallback: %+v", target)
	}

	// A renamed jar tells the panel nothing, and 未知 is the honest answer.
	if target := DetectTarget(dir, "server.jar", core); target.Known() {
		t.Errorf("a renamed jar should not produce a target: %+v", target)
	}
}

func TestModsGoToModsAndPluginsGoToPlugins(t *testing.T) {
	if dir := TargetDirFor("fabric"); dir != "mods" {
		t.Errorf("fabric target dir = %q", dir)
	}
	if dir := TargetDirFor("paper"); dir != DefaultTargetDir {
		t.Errorf("paper target dir = %q", dir)
	}
}

func TestNoTargetsMeansNoVerdictAtAll(t *testing.T) {
	// Not "unknown" — nil. A row with nothing to judge against draws no badge,
	// because a column reading 未知兼容性 all the way down is the most
	// prominent position on the row spent on saying nothing.
	if verdict := JudgeAcross(nil, []string{"paper"}, []string{"1.20.4"}); verdict != nil {
		t.Fatalf("expected no verdict, got %+v", verdict)
	}
}

func TestOneTargetJudgesExactlyAsBefore(t *testing.T) {
	paper := []NamedTarget{{Name: "生存服", Target: Target{Loader: "paper", MCVersion: "1.20.4"}}}

	verdict := JudgeAcross(paper, []string{"spigot"}, []string{"1.20.1"})
	if verdict == nil || verdict.State != CompatOK || verdict.Label != "兼容 1.20.4" {
		t.Fatalf("single target should read like Judge: %+v", verdict)
	}
}

func TestAcrossServersIsPessimistic(t *testing.T) {
	fleet := []NamedTarget{
		{Name: "生存服", Target: Target{Loader: "paper", MCVersion: "1.20.4"}},
		{Name: "群组端", Target: Target{Loader: "velocity", MCVersion: "3.3.0"}},
	}

	// Fits the Paper server, will never load on the proxy. One bad server
	// makes the row bad, and the detail has to name which one.
	verdict := JudgeAcross(fleet, []string{"paper"}, []string{"1.20.4"})
	if verdict == nil || verdict.State != CompatBad {
		t.Fatalf("one incompatible server should sink the row: %+v", verdict)
	}
	if !strings.Contains(verdict.Detail, "群组端") {
		t.Errorf("detail should blame the server that failed: %q", verdict.Detail)
	}

	both := []NamedTarget{
		{Name: "生存服", Target: Target{Loader: "paper", MCVersion: "1.20.4"}},
		{Name: "创造服", Target: Target{Loader: "paper", MCVersion: "1.20.1"}},
	}
	if verdict := JudgeAcross(both, []string{"paper"}, []string{"1.20.4", "1.20.1"}); verdict.State != CompatOK {
		t.Errorf("both fit, so the row fits: %+v", verdict)
	}
}

// The bug this exists for, in one test.
//
// A Paper server running the bukkit build of a release is not "behind" because
// the proxy build of the same number, or a Fabric-only release published
// later, sits above it in the library's list. Offering either is offering a
// jar the server cannot load.
func TestUpdateForSkipsBuildsThisServerCannotLoad(t *testing.T) {
	paper := Target{MCVersion: "1.20.4", Loader: "paper"}
	velocity := Target{MCVersion: "3.3.0", Loader: "velocity"}

	item := Plugin{
		ID:   "luckperms",
		Name: "LuckPerms",
		Versions: []Version{
			{
				Tag: "v5.5.72", Version: "v5.5.72",
				Artifacts: []Artifact{
					{SHA256: "f1", FileName: "LuckPerms-Fabric-5.5.72.jar", Platform: "fabric", Loaders: []string{"fabric"}},
				},
			},
			{
				Tag: "v5.5.71", Version: "v5.5.71",
				Artifacts: []Artifact{
					{SHA256: "b1", FileName: "LuckPerms-Bukkit-5.5.71.jar", Platform: "bukkit", Loaders: []string{"bukkit", "paper", "spigot"}},
					{SHA256: "v1", FileName: "LuckPerms-Velocity-5.5.71.jar", Platform: "velocity", Loaders: []string{"velocity"}},
				},
			},
			{
				Tag: "v5.5.70", Version: "v5.5.70",
				Artifacts: []Artifact{
					{SHA256: "b0", FileName: "LuckPerms-Bukkit-5.5.70.jar", Platform: "bukkit", Loaders: []string{"bukkit", "paper", "spigot"}},
				},
			},
		},
	}

	// On v5.5.70, the Paper server is offered v5.5.71 — and specifically its
	// bukkit jar, not whichever build sorted first.
	offer := UpdateFor(item, "v5.5.70", paper)
	if offer == nil || offer.Tag != "v5.5.71" {
		t.Fatalf("offered %+v, want v5.5.71", offer)
	}
	if offer.SHA256 != "b1" {
		t.Errorf("offered the %s jar, want the bukkit build", offer.FileName)
	}

	// On v5.5.71 there is nothing above it this server can run: v5.5.72 is
	// Fabric only. This is the one that used to say "→ v5.5.71-velocity".
	if offer := UpdateFor(item, "v5.5.71", paper); offer != nil {
		t.Errorf("offered %+v to a Paper server, want nothing", offer)
	}

	// The proxy is a different question with a different answer.
	if offer := UpdateFor(item, "v5.5.70", velocity); offer == nil || offer.SHA256 != "v1" {
		t.Errorf("the proxy was offered %+v, want the velocity jar of v5.5.71", offer)
	}

	// A pinned plugin is pinned.
	pinned := item
	pinned.Policy = Policy{Pin: "v5.5.70"}
	if offer := UpdateFor(pinned, "v5.5.70", paper); offer != nil {
		t.Errorf("offered %+v for a pinned plugin", offer)
	}
}

// A GitHub release publishes no loader metadata, and "unknown" must not become
// "refused" — that would hide every update for every plugin tracked that way.
func TestUpdateForStillOffersUnjudgeableReleases(t *testing.T) {
	item := Plugin{
		ID: "essentials",
		Versions: []Version{
			{Tag: "v2.21.0", Version: "2.21.0", Artifacts: []Artifact{{SHA256: "a", FileName: "EssentialsX-2.21.0.jar"}}},
			{Tag: "v2.20.1", Version: "2.20.1", Artifacts: []Artifact{{SHA256: "b", FileName: "EssentialsX-2.20.1.jar"}}},
		},
	}
	offer := UpdateFor(item, "v2.20.1", Target{MCVersion: "1.20.4", Loader: "paper"})
	if offer == nil || offer.Tag != "v2.21.0" {
		t.Fatalf("offered %+v, want v2.21.0", offer)
	}
}

// The market page's version of the same bug.
//
// A release's Loaders is the union across its builds, so judging every jar by
// it makes the Bukkit build of LuckPerms look compatible with a Velocity
// proxy — which is exactly the badge somebody reads right before downloading
// the wrong file. Each jar has to be judged by its own claim.
func TestAssetClaimsJudgeEachBuildOnItsOwn(t *testing.T) {
	release := Release{
		Tag:     "v5.5.71",
		Version: "5.5.71",
		// What the release says, which is true of the release and of no file
		// under it.
		Loaders:      []string{"bukkit", "paper", "spigot", "velocity"},
		GameVersions: []string{"1.20.4"},
		Assets: []Asset{
			{Name: "LuckPerms-Bukkit-5.5.71.jar", Platform: "bukkit", Loaders: []string{"bukkit", "paper", "spigot"}},
			{Name: "LuckPerms-Velocity-5.5.71.jar", Platform: "velocity", Loaders: []string{"velocity"}},
			// Hangar labels the platform and leaves the loader list empty;
			// the label is still enough to know what the jar is.
			{Name: "LuckPerms-Fabric-5.5.71.jar", Platform: "fabric"},
		},
	}
	proxy := Target{MCVersion: "1.20.4", Loader: "velocity"}

	// The release as a whole passes for a proxy — one of its builds fits —
	// and that is what makes the per-jar verdict necessary rather than
	// redundant.
	if state := Judge(proxy, release.Loaders, release.GameVersions).State; state != CompatOK {
		t.Fatalf("the release judges %q against a proxy, want ok", state)
	}

	want := map[string]string{
		"LuckPerms-Bukkit-5.5.71.jar":   CompatBad,
		"LuckPerms-Velocity-5.5.71.jar": CompatOK,
		"LuckPerms-Fabric-5.5.71.jar":   CompatBad,
	}
	for _, asset := range release.Assets {
		loaders, gameVersions := AssetClaims(release, asset)
		if state := Judge(proxy, loaders, gameVersions).State; state != want[asset.Name] {
			t.Errorf("%s judges %q against a proxy, want %q", asset.Name, state, want[asset.Name])
		}
	}
}

// A source that never broke its metadata down per file must not end up with
// every jar judged as unknown: the release's own claim is the best answer
// available, and falling back to it is what keeps a single-jar plugin's badge
// working.
func TestAssetClaimsFallsBackToTheRelease(t *testing.T) {
	release := Release{
		Loaders:      []string{"paper"},
		GameVersions: []string{"1.20.4"},
		Assets:       []Asset{{Name: "EssentialsX-2.21.0.jar"}},
	}
	loaders, gameVersions := AssetClaims(release, release.Assets[0])
	if state := Judge(Target{MCVersion: "1.20.4", Loader: "paper"}, loaders, gameVersions).State; state != CompatOK {
		t.Errorf("judged %q, want ok", state)
	}
}
