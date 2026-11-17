//go:build !windows

package transport

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
