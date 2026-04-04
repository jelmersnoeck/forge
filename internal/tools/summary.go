package tools

// CallSummary returns a short human-readable description of a tool call.
// Used by the audit logger and the conversation loop event stream.
func CallSummary(name string, input map[string]any) string {
	str := func(key string) string {
		if v, ok := input[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	switch name {
	case "Bash":
		cmd := str("command")
		if len(cmd) > 120 {
			return cmd[:120] + "..."
		}
		return cmd
	case "Read":
		return str("file_path")
	case "Write":
		return str("file_path")
	case "Edit":
		return str("file_path")
	case "Glob":
		return str("pattern")
	case "Grep":
		return str("pattern")
	default:
		return ""
	}
}
