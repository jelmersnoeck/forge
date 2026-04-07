package main

// slashCommand defines a slash command available in the TUI.
type slashCommand struct {
	Name        string // e.g. "/review"
	Description string // e.g. "Run multi-agent code review on current diff"
	Hidden      bool   // if true, not shown in autocomplete
}

// slashCommands is the registry of all available slash commands.
var slashCommands = []slashCommand{
	{Name: "/review", Description: "Run multi-agent code review on current diff"},
}

// slashCommandNames returns just the command names for textinput.SetSuggestions.
func slashCommandNames() []string {
	names := make([]string, 0, len(slashCommands))
	for _, cmd := range slashCommands {
		if !cmd.Hidden {
			names = append(names, cmd.Name)
		}
	}
	return names
}
