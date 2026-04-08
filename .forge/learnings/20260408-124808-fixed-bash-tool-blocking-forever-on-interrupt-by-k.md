# Learnings - 2026-04-08 12:48

- exec.CommandContext in Go only sends SIGKILL to the direct child process — grandchildren (tail -f, sleep, servers) survive as orphans. Use cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} + cmd.Cancel to kill the entire process group via syscall.Kill(-pgid, SIGKILL)
- Even after killing a parent process, cmd.Run()/cmd.Wait() can block indefinitely if child processes hold stdout/stderr pipes open. cmd.WaitDelay (Go 1.20+) provides a backstop timeout for pipe cleanup
- Go's cmd.Cancel and cmd.WaitDelay (added in Go 1.20) are the modern way to handle process tree cleanup — prefer them over manual goroutine-based approaches with cmd.Process.Kill()
