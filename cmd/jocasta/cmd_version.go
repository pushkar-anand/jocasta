package main

import (
	"fmt"
	"os"

	"github.com/pushkar-anand/jocasta/internal/version"
)

// VersionCmd prints the build information and exits. main runs it before
// loading config or opening the database, since it needs neither.
type VersionCmd struct{}

func (c *VersionCmd) Run() error {
	_, err := fmt.Fprintln(os.Stdout, version.Get().String())

	return err
}
