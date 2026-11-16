//go:build windows

package cli

import (
	"os/exec"
	"strconv"
)

func configureDashboardProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
