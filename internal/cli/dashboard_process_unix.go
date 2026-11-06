//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func configureDashboardProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func readDashboardApproval(ctx context.Context, input *os.File) (string, error) {
	fd := int(input.Fd())
	if fd < 0 || fd >= unix.FD_SETSIZE {
		return "", errors.New("terminal approval file descriptor exceeds select capacity")
	}
	for ctx.Err() == nil {
		// Darwin reports POLLNVAL for /dev/tty with poll; select supports it.
		var readable unix.FdSet
		readable.Set(fd)
		_, err := unix.Select(fd+1, &readable, nil, nil, &unix.Timeval{Usec: 100000})
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return "", err
		}
		if readable.IsSet(fd) && ctx.Err() == nil {
			buffer := make([]byte, 1024)
			count, err := unix.Read(fd, buffer)
			if count == 0 && err == nil {
				err = errors.New("terminal approval disconnected")
			}
			return string(buffer[:max(0, count)]), err
		}
	}
	return "", ctx.Err()
}
