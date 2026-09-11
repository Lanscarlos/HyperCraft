// Package launchscript reads an existing server's start script and works out
// the launch settings hiding inside it, so the panel can take the command line
// over rather than execute somebody else's shell.
//
// It never runs anything. The whole point of importing a server that has been
// running by hand for a year is that nothing happens to it, and a "just run it
// with a fake java on PATH" probe would trade that promise for accuracy the
// preview does not need: whatever comes out of here is a draft the operator
// confirms, not a verdict.
//
// Refusing is a first-class answer. A script this cannot take apart with
// certainty is reported with the line that defeated it, never guessed at —
// half a command line is worse than none, because it starts.
package launchscript

// Dialect selects the syntax to read. The caller knows it from the file's
// extension.
type Dialect int

const (
	Shell Dialect = iota
	Batch
)

// Result is the launch configuration drafted from a script.
type Result struct {
	// Java is the executable, as written: "java" for a PATH lookup, an
	// absolute path, or a path relative to the instance directory, which the
	// caller resolves. Empty when JavaVar says it has to be asked for.
	Java string
	// JavaVar names the environment variable a script expected to supply the
	// JVM when nothing in the script defines it. The panel does not read its
	// own environment for this: the answer would be right on the machine that
	// happens to export it and silently wrong everywhere else.
	JavaVar     string
	MinMemoryMB int
	MaxMemoryMB int
	JVMArgs     []string
	// Jar and ArgFiles are the two launch targets, and exactly one is set.
	// Forge and NeoForge from 1.17 have no runnable jar at all — their run.sh
	// passes @user_jvm_args.txt and an @unix_args.txt instead.
	Jar        string
	ArgFiles   []string
	ServerArgs []string
	// Javaw records that the script launched the console-less JVM. The panel
	// needs the console, so Java says "java"; the preview says why.
	Javaw bool
	// Wrappers are the shells-around-the-shell that were stripped: exec,
	// nohup, screen, a trailing &. Reported rather than silently dropped,
	// because "the panel will run this in the foreground instead" is a change
	// the operator should hear about before it happens.
	Wrappers []string
}

// Refusal is one reason a script could not be taken apart, with the line that
// caused it so the operator can find it in their own file.
type Refusal struct {
	Code   string
	Reason string
	Line   int
	Text   string
}
