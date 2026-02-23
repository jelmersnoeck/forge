package agent

import (
	"context"
	"sort"
	"testing"
)

// mockAgent is a simple Agent implementation for testing.
type mockAgent struct {
	name string
}

func (m *mockAgent) Run(_ context.Context, _ Request) (*Response, error) {
	return &Response{Output: m.name}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := &mockAgent{name: "test-agent"}

	r.Register("test", a)

	got, ok := r.Get("test")
	if !ok {
		t.Fatal("Get() returned false, want true")
	}
	if got != a {
		t.Error("Get() returned different agent instance")
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("Get() returned true for nonexistent agent, want false")
	}
}

func TestRegistry_MustGet(t *testing.T) {
	r := NewRegistry()
	a := &mockAgent{name: "test"}
	r.Register("test", a)

	got, err := r.MustGet("test")
	if err != nil {
		t.Fatalf("MustGet() unexpected error: %v", err)
	}
	if got != a {
		t.Error("MustGet() returned different agent instance")
	}
}

func TestRegistry_MustGetMissing(t *testing.T) {
	r := NewRegistry()

	_, err := r.MustGet("nonexistent")
	if err == nil {
		t.Fatal("MustGet() expected error for missing agent, got nil")
	}
}

func TestRegistry_Overwrite(t *testing.T) {
	r := NewRegistry()
	a1 := &mockAgent{name: "first"}
	a2 := &mockAgent{name: "second"}

	r.Register("test", a1)
	r.Register("test", a2)

	got, ok := r.Get("test")
	if !ok {
		t.Fatal("Get() returned false, want true")
	}
	if got != a2 {
		t.Error("Get() returned first agent, want second (overwritten)")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.Register("claude-code", &mockAgent{name: "cc"})
	r.Register("opencode", &mockAgent{name: "oc"})
	r.Register("http", &mockAgent{name: "h"})

	names := r.List()
	sort.Strings(names)

	want := []string{"claude-code", "http", "opencode"}
	if len(names) != len(want) {
		t.Fatalf("List() returned %d names, want %d", len(names), len(want))
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("List()[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestRegistry_ListEmpty(t *testing.T) {
	r := NewRegistry()
	names := r.List()
	if len(names) != 0 {
		t.Errorf("List() returned %d names for empty registry, want 0", len(names))
	}
}
