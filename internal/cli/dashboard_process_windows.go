//go:build windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configureDashboardProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}

func readDashboardApproval(context.Context, *os.File) (string, error) {
	return "", errors.New("trusted native terminal approval is not available on Windows; maintenance remains copyable")
}
