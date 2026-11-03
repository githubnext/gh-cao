//go:build windows

package collect

import "os/exec"

func remoteValueProcessGroup(command *exec.Cmd) {}
func stopRemoteValueProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
