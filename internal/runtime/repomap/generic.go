package repomap

import (
	"regexp"
)

// pattern pairs a compiled regex with the kind it yields. The first capture
// group is the symbol name.
type pattern struct {
	re   *regexp.Regexp
	kind string
}

// langPatterns holds top-level declaration patterns per language key.
var langPatterns = map[string][]pattern{
	"py": {
		{regexp.MustCompile(`(?m)^class\s+([A-Za-z_]\w*)`), "type"},
		{regexp.MustCompile(`(?m)^def\s+([A-Za-z_]\w*)`), "func"},
	},
	"js": {
		{regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][\w$]*)`), "type"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), "func"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*=`), "const"},
	},
	"ts": {
		{regexp.MustCompile(`(?m)^(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`), "type"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)`), "type"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?type\s+([A-Za-z_$][\w$]*)`), "type"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), "func"},
		{regexp.MustCompile(`(?m)^(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*[:=]`), "const"},
	},
	"rb": {
		{regexp.MustCompile(`(?m)^\s*(?:class|module)\s+([A-Z]\w*)`), "type"},
		{regexp.MustCompile(`(?m)^\s*def\s+([A-Za-z_]\w*[!?=]?)`), "func"},
	},
	"rs": {
		{regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:struct|enum|trait)\s+([A-Za-z_]\w*)`), "type"},
		{regexp.MustCompile(`(?m)^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`), "func"},
		{regexp.MustCompile(`(?m)^\s*(?:pub\s+)?const\s+([A-Za-z_]\w*)`), "const"},
	},
	"java": {
		{regexp.MustCompile(`(?m)^\s*(?:public|private|protected|abstract|final|\s)*(?:class|interface|enum)\s+([A-Za-z_]\w*)`), "type"},
	},
}

// genericExtractor returns an extractor for a language key using regex patterns.
// It dedups by (kind,name) and reports byte-offset-derived line numbers.
func genericExtractor(lang string) extractor {
	pats := langPatterns[lang]
	return func(src []byte) []Symbol {
		var syms []Symbol
		seen := map[string]bool{}
		for _, p := range pats {
			for _, m := range p.re.FindAllSubmatchIndex(src, -1) {
				name := string(src[m[2]:m[3]])
				key := p.kind + " " + name
				if seen[key] {
					continue
				}
				seen[key] = true
				syms = append(syms, Symbol{Name: name, Kind: p.kind, Line: lineAt(src, m[0])})
			}
		}
		return syms
	}
}

// lineAt returns the 1-based line number of byte offset off.
func lineAt(src []byte, off int) int {
	line := 1
	for i := 0; i < off && i < len(src); i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}
