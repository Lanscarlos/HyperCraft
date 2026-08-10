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
