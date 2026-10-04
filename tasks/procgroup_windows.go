//go:build windows

package tasks

import (
	"os/exec"
	"time"
)

// killGroupOnCancel on Windows kills the process (no process groups here)
// and bounds the wait for pipes a child might still hold.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
