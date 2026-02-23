package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/jelmersnoeck/forge/internal/principles/templates"
	"github.com/spf13/cobra"
)

var templatesOutputFormat string

var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "Manage principle templates",
	Long:  "List, inspect, and install built-in principle set templates.",
}

var templatesListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show available principle templates",
	Long:  "Lists all built-in principle set templates with their names, descriptions, and principle counts.",
	RunE:  runTemplatesList,
}

var templatesInstallCmd = &cobra.Command{
	Use:   "install NAME",
	Short: "Install a template to .forge/principles/",
	Long:  "Copies a built-in principle set template into the project's .forge/principles/ directory.",
	Args:  cobra.ExactArgs(1),
	RunE:  runTemplatesInstall,
}

func init() {
	templatesListCmd.Flags().StringVar(&templatesOutputFormat, "format", "table", "Output format (table, json)")
	templatesCmd.AddCommand(templatesListCmd)
	templatesCmd.AddCommand(templatesInstallCmd)
	rootCmd.AddCommand(templatesCmd)
}

func runTemplatesList(cmd *cobra.Command, args []string) error {
	infos, err := templates.ListTemplates()
	if err != nil {
		return fmt.Errorf("listing templates: %w", err)
	}

	if templatesOutputFormat == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(infos)
	}

	// Table output.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tPRINCIPLES\tDESCRIPTION")
	for _, info := range infos {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", info.Name, info.Version, info.Principles, info.Description)
	}
	return w.Flush()
}

func runTemplatesInstall(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Determine destination directory.
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	destDir := filepath.Join(dir, ".forge", "principles")

	if err := templates.InstallTemplate(name, destDir); err != nil {
		return fmt.Errorf("installing template %q: %w", name, err)
	}

	fmt.Printf("Installed template %q to %s/%s.yaml\n", name, destDir, name)
	return nil
}
