package config_test

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/config"
)

// referenceRow matches the key cell that opens each table row of the
// configuration reference.
var referenceRow = regexp.MustCompile("(?m)^\\| `([a-z_.<>]+)` \\|")

// TestReferenceDocumentsEveryKey fails when a setting in Config has no row in
// the configuration reference. It also fails when a row names a setting that
// Config no longer has.
func TestReferenceDocumentsEveryKey(t *testing.T) {
	raw, err := os.ReadFile("../../docs/reference/configuration.md")
	require.NoError(t, err)

	var documented []string
	for _, m := range referenceRow.FindAllStringSubmatch(string(raw), -1) {
		documented = append(documented, m[1])
	}

	keys := settingKeys(reflect.TypeFor[config.Config](), "")

	for _, k := range keys {
		assert.Contains(t, documented, k, "docs/reference/configuration.md has no row for %s", k)
	}

	for _, k := range documented {
		assert.Contains(t, keys, k, "docs/reference/configuration.md documents %s, which Config does not have", k)
	}
}

// settingKeys returns the dotted koanf path of every leaf setting under t. The
// key of a map of structs is a name that the user chooses. It is <instance>
// under plugins and <name> under notify. Any other map is a leaf.
func settingKeys(t reflect.Type, prefix string) []string {
	var keys []string

	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("koanf"), ",")
		if tag == "" && !strings.Contains(f.Tag.Get("koanf"), "squash") {
			continue
		}

		path := prefix
		if tag != "" {
			path = join(prefix, tag)
		}

		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}

		switch {
		case ft.Kind() == reflect.Struct:
			keys = append(keys, settingKeys(ft, path)...)
		case ft.Kind() == reflect.Map && ft.Elem().Kind() == reflect.Struct:
			name := "<instance>"
			if path == "notify" {
				name = "<name>"
			}

			keys = append(keys, settingKeys(ft.Elem(), join(path, name))...)
		default:
			keys = append(keys, path)
		}
	}

	slices.Sort(keys)

	return keys
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}
