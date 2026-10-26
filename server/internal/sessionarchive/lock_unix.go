//go:build !windows

package sessionarchive

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func ownerLock(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
		return ErrBusy
	}
	return err
}
