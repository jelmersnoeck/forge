package principles

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

const validYAML = `
name: test-set
version: "1.0.0"
description: A test principle set.
principles:
  - id: sec-001
    category: security
    severity: critical
    title: No hardcoded secrets
    description: Never embed secrets in code.
    rationale: Secrets in code end up in version control.
    check: Scan for string literals that look like keys.
    examples:
      - type: bad
        code: 'const key = "abc123"'
        explanation: Secret is hardcoded.
      - type: good
        code: 'key := os.Getenv("KEY")'
        explanation: Secret comes from environment.
  - id: sec-002
    category: security
    severity: warning
    title: Input validation
    description: Validate inputs at boundaries.
    rationale: Unvalidated input causes vulnerabilities.
    check: Check for validation at handlers.
    examples:
      - type: good
        code: 'if err := validate(input); err != nil { return err }'
        explanation: Input is validated before use.
`

const secondSetYAML = `
name: arch-set
version: "1.0.0"
description: Architecture principles.
principles:
  - id: arch-001
    category: architecture
    severity: warning
    title: Interface-first design
    description: Define interfaces before implementations.
    rationale: Enables testability and swappable backends.
    check: Verify key abstractions are interfaces.
    examples:
      - type: good
        code: 'func Run(a Agent) error { ... }'
        explanation: Accepts interface, not concrete type.
`

const invalidYAML = `
name: broken
version: "1.0.0"
principles:
  - id: [this is not valid yaml
`

const missingNameYAML = `
version: "1.0.0"
description: No name field.
principles:
  - id: sec-001
    category: security
    severity: critical
    title: Test
    description: Test.
    rationale: Test.
    check: Test.
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing test file %s: %v", path, err)
	}
	return path
}

func TestLoadFile_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "security.yaml", validYAML)

	store := NewStore()
	if err := store.LoadFile(path); err != nil {
		t.Fatalf("LoadFile returned unexpected error: %v", err)
	}

	set, ok := store.Get("test-set")
	if !ok {
		t.Fatal("expected to find set 'test-set'")
	}
	if set.Name != "test-set" {
		t.Errorf("set name = %q, want %q", set.Name, "test-set")
	}
	if len(set.Principles) != 2 {
		t.Errorf("got %d principles, want 2", len(set.Principles))
	}
	if set.Principles[0].ID != "sec-001" {
		t.Errorf("first principle ID = %q, want %q", set.Principles[0].ID, "sec-001")
	}
	if set.Principles[0].Severity != SeverityCritical {
		t.Errorf("first principle severity = %q, want %q", set.Principles[0].Severity, SeverityCritical)
	}
	if set.Principles[0].Category != CategorySecurity {
		t.Errorf("first principle category = %q, want %q", set.Principles[0].Category, CategorySecurity)
	}
	if len(set.Principles[0].Examples) != 2 {
		t.Errorf("first principle has %d examples, want 2", len(set.Principles[0].Examples))
	}
}

func TestLoadFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "broken.yaml", invalidYAML)

	store := NewStore()
	err := store.LoadFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoadFile_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "noname.yaml", missingNameYAML)

	store := NewStore()
	err := store.LoadFile(path)
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
}

func TestLoadFile_NonexistentFile(t *testing.T) {
	store := NewStore()
	err := store.LoadFile("/nonexistent/path/file.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoadDir_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yaml", validYAML)
	writeFile(t, dir, "architecture.yaml", secondSetYAML)
	// Non-YAML file should be ignored.
	writeFile(t, dir, "README.md", "# Not a principle file")

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	names := store.Sets()
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("got %d sets, want 2: %v", len(names), names)
	}
	if names[0] != "arch-set" || names[1] != "test-set" {
		t.Errorf("set names = %v, want [arch-set test-set]", names)
	}
}

func TestLoadDir_NonexistentDir(t *testing.T) {
	store := NewStore()
	err := store.LoadDir("/nonexistent/directory")
	if err == nil {
		t.Fatal("expected error for nonexistent directory, got nil")
	}
}

func TestLoadDir_YMLExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yml", validYAML)

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	if len(store.Sets()) != 1 {
		t.Errorf("got %d sets, want 1", len(store.Sets()))
	}
}

func TestLoad_SpecificSets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yaml", validYAML)
	writeFile(t, dir, "architecture.yaml", secondSetYAML)

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	// Load only the security set.
	principles, err := store.Load(context.Background(), "test-set")
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if len(principles) != 2 {
		t.Errorf("got %d principles, want 2", len(principles))
	}

	// Load both sets.
	principles, err = store.Load(context.Background(), "test-set", "arch-set")
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if len(principles) != 3 {
		t.Errorf("got %d principles from both sets, want 3", len(principles))
	}

	// Load nonexistent set.
	_, err = store.Load(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent set, got nil")
	}
}

func TestAll_ReturnsEverything(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yaml", validYAML)
	writeFile(t, dir, "architecture.yaml", secondSetYAML)

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	all := store.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d principles, want 3", len(all))
	}
}

func TestGet_ReturnsSpecificSet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yaml", validYAML)

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	set, ok := store.Get("test-set")
	if !ok {
		t.Fatal("expected to find set 'test-set'")
	}
	if set.Name != "test-set" {
		t.Errorf("set name = %q, want %q", set.Name, "test-set")
	}

	_, ok = store.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for nonexistent set")
	}
}

func TestSets_ReturnsNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yaml", validYAML)
	writeFile(t, dir, "architecture.yaml", secondSetYAML)

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	names := store.Sets()
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2", len(names))
	}
	if names[0] != "arch-set" {
		t.Errorf("names[0] = %q, want %q", names[0], "arch-set")
	}
	if names[1] != "test-set" {
		t.Errorf("names[1] = %q, want %q", names[1], "test-set")
	}
}

func TestConcurrencySafety(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "security.yaml", validYAML)
	writeFile(t, dir, "architecture.yaml", secondSetYAML)

	store := NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir returned unexpected error: %v", err)
	}

	// Create a third file to load concurrently.
	thirdSetYAML := `
name: sim-set
version: "1.0.0"
description: Simplicity.
principles:
  - id: sim-001
    category: simplicity
    severity: info
    title: No premature abstraction
    description: Wait for two use cases.
    rationale: Premature abstractions add complexity.
    check: Look for single-implementation interfaces.
    examples:
      - type: good
        code: 'func process(data []byte) {}'
        explanation: Direct implementation.
`
	thirdPath := writeFile(t, dir, "simplicity.yaml", thirdSetYAML)

	var wg sync.WaitGroup
	const goroutines = 50

	// Mix of reads and writes running concurrently.
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			switch n % 5 {
			case 0:
				_ = store.All()
			case 1:
				_ = store.Sets()
			case 2:
				_, _ = store.Get("test-set")
			case 3:
				_, _ = store.Load(context.Background(), "test-set")
			case 4:
				_ = store.LoadFile(thirdPath)
			}
		}(i)
	}

	wg.Wait()

	// If we got here without a race detector panic, concurrency is safe.
	// Verify the store is still functional.
	if len(store.Sets()) == 0 {
		t.Error("store has no sets after concurrent access")
	}
}

func TestNewStore_Empty(t *testing.T) {
	store := NewStore()
	if len(store.All()) != 0 {
		t.Error("new store should have no principles")
	}
	if len(store.Sets()) != 0 {
		t.Error("new store should have no sets")
	}
}
