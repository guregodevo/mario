//go:build !windows

package tasks

import (
	"os/exec"
	"syscall"
	"time"
)

// killGroupOnCancel starts cmd in its own process group and, when its context
// is cancelled, kills the whole group: the shell and every child it started.
// WaitDelay bounds the wait for pipes a straggler might still hold.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
}
