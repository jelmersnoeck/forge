package repomap

import (
	"fmt"
	"strings"
)

// Render turns a Map into deterministic prompt text. Empty Map → "".
func Render(m Map) string {
	if len(m.Files) == 0 {
		return ""
	}
	var b strings.Builder
	for _, fe := range m.Files {
		b.WriteString(renderFile(fe))
	}
	if m.OmittedFiles > 0 || m.OmittedSymbols > 0 {
		fmt.Fprintf(&b, "... (%d more files, %d more symbols omitted)\n",
			m.OmittedFiles, m.OmittedSymbols)
	}
	return b.String()
}

// renderFile renders one file's header and its symbols.
func renderFile(fe FileEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", fe.Path)
	for _, s := range fe.Symbols {
		fmt.Fprintf(&b, "  %s %s\n", s.Kind, s.Name)
	}
	return b.String()
}
