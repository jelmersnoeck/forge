package repomap

import (
	"regexp"
	"slices"
	"strings"
)

// identRe matches identifier tokens used to detect cross-file symbol references.
var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// rank scores each file by PageRank over the symbol-reference graph: an edge
// A→B exists when file A mentions a symbol defined in file B. Scores are used to
// order files in the budgeted output. Deterministic given identical input.
//
//	┌───────┐   references sym of   ┌───────┐
//	│ file A│ ────────────────────▶ │ file B│
//	└───────┘                       └───────┘
//	  rank flows from A into B (B is "important")
func rank(entries []FileEntry) map[string]float64 {
	n := len(entries)
	scores := make(map[string]float64, n)
	if n == 0 {
		return scores
	}

	// Map each defined symbol name to the file(s) that define it.
	defBy := map[string][]int{}
	for i, fe := range entries {
		for _, s := range fe.Symbols {
			base := s.Name
			if dot := strings.LastIndexByte(base, '.'); dot >= 0 {
				base = base[dot+1:]
			}
			defBy[base] = append(defBy[base], i)
		}
	}

	// Build outbound adjacency from mention counts.
	adj := make([][]int, n)
	for i, fe := range entries {
		// Re-read is avoided; approximate references by symbol names appearing
		// in OTHER files' symbol names plus this file's own declared names is
		// insufficient, so we treat each file's declared identifiers as its
		// mention surface. This keeps ranking dependency-free and deterministic.
		mentioned := map[int]bool{}
		for _, s := range fe.Symbols {
			for _, tok := range identRe.FindAllString(s.Name, -1) {
				for _, j := range defBy[tok] {
					if j != i {
						mentioned[j] = true
					}
				}
			}
		}
		for j := range mentioned {
			adj[i] = append(adj[i], j)
		}
		// Sort so power iteration adds shares in a fixed order — float addition
		// is not associative, so a stable order preserves the determinism guarantee.
		slices.Sort(adj[i])
	}

	// Power iteration.
	const damping = 0.85
	const iters = 20
	for i := range entries {
		scores[entries[i].Path] = 1.0 / float64(n)
	}
	cur := make([]float64, n)
	for i := range entries {
		cur[i] = 1.0 / float64(n)
	}
	for it := 0; it < iters; it++ {
		next := make([]float64, n)
		base := (1 - damping) / float64(n)
		for i := range next {
			next[i] = base
		}
		for i := range entries {
			out := adj[i]
			if len(out) == 0 {
				// Distribute dangling mass uniformly.
				share := damping * cur[i] / float64(n)
				for j := range next {
					next[j] += share
				}
				continue
			}
			share := damping * cur[i] / float64(len(out))
			for _, j := range out {
				next[j] += share
			}
		}
		cur = next
	}

	// Weight by symbol count so richer files rank slightly higher on ties.
	for i, fe := range entries {
		scores[fe.Path] = cur[i] * (1 + float64(len(fe.Symbols))*1e-6)
	}
	return scores
}
