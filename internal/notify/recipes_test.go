package notify_test

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pushkar-anand/jocasta/internal/notify"
)

// recipeBody matches a single-quoted body line in a YAML example. Two single
// quotes inside it are YAML's escaped quote.
var recipeBody = regexp.MustCompile(`(?m)^\s*body: '((?:[^']|'')*)'`)

// Every http body the docs and the example config show must pass the check
// the server runs when it starts, so a recipe cannot rot unnoticed.
func TestRecipesInTheDocsRender(t *testing.T) {
	repo := os.DirFS("../..")

	for _, path := range []string{"docs/setup.md", "jocasta.example.yaml"} {
		raw, err := fs.ReadFile(repo, path)
		require.NoError(t, err)

		bodies := recipeBody.FindAllStringSubmatch(string(raw), -1)
		require.NotEmpty(t, bodies, "%s shows no http body", path)

		for _, b := range bodies {
			body := strings.ReplaceAll(b[1], "''", "'")

			_, err := notify.NewDestination("recipe", notify.Config{HTTP: &notify.HTTP{
				URL: "https://hooks.example.com/x", Body: body,
			}})
			assert.NoError(t, err, "%s: %s", path, body)
		}
	}
}
