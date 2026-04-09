//go:build windows

package tools

import "os/exec"

// setProcGroup is a no-op on Windows. Process group management is not
// supported through the unix Setpgid mechanism.
func setProcGroup(_ *exec.Cmd) {}
