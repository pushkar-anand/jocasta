//go:build !windows

package scanner

import (
	"errors"
	"syscall"
)

// refused reports whether a dial failed because the host refused the
// connection, which proves the host is up.
func refused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
