// Package env defines the Environment interface for deploy targets.
//
// Environments are where agents deploy and test code during the governed
// build loop. Backends include docker-compose, k3d, and vCluster.
package env

import "context"

// Environment manages infrastructure for deploying and testing code.
type Environment interface {
	// Provision creates a new environment from a specification.
	Provision(ctx context.Context, spec Spec) (*Env, error)

	// Deploy pushes artifacts into a provisioned environment.
	Deploy(ctx context.Context, env *Env, artifacts []Artifact) error

	// Test runs tests against a deployed environment.
	Test(ctx context.Context, env *Env, tests TestSpec) (*TestResult, error)

	// Teardown destroys an environment and cleans up resources.
	Teardown(ctx context.Context, env *Env) error
}

// Spec describes the desired environment configuration.
type Spec struct {
	Name     string            // Environment name.
	Type     string            // Backend type (docker-compose, k3d, vcluster).
	Config   map[string]string // Backend-specific configuration.
	Timeout  int               // Provisioning timeout in seconds.
}

// Env represents a running environment.
type Env struct {
	ID       string            // Unique environment identifier.
	Name     string            // Human-readable name.
	Type     string            // Backend type.
	Status   string            // running | stopped | failed.
	Endpoint string            // Access endpoint (URL, port, etc.).
	Metadata map[string]string // Backend-specific metadata.
}

// Artifact is something to deploy (container image, binary, config).
type Artifact struct {
	Type string // "image", "binary", "config".
	Path string // Local path or image reference.
	Name string // Target name in the environment.
}

// TestSpec describes which tests to run.
type TestSpec struct {
	Command string   // Test command to execute.
	Args    []string // Command arguments.
	Timeout int      // Test timeout in seconds.
}

// TestResult contains test execution results.
type TestResult struct {
	Passed  bool   // All tests passed.
	Output  string // Combined stdout/stderr.
	Summary string // Brief summary of results.
}
