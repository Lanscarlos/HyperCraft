//go:build !windows

package javaruntime

// isLocked reports whether err says the file is busy rather than wrong. Unix
// renames a directory happily while it is open, so nothing here is transient.
func isLocked(error) bool { return false }
