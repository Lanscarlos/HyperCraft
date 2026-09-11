package instance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/launchscript"
)

// Migrating off script mode.
//
// The panel used to run the operator's own start script. It no longer does:
// launch settings the panel cannot read are launch settings it cannot manage,
// and a memory field that silently changes nothing is worse than no field.
//
// That leaves the instances already configured that way, and they must not
// simply stop working. Each is read once and converted into the settings the
// panel builds a command line from. What cannot be converted is flagged rather
// than guessed at: an instance that refuses to start and says why costs its
// owner five minutes, and one that starts with the wrong flags costs them an
// afternoon and possibly a world.

// migrateRetiredCommand converts a config still carrying the retired Command
// into one the panel builds the command line for. The bool reports whether
// anything changed, and is false for every config that never used script mode.
func migrateRetiredCommand(cfg Config) (Config, bool) {
	if len(cfg.Command) == 0 {
		return cfg, false
	}
	original := slices.Clone(cfg.Command)
	cfg.Command = nil

	if result, refusals := parseRetired(cfg, original); len(refusals) == 0 {
		if migrated, err := applyLaunch(cfg, result); err == nil {
			return migrated, true
		}
	}

	// Command is cleared either way — the panel will not run it again — so the
	// argv is kept where the launch settings page can show it. Without that,
	// the upgrade would take away both the launch and the record of it.
	cfg.LegacyCommand = original
	cfg.NeedsLaunchSetup = true
	return cfg, true
}

// parseRetired reads the launch out of whichever of the two shapes this
// instance was in: an argv pointing at a script, or an argv typed by hand.
func parseRetired(cfg Config, command []string) (launchscript.Result, []launchscript.Refusal) {
	if text, dialect, ok := retiredScript(cfg.Directory, command); ok {
		return launchscript.Parse(text, dialect)
	}
	return launchscript.ParseArgv(command)
}

// retiredScript finds the first element of the argv that is a readable text
// file inside the instance directory. "/bin/sh start.sh" puts the interpreter
// first and the script second, so this is not simply Command[0].
func retiredScript(dir string, command []string) (string, launchscript.Dialect, bool) {
	for _, argv := range command {
		argv = strings.TrimSpace(argv)
		if argv == "" || strings.HasPrefix(argv, "-") {
			continue
		}
		rel := argv
		if filepath.IsAbs(argv) {
			inside, err := filepath.Rel(dir, argv)
			if err != nil || strings.HasPrefix(inside, "..") {
				continue
			}
			rel = inside
		}
		rel = filepath.ToSlash(filepath.Clean(rel))
		if strings.HasPrefix(rel, "../") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		// A Bedrock binary is also a file in the right place with the right
		// name. Reading it as a script would produce nonsense rather than the
		// refusal its owner needs to see.
		if bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		dialect := launchscript.Shell
		if ext := strings.ToLower(filepath.Ext(rel)); ext == ".bat" || ext == ".cmd" {
			dialect = launchscript.Batch
		}
		return string(data), dialect, true
	}
	return "", launchscript.Shell, false
}

// applyLaunch writes a parsed launch into a config, or says why it will not.
func applyLaunch(cfg Config, result launchscript.Result) (Config, error) {
	// The panel does not read its own environment for this. $JAVA_HOME is
	// right on the machine that exports it and silently wrong everywhere else,
	// and picking a JVM for somebody's modded server is not a coin worth
	// flipping.
	if result.JavaVar != "" {
		return cfg, fmt.Errorf("%w: the script took its JVM from $%s", ErrInvalidConfig, result.JavaVar)
	}
	if result.Java != "" {
		cfg.Java = result.Java
	}
	cfg.MinMemoryMB, cfg.MaxMemoryMB = result.MinMemoryMB, result.MaxMemoryMB
	cfg.JVMArgs, cfg.ServerArgs = result.JVMArgs, result.ServerArgs
	cfg.Jar, cfg.ArgFiles = result.Jar, result.ArgFiles
	cfg.LegacyCommand, cfg.NeedsLaunchSetup = nil, false

	// The safety net for everything the parser is not in a position to judge:
	// a jar outside the instance directory, an argfile that escapes it. Those
	// belong on the "tell the operator" path, not in a stored config that only
	// fails at the next start.
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
