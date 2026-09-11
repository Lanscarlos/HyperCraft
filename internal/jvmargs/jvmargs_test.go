package jvmargs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The file Forge actually writes: an explanation, and the flag commented out.
const forgeDefault = `# Xmx and Xms set the maximum and minimum RAM usage, respectively.
# They can take any number, followed by an M or a G.
# M means Megabyte, G means Gigabyte.
# For example, to set the maximum to 3GB: -Xmx3G

# A good default for a modded server is 4GB.
# Uncomment the next line to set it.
# -Xmx4G
`

func parse(t *testing.T, body string) *File {
	t.Helper()
	file, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return file
}

func TestARoundTripThroughDiskKeepsTheSetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(forgeDefault), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	file, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	file.SetMaxMemoryMB(8192)
	if err := os.WriteFile(path, file.Render(), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.MaxMemoryMB(); got != 8192 {
		t.Errorf("after a round trip MaxMemoryMB = %d, want 8192", got)
	}
}

func TestTheCommentedOutExampleIsWhereTheHeapGoes(t *testing.T) {
	file := parse(t, forgeDefault)
	if got := file.MaxMemoryMB(); got != 0 {
		t.Fatalf("MaxMemoryMB on the stock file = %d, want 0 — the line is a comment", got)
	}

	file.SetMaxMemoryMB(6144)
	out := string(file.Render())

	if strings.Contains(out, "# -Xmx4G") {
		t.Errorf("the commented-out example survived the edit:\n%s", out)
	}
	if !strings.Contains(out, "-Xmx6144M") {
		t.Errorf("-Xmx6144M missing:\n%s", out)
	}
	// The paragraph explaining the file is the reason anyone can hand-edit it.
	if !strings.Contains(out, "# A good default for a modded server is 4GB.") {
		t.Errorf("the explanation was thrown away:\n%s", out)
	}
	// Prose that merely mentions a flag is prose, not a setting.
	if !strings.Contains(out, "to set the maximum to 3GB: -Xmx3G") {
		t.Errorf("a comment mentioning a flag was rewritten as one:\n%s", out)
	}
}

func TestSizesAreReadInEveryUnitTheJVMAccepts(t *testing.T) {
	cases := map[string]int{
		"-Xmx4G":         4096,
		"-Xmx4g":         4096,
		"-Xmx4096M":      4096,
		"-Xmx4096m":      4096,
		"-Xmx4194304K":   4096,
		"-Xmx4294967296": 4096,
		// Below a megabyte still means "there is a ceiling", so it must not
		// come back as the 0 that means "nobody set one".
		"-Xmx512K": 1,
	}
	for arg, want := range cases {
		if got := parse(t, arg+"\n").MaxMemoryMB(); got != want {
			t.Errorf("%s -> %d MB, want %d", arg, got, want)
		}
	}
}

func TestGarbageIsNotASize(t *testing.T) {
	for _, arg := range []string{"-Xmx", "-XmxLots", "-Xmx4X", "-Xmx-1G", "-Xmx0"} {
		if got := parse(t, arg+"\n").MaxMemoryMB(); got != 0 {
			t.Errorf("%s -> %d MB, want 0", arg, got)
		}
	}
}

func TestTheLastHeapFlagWinsAndTheOthersGo(t *testing.T) {
	file := parse(t, "-Xmx1G\n-XX:+UseG1GC\n-Xmx2G\n")
	if got := file.MaxMemoryMB(); got != 2048 {
		t.Fatalf("MaxMemoryMB = %d, want 2048 — the JVM honours the last one", got)
	}

	file.SetMaxMemoryMB(3072)
	out := string(file.Render())
	if strings.Count(out, "-Xmx") != 1 {
		t.Errorf("a file that disagreed with itself still does:\n%s", out)
	}
	if !strings.Contains(out, "-Xmx3072M") || !strings.Contains(out, "-XX:+UseG1GC") {
		t.Errorf("wrong result:\n%s", out)
	}
}

func TestZeroRemovesTheFlagRatherThanWritingZero(t *testing.T) {
	file := parse(t, "-Xms1G\n-Xmx4G\n-XX:+UseG1GC\n")
	file.SetMaxMemoryMB(0)
	out := string(file.Render())
	if strings.Contains(out, "-Xmx") {
		t.Errorf("-Xmx survived being cleared:\n%s", out)
	}
	if !strings.Contains(out, "-Xms1G") || !strings.Contains(out, "-XX:+UseG1GC") {
		t.Errorf("clearing -Xmx took something else with it:\n%s", out)
	}
}

func TestEverythingElseIsCarriedThroughUntouched(t *testing.T) {
	const body = "-XX:+UseG1GC\n-Dsomething=x\n\n# trailing note\n"
	file := parse(t, body)
	file.SetMinMemoryMB(1024)

	out := string(file.Render())
	for _, want := range []string{"-XX:+UseG1GC", "-Dsomething=x", "# trailing note", "-Xms1024M"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from:\n%s", want, out)
		}
	}
	if got := file.Args(); len(got) != 3 {
		t.Errorf("Args() = %v, want the three flags without the comment", got)
	}
}
