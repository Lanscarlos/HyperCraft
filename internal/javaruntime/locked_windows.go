//go:build windows

package javaruntime

import (
	"errors"
	"syscall"
)

// Windows' answer to "something else has this open". Neither constant is in
// package syscall, and both come back from a rename of a directory that a
// virus scanner or an indexer is still reading.
const (
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// isLocked reports whether err says the file is busy rather than wrong.
func isLocked(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	// ERROR_ACCESS_DENIED is included because Windows reports a rename blocked
	// by a delete still pending on the target that way.
	return errno == errorSharingViolation || errno == errorLockViolation ||
		errno == syscall.ERROR_ACCESS_DENIED
}
