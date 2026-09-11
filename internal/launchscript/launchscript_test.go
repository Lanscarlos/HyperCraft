package launchscript

import (
	"reflect"
	"strings"
	"testing"
)

// parseShell is the shorthand every shell case uses; a refusal here is a test
// failure, since the refusal cases assert through refuse() instead.
func parseShell(t *testing.T, text string) Result {
	t.Helper()
	got, refusals := Parse(text, Shell)
	if len(refusals) > 0 {
		t.Fatalf("unexpected refusal %+v", refusals)
	}
	return got
}

// refuse asserts that a script is rejected, and with which code.
func refuse(t *testing.T, text string, dialect Dialect, code string) Refusal {
	t.Helper()
	_, refusals := Parse(text, dialect)
	for _, r := range refusals {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("want refusal %q, got %+v", code, refusals)
	return Refusal{}
}

func TestPlainPaperScript(t *testing.T) {
	got := parseShell(t, "#!/bin/sh\njava -Xms2G -Xmx4G -XX:+UseG1GC -jar paper-1.20.4-496.jar --nogui\n")

	if got.Java != "java" {
		t.Errorf("Java = %q, want java", got.Java)
	}
	if got.MinMemoryMB != 2048 || got.MaxMemoryMB != 4096 {
		t.Errorf("memory = %d/%d, want 2048/4096", got.MinMemoryMB, got.MaxMemoryMB)
	}
	if !reflect.DeepEqual(got.JVMArgs, []string{"-XX:+UseG1GC"}) {
		t.Errorf("JVMArgs = %q", got.JVMArgs)
	}
	if got.Jar != "paper-1.20.4-496.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
	if !reflect.DeepEqual(got.ServerArgs, []string{"--nogui"}) {
		t.Errorf("ServerArgs = %q", got.ServerArgs)
	}
}

func TestMemoryUnits(t *testing.T) {
	cases := []struct {
		arg  string
		want int
	}{
		{"-Xmx4G", 4096},
		{"-Xmx4g", 4096},
		{"-Xmx4096M", 4096},
		{"-Xmx4096m", 4096},
		{"-Xmx2048K", 2},
		{"-Xmx1073741824", 1024},
	}
	for _, c := range cases {
		got := parseShell(t, "java "+c.arg+" -jar s.jar\n")
		if got.MaxMemoryMB != c.want {
			t.Errorf("%s -> %d, want %d", c.arg, got.MaxMemoryMB, c.want)
		}
	}
}

func TestCommentsAndBlankLinesIgnored(t *testing.T) {
	got := parseShell(t, "#!/bin/bash\n# 启动脚本\n\n   # -Xmx8G 这行是注释\njava -Xmx4G -jar s.jar\n")
	if got.MaxMemoryMB != 4096 {
		t.Errorf("MaxMemoryMB = %d, want 4096", got.MaxMemoryMB)
	}
}

func TestLineContinuation(t *testing.T) {
	got := parseShell(t, "java \\\n  -Xmx4G \\\n  -jar s.jar \\\n  --nogui\n")
	if got.MaxMemoryMB != 4096 || got.Jar != "s.jar" {
		t.Errorf("got %+v", got)
	}
	if !reflect.DeepEqual(got.ServerArgs, []string{"--nogui"}) {
		t.Errorf("ServerArgs = %q", got.ServerArgs)
	}
}

func TestVariableExpansion(t *testing.T) {
	got := parseShell(t, "MEM=4G\nJAR=paper.jar\njava -Xmx$MEM -jar ${JAR} --nogui\n")
	if got.MaxMemoryMB != 4096 {
		t.Errorf("MaxMemoryMB = %d, want 4096", got.MaxMemoryMB)
	}
	if got.Jar != "paper.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
}

func TestExportedVariableExpansion(t *testing.T) {
	got := parseShell(t, "export MEM=8G\njava -Xmx$MEM -jar s.jar\n")
	if got.MaxMemoryMB != 8192 {
		t.Errorf("MaxMemoryMB = %d, want 8192", got.MaxMemoryMB)
	}
}

func TestVariableDefaultValue(t *testing.T) {
	got := parseShell(t, "java -Xmx${MEM:-2G} -jar s.jar\n")
	if got.MaxMemoryMB != 2048 {
		t.Errorf("MaxMemoryMB = %d, want 2048", got.MaxMemoryMB)
	}
}

func TestVariableReferencingEarlierVariable(t *testing.T) {
	got := parseShell(t, "BASE=/opt/jdk21\nJAVA=$BASE/bin/java\n$JAVA -Xmx4G -jar s.jar\n")
	if got.Java != "/opt/jdk21/bin/java" {
		t.Errorf("Java = %q", got.Java)
	}
}

func TestAbsoluteJavaPath(t *testing.T) {
	got := parseShell(t, "/usr/lib/jvm/temurin-21/bin/java -Xmx4G -jar s.jar\n")
	if got.Java != "/usr/lib/jvm/temurin-21/bin/java" {
		t.Errorf("Java = %q", got.Java)
	}
}

func TestRelativeJavaPathKeptRelative(t *testing.T) {
	// The caller resolves it against the instance directory; the parser has no
	// directory to resolve against and must not invent one.
	got := parseShell(t, "./jre/bin/java -Xmx4G -jar s.jar\n")
	if got.Java != "./jre/bin/java" {
		t.Errorf("Java = %q", got.Java)
	}
}

func TestQuotedJavaPathWithSpaces(t *testing.T) {
	got := parseShell(t, "\"/opt/my java/bin/java\" -Xmx4G -jar s.jar\n")
	if got.Java != "/opt/my java/bin/java" {
		t.Errorf("Java = %q", got.Java)
	}
}

func TestUndefinedJavaHomeAsksTheUser(t *testing.T) {
	got := parseShell(t, "java_cmd=$JAVA_HOME/bin/java\n$java_cmd -Xmx4G -jar s.jar\n")
	if got.JavaVar != "JAVA_HOME" {
		t.Errorf("JavaVar = %q, want JAVA_HOME", got.JavaVar)
	}
	if got.Java != "" {
		t.Errorf("Java = %q, want empty when it has to be asked for", got.Java)
	}
	// The rest of the command line is still worth showing.
	if got.MaxMemoryMB != 4096 || got.Jar != "s.jar" {
		t.Errorf("got %+v", got)
	}
}

func TestDefinedJavaHomeExpands(t *testing.T) {
	got := parseShell(t, "JAVA_HOME=/opt/jdk21\n$JAVA_HOME/bin/java -Xmx4G -jar s.jar\n")
	if got.Java != "/opt/jdk21/bin/java" {
		t.Errorf("Java = %q", got.Java)
	}
	if got.JavaVar != "" {
		t.Errorf("JavaVar = %q, want empty", got.JavaVar)
	}
}

func TestJavawBecomesJava(t *testing.T) {
	got := parseShell(t, "javaw -Xmx4G -jar s.jar\n")
	if got.Java != "java" {
		t.Errorf("Java = %q, want java", got.Java)
	}
	if !got.Javaw {
		t.Error("Javaw = false, want true so the preview can say so")
	}
}

func TestForgeArgFiles(t *testing.T) {
	got := parseShell(t, "java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt \"$@\"\n")

	want := []string{"user_jvm_args.txt", "libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt"}
	if !reflect.DeepEqual(got.ArgFiles, want) {
		t.Errorf("ArgFiles = %q", got.ArgFiles)
	}
	if got.Jar != "" {
		t.Errorf("Jar = %q, want empty for an argfile launch", got.Jar)
	}
	// Memory lives in user_jvm_args.txt, not here.
	if got.MaxMemoryMB != 0 {
		t.Errorf("MaxMemoryMB = %d, want 0", got.MaxMemoryMB)
	}
}

func TestExecStripped(t *testing.T) {
	got := parseShell(t, "exec java -Xmx4G -jar s.jar\n")
	if got.Jar != "s.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
	if !reflect.DeepEqual(got.Wrappers, []string{"exec"}) {
		t.Errorf("Wrappers = %q", got.Wrappers)
	}
}

func TestNohupAndBackgroundStripped(t *testing.T) {
	got := parseShell(t, "nohup java -Xmx4G -jar s.jar &\n")
	if got.Jar != "s.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
	if !reflect.DeepEqual(got.Wrappers, []string{"nohup", "&"}) {
		t.Errorf("Wrappers = %q, want nohup and &", got.Wrappers)
	}
}

func TestScreenStripped(t *testing.T) {
	got := parseShell(t, "screen -dmS mc java -Xmx4G -jar s.jar --nogui\n")
	if got.Jar != "s.jar" || got.MaxMemoryMB != 4096 {
		t.Errorf("got %+v", got)
	}
	if !reflect.DeepEqual(got.Wrappers, []string{"screen"}) {
		t.Errorf("Wrappers = %q", got.Wrappers)
	}
}

func TestCdToScriptDirectoryIgnored(t *testing.T) {
	// Every variant of "cd to where this script lives" is a no-op for the
	// panel, which starts the process in the instance directory anyway.
	for _, line := range []string{
		`cd "$(dirname "$0")"`,
		`cd $(dirname $0)`,
		`cd "$(cd "$(dirname "$0")" && pwd)"`,
		`cd "${0%/*}"`,
	} {
		got := parseShell(t, line+"\njava -Xmx4G -jar s.jar\n")
		if got.Jar != "s.jar" {
			t.Errorf("%s: Jar = %q", line, got.Jar)
		}
	}
}

func TestConditionalBeforeJavaIsFine(t *testing.T) {
	text := "if [ ! -f eula.txt ]; then\n  echo eula=true > eula.txt\nfi\njava -Xmx4G -jar s.jar\n"
	got := parseShell(t, text)
	if got.Jar != "s.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
}

func TestSemicolonSeparatedCommands(t *testing.T) {
	got := parseShell(t, "echo starting; java -Xmx4G -jar s.jar\n")
	if got.Jar != "s.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
}

func TestTrailingPassthroughIgnored(t *testing.T) {
	got := parseShell(t, "java -Xmx4G -jar s.jar \"$@\"\n")
	if len(got.ServerArgs) != 0 {
		t.Errorf("ServerArgs = %q, want none", got.ServerArgs)
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name string
		text string
		code string
	}{
		{"empty script", "#!/bin/sh\n# 什么都没有\n", "no-java"},
		{"bedrock binary", "#!/bin/sh\nLD_LIBRARY_PATH=. ./bedrock_server\n", "not-java"},
		{"python wrapper", "python3 launcher.py\n", "not-java"},
		{"two different java lines", "java -jar a.jar\njava -jar b.jar\n", "multiple-java"},
		{"command substitution", "java -Xmx$(cat mem.txt) -jar s.jar\n", "command-substitution"},
		{"redirect", "java -Xmx4G -jar s.jar > logs/latest.log 2>&1\n", "redirection"},
		{"pipe", "java -Xmx4G -jar s.jar | tee log.txt\n", "redirection"},
		{"unknown variable in args", "java -Xmx4G -jar $JARFILE\n", "unknown-var"},
		{"sourced file", ". ./config.sh\njava -Xmx4G -jar s.jar\n", "external-source"},
		{"source keyword", "source config.sh\njava -jar s.jar\n", "external-source"},
		{"restart loop", "while true; do\n  java -Xmx4G -jar s.jar\n  sleep 5\ndone\n", "wrapped-in-loop"},
		{"until loop", "until false; do\n  java -jar s.jar\ndone\n", "wrapped-in-loop"},
		{"java inside if", "if [ -f a.jar ]; then\n  java -jar a.jar\nfi\n", "conditional"},
		{"cd elsewhere", "cd /opt/minecraft\njava -jar s.jar\n", "cd-elsewhere"},
		{"classpath launch", "java -cp libs/* net.minecraft.server.Main\n", "classpath-launch"},
		{"main class launch", "java -Xmx4G net.minecraft.server.Main\n", "classpath-launch"},
		{"no jar and no argfile", "java -Xmx4G -XX:+UseG1GC\n", "no-target"},
		{"passthrough in the middle", "java \"$@\" -jar s.jar\n", "passthrough-args"},
		{"tmux quoted command", "tmux new -d 'java -jar s.jar'\n", "not-java"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { refuse(t, c.text, Shell, c.code) })
	}
}

func TestRefusalCarriesLineAndText(t *testing.T) {
	got := refuse(t, "#!/bin/sh\ncd /opt/minecraft\njava -jar s.jar\n", Shell, "cd-elsewhere")
	if got.Line != 2 {
		t.Errorf("Line = %d, want 2", got.Line)
	}
	if got.Text != "cd /opt/minecraft" {
		t.Errorf("Text = %q", got.Text)
	}
	if strings.TrimSpace(got.Reason) == "" {
		t.Error("Reason is empty; a refusal has to say why")
	}
}

func TestIdenticalJavaLinesAreNotAmbiguous(t *testing.T) {
	// Windows scripts often repeat the same launch in two branches. The same
	// command twice is still one answer.
	got := parseShell(t, "java -Xmx4G -jar s.jar\njava -Xmx4G -jar s.jar\n")
	if got.Jar != "s.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
}

func TestBatchScript(t *testing.T) {
	text := "@echo off\r\nrem 启动服务器\r\nset MEM=4G\r\njava -Xmx%MEM% -jar server.jar nogui\r\npause\r\n"
	got, refusals := Parse(text, Batch)
	if len(refusals) > 0 {
		t.Fatalf("unexpected refusal %+v", refusals)
	}
	if got.MaxMemoryMB != 4096 {
		t.Errorf("MaxMemoryMB = %d, want 4096", got.MaxMemoryMB)
	}
	if got.Jar != "server.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
	if !reflect.DeepEqual(got.ServerArgs, []string{"nogui"}) {
		t.Errorf("ServerArgs = %q", got.ServerArgs)
	}
}

func TestBatchDoubleColonComment(t *testing.T) {
	got, refusals := Parse(":: -Xmx8G 这行是注释\r\njava -Xmx4G -jar s.jar\r\n", Batch)
	if len(refusals) > 0 {
		t.Fatalf("unexpected refusal %+v", refusals)
	}
	if got.MaxMemoryMB != 4096 {
		t.Errorf("MaxMemoryMB = %d, want 4096", got.MaxMemoryMB)
	}
}

func TestParseArgvForMigration(t *testing.T) {
	// Existing instances store an argv, not a script: the hand-typed ones go
	// through the same splitting without a tokenizer in front.
	got, refusals := ParseArgv([]string{"java", "-Xms1G", "-Xmx6G", "-jar", "fabric.jar", "--nogui"})
	if len(refusals) > 0 {
		t.Fatalf("unexpected refusal %+v", refusals)
	}
	if got.MinMemoryMB != 1024 || got.MaxMemoryMB != 6144 || got.Jar != "fabric.jar" {
		t.Errorf("got %+v", got)
	}
}

func TestParseArgvRefusesNonJava(t *testing.T) {
	_, refusals := ParseArgv([]string{"./bedrock_server"})
	if len(refusals) == 0 {
		t.Fatal("want a refusal for a non-Java server")
	}
	if refusals[0].Code != "not-java" {
		t.Errorf("Code = %q, want not-java", refusals[0].Code)
	}
}

func TestServerArgsAfterArgFiles(t *testing.T) {
	// Forge's own run.sh ends with "$@", but plenty of people bake the flag in
	// instead. --nogui is a server flag wherever it appears, never a JVM one.
	got := parseShell(t, "java @user_jvm_args.txt @libraries/unix_args.txt --nogui\n")

	if !reflect.DeepEqual(got.ServerArgs, []string{"--nogui"}) {
		t.Errorf("ServerArgs = %q, want [--nogui]", got.ServerArgs)
	}
	if len(got.JVMArgs) != 0 {
		t.Errorf("JVMArgs = %q, want none: --nogui is not a JVM flag", got.JVMArgs)
	}
}

func TestJarAsLastArgument(t *testing.T) {
	got := parseShell(t, "java -Xmx4G -jar server.jar\n")
	if got.Jar != "server.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
	if len(got.ServerArgs) != 0 {
		t.Errorf("ServerArgs = %q, want none", got.ServerArgs)
	}
}

func TestServerArgRepeatingTheJarName(t *testing.T) {
	// A server argument that happens to equal the jar name must survive.
	got := parseShell(t, "java -jar s.jar s.jar\n")
	if got.Jar != "s.jar" {
		t.Errorf("Jar = %q", got.Jar)
	}
	if !reflect.DeepEqual(got.ServerArgs, []string{"s.jar"}) {
		t.Errorf("ServerArgs = %q, want [s.jar]", got.ServerArgs)
	}
}
