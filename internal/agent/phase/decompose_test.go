package phase

import (
	"testing"

	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

func TestParseDecomposition(t *testing.T) {
	tests := map[string]struct {
		input   string
		want    []SubTask
		wantErr bool
	}{
		"single phase": {
			input: `[{"title":"Add study room","body":"Build it","depends_on":[]}]`,
			want: []SubTask{
				{Title: "Add study room", Body: "Build it"},
			},
		},
		"ordered phases with deps": {
			input: `[{"title":"Foundation","body":"first","depends_on":[]},{"title":"Roof","body":"second","depends_on":[0]}]`,
			want: []SubTask{
				{Title: "Foundation", Body: "first"},
				{Title: "Roof", Body: "second", DependsOn: []int{0}},
			},
		},
		"code-fenced json": {
			input: "```json\n[{\"title\":\"Greendale\",\"body\":\"x\",\"depends_on\":[]}]\n```",
			want: []SubTask{
				{Title: "Greendale", Body: "x"},
			},
		},
		"empty array": {
			input: `[]`,
			want:  nil,
		},
		"out-of-range depends_on dropped": {
			input: `[{"title":"A","body":"x","depends_on":[5,-1]}]`,
			want: []SubTask{
				{Title: "A", Body: "x"},
			},
		},
		"valid and invalid depends_on mixed": {
			input: `[{"title":"A","body":"x","depends_on":[]},{"title":"B","body":"y","depends_on":[9,0,-3]}]`,
			want: []SubTask{
				{Title: "A", Body: "x"},
				{Title: "B", Body: "y", DependsOn: []int{0}},
			},
		},
		"empty title is fatal": {
			input:   `[{"title":"  ","body":"x","depends_on":[]}]`,
			wantErr: true,
		},
		"malformed json": {
			input:   `[{"title": `,
			wantErr: true,
		},
		"not an array": {
			input:   `{"title":"A"}`,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got, err := parseDecomposition(tc.input)
			if tc.wantErr {
				r.Error(err)
				r.Nil(got)
				return
			}
			r.NoError(err)
			r.Equal(tc.want, got)
		})
	}
}

func TestDecomposeEmptyBody(t *testing.T) {
	r := require.New(t)
	for _, body := range []string{"", "   ", "\n\t "} {
		got, err := Decompose(t.Context(), nil, body)
		r.Error(err)
		r.Nil(got)
	}
}

func TestDecomposeSuccess(t *testing.T) {
	r := require.New(t)
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			types.LightweightModels[0]: {
				{Type: "text_delta", Text: `[{"title":"Build the cannon","body":"for the paintball war","depends_on":[]}]`},
			},
		},
	}

	got, err := Decompose(t.Context(), prov, "We need a comprehensive paintball defense system for Greendale.")
	r.NoError(err)
	r.Len(got, 1)
	r.Equal("Build the cannon", got[0].Title)
	r.Len(prov.calls, 1, "should only try first model on success")
}

func TestDecomposeModelFallback(t *testing.T) {
	r := require.New(t)
	r.GreaterOrEqual(len(types.LightweightModels), 2, "need at least 2 models for fallback test")

	lastModel := types.LightweightModels[len(types.LightweightModels)-1]
	prov := &mockProvider{
		responses: map[string][]types.ChatDelta{
			lastModel: {
				{Type: "text_delta", Text: `[{"title":"Troy and Abed in the morning","body":"x","depends_on":[]}]`},
			},
		},
	}

	got, err := Decompose(t.Context(), prov, "Some large multi-phase issue body.")
	r.NoError(err)
	r.Len(got, 1)
	r.Len(prov.calls, len(types.LightweightModels), "should try every model until one succeeds")
}

func TestDecomposeAllModelsFail(t *testing.T) {
	r := require.New(t)
	prov := &mockProvider{responses: map[string][]types.ChatDelta{}}

	got, err := Decompose(t.Context(), prov, "Some issue body.")
	r.Error(err)
	r.Nil(got)
	r.Len(prov.calls, len(types.LightweightModels))
}

func TestExtractIssueNumberFromURL(t *testing.T) {
	tests := map[string]struct {
		input string
		want  int
	}{
		"plain url":        {input: "https://github.com/greendale/community/issues/42", want: 42},
		"trailing newline": {input: "https://github.com/greendale/community/issues/7\n", want: 7},
		"no number":        {input: "https://github.com/greendale/community", want: 0},
		"empty":            {input: "", want: 0},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, extractIssueNumberFromURL(tc.input))
		})
	}
}
