//go:build linux

package hostedcli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

func podmanStorageIdentity(paths ...string) (string, error) {
	var identity strings.Builder
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			return "", errors.New("hosted CLI Podman storage object identity is unavailable")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", errors.New("hosted CLI Podman storage filesystem identity is unavailable")
		}
		_, _ = fmt.Fprintf(&identity, "%d:%d\x00", stat.Dev, stat.Ino)
	}
	return identity.String(), nil
}
