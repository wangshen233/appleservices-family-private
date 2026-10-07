//go:build windows

// Package backgroundexec prevents protocol helpers from opening console windows.
package backgroundexec

import (
	"os/exec"
	"syscall"
)

func Configure(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
