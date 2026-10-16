package scanner

import (
	"errors"

	"golang.org/x/sys/windows"
)

// refused reports whether a dial failed because the host refused the
// connection. This proves that the host is up. Windows reports a refusal as
// WSAECONNREFUSED. There, syscall.ECONNREFUSED does not match it.
func refused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
