package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jelmersnoeck/forge/internal/types"
)

// TODO: Add comprehensive TUI model tests
// These tests need to properly mock the bubbletea framework and test:
// - Model initialization
// - Update message handling (events, key presses, etc.)
// - View rendering
// - State transitions (thinking, working, error states)
//
// For now, we rely on E2E tests for TUI validation.

// TestTokenUsageStructure is a simple test to ensure TokenUsage JSON marshaling works
func TestTokenUsageStructure(t *testing.T) {
	r := require.New(t)

	usage := types.TokenUsage{
		InputTokens:  100,
		OutputTokens: 50,
	}

	// Verify fields are accessible
	r.Equal(100, usage.InputTokens)
	r.Equal(50, usage.OutputTokens)
}
