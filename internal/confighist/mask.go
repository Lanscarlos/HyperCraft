package confighist

import "strings"

// Secrets in the diff.
//
// server.properties holds the rcon password, DiscordSRV holds a bot token,
// LuckPerms and CoreProtect hold database credentials — all of it is
// configuration, so all of it is in the history. The design's §7 decides not
// to encrypt (a local, never-pushed repository does not earn a key management
// story) and to make up for it in two ways: nothing can leave the machine, and
// nothing is shown by accident. This is the second half — a credential is
// masked until the operator clicks it.

// sensitiveKeys are matched as substrings of a lowercased config key.
//
// Deliberately a little greedy. A false positive costs one click; a false
// negative puts a bot token on screen behind whoever is standing there while
// an operator reads a diff on a shared screen.
var sensitiveKeys = []string{
	"password",
	"passwd",
	"token",
	"secret",
	"credential",
	"private-key",
	"privatekey",
}

const maskText = "••••••"

// decorate fills in the masking fields of a diff line.
func decorate(line DiffLine) DiffLine {
	if masked, ok := maskLine(line.Text); ok {
		line.Sensitive = true
		line.Masked = masked
	}
	return line
}

// maskLine hides the value of a key/value line whose key names a credential.
// It handles the four shapes the collected files come in — properties, YAML,
// JSON and TOML — by looking for the first separator rather than by parsing,
// which is the right trade for something whose only job is to blur a value.
func maskLine(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, " \t-")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return "", false
	}

	cut := strings.IndexAny(text, "=:")
	if cut < 0 || cut == len(text)-1 {
		return "", false
	}

	key := strings.ToLower(strings.Trim(text[:cut], " \t-\"'"))
	if key == "" {
		return "", false
	}
	matched := false
	for _, needle := range sensitiveKeys {
		if strings.Contains(key, needle) {
			matched = true
			break
		}
	}
	if !matched {
		return "", false
	}

	value := text[cut+1:]
	spaces := len(value) - len(strings.TrimLeft(value, " \t"))
	body := strings.TrimSpace(value)
	if body == "" || body == "\"\"" || body == "''" {
		// An empty credential is not a secret, and blurring it would hide the
		// one thing worth seeing: that it is blank.
		return "", false
	}

	// Keep a trailing comma so a masked JSON line still reads as JSON.
	suffix := ""
	if strings.HasSuffix(body, ",") {
		suffix = ","
	}
	return text[:cut+1] + value[:spaces] + maskText + suffix, true
}
