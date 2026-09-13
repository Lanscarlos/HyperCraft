package javaruntime

import (
	"context"
	"strings"
)

// MigrateInstanceJava registers every java path the instances are already
// launching with, so that tightening the instance form to "pick a registered
// one" does not invalidate every instance that existed before the registry.
//
// These paths are not guesses. Each one is what a server on this machine is
// configured to run right now, which is more certain than anything detection
// could tell us — so they are recorded without asking, unlike a java merely
// found on PATH, which the operator confirms before it joins the list.
//
// Returns how many were added. Errors on individual entries are swallowed on
// purpose: this runs during startup, and a registry that ends up one row short
// costs a dropdown entry, while a startup that fails costs every server.
func MigrateInstanceJava(ctx context.Context, store *Store, reg *Registry, javaPaths []string) int {
	if reg == nil {
		return 0
	}

	known := make(map[string]bool)
	if available, err := AvailableList(store, reg); err == nil {
		for _, entry := range available {
			known[entry.JavaPath] = true
		}
	} else {
		for _, entry := range reg.List() {
			known[entry.JavaPath] = true
		}
	}

	added := 0
	for _, path := range javaPaths {
		path = strings.TrimSpace(path)
		if path == "" || known[path] {
			continue
		}
		known[path] = true

		entry := Entry{JavaPath: path, AddedBy: AddedMigrated}
		// A path that will not answer `java -version` is still recorded, with
		// the version left blank. AvailableList marks it unusable, which is a
		// far better thing for the page to show than a version nobody read.
		if probed, ok := probe(ctx, path); ok {
			entry.Vendor, entry.Version, entry.Major = probed.Vendor, probed.Version, probed.Major
		}
		if _, err := reg.Add(entry); err == nil {
			added++
		}
	}
	return added
}
