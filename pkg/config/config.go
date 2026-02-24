// Package config handles parsing and validation of .forge/config.yaml files.
package config

// Config is the top-level Forge configuration structure.
type Config struct {
	Agent      AgentConfig      `yaml:"agent"      mapstructure:"agent"`
	Tracker    TrackerConfig    `yaml:"tracker"     mapstructure:"tracker"`
	Principles PrincipleConfig `yaml:"principles"  mapstructure:"principles"`
	Build      BuildConfig      `yaml:"build"       mapstructure:"build"`
	Server     ServerConfig     `yaml:"server"      mapstructure:"server"`
}

// AgentConfig defines agent backend configuration.
type AgentConfig struct {
	Default  string                    `yaml:"default"  mapstructure:"default"`
	Backends map[string]AgentBackend   `yaml:"backends" mapstructure:"backends"`
	Roles    AgentRoles                `yaml:"roles"    mapstructure:"roles"`
}

// AgentBackend configures a specific agent backend.
type AgentBackend struct {
	Binary string `yaml:"binary" mapstructure:"binary"`
	Model  string `yaml:"model"  mapstructure:"model"`
}

// AgentRoles maps operational roles to agent backend names.
type AgentRoles struct {
	Planner  string `yaml:"planner"  mapstructure:"planner"`
	Coder    string `yaml:"coder"    mapstructure:"coder"`
	Reviewer string `yaml:"reviewer" mapstructure:"reviewer"`
}

// TrackerConfig defines tracker backend configuration.
type TrackerConfig struct {
	Default string              `yaml:"default" mapstructure:"default"`
	GitHub  *GitHubConfig       `yaml:"github"  mapstructure:"github"`
	Jira    *JiraConfig         `yaml:"jira"    mapstructure:"jira"`
	Linear  *LinearConfig       `yaml:"linear"  mapstructure:"linear"`
}

// GitHubConfig holds GitHub-specific settings.
type GitHubConfig struct {
	Org         string `yaml:"org"          mapstructure:"org"`
	DefaultRepo string `yaml:"default_repo" mapstructure:"default_repo"`
}

// JiraConfig holds Jira-specific settings.
type JiraConfig struct {
	Instance       string `yaml:"instance"        mapstructure:"instance"`
	DefaultProject string `yaml:"default_project" mapstructure:"default_project"`
	Auth           string `yaml:"auth"            mapstructure:"auth"`
}

// LinearConfig holds Linear-specific settings.
type LinearConfig struct {
	Team string `yaml:"team" mapstructure:"team"`
	Auth string `yaml:"auth" mapstructure:"auth"`
}

// PrincipleConfig defines where to find and which principles to use.
type PrincipleConfig struct {
	Paths  []string `yaml:"paths"  mapstructure:"paths"`
	Active []string `yaml:"active" mapstructure:"active"`
}

// BuildConfig defines build loop behavior.
type BuildConfig struct {
	MaxIterations          int          `yaml:"max_iterations"           mapstructure:"max_iterations"`
	BranchPattern          string       `yaml:"branch_pattern"           mapstructure:"branch_pattern"`
	WorkstreamBranchPattern string     `yaml:"workstream_branch_pattern" mapstructure:"workstream_branch_pattern"`
	TestCommand            string       `yaml:"test_command"             mapstructure:"test_command"`
	RequirePlanApproval    bool         `yaml:"require_plan_approval"    mapstructure:"require_plan_approval"`
	Review                 ReviewConfig `yaml:"review"                   mapstructure:"review"`
}

// ReviewConfig defines review behavior within the build loop.
type ReviewConfig struct {
	ParallelReviewers  int    `yaml:"parallel_reviewers"  mapstructure:"parallel_reviewers"`
	ReviewerAgent      string `yaml:"reviewer_agent"      mapstructure:"reviewer_agent"`
	SeverityThreshold  string `yaml:"severity_threshold"  mapstructure:"severity_threshold"`
}

// ServerConfig defines HTTP server settings.
type ServerConfig struct {
	Port           int           `yaml:"port"            mapstructure:"port"`
	AllowedOrigins []string      `yaml:"allowed_origins" mapstructure:"allowed_origins"`
	Webhooks       WebhookConfig `yaml:"webhooks"        mapstructure:"webhooks"`
	DatabasePath   string        `yaml:"database_path"   mapstructure:"database_path"`
}

// WebhookConfig defines webhook endpoints.
type WebhookConfig struct {
	GitHub *GitHubWebhookConfig `yaml:"github" mapstructure:"github"`
	Jira   *JiraWebhookConfig   `yaml:"jira"   mapstructure:"jira"`
}

// GitHubWebhookConfig holds GitHub webhook settings.
type GitHubWebhookConfig struct {
	Secret   string           `yaml:"secret"   mapstructure:"secret"`
	Triggers []WebhookTrigger `yaml:"triggers" mapstructure:"triggers"`
}

// JiraWebhookConfig holds Jira webhook settings.
type JiraWebhookConfig struct {
	Auth     string           `yaml:"auth"     mapstructure:"auth"`
	Triggers []WebhookTrigger `yaml:"triggers" mapstructure:"triggers"`
}

// WebhookTrigger maps an incoming event to a Forge action.
type WebhookTrigger struct {
	Event        string `yaml:"event"         mapstructure:"event"`
	Label        string `yaml:"label"         mapstructure:"label"`
	TransitionTo string `yaml:"transition_to" mapstructure:"transition_to"`
	Pattern      string `yaml:"pattern"       mapstructure:"pattern"`
	Action       string `yaml:"action"        mapstructure:"action"`
}
