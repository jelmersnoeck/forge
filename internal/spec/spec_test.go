package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jelmersnoeck/forge/internal/config"
	"github.com/jelmersnoeck/forge/internal/types"
	"github.com/stretchr/testify/require"
)

const sampleSpec = `---
id: paintball-defense
status: active
---
# Campus-wide paintball defense system

## Description
Greendale Community College needs an automated paintball defense grid
to prevent future paintball assassin games from spiraling out of control.

## Context
- security/grid.go — main defense controller
- campus/zones.go — zone definitions for each building
- alerts/notify.go — notification system for Dean Pelton

## Behavior
- Troy Barnes can arm/disarm all zones from the student lounge
- Abed Nadir monitors the grid via a tablet dashboard
- The system announces "PEW PEW PEW" over the PA when triggered

## Constraints
- Must not use live ammunition (Señor Chang's suggestion rejected)
- Cannot lock the study room — Jeff Winger needs access at all times
- Avoid dependency on City College infrastructure

## Interfaces
` + "```go" + `
type DefenseGrid struct {
    Zones    []Zone
    Armed    bool
    Operator string // must be a Greendale student
}

func (g *DefenseGrid) Arm(operator string) error
func (g *DefenseGrid) Disarm() error
func (g *DefenseGrid) Status() GridStatus
` + "```" + `

## Edge Cases
- What if the Dean declares a school-wide pillow fight during armed mode?
- Multiple operators trying to arm/disarm simultaneously
- Chang somehow gets root access (he will try)
`

func TestParseSpec(t *testing.T) {
	tests := map[string]struct {
		content    string
		wantID     string
		wantStatus string
		wantHeader string
		wantDesc   string
		wantErr    bool
	}{
		"full spec": {
			content:    sampleSpec,
			wantID:     "paintball-defense",
			wantStatus: "active",
			wantHeader: "Campus-wide paintball defense system",
			wantDesc:   "Greendale Community College needs an automated paintball defense grid\nto prevent future paintball assassin games from spiraling out of control.",
		},
		"minimal spec without frontmatter": {
			content:    "# Just a header\n\n## Description\nBrief.",
			wantID:     "",
			wantStatus: "draft",
			wantHeader: "Just a header",
			wantDesc:   "Brief.",
		},
		"spec with only frontmatter": {
			content:    "---\nid: empty-spec\nstatus: draft\n---\n# Empty spec",
			wantID:     "empty-spec",
			wantStatus: "draft",
			wantHeader: "Empty spec",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "spec.md")
			r.NoError(os.WriteFile(path, []byte(tc.content), 0o644))

			doc, err := ParseSpec(path)
			if tc.wantErr {
				r.Error(err)
				return
			}

			r.NoError(err)
			r.Equal(tc.wantID, doc.ID)
			r.Equal(tc.wantStatus, doc.Status)
			r.Equal(tc.wantHeader, doc.Header)
			if tc.wantDesc != "" {
				r.Equal(tc.wantDesc, doc.Description)
			}
		})
	}
}

func TestParseSpec_allSections(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.md")
	r.NoError(os.WriteFile(path, []byte(sampleSpec), 0o644))

	doc, err := ParseSpec(path)
	r.NoError(err)

	r.Contains(doc.Context, "security/grid.go")
	r.Contains(doc.Behavior, "Troy Barnes")
	r.Contains(doc.Constraints, "Señor Chang")
	r.Contains(doc.Interfaces, "DefenseGrid")
	r.Contains(doc.EdgeCases, "Chang somehow gets root access")
}

func TestParseSpec_alternatives(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	specWithAlts := `---
id: blanket-fort
status: draft
---
# Blanket fort architectural review

## Description
Troy and Abed need to decide on blanket fort construction strategy.

## Alternatives
- Use pillows instead (rejected — leads to pillow war with City College)
- Outsource to Subway (rejected — corporate infiltration risk)
- Let the Dean design it (rejected — too many dalmatian prints)
`
	path := filepath.Join(dir, "blanket-fort.md")
	r.NoError(os.WriteFile(path, []byte(specWithAlts), 0o644))

	doc, err := ParseSpec(path)
	r.NoError(err)
	r.Contains(doc.Alternatives, "pillows instead")
	r.Contains(doc.Alternatives, "dalmatian prints")

	// Verify specs without Alternatives still parse fine.
	origPath := filepath.Join(dir, "paintball.md")
	r.NoError(os.WriteFile(origPath, []byte(sampleSpec), 0o644))

	origDoc, err := ParseSpec(origPath)
	r.NoError(err)
	r.Empty(origDoc.Alternatives)
	r.Contains(origDoc.EdgeCases, "Chang somehow gets root access")
}

func TestParseSpec_missingFile(t *testing.T) {
	r := require.New(t)
	_, err := ParseSpec("/nonexistent/troy-barnes.md")
	r.Error(err)
}

func TestLoadSpecs(t *testing.T) {
	tests := map[string]struct {
		files     map[string]string
		wantCount int
		wantIDs   []string
	}{
		"empty directory": {
			files:     map[string]string{},
			wantCount: 0,
		},
		"multiple specs": {
			files: map[string]string{
				"paintball.md": "---\nid: paintball\nstatus: active\n---\n# Paintball episode",
				"pillow.md":    "---\nid: pillow-fight\nstatus: draft\n---\n# Pillow fort wars",
			},
			wantCount: 2,
			wantIDs:   []string{"paintball", "pillow-fight"},
		},
		"ignores non-md files": {
			files: map[string]string{
				"paintball.md": "---\nid: paintball\nstatus: active\n---\n# Paintball",
				"README.txt":   "not a spec",
				"notes.json":   "{}",
			},
			wantCount: 1,
			wantIDs:   []string{"paintball"},
		},
		"ignores subdirectories": {
			files: map[string]string{
				"paintball.md": "---\nid: paintball\nstatus: active\n---\n# Paintball",
			},
			wantCount: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			dir := t.TempDir()

			for name, content := range tc.files {
				r.NoError(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
			}

			specs, err := LoadSpecs(dir)
			r.NoError(err)
			r.Len(specs, tc.wantCount)

			if tc.wantIDs != nil {
				gotIDs := make([]string, len(specs))
				for i, s := range specs {
					gotIDs[i] = s.ID
				}
				r.ElementsMatch(tc.wantIDs, gotIDs)
			}
		})
	}
}

func TestLoadSpecs_nonexistentDir(t *testing.T) {
	r := require.New(t)
	specs, err := LoadSpecs("/nonexistent/greendale")
	r.NoError(err)
	r.Nil(specs)
}

func TestLoadSpecs_populatesDescription(t *testing.T) {
	r := require.New(t)
	dir := t.TempDir()

	content := "---\nid: paintball\nstatus: active\n---\n# Campus-wide paintball\n\n## Description\nGreendale needs a defense grid for paintball episodes.\n\n## Behavior\nTroy arms the grid."
	r.NoError(os.WriteFile(filepath.Join(dir, "paintball.md"), []byte(content), 0o644))

	specs, err := LoadSpecs(dir)
	r.NoError(err)
	r.Len(specs, 1)
	r.Equal("Greendale needs a defense grid for paintball episodes.", specs[0].Description)
}

func TestFormatSpecIndex(t *testing.T) {
	tests := map[string]struct {
		specs []types.SpecEntry
		want  string
	}{
		"empty specs": {
			specs: nil,
			want:  "",
		},
		"single spec": {
			specs: []types.SpecEntry{
				{ID: "paintball-defense", Status: "active", Header: "Campus-wide paintball defense system"},
			},
			want: "Existing Specs:\n\n- **paintball-defense** (active): Campus-wide paintball defense system\n",
		},
		"multiple specs sorted by ID": {
			specs: []types.SpecEntry{
				{ID: "study-room", Status: "implemented", Header: "Study room booking"},
				{ID: "blanket-fort", Status: "draft", Header: "Blanket fort construction"},
				{ID: "paintball", Status: "active", Header: "Paintball defense grid"},
			},
			want: "Existing Specs:\n\n- **blanket-fort** (draft): Blanket fort construction\n- **paintball** (active): Paintball defense grid\n- **study-room** (implemented): Study room booking\n",
		},
		"includes all statuses": {
			specs: []types.SpecEntry{
				{ID: "active-spec", Status: "active", Header: "Active one"},
				{ID: "draft-spec", Status: "draft", Header: "Draft one"},
				{ID: "implemented-spec", Status: "implemented", Header: "Done one"},
				{ID: "superseded-spec", Status: "superseded", Header: "Dead one"},
			},
			want: "Existing Specs:\n\n- **active-spec** (active): Active one\n- **draft-spec** (draft): Draft one\n- **implemented-spec** (implemented): Done one\n- **superseded-spec** (superseded): Dead one\n",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := FormatSpecIndex(tc.specs)
			r.Equal(tc.want, got)
		})
	}
}

func TestFindSpecsDir(t *testing.T) {
	tests := map[string]struct {
		cwd  string
		cfg  config.ForgeConfig
		want string
	}{
		"default": {
			cwd:  "/projects/greendale",
			cfg:  config.ForgeConfig{},
			want: "/projects/greendale/.forge/specs",
		},
		"config override relative": {
			cwd:  "/projects/greendale",
			cfg:  config.ForgeConfig{SpecsDir: "docs/specs"},
			want: "/projects/greendale/docs/specs",
		},
		"config override absolute": {
			cwd:  "/projects/greendale",
			cfg:  config.ForgeConfig{SpecsDir: "/shared/specs"},
			want: "/shared/specs",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			got := FindSpecsDir(tc.cwd, tc.cfg)
			r.Equal(tc.want, got)
		})
	}
}
