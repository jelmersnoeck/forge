package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jelmersnoeck/forge/internal/types"
)

// envFileErrMsg is the canonical error returned when a tool tries to access a
// .env file. No overrides, no exceptions, no "but I really need it".
const envFileErrMsg = "access to .env files is blocked — these files contain secrets and must not be read, written, or modified by tools"

// envFileSafeSuffixes are .env.* variants that contain no real secrets —
// they exist to document required keys and are meant to be committed.
var envFileSafeSuffixes = []string{
	".example",
	".template",
	".sample",
}

// isEnvFile reports whether path points to a .env file that contains secrets.
//
// Blocked:  .env, .env.local, .env.production, .env.development, .env.test,
//
//	.env.staging, and any other .env.* variant.
//
// Allowed:  .env.example, .env.template, .env.sample — these are
//
//	documentation files meant to be committed.
func isEnvFile(path string) bool {
	base := filepath.Base(path)
	switch {
	case base == ".env":
		return true
	case strings.HasPrefix(base, ".env."):
		for _, safe := range envFileSafeSuffixes {
			if strings.HasSuffix(base, safe) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// envFileError returns the standard ToolResult error for .env access attempts.
func envFileError(path string) types.ToolResult {
	return types.ToolResult{
		Content: []types.ToolResultContent{{
			Type: "text",
			Text: fmt.Sprintf("%s: %s", envFileErrMsg, path),
		}},
		IsError: true,
	}
}

// commandAccessesEnvFile checks whether a bash command appears to read, write,
// source, or otherwise touch a .env file. Returns the offending token or "".
//
// This is intentionally conservative — it looks for .env as a distinct token
// or as part of common shell patterns (cat .env, source .env, cp .env.example
// .env, etc.). It won't catch every obfuscation (eval "$(cat .en" + "v")"),
// but it blocks the straightforward cases that an LLM would produce.
func commandAccessesEnvFile(command string) string {
	tokens := shellTokenize(command)
	for _, tok := range tokens {
		// Strip common shell redirections/prefixes
		cleaned := strings.TrimLeft(tok, "<>|&;()")
		if isEnvFile(cleaned) {
			return cleaned
		}
		// Handle redirect targets: >>.env, >.env
		for _, prefix := range []string{">>", ">"} {
			if strings.HasPrefix(cleaned, prefix) {
				target := strings.TrimPrefix(cleaned, prefix)
				if isEnvFile(target) {
					return target
				}
			}
		}
	}
	return ""
}

// shellTokenize does a rough split of a shell command into tokens, respecting
// quotes just enough for .env detection. Not a real parser — good enough.
func shellTokenize(cmd string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteByte(ch)
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteByte(ch)
		case (ch == ' ' || ch == '\t' || ch == '\n') && !inSingle && !inDouble:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
