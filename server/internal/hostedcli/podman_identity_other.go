//go:build !linux

package hostedcli

import "errors"

func podmanStorageIdentity(_ ...string) (string, error) {
	return "", errors.New("hosted CLI Podman storage identity requires Linux")
}
