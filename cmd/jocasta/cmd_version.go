package main

import (
	"fmt"
	"os"

	"github.com/pushkar-anand/jocasta/internal/version"
)

// VersionCmd prints the build information and exits. main dispatches it before
// it loads the configuration or opens the database, neither of which it needs.
type VersionCmd struct{}

func (c *VersionCmd) Run() error {
	_, err := fmt.Fprintln(os.Stdout, version.Get().String())

	return err
}
