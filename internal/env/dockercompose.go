package env

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// execCommand is the function used to create exec.Cmd instances.
// It is a variable so tests can replace it with a mock.
var execCommand = exec.CommandContext

// defaultHealthTimeout is the default health check timeout in seconds.
const defaultHealthTimeout = 120

// DockerCompose implements the Environment interface by shelling out to
// docker compose (or the legacy docker-compose binary).
type DockerCompose struct {
	logger *slog.Logger
}

// NewDockerCompose creates a new DockerCompose environment backend.
func NewDockerCompose(logger *slog.Logger) *DockerCompose {
	if logger == nil {
		logger = slog.Default()
	}
	return &DockerCompose{logger: logger}
}

// composeService holds the parsed JSON output from docker compose ps.
type composeService struct {
	Name    string `json:"Name"`
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
	Publishers []struct {
		URL           string `json:"URL"`
		TargetPort    int    `json:"TargetPort"`
		PublishedPort int    `json:"PublishedPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

// composeBinary returns the compose command parts. It prefers "docker compose"
// (the plugin form) and falls back to "docker-compose" (the standalone binary).
func composeBinary(ctx context.Context) (string, []string) {
	// Try docker compose (plugin).
	cmd := execCommand(ctx, "docker", "compose", "version")
	if err := cmd.Run(); err == nil {
		return "docker", []string{"compose"}
	}
	// Fall back to docker-compose.
	return "docker-compose", nil
}

// composeArgs builds the full argument list for a compose command.
func composeArgs(subArgs []string, composeFile, projectName string) []string {
	var args []string
	args = append(args, subArgs...)
	args = append(args, "-f", composeFile, "-p", projectName)
	return args
}

// runCompose runs a docker compose command and returns combined output.
func runCompose(ctx context.Context, composeFile, projectName string, cmdArgs ...string) ([]byte, error) {
	bin, binArgs := composeBinary(ctx)
	args := append(binArgs, "-f", composeFile, "-p", projectName)
	args = append(args, cmdArgs...)

	cmd := execCommand(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %s: %w", bin, strings.Join(cmdArgs, " "), stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

// Provision creates a new docker-compose environment.
//
// Spec.Config keys:
//   - compose_file: path to docker-compose.yaml (default: "docker-compose.yaml")
//   - project_name: compose project name (default: spec.Name)
//   - build: "true" to force build during provision
func (dc *DockerCompose) Provision(ctx context.Context, spec Spec) (*Env, error) {
	composeFile := spec.Config["compose_file"]
	if composeFile == "" {
		composeFile = "docker-compose.yaml"
	}
	projectName := spec.Config["project_name"]
	if projectName == "" {
		projectName = spec.Name
	}

	dc.logger.InfoContext(ctx, "provisioning docker-compose environment",
		"project", projectName,
		"compose_file", composeFile,
	)

	// Build the up command args.
	upArgs := []string{"up", "-d"}
	if spec.Config["build"] == "true" {
		upArgs = append(upArgs, "--build")
	}

	if _, err := runCompose(ctx, composeFile, projectName, upArgs...); err != nil {
		return nil, fmt.Errorf("provision: %w", err)
	}

	// Determine health check timeout.
	timeout := defaultHealthTimeout
	if spec.Timeout > 0 {
		timeout = spec.Timeout
	}
	if t, err := strconv.Atoi(spec.Config["timeout"]); err == nil && t > 0 {
		timeout = t
	}

	// Wait for services to become healthy/running.
	endpoint, err := dc.waitHealthy(ctx, composeFile, projectName, timeout)
	if err != nil {
		return nil, fmt.Errorf("provision: %w", err)
	}

	workDir := filepath.Dir(composeFile)
	if workDir == "." {
		workDir = ""
	}

	return &Env{
		ID:       projectName,
		Name:     spec.Name,
		Type:     "docker-compose",
		Status:   "running",
		Endpoint: endpoint,
		Metadata: map[string]string{
			"compose_file": composeFile,
			"project_name": projectName,
			"working_dir":  workDir,
		},
	}, nil
}

// waitHealthy polls docker compose ps until all services are running/healthy
// or the timeout expires.
func (dc *DockerCompose) waitHealthy(ctx context.Context, composeFile, projectName string, timeoutSec int) (string, error) {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	pollInterval := 2 * time.Second

	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("health check timed out after %ds", timeoutSec)
		}

		services, err := dc.listServices(ctx, composeFile, projectName)
		if err != nil {
			dc.logger.WarnContext(ctx, "failed to list services, retrying", "error", err)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(pollInterval):
				continue
			}
		}

		if len(services) == 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(pollInterval):
				continue
			}
		}

		allReady := true
		for _, svc := range services {
			state := strings.ToLower(svc.State)
			health := strings.ToLower(svc.Health)
			if state != "running" {
				allReady = false
				break
			}
			// If the service has a health check defined, it must be healthy.
			if health != "" && health != "healthy" {
				allReady = false
				break
			}
		}

		if allReady {
			endpoint := extractEndpoint(services)
			dc.logger.InfoContext(ctx, "all services healthy",
				"project", projectName,
				"endpoint", endpoint,
			)
			return endpoint, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// listServices runs docker compose ps --format json and parses the output.
func (dc *DockerCompose) listServices(ctx context.Context, composeFile, projectName string) ([]composeService, error) {
	out, err := runCompose(ctx, composeFile, projectName, "ps", "--format", "json")
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}

	var services []composeService
	// docker compose ps --format json can return either a JSON array or
	// newline-delimited JSON objects depending on the version.
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &services); err != nil {
			return nil, fmt.Errorf("parse compose ps output: %w", err)
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var svc composeService
			if err := json.Unmarshal([]byte(line), &svc); err != nil {
				return nil, fmt.Errorf("parse compose ps line: %w", err)
			}
			services = append(services, svc)
		}
	}

	return services, nil
}

// extractEndpoint returns the first published port endpoint from the services.
func extractEndpoint(services []composeService) string {
	for _, svc := range services {
		for _, pub := range svc.Publishers {
			if pub.PublishedPort > 0 {
				host := pub.URL
				if host == "" || host == "0.0.0.0" || host == "::" {
					host = "localhost"
				}
				return fmt.Sprintf("http://%s:%d", host, pub.PublishedPort)
			}
		}
	}
	return ""
}

// Deploy pushes artifacts into a provisioned environment.
//
// For image artifacts: rebuilds the service specified by artifact.Name.
// For config artifacts: copies the file into the service and restarts.
func (dc *DockerCompose) Deploy(ctx context.Context, e *Env, artifacts []Artifact) error {
	composeFile := e.Metadata["compose_file"]
	projectName := e.Metadata["project_name"]

	dc.logger.InfoContext(ctx, "deploying artifacts",
		"project", projectName,
		"count", len(artifacts),
	)

	for _, art := range artifacts {
		switch art.Type {
		case "image":
			dc.logger.InfoContext(ctx, "rebuilding service", "service", art.Name)
			if _, err := runCompose(ctx, composeFile, projectName, "up", "-d", "--build", art.Name); err != nil {
				return fmt.Errorf("deploy image %s: %w", art.Name, err)
			}
		case "config":
			dc.logger.InfoContext(ctx, "copying config", "path", art.Path, "service", art.Name)
			// Use docker compose cp to copy file into the container.
			dest := fmt.Sprintf("%s:%s", art.Name, art.Path)
			if _, err := runCompose(ctx, composeFile, projectName, "cp", art.Path, dest); err != nil {
				return fmt.Errorf("deploy config %s: %w", art.Name, err)
			}
			// Restart the service to pick up the new config.
			if _, err := runCompose(ctx, composeFile, projectName, "restart", art.Name); err != nil {
				return fmt.Errorf("deploy restart %s: %w", art.Name, err)
			}
		default:
			dc.logger.WarnContext(ctx, "unsupported artifact type", "type", art.Type, "name", art.Name)
		}
	}
	return nil
}

// Test runs tests against a deployed environment.
func (dc *DockerCompose) Test(ctx context.Context, e *Env, tests TestSpec) (*TestResult, error) {
	dc.logger.InfoContext(ctx, "running tests",
		"project", e.Metadata["project_name"],
		"command", tests.Command,
	)

	testCtx := ctx
	if tests.Timeout > 0 {
		var cancel context.CancelFunc
		testCtx, cancel = context.WithTimeout(ctx, time.Duration(tests.Timeout)*time.Second)
		defer cancel()
	}

	args := append([]string{tests.Command}, tests.Args...)
	cmd := execCommand(testCtx, args[0], args[1:]...)

	// Set endpoint as an environment variable so test commands can use it.
	if e.Endpoint != "" {
		cmd.Env = append(cmd.Environ(), "TEST_ENDPOINT="+e.Endpoint)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String() + stderr.String()

	if err != nil {
		return &TestResult{
			Passed:  false,
			Output:  output,
			Summary: fmt.Sprintf("tests failed: %v", err),
		}, nil
	}

	return &TestResult{
		Passed:  true,
		Output:  output,
		Summary: "all tests passed",
	}, nil
}

// Teardown destroys the environment and cleans up resources.
func (dc *DockerCompose) Teardown(ctx context.Context, e *Env) error {
	composeFile := e.Metadata["compose_file"]
	projectName := e.Metadata["project_name"]

	dc.logger.InfoContext(ctx, "tearing down environment",
		"project", projectName,
	)

	if _, err := runCompose(ctx, composeFile, projectName, "down", "-v", "--remove-orphans"); err != nil {
		return fmt.Errorf("teardown: %w", err)
	}

	e.Status = "stopped"
	return nil
}
