package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/jelmersnoeck/forge/internal/config"
)

func runConfig(args []string) int {
	if len(args) < 2 {
		printConfigHelp()
		return 1
	}

	switch args[1] {
	case "get":
		return runConfigGet(args[1:])
	case "set":
		return runConfigSet(args[1:])
	case "list", "ls":
		return runConfigList()
	case "help", "-h", "--help":
		printConfigHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Unknown config command: %s\n\n", args[1])
		printConfigHelp()
		return 1
	}
}

func runConfigGet(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: forge config get <key>")
		fmt.Fprintln(os.Stderr, "")
		printValidKeys()
		return 1
	}
	key := args[1]

	value, err := config.GetValue(key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if value == "" {
		fmt.Println("(not set)")
		return 0
	}

	fmt.Println(value)
	return 0
}

func runConfigSet(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: forge config set <key> <value>")
		fmt.Fprintln(os.Stderr, "")
		printValidKeys()
		return 1
	}

	key := args[1]
	value := args[2]

	if err := config.SetValue(key, value); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return 0
}

func runConfigList() int {
	values, err := config.ListValues()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	keys := config.ValidKeys()
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tVALUE\tDESCRIPTION")

	for _, k := range sorted {
		v := values[k]
		display := v
		switch {
		case display == "":
			display = "(not set)"
		case k == "provider.default" && os.Getenv("FORGE_PROVIDER") != "":
			display += " (overridden by FORGE_PROVIDER=" + os.Getenv("FORGE_PROVIDER") + ")"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", k, display, keys[k])
	}
	_ = tw.Flush()

	return 0
}

func printConfigHelp() {
	fmt.Println(`forge config — manage persistent configuration

Usage:
  forge config get <key>          read a config value
  forge config set <key> <value>  set a config value
  forge config list               show all config values

Stored in: ~/.forge/config.toml`)
	fmt.Println("")
	printValidKeys()
}

func printValidKeys() {
	fmt.Println("Available keys:")
	keys := config.ValidKeys()
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		fmt.Printf("  %-24s %s\n", k, keys[k])
	}
}
