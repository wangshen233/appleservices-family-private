//go:build !windows

package backgroundexec

import "os/exec"

func Configure(command *exec.Cmd) {}
