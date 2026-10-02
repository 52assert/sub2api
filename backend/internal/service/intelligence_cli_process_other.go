//go:build !unix

package service

import (
	"os/exec"
	"time"
)

func configureIntelligenceCLIProcess(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}

func terminateIntelligenceCLIChildren(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
