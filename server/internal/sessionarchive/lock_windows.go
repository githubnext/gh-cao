//go:build windows

package sessionarchive

import (
	"errors"
	"os"
)

func ownerLock(*os.File) error {
	return errors.New("session archive requires POSIX locking and durability")
}
