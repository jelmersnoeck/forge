# Learnings - 2026-06-29 10:04

- The forge Bash tool's own 30s idle watchdog will kill a wrapped `go test ./...` invocation when the compile phase produces no stdout for 30s (cold cache). Work around by precompiling the test binary first with `go test -c ./pkg -o /tmp/x.test` (which streams compiler progress to keep the watchdog fed) then running `/tmp/x.test -test.run ...`. Running tests directly through the agent's Bash tool risks an idle-kill that masks real results.
- When bash idle-kills a wrapper that redirected output to a file (`cmd > /tmp/log 2>&1`), the inner process's partial output is still on disk — `cat` the file to recover results even though the Bash tool returned a killed/error result.
- internal/tools/bash.go reader goroutine: the latest-output-line tracking for live streaming must share the SAME mutex (outputMu) as outputBuf, otherwise -race flags a data race between the reader writing lastLine and the select loop reading it.
