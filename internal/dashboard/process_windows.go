//go:build windows

package dashboard

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

var queryImage = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

func intString(number int) string { return strconv.Itoa(number) }
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200}
}
func processPath(pid int) string {
	if pid <= 0 {
		return ""
	}
	handle, err := syscall.OpenProcess(0x1000|0x00100000, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(handle)
	if wait, err := syscall.WaitForSingleObject(handle, 0); err != nil || wait != syscall.WAIT_TIMEOUT {
		return ""
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	ok, _, _ := queryImage.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer[:size])
}
