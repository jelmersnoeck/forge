package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLightweightModelsNotEmpty(t *testing.T) {
	r := require.New(t)
	r.NotEmpty(LightweightModels, "LightweightModels must have at least one model")
	for _, m := range LightweightModels {
		r.NotEmpty(m, "model name must not be empty")
		r.True(strings.HasPrefix(m, "claude-"), "model %q should start with claude-", m)
	}
}
