package main

// Workstream mode is implemented as the --workstream flag on the build command.
// See build.go for the flag registration and the early return when --workstream
// is set.
//
// This file exists as a placeholder for Phase 4 workstream orchestration logic.
// When implemented, it will support:
//
//   - forge build --workstream: orchestrate multiple issues in parallel
//   - Dependency graph resolution between sub-issues
//   - Workstream branch management (forge/ws-{{.WorkstreamID}})
//   - Progress tracking across the workstream
//
// See forge-architecture-v0.md for the full workstream design.
