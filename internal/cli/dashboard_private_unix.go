//go:build !windows

package cli

import "os"

// dashboardSecurePrivate restricts a managed dashboard path to the current user.
func dashboardSecurePrivate(path string, directory bool) error {
	mode := os.FileMode(0o600)
	if directory {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}

// dashboardIsPrivate reports whether only the current user can access path.
func dashboardIsPrivate(_ string, info os.FileInfo) bool {
	return info.Mode().Perm()&0o077 == 0
}
