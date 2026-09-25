package classify_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pushkar-anand/jocasta/internal/classify"
)

func TestClassLabelCoversEveryClass(t *testing.T) {
	t.Parallel()

	for _, c := range classify.Classes() {
		assert.NotEmptyf(t, c.Label(), "class %q has no label", c)
	}

	assert.Empty(t, classify.Unknown.Label(), "the zero class has no label")
	assert.Empty(t, classify.Class("toaster").Label())
}
