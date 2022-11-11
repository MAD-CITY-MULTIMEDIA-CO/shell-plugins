package example

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlugin(t *testing.T) {
	_, report := New().Validate()
	for _, c := range *report.Checks {
		assert.Equal(t, true, c.Assertion)
	}
}
