package serverfiles

import (
	"fmt"
	"path"
	"strings"
)

// Confining a browser to part of an instance directory.
//
// This is the one boundary in the panel that is finer than a whole server:
// "let them edit plugins/MyPlugin/ and nothing else". It is enforced here
// rather than in the handlers because every file operation already funnels
// through one path-cleaning step, and a rule enforced at the funnel cannot be
// missed by a route somebody adds later. TestEveryMethodResolvesThroughScope
// keeps it that way.
//
// What it is worth is exactly what os.Root gives it. The prefixes are compared
// against the cleaned relative path, which has already had "..", absolute
// paths and backslashes taken out of it, and the open that follows is still
// confined to the instance directory by the kernel. So this narrows an already
// safe path; it is not the thing standing between a request and /etc/shadow.
//
// What it is NOT worth: anything at all, if the account also holds a
// capability that runs code. Somebody who can install a plugin or change the
// launch command reaches the whole machine and this rule with it. See
// docs/security.md — the role editor says so where an operator can read it.

// Restrict returns a browser confined to the given path prefixes.
//
// An empty list means unrestricted, which is what every role had before this
// existed and what an administrator has. Prefixes are cleaned the same way
// request paths are, so "plugins/MyPlugin", "/plugins/MyPlugin/" and
// "plugins//MyPlugin" are one rule rather than three.
func (b *Browser) Restrict(prefixes []string) *Browser {
	cleaned := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		name, err := clean(prefix)
		if err != nil || name == "." {
			// "." is the whole instance, which is not a restriction — a rule
			// that silently means "everything" is worse than no rule, so it is
			// dropped and the caller's other prefixes still apply.
			continue
		}
		cleaned = append(cleaned, name)
	}
	return &Browser{dir: b.dir, scope: cleaned}
}

// Scope returns the prefixes this browser is confined to, empty when it is not.
func (b *Browser) Scope() []string { return b.scope }

// resolve cleans a request path and refuses one outside the scope.
//
// Every method that touches a file goes through this rather than through clean
// directly. The difference between the two is the whole feature.
func (b *Browser) resolve(rel string) (string, error) {
	name, err := clean(rel)
	if err != nil {
		return "", err
	}
	if b.within(name) {
		return name, nil
	}
	// Not found rather than not allowed, and for the usual reason: a refusal
	// that distinguishes the two tells the caller which paths exist.
	return "", fmt.Errorf("%w: %s", ErrNotFound, rel)
}

// resolveDir is resolve for the operations that have to be able to walk down
// to the scope: listing "/" when the rule is "plugins/MyPlugin" has to work,
// or the file manager has no way of reaching the one directory it may open.
//
// The listing itself is filtered — see keep — so an ancestor directory shows
// the way down and nothing else.
func (b *Browser) resolveDir(rel string) (string, error) {
	name, err := clean(rel)
	if err != nil {
		return "", err
	}
	if b.within(name) || b.leadsTo(name) {
		return name, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, rel)
}

// within reports whether a cleaned path is inside the scope.
func (b *Browser) within(name string) bool {
	if len(b.scope) == 0 {
		return true
	}
	for _, prefix := range b.scope {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

// leadsTo reports whether a cleaned path is an ancestor of something in scope,
// which is what makes it navigable but not readable.
func (b *Browser) leadsTo(name string) bool {
	if len(b.scope) == 0 {
		return true
	}
	if name == "." {
		return true
	}
	for _, prefix := range b.scope {
		if strings.HasPrefix(prefix, name+"/") {
			return true
		}
	}
	return false
}

// keep reports whether one entry of a listing should be shown. Entries that
// are neither in scope nor on the way to it are left out entirely rather than
// shown greyed: a directory listing is not a place to advertise what exists.
func (b *Browser) keep(dir, name string) bool {
	if len(b.scope) == 0 {
		return true
	}
	full := name
	if dir != "." {
		full = path.Join(dir, name)
	}
	return b.within(full) || b.leadsTo(full)
}

// Allows reports whether a path may be written to under this browser's scope.
//
// It exists for the UI: a directory that is only on the way down to the scope
// is listable but not writable, and offering an 上传文件 button there is a
// promise the next request breaks. The answer is the browser's own, so the
// front end does not carry a second copy of the prefix rules.
func (b *Browser) Allows(rel string) bool {
	name, err := clean(rel)
	if err != nil {
		return false
	}
	return b.within(name)
}
