//go:build !windows

package adxtrace

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockTrace(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
		return ErrBusy
	}
	return err
}
