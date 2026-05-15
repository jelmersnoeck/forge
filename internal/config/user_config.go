// Package config loads forge-level configuration from user and project levels.
//
//	~/.forge/config.json   (user, project-level JSON)
//	.forge/config.json     (project — overrides user)
//	~/.forge/config.toml   (user-level persistent config)
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// UserConfig represents ~/.forge/config.toml — the user's persistent preferences.
type UserConfig struct {
	Provider ProviderConfig   `toml:"provider"`
	Commit   CommitUserConfig `toml:"commit"`
	PR       PRUserConfig     `toml:"pr"`
}

// CommitUserConfig holds commit attribution settings.
// Note: CoAuthor is nested under Attribution (not at top-level) to group all
// attribution-related settings together in the TOML file under
// [commit.attribution]. This differs from the attribution package's flat
// CommitConfig which separates CoAuthor for API convenience.
type CommitUserConfig struct {
	Attribution CommitAttributionConfig `toml:"attribution"`
}

// PRUserConfig holds PR attribution settings.
type PRUserConfig struct {
	Attribution AttributionEnabled `toml:"attribution"`
}

// AttributionEnabled controls whether attribution trailers are added.
type AttributionEnabled struct {
	Enabled     *bool  `toml:"enabled"`      // nil = default (true)
	GeneratedBy string `toml:"generated_by"` // default "forge"
}

// CommitAttributionConfig extends AttributionEnabled with a CoAuthor field for commits.
type CommitAttributionConfig struct {
	Enabled     *bool  `toml:"enabled"`      // nil = default (true)
	CoAuthor    string `toml:"co_author"`    // default "Forge <forge@noreply.invalid>"
	GeneratedBy string `toml:"generated_by"` // default "forge"
}

// IsEnabled returns whether attribution is enabled, defaulting to true.
func (a CommitAttributionConfig) IsEnabled() bool {
	if a.Enabled == nil {
		return true
	}
	return *a.Enabled
}

// IsEnabled returns whether attribution is enabled, defaulting to true.
func (a AttributionEnabled) IsEnabled() bool {
	if a.Enabled == nil {
		return true
	}
	return *a.Enabled
}

// ProviderConfig holds LLM provider preferences.
type ProviderConfig struct {
	Default string `toml:"default"` // "anthropic", "claude-cli", "openai"
}

// validProviders lists the accepted values for provider.default.
var validProviders = []string{"anthropic", "claude-cli", "openai"}

// validKeys maps dotted config keys to descriptions.
var validKeys = map[string]string{
	"provider.default":               "default LLM provider (anthropic, claude-cli, openai)",
	"commit.attribution.coAuthor":    "co-author trailer value (\"Name <email>\"), default: Forge <forge@noreply.invalid>",
	"commit.attribution.enabled":     "enable commit attribution trailers (true/false, default: true)",
	"commit.attribution.generatedBy": "Generated-by prefix (default: forge)",
	"pr.attribution.enabled":         "enable PR attribution block (true/false, default: true)",
}

// ValidKeys returns the known configuration keys with descriptions.
func ValidKeys() map[string]string {
	out := make(map[string]string, len(validKeys))
	for k, v := range validKeys {
		out[k] = v
	}
	return out
}

// userConfigPath returns ~/.forge/config.toml.
func userConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".forge", "config.toml"), nil
}

// LoadUserConfig loads ~/.forge/config.toml. Returns zero value if the file
// doesn't exist.
func LoadUserConfig() (UserConfig, error) {
	path, err := userConfigPath()
	if err != nil {
		return UserConfig{}, err
	}
	return loadUserConfigFrom(path)
}

// loadUserConfigFrom loads user config from a specific path (testable).
func loadUserConfigFrom(path string) (UserConfig, error) {
	var cfg UserConfig
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("stat config: %w", err)
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// SaveUserConfig writes the config to ~/.forge/config.toml, creating
// the directory if needed.
func SaveUserConfig(cfg UserConfig) error {
	path, err := userConfigPath()
	if err != nil {
		return err
	}
	return saveUserConfigTo(path, cfg)
}

// saveUserConfigTo writes config to a specific path (testable).
func saveUserConfigTo(path string, cfg UserConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// SetValue sets a dotted key (e.g. "provider.default") to a value in
// ~/.forge/config.toml.
func SetValue(key, value string) error {
	path, err := userConfigPath()
	if err != nil {
		return err
	}
	return setValueAt(path, key, value)
}

// setValueAt sets a value in a specific config file (testable).
func setValueAt(path, key, value string) error {
	if _, ok := validKeys[key]; !ok {
		return fmt.Errorf("unknown config key %q; valid keys: %s", key, strings.Join(sortedKeys(validKeys), ", "))
	}

	if err := validateValue(key, value); err != nil {
		return err
	}

	cfg, err := loadUserConfigFrom(path)
	if err != nil {
		return err
	}

	switch key {
	case "provider.default":
		cfg.Provider.Default = value
	case "commit.attribution.coAuthor":
		cfg.Commit.Attribution.CoAuthor = value
	case "commit.attribution.enabled":
		b := value == "true"
		cfg.Commit.Attribution.Enabled = &b
	case "commit.attribution.generatedBy":
		cfg.Commit.Attribution.GeneratedBy = value
	case "pr.attribution.enabled":
		b := value == "true"
		cfg.PR.Attribution.Enabled = &b
	}

	return saveUserConfigTo(path, cfg)
}

// GetValue returns the value for a dotted key from ~/.forge/config.toml.
// Returns "" if unset.
func GetValue(key string) (string, error) {
	path, err := userConfigPath()
	if err != nil {
		return "", err
	}
	return getValueAt(path, key)
}

// getValueAt reads a value from a specific config file (testable).
func getValueAt(path, key string) (string, error) {
	if _, ok := validKeys[key]; !ok {
		return "", fmt.Errorf("unknown config key %q; valid keys: %s", key, strings.Join(sortedKeys(validKeys), ", "))
	}

	cfg, err := loadUserConfigFrom(path)
	if err != nil {
		return "", err
	}

	switch key {
	case "provider.default":
		return cfg.Provider.Default, nil
	case "commit.attribution.coAuthor":
		return cfg.Commit.Attribution.CoAuthor, nil
	case "commit.attribution.enabled":
		if cfg.Commit.Attribution.Enabled == nil {
			return "", nil
		}
		if *cfg.Commit.Attribution.Enabled {
			return "true", nil
		}
		return "false", nil
	case "commit.attribution.generatedBy":
		return cfg.Commit.Attribution.GeneratedBy, nil
	case "pr.attribution.enabled":
		if cfg.PR.Attribution.Enabled == nil {
			return "", nil
		}
		if *cfg.PR.Attribution.Enabled {
			return "true", nil
		}
		return "false", nil
	}
	return "", nil
}

// ListValues returns all config key-value pairs from ~/.forge/config.toml.
func ListValues() (map[string]string, error) {
	path, err := userConfigPath()
	if err != nil {
		return nil, err
	}
	return listValuesAt(path)
}

// listValuesAt returns all config values from a specific path (testable).
func listValuesAt(path string) (map[string]string, error) {
	cfg, err := loadUserConfigFrom(path)
	if err != nil {
		return nil, err
	}

	result := map[string]string{
		"provider.default":               cfg.Provider.Default,
		"commit.attribution.coAuthor":    cfg.Commit.Attribution.CoAuthor,
		"commit.attribution.generatedBy": cfg.Commit.Attribution.GeneratedBy,
	}

	if cfg.Commit.Attribution.Enabled != nil {
		if *cfg.Commit.Attribution.Enabled {
			result["commit.attribution.enabled"] = "true"
		} else {
			result["commit.attribution.enabled"] = "false"
		}
	} else {
		result["commit.attribution.enabled"] = ""
	}

	if cfg.PR.Attribution.Enabled != nil {
		if *cfg.PR.Attribution.Enabled {
			result["pr.attribution.enabled"] = "true"
		} else {
			result["pr.attribution.enabled"] = "false"
		}
	} else {
		result["pr.attribution.enabled"] = ""
	}

	return result, nil
}

// validateValue checks that a value is valid for the given key.
func validateValue(key, value string) error {
	switch key {
	case "provider.default":
		for _, p := range validProviders {
			if value == p {
				return nil
			}
		}
		return fmt.Errorf("invalid provider %q; valid options: %s", value, strings.Join(validProviders, ", "))

	case "commit.attribution.coAuthor":
		return validateCoAuthor(value)

	case "commit.attribution.enabled", "pr.attribution.enabled":
		if value != "true" && value != "false" {
			return fmt.Errorf("invalid value %q; must be \"true\" or \"false\"", value)
		}

	case "commit.attribution.generatedBy":
		// Any non-empty string is valid.
		if value == "" {
			return fmt.Errorf("generatedBy must not be empty")
		}
	}
	return nil
}

// validateCoAuthor checks that a co-author value is in "Name <email>" format.
func validateCoAuthor(value string) error {
	// Must contain angle brackets with something inside.
	ltIdx := strings.Index(value, "<")
	gtIdx := strings.Index(value, ">")
	if ltIdx < 0 || gtIdx < 0 || gtIdx <= ltIdx+1 {
		return fmt.Errorf("invalid co-author %q; expected format: \"Name <email>\"", value)
	}
	// Name part must not be empty.
	name := strings.TrimSpace(value[:ltIdx])
	if name == "" {
		return fmt.Errorf("invalid co-author %q; name part is empty", value)
	}
	// Email must contain '@' and a domain part.
	email := value[ltIdx+1 : gtIdx]
	atIdx := strings.Index(email, "@")
	if atIdx < 1 || atIdx >= len(email)-1 {
		return fmt.Errorf("invalid co-author %q; email must contain a valid address (user@domain)", value)
	}
	domain := email[atIdx+1:]
	if !strings.Contains(domain, ".") && domain != "localhost" {
		return fmt.Errorf("invalid co-author %q; email domain must contain a dot or be localhost", value)
	}
	return nil
}

// sortedKeys returns map keys in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — tiny map.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
