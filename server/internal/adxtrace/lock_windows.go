package adxtrace

import (
	"errors"
	"os"
)

func tryLockTrace(*os.File) error {
	return errors.New("unified trace spooling requires a POSIX host")
}
