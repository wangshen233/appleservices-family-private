//go:build !windows

package dashboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func intString(number int) string        { return strconv.Itoa(number) }
func configureProcess(command *exec.Cmd) {}
func processPath(pid int) string {
	path, _ := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	return path
}
