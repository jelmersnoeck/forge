# Learnings - 2026-06-28 17:49

- The forge agent worker runs with cwd == worktree root (spawnLocalAgent passes --cwd cwd after setting cwd=worktreePath). So worktree-scoped sidecar files like .forge-state can be read/written directly via w.cwd in internal/agent/worker.go — no need to plumb state through agent flags or server.Config.
- internal/agent/worker.go WorkerState is mutated in 4 switch arms then the loop continues; the clean single place to persist per-turn state is right after turnCancel() and before the runErr!=nil block — wasInterrupted is already computed before turnCancel, so persisting there captures latest history IDs even on errored/interrupted turns.
