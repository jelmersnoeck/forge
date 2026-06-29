package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractIssueNumberFromURL(t *testing.T) {
	tests := map[string]struct {
		url  string
		want int
	}{
		"full url":      {"https://github.com/jelmersnoeck/forge/issues/224", 224},
		"trailing path": {"https://github.com/o/r/issues/42/", 42},
		"no number":     {"https://github.com/o/r/pulls", 0},
		"empty":         {"", 0},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, extractIssueNumberFromURL(tc.url))
		})
	}
}

func TestBranchToSessionSuffix(t *testing.T) {
	require.Equal(t, "jelmer-build-greendale-3", branchToSessionSuffix("jelmer/build-greendale-3"))
	require.Equal(t, "plain", branchToSessionSuffix("plain"))
}
