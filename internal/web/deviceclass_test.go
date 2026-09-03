package web

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pushkar-anand/jocasta/internal/classify"
)

func TestClassLabelCoversEveryClass(t *testing.T) {
	t.Parallel()

	for _, c := range classify.Classes() {
		assert.NotEmptyf(t, classLabel(c), "class %q has no label", c)
	}

	assert.Empty(t, classLabel(classify.Unknown), "the zero class has no label")
	assert.Empty(t, classLabel(classify.Class("toaster")))
}

func TestClassIconCoversEveryClass(t *testing.T) {
	t.Parallel()

	for _, c := range classify.Classes() {
		assert.NotEmptyf(t, classIcon(c), "class %q has no glyph", c)
	}

	assert.Empty(t, classIcon(classify.Unknown), "an unclassified device shows no icon")
}

func TestClassChoicesMatchesTheVocabulary(t *testing.T) {
	t.Parallel()

	choices := classChoices()

	assert.Len(t, choices, len(classify.Classes()))

	var (
		values []classify.Class
		labels []string
	)

	for _, ch := range choices {
		assert.Equal(t, classLabel(ch.Value), ch.Label)
		assert.NotEqual(t, classify.Unknown, ch.Value, "the blank option is the template's to add")

		values = append(values, ch.Value)
		labels = append(labels, ch.Label)
	}

	assert.ElementsMatch(t, classify.Classes(), values, "every class is offered exactly once")
	assert.IsIncreasing(t, labels, "the picker is ordered by label")
}
