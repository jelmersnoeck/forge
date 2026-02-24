package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExecCommand replaces execCommand for tests and records all invocations.
// The handler function is called with the command name and args, and should
// return (stdout, stderr, error).
type fakeExec struct {
	calls   []fakeCall
	handler func(name string, args []string) (stdout, stderr string, err error)
}

type fakeCall struct {
	Name string
	Args []string
}

// install replaces execCommand for the duration of a test and restores it after.
func (f *fakeExec) install(t *testing.T) {
	t.Helper()
	orig := execCommand
	tmpDir := t.TempDir()
	callNum := 0

	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		f.calls = append(f.calls, fakeCall{Name: name, Args: args})

		stdout, stderr, err := f.handler(name, args)

		// Write stdout/stderr to temp files to avoid shell quoting issues.
		num := callNum
		callNum++
		outFile := filepath.Join(tmpDir, fmt.Sprintf("stdout-%d", num))
		errFile := filepath.Join(tmpDir, fmt.Sprintf("stderr-%d", num))
		_ = os.WriteFile(outFile, []byte(stdout), 0644)
		_ = os.WriteFile(errFile, []byte(stderr), 0644)

		if err != nil {
			cmd := exec.CommandContext(ctx, "sh", "-c",
				fmt.Sprintf("cat %q >&2; exit 1", errFile))
			return cmd
		}
		cmd := exec.CommandContext(ctx, "sh", "-c",
			fmt.Sprintf("cat %q", outFile))
		return cmd
	}
	t.Cleanup(func() { execCommand = orig })
}

// callsWith returns all calls whose args contain the given substring.
func (f *fakeExec) callsWith(sub string) []fakeCall {
	var matches []fakeCall
	for _, c := range f.calls {
		joined := c.Name + " " + strings.Join(c.Args, " ")
		if strings.Contains(joined, sub) {
			matches = append(matches, c)
		}
	}
	return matches
}

func newLogger() *slog.Logger {
	return slog.Default()
}

func TestDockerCompose_Provision_Success(t *testing.T) {
	psOutput := []composeService{
		{
			Name:    "test-web-1",
			Service: "web",
			State:   "running",
			Health:  "",
			Publishers: []struct {
				URL           string `json:"URL"`
				TargetPort    int    `json:"TargetPort"`
				PublishedPort int    `json:"PublishedPort"`
				Protocol      string `json:"Protocol"`
			}{
				{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"},
			},
		},
	}
	psJSON, _ := json.Marshal(psOutput)

	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			// composeBinary check.
			if strings.Contains(joined, "compose version") {
				return "Docker Compose version v2.20.0", "", nil
			}
			// up -d.
			if strings.Contains(joined, "up -d") {
				return "", "", nil
			}
			// ps --format json.
			if strings.Contains(joined, "ps --format json") {
				return string(psJSON), "", nil
			}
			return "", "", fmt.Errorf("unexpected command: %s %v", name, args)
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env, err := dc.Provision(context.Background(), Spec{
		Name: "myproject",
		Type: "docker-compose",
		Config: map[string]string{
			"compose_file": "docker-compose.yaml",
		},
		Timeout: 10,
	})
	if err != nil {
		t.Fatalf("Provision unexpected error: %v", err)
	}

	if env.ID != "myproject" {
		t.Errorf("ID = %q, want %q", env.ID, "myproject")
	}
	if env.Status != "running" {
		t.Errorf("Status = %q, want %q", env.Status, "running")
	}
	if env.Type != "docker-compose" {
		t.Errorf("Type = %q, want %q", env.Type, "docker-compose")
	}
	if env.Endpoint != "http://localhost:8080" {
		t.Errorf("Endpoint = %q, want %q", env.Endpoint, "http://localhost:8080")
	}
	if env.Metadata["compose_file"] != "docker-compose.yaml" {
		t.Errorf("Metadata[compose_file] = %q", env.Metadata["compose_file"])
	}
	if env.Metadata["project_name"] != "myproject" {
		t.Errorf("Metadata[project_name] = %q", env.Metadata["project_name"])
	}

	// Verify up -d was called.
	upCalls := fe.callsWith("up -d")
	if len(upCalls) == 0 {
		t.Error("expected 'up -d' call, found none")
	}
}

func TestDockerCompose_Provision_WithBuild(t *testing.T) {
	psOutput := []composeService{
		{Name: "svc", Service: "app", State: "running"},
	}
	psJSON, _ := json.Marshal(psOutput)

	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "", nil
			}
			if strings.Contains(joined, "ps") {
				return string(psJSON), "", nil
			}
			return "", "", fmt.Errorf("unexpected: %s %v", name, args)
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	_, err := dc.Provision(context.Background(), Spec{
		Name: "buildtest",
		Type: "docker-compose",
		Config: map[string]string{
			"build": "true",
		},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Provision unexpected error: %v", err)
	}

	// Verify --build flag was passed.
	upCalls := fe.callsWith("up -d")
	found := false
	for _, c := range upCalls {
		for _, a := range c.Args {
			if a == "--build" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected --build flag in up command")
	}
}

func TestDockerCompose_Provision_CustomProjectName(t *testing.T) {
	psOutput := []composeService{
		{Name: "svc", Service: "app", State: "running"},
	}
	psJSON, _ := json.Marshal(psOutput)

	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") || strings.Contains(joined, "ps") {
				if strings.Contains(joined, "ps") {
					return string(psJSON), "", nil
				}
				return "", "", nil
			}
			return "", "", fmt.Errorf("unexpected: %s %v", name, args)
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env, err := dc.Provision(context.Background(), Spec{
		Name: "myenv",
		Config: map[string]string{
			"project_name": "custom-project",
		},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Provision unexpected error: %v", err)
	}
	if env.ID != "custom-project" {
		t.Errorf("ID = %q, want %q", env.ID, "custom-project")
	}

	// Verify -p custom-project was used.
	upCalls := fe.callsWith("up -d")
	found := false
	for _, c := range upCalls {
		for _, a := range c.Args {
			if a == "custom-project" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected project name 'custom-project' in up command args")
	}
}

func TestDockerCompose_Provision_UpFails(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "compose file not found", errors.New("exit 1")
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	_, err := dc.Provision(context.Background(), Spec{
		Name:    "fail",
		Config:  map[string]string{},
		Timeout: 5,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provision") {
		t.Errorf("error should contain 'provision', got: %v", err)
	}
}

func TestDockerCompose_Provision_HealthTimeout(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "", nil
			}
			// Service is always "starting", never healthy.
			if strings.Contains(joined, "ps") {
				svc := []composeService{
					{Name: "svc", Service: "app", State: "running", Health: "starting"},
				}
				data, _ := json.Marshal(svc)
				return string(data), "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	_, err := dc.Provision(context.Background(), Spec{
		Name:    "timeout-test",
		Config:  map[string]string{},
		Timeout: 1, // 1 second timeout -- will expire quickly.
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should contain 'timed out', got: %v", err)
	}
}

func TestDockerCompose_Provision_HealthyWithHealthCheck(t *testing.T) {
	psOutput := []composeService{
		{Name: "svc", Service: "db", State: "running", Health: "healthy"},
	}
	psJSON, _ := json.Marshal(psOutput)

	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "", nil
			}
			if strings.Contains(joined, "ps") {
				return string(psJSON), "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env, err := dc.Provision(context.Background(), Spec{
		Name:    "health-test",
		Config:  map[string]string{},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Provision unexpected error: %v", err)
	}
	if env.Status != "running" {
		t.Errorf("Status = %q, want %q", env.Status, "running")
	}
}

func TestDockerCompose_Provision_NewlineDelimitedJSON(t *testing.T) {
	// Test parsing newline-delimited JSON (older compose versions).
	svc1, _ := json.Marshal(composeService{
		Name: "svc1", Service: "web", State: "running",
		Publishers: []struct {
			URL           string `json:"URL"`
			TargetPort    int    `json:"TargetPort"`
			PublishedPort int    `json:"PublishedPort"`
			Protocol      string `json:"Protocol"`
		}{{URL: "", TargetPort: 80, PublishedPort: 3000, Protocol: "tcp"}},
	})
	svc2, _ := json.Marshal(composeService{
		Name: "svc2", Service: "db", State: "running",
	})
	ndjson := string(svc1) + "\n" + string(svc2)

	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "", nil
			}
			if strings.Contains(joined, "ps") {
				return ndjson, "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env, err := dc.Provision(context.Background(), Spec{
		Name:    "ndjson-test",
		Config:  map[string]string{},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Provision unexpected error: %v", err)
	}
	if env.Endpoint != "http://localhost:3000" {
		t.Errorf("Endpoint = %q, want %q", env.Endpoint, "http://localhost:3000")
	}
}

func TestDockerCompose_Provision_FallbackBinary(t *testing.T) {
	psOutput := []composeService{
		{Name: "svc", Service: "app", State: "running"},
	}
	psJSON, _ := json.Marshal(psOutput)

	versionChecked := false
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			// First call: "docker compose version" should fail.
			if strings.Contains(joined, "compose version") {
				versionChecked = true
				return "", "", errors.New("not found")
			}
			// After fallback, binary should be docker-compose.
			if name == "docker-compose" || strings.Contains(joined, "up") || strings.Contains(joined, "ps") {
				if strings.Contains(joined, "ps") {
					return string(psJSON), "", nil
				}
				return "", "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	_, err := dc.Provision(context.Background(), Spec{
		Name:    "fallback",
		Config:  map[string]string{},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Provision unexpected error: %v", err)
	}
	if !versionChecked {
		t.Error("expected compose version check")
	}

	// Verify that docker-compose was used as the binary.
	found := false
	for _, c := range fe.calls {
		if c.Name == "docker-compose" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected fallback to docker-compose binary")
	}
}

func TestDockerCompose_Deploy_ImageArtifact(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID: "myproject",
		Metadata: map[string]string{
			"compose_file": "docker-compose.yaml",
			"project_name": "myproject",
		},
	}

	err := dc.Deploy(context.Background(), env, []Artifact{
		{Type: "image", Name: "web", Path: "myapp:latest"},
	})
	if err != nil {
		t.Fatalf("Deploy unexpected error: %v", err)
	}

	// Verify up -d --build <service> was called.
	upCalls := fe.callsWith("up -d --build")
	if len(upCalls) == 0 {
		t.Error("expected 'up -d --build' call for image deploy")
	}
	found := false
	for _, c := range upCalls {
		for _, a := range c.Args {
			if a == "web" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected service name 'web' in up --build command")
	}
}

func TestDockerCompose_Deploy_ConfigArtifact(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID: "myproject",
		Metadata: map[string]string{
			"compose_file": "docker-compose.yaml",
			"project_name": "myproject",
		},
	}

	err := dc.Deploy(context.Background(), env, []Artifact{
		{Type: "config", Name: "web", Path: "/app/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Deploy unexpected error: %v", err)
	}

	// Verify cp and restart were called.
	cpCalls := fe.callsWith("cp")
	if len(cpCalls) == 0 {
		t.Error("expected 'cp' call for config deploy")
	}
	restartCalls := fe.callsWith("restart")
	if len(restartCalls) == 0 {
		t.Error("expected 'restart' call after config deploy")
	}
}

func TestDockerCompose_Deploy_Error(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "service not found", errors.New("exit 1")
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID: "myproject",
		Metadata: map[string]string{
			"compose_file": "docker-compose.yaml",
			"project_name": "myproject",
		},
	}

	err := dc.Deploy(context.Background(), env, []Artifact{
		{Type: "image", Name: "web", Path: "myapp:latest"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "deploy image web") {
		t.Errorf("error should contain 'deploy image web', got: %v", err)
	}
}

func TestDockerCompose_Deploy_UnsupportedArtifact(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID: "myproject",
		Metadata: map[string]string{
			"compose_file": "docker-compose.yaml",
			"project_name": "myproject",
		},
	}

	// Should not error, just log a warning.
	err := dc.Deploy(context.Background(), env, []Artifact{
		{Type: "binary", Name: "app", Path: "/tmp/app"},
	})
	if err != nil {
		t.Fatalf("Deploy unexpected error for unsupported type: %v", err)
	}
}

func TestDockerCompose_Test_Success(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			// The test command itself.
			if name == "go" || (len(args) > 0 && args[0] == "test") {
				return "ok  all tests passed", "", nil
			}
			return "PASS\n3 tests passed", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID:       "myproject",
		Endpoint: "http://localhost:8080",
		Metadata: map[string]string{
			"project_name": "myproject",
		},
	}

	result, err := dc.Test(context.Background(), env, TestSpec{
		Command: "go",
		Args:    []string{"test", "./..."},
		Timeout: 30,
	})
	if err != nil {
		t.Fatalf("Test unexpected error: %v", err)
	}
	if !result.Passed {
		t.Errorf("Passed = false, want true")
	}
	if result.Summary != "all tests passed" {
		t.Errorf("Summary = %q, want %q", result.Summary, "all tests passed")
	}
}

func TestDockerCompose_Test_Failure(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			return "FAIL test_foo.go:42", "exit status 1", errors.New("exit 1")
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID: "myproject",
		Metadata: map[string]string{
			"project_name": "myproject",
		},
	}

	result, err := dc.Test(context.Background(), env, TestSpec{
		Command: "pytest",
		Args:    []string{"-v"},
		Timeout: 10,
	})
	if err != nil {
		t.Fatalf("Test should return result, not error: %v", err)
	}
	if result.Passed {
		t.Error("Passed = true, want false")
	}
	if !strings.Contains(result.Summary, "tests failed") {
		t.Errorf("Summary should contain 'tests failed', got: %q", result.Summary)
	}
}

func TestDockerCompose_Teardown_Success(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID:     "myproject",
		Status: "running",
		Metadata: map[string]string{
			"compose_file": "docker-compose.yaml",
			"project_name": "myproject",
		},
	}

	err := dc.Teardown(context.Background(), env)
	if err != nil {
		t.Fatalf("Teardown unexpected error: %v", err)
	}

	if env.Status != "stopped" {
		t.Errorf("Status = %q, want %q", env.Status, "stopped")
	}

	// Verify down -v --remove-orphans was called.
	downCalls := fe.callsWith("down")
	if len(downCalls) == 0 {
		t.Error("expected 'down' call")
	}
	found := map[string]bool{}
	for _, c := range downCalls {
		for _, a := range c.Args {
			if a == "-v" || a == "--remove-orphans" {
				found[a] = true
			}
		}
	}
	if !found["-v"] {
		t.Error("expected -v flag in down command")
	}
	if !found["--remove-orphans"] {
		t.Error("expected --remove-orphans flag in down command")
	}
}

func TestDockerCompose_Teardown_Error(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "down") {
				return "", "network not found", errors.New("exit 1")
			}
			return "", "", nil
		},
	}
	fe.install(t)

	dc := NewDockerCompose(newLogger())
	env := &Env{
		ID: "myproject",
		Metadata: map[string]string{
			"compose_file": "docker-compose.yaml",
			"project_name": "myproject",
		},
	}

	err := dc.Teardown(context.Background(), env)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "teardown") {
		t.Errorf("error should contain 'teardown', got: %v", err)
	}
}

func TestDockerCompose_Provision_ContextCancelled(t *testing.T) {
	fe := &fakeExec{
		handler: func(name string, args []string) (string, string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "compose version") {
				return "v2.20.0", "", nil
			}
			if strings.Contains(joined, "up") {
				return "", "", nil
			}
			// Return empty services to force polling.
			if strings.Contains(joined, "ps") {
				return "[]", "", nil
			}
			return "", "", nil
		},
	}
	fe.install(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	dc := NewDockerCompose(newLogger())
	_, err := dc.Provision(ctx, Spec{
		Name:    "cancelled",
		Config:  map[string]string{},
		Timeout: 60,
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestExtractEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		services []composeService
		want     string
	}{
		{
			name:     "no services",
			services: nil,
			want:     "",
		},
		{
			name: "no publishers",
			services: []composeService{
				{Name: "svc", State: "running"},
			},
			want: "",
		},
		{
			name: "with published port on 0.0.0.0",
			services: []composeService{
				{
					Name:  "web",
					State: "running",
					Publishers: []struct {
						URL           string `json:"URL"`
						TargetPort    int    `json:"TargetPort"`
						PublishedPort int    `json:"PublishedPort"`
						Protocol      string `json:"Protocol"`
					}{
						{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 9090, Protocol: "tcp"},
					},
				},
			},
			want: "http://localhost:9090",
		},
		{
			name: "with published port on empty URL",
			services: []composeService{
				{
					Name:  "web",
					State: "running",
					Publishers: []struct {
						URL           string `json:"URL"`
						TargetPort    int    `json:"TargetPort"`
						PublishedPort int    `json:"PublishedPort"`
						Protocol      string `json:"Protocol"`
					}{
						{URL: "", TargetPort: 80, PublishedPort: 3000, Protocol: "tcp"},
					},
				},
			},
			want: "http://localhost:3000",
		},
		{
			name: "with published port on ::",
			services: []composeService{
				{
					Name:  "web",
					State: "running",
					Publishers: []struct {
						URL           string `json:"URL"`
						TargetPort    int    `json:"TargetPort"`
						PublishedPort int    `json:"PublishedPort"`
						Protocol      string `json:"Protocol"`
					}{
						{URL: "::", TargetPort: 80, PublishedPort: 4000, Protocol: "tcp"},
					},
				},
			},
			want: "http://localhost:4000",
		},
		{
			name: "with specific host",
			services: []composeService{
				{
					Name:  "web",
					State: "running",
					Publishers: []struct {
						URL           string `json:"URL"`
						TargetPort    int    `json:"TargetPort"`
						PublishedPort int    `json:"PublishedPort"`
						Protocol      string `json:"Protocol"`
					}{
						{URL: "192.168.1.10", TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"},
					},
				},
			},
			want: "http://192.168.1.10:8080",
		},
		{
			name: "multiple services picks first published",
			services: []composeService{
				{Name: "db", State: "running"},
				{
					Name:  "web",
					State: "running",
					Publishers: []struct {
						URL           string `json:"URL"`
						TargetPort    int    `json:"TargetPort"`
						PublishedPort int    `json:"PublishedPort"`
						Protocol      string `json:"Protocol"`
					}{
						{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 5555, Protocol: "tcp"},
					},
				},
			},
			want: "http://localhost:5555",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEndpoint(tt.services)
			if got != tt.want {
				t.Errorf("extractEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewDockerCompose_NilLogger(t *testing.T) {
	dc := NewDockerCompose(nil)
	if dc.logger == nil {
		t.Error("logger should default to slog.Default(), got nil")
	}
}

func TestDockerCompose_InterfaceCompliance(t *testing.T) {
	// Compile-time check that DockerCompose implements Environment.
	var _ Environment = (*DockerCompose)(nil)
}
