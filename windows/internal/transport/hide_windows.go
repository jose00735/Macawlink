//go:build windows

package transport

import (
	"os/exec"
	"syscall"
)

// hideWindow stops adb from flashing a console window from the tray app.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
