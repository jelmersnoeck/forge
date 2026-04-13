# Learnings - 2026-04-13 09:51

- Setting cmd.Stderr to a strings.Builder or bytes.Buffer creates a data race: exec.Cmd starts an internal goroutine doing io.Copy to the writer, which races with any read of the buffer in your own goroutine. Use cmd.StderrPipe() + your own drain goroutine with a done channel to synchronize access.
- Claude CLI `--output-format stream-json` emits NDJSON with top-level types: system (init + session_id), stream_event (partial tokens), assistant (complete turn with usage), result (final done signal). Tool use is internal to the CLI and not surfaced separately.
