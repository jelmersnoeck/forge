# Learnings - 2026-06-28 16:30

- forge pipeline routing: a session with no pipeline_hint defaults to 'auto', which runs ClassifyIntent + a size gate; large tasks route to the ideation/debate pipeline. To force a plain single-agent spec creator (no ideation, no spec-skip), use pipeline_hint='spec' (resolveTaskPipeline in internal/agent/phase/orchestrator.go).
- forge orchestrator classifies intent (question/investigate/review/task) unless SpecPath is set. To force the task path while still running classification for size/spec-match, use OrchestratorOpts.ForceTask which guards only the non-task branches.
- forge worker tracks first-vs-followup via WorkerState.Phase: PhaseIdle = first message. Use state.Phase == PhaseIdle to apply behavior only to a session's opening message (e.g. issue-driven forcing).
