package scanner

import (
	"errors"

	"golang.org/x/sys/windows"
)

// refused reports whether a dial failed because the host refused the
// connection, which proves the host is up. Windows reports a refusal as
// WSAECONNREFUSED, which syscall.ECONNREFUSED does not match there.
func refused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
