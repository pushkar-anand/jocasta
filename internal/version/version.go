// Package version reports which build of jocasta is running.
//
// A release build has the tag, commit and date linked in with -ldflags -X. Any
// other build has none of that, so the values are read back from the metadata
// the Go toolchain stamps into every binary instead.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
)

// Linked in at release time. See .goreleaser.yaml and the Dockerfile.
var (
	tag    string
	commit string
	date   string
)

// Info is the resolved build information for the running binary.
type Info struct {
	// Version is the release tag ("1.4.0"), the module version a `go install`
	// recorded, or "dev" when neither is known.
	Version string
	// Commit is the full revision the binary was built from, or "" if unknown.
	Commit string
	// Date is when the binary was built, in RFC 3339, or "" if unknown.
	Date string
	// Modified is true when the build tree had uncommitted changes.
	Modified bool
}

// Get resolves the build information, preferring what a release linked in and
// falling back to the toolchain's own build metadata.
func Get() Info {
	i := Info{Version: tag, Commit: commit, Date: date}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		if i.Version == "" {
			i.Version = "dev"
		}

		return i
	}

	if i.Version == "" {
		// Since Go 1.24 a plain `go build` stamps a matching semver tag here;
		// otherwise this is a pseudo-version or the literal "(devel)".
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			i.Version = v
		} else {
			i.Version = "dev"
		}
	}

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if i.Commit == "" {
				i.Commit = s.Value
			}
		case "vcs.time":
			if i.Date == "" {
				i.Date = s.Value
			}
		case "vcs.modified":
			i.Modified, _ = strconv.ParseBool(s.Value)
		}
	}

	return i
}

// String renders the information over a few lines, for `jocasta version`.
func (i Info) String() string {
	v := i.Version
	if i.Modified {
		v += " (modified)"
	}

	s := "jocasta " + v

	if i.Commit != "" {
		s += "\ncommit  " + shortCommit(i.Commit)
	}

	if i.Date != "" {
		s += "\nbuilt   " + i.Date
	}

	s += fmt.Sprintf("\ngo      %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	return s
}

// shortCommit trims a full revision to the first 12 characters, the length
// git uses for an unambiguous abbreviation in most repositories.
func shortCommit(c string) string {
	const n = 12
	if len(c) <= n {
		return c
	}

	return c[:n]
}
