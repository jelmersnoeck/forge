// Package repomap builds a deterministic, token-budgeted structural overview of
// a source tree: files and their top-level symbols, ranked by cross-file
// reference importance (PageRank-style). It is injected into the agent's context
// so the model has a high-level map without blind-grepping.
package repomap

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// defaultTokenBudget bounds rendered output when the caller passes <=0.
const defaultTokenBudget = 2000

// maxFileBytes skips parsing files larger than this (1 MiB).
const maxFileBytes = 1 << 20

// defaultGitTimeout bounds the `git ls-files` call so a hung git process
// (credential helper prompt, NFS lock) cannot block context loading
// indefinitely. Operators can override it via FORGE_REPOMAP_GIT_TIMEOUT
// (a Go duration string, e.g. "45s"); invalid or non-positive values fall
// back to this default.
const defaultGitTimeout = 20 * time.Second

// gitTimeoutEnv is the environment variable operators use to override the git
// timeout. It holds a Go duration string (e.g. "10s", "2m").
const gitTimeoutEnv = "FORGE_REPOMAP_GIT_TIMEOUT"

// gitTimeout resolves the effective git timeout from the FORGE_REPOMAP_GIT_TIMEOUT
// env var, falling back to the default. It reads the env var on each call
// (rather than caching) so the value stays injectable/resettable in tests and
// picks up changes without a process restart; trackedFiles reads it once per
// invocation into a local so a single git call uses a consistent value.
func gitTimeout() time.Duration {
	return resolveGitTimeout(os.Getenv(gitTimeoutEnv))
}

// resolveGitTimeout parses the raw git-timeout value, returning the default for
// empty, unparsable, or non-positive inputs. Invalid and non-positive values
// warn (naming the env var and allowed format) so operators can spot and fix
// misconfiguration; a valid non-default value logs at Info so operators can
// confirm the active timeout.
func resolveGitTimeout(v string) time.Duration {
	if v == "" {
		return defaultGitTimeout
	}
	d, err := time.ParseDuration(v)
	switch {
	case err != nil:
		slog.Warn("repomap: invalid git timeout, using default",
			"env", gitTimeoutEnv, "value", v, "default", defaultGitTimeout,
			"format", "Go duration string, e.g. 10s or 2m", "error", err)
		return defaultGitTimeout
	case d <= 0:
		slog.Warn("repomap: non-positive git timeout, using default",
			"env", gitTimeoutEnv, "value", v, "default", defaultGitTimeout,
			"format", "positive Go duration string, e.g. 10s or 2m")
		return defaultGitTimeout
	default:
		slog.Info("repomap: using non-default git timeout",
			"env", gitTimeoutEnv, "timeout", d)
		return d
	}
}

// Options tunes a Build.
type Options struct {
	TokenBudget int // <=0 → defaultTokenBudget
	MaxFiles    int // 0 → unlimited
}

// Symbol is a single top-level declaration.
type Symbol struct {
	Name string
	Kind string // "func", "method", "type", "const", "var"
	Line int
}

// FileEntry is a file and its extracted symbols.
type FileEntry struct {
	Path    string // relative to cwd
	Symbols []Symbol
}

// Map is the ranked, budgeted result.
type Map struct {
	Files          []FileEntry
	OmittedFiles   int
	OmittedSymbols int
}

// extractor pulls top-level symbols out of a file's bytes.
type extractor func(src []byte) []Symbol

// extractors maps a lowercase file extension to its symbol extractor.
var extractors = map[string]extractor{
	".go":   extractGo,
	".py":   genericExtractor("py"),
	".js":   genericExtractor("js"),
	".jsx":  genericExtractor("js"),
	".ts":   genericExtractor("ts"),
	".tsx":  genericExtractor("ts"),
	".rb":   genericExtractor("rb"),
	".rs":   genericExtractor("rs"),
	".java": genericExtractor("java"),
}

// Build walks the git-tracked tree under cwd, extracts and ranks symbols, and
// returns a token-budgeted Map. Returns an empty Map (not an error) when cwd is
// not a git repo or git is unavailable; the underlying git error is logged at
// debug level for observability rather than surfaced, so callers can treat this
// optional feature as best-effort.
func Build(cwd string, opts Options) (Map, error) {
	files, err := trackedFiles(cwd)
	if err != nil {
		slog.Debug("repomap: git ls-files failed, skipping map", "cwd", cwd, "error", err)
		return Map{}, nil
	}
	if len(files) == 0 {
		slog.Debug("repomap: no git-tracked files, skipping map", "cwd", cwd)
		return Map{}, nil
	}
	slices.Sort(files)

	budget := opts.TokenBudget
	if budget <= 0 {
		budget = defaultTokenBudget
	}

	// Extract symbols per parseable file, respecting MaxFiles. Files past the
	// MaxFiles cutoff are counted as omitted so the reported statistics reflect
	// what the map left out. Files with unrecognized extensions were never
	// candidates for symbol extraction, so they are skipped without inflating
	// the omitted count.
	var entries []FileEntry
	parsed := 0
	var capped Map
	for _, rel := range files {
		ext := strings.ToLower(filepath.Ext(rel))
		ex, ok := extractors[ext]
		if !ok {
			// Unrecognized extension: not a symbol candidate, not counted.
			continue
		}
		if opts.MaxFiles > 0 && parsed >= opts.MaxFiles {
			capped.OmittedFiles++
			continue
		}
		src, ok := readCapped(cwd, rel)
		if !ok {
			// Unreadable or oversized (>1 MiB): counted as omitted.
			capped.OmittedFiles++
			continue
		}
		parsed++
		syms := ex(src)
		if len(syms) == 0 {
			// Parseable but no top-level symbols: counted, no header rendered.
			capped.OmittedFiles++
			continue
		}
		entries = append(entries, FileEntry{Path: rel, Symbols: syms})
	}

	scores := rank(entries)
	m := budgeted(entries, scores, budget)
	m.OmittedFiles += capped.OmittedFiles
	return m, nil
}

// budgeted orders files by descending rank score, sorts symbols within a file
// by name asc, and truncates to the token budget. It does not mutate the input
// entries slice.
func budgeted(entries []FileEntry, scores map[string]float64, budget int) Map {
	// Clone before mutating so the caller's slice/backing symbol slices stay
	// untouched.
	ordered := make([]FileEntry, len(entries))
	for i, fe := range entries {
		syms := slices.Clone(fe.Symbols)
		slices.SortFunc(syms, func(a, b Symbol) int {
			return strings.Compare(a.Name, b.Name)
		})
		ordered[i] = FileEntry{Path: fe.Path, Symbols: syms}
	}

	// Order files by score desc, then path asc for determinism.
	slices.SortFunc(ordered, func(a, b FileEntry) int {
		if sa, sb := scores[a.Path], scores[b.Path]; sa != sb {
			if sa > sb {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})

	var out Map
	used := 0
	for i, fe := range ordered {
		cost := fileTokens(fe)
		if used+cost > budget && len(out.Files) > 0 {
			// Budget exhausted; remaining files/symbols are omitted.
			out.OmittedFiles += len(ordered) - i
			for _, rest := range ordered[i:] {
				out.OmittedSymbols += len(rest.Symbols)
			}
			break
		}
		out.Files = append(out.Files, fe)
		used += cost
	}

	// Re-sort included files by path for stable rendering.
	slices.SortFunc(out.Files, func(a, b FileEntry) int {
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// fileTokens approximates the token cost of a file entry directly from its data,
// avoiding a second render pass. Mirrors renderFile's format: a path line plus
// one indented line per symbol, divided by 4 (the len/4 token heuristic).
func fileTokens(fe FileEntry) int {
	chars := len(fe.Path) + 1 // path + newline
	for _, s := range fe.Symbols {
		chars += 2 + len(s.Kind) + 1 + len(s.Name) + 1 // "  " kind " " name "\n"
	}
	return chars / 4
}

// readCapped reads cwd/rel, returning ok=false when it cannot be read, exceeds
// maxFileBytes, or resolves outside cwd (a defense against tracked paths that
// use ".." components to escape the tree).
func readCapped(cwd, rel string) ([]byte, bool) {
	if !within(cwd, rel) {
		return nil, false
	}
	path := filepath.Join(cwd, rel)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxFileBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// within reports whether rel resolves to a path strictly *under* cwd. It uses
// filepath.Rel on cleaned paths rather than a string prefix check, which is
// robust against trailing separators, root ("/") cwd, and sibling-prefix false
// positives (e.g. cwd "/tmp/rep" vs path "/tmp/reposecret").
//
// Contract:
//   - filepath.Rel returns a path beginning with ".." exactly when target
//     escapes base, and otherwise never returns an absolute path on success, so
//     the sole escape test is a "." or ".."-leading relative result.
//   - rel == "." (target == cwd itself) is treated as NOT within: readCapped
//     only ever wants regular files under cwd, and cwd is a directory — a git
//     tracked path never resolves to cwd, so rejecting it costs nothing and
//     keeps the containment strict.
//   - An absolute-looking rel such as "/etc/passwd" is confined by
//     filepath.Join (which treats a leading separator in the second arg as
//     relative on all platforms), so it lands under cwd and is allowed. Git
//     never emits absolute tracked paths, so this is safe in practice.
func within(cwd, rel string) bool {
	base := filepath.Clean(cwd)
	target := filepath.Clean(filepath.Join(cwd, rel))
	r, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return r != "." && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// trackedFiles returns git-tracked paths (relative to cwd) via `git ls-files`.
// Errors (not a repo, git missing, timeout) yield a nil slice + error.
func trackedFiles(cwd string) ([]string, error) {
	timeout := gitTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Prefix cwd with "./" when relative-looking so a directory name starting
	// with "-" cannot be interpreted as a git flag after "-C".
	dir := cwd
	if !filepath.IsAbs(dir) {
		dir = "." + string(filepath.Separator) + dir
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files")
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.Debug("repomap: git ls-files timed out", "cwd", cwd, "timeout", timeout)
		}
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
