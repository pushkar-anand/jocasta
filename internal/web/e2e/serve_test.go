//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/signal"
	"testing"
)

// TestServe serves one fixture until interrupted, to look at by hand:
//
//	E2E_SERVE=weird go test -tags e2e -run TestServe -timeout 0 ./internal/web/e2e/
//
// Sign in as e2e-admin, e2e-writer or e2e-reader with testPassword.
func TestServe(t *testing.T) {
	name := os.Getenv("E2E_SERVE")
	if name == "" {
		t.Skip("E2E_SERVE names the fixture to serve")
	}

	var f fixture

	for _, c := range []fixture{fixtureEmpty, fixtureNormal, fixtureWeird, fixtureFresh} {
		if c.name == name {
			f = c
		}
	}

	if f.name == "" {
		t.Fatalf("no fixture %q", name)
	}

	a, stop, err := startApp(t.Context(), t.TempDir(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	fmt.Printf("%s fixture at %s, password %s\n", f.name, a.srv.URL, testPassword)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
