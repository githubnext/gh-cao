package hostedcli

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// Exclude Config.Env: effective isolation evidence must not capture credentials.
const podmanIsolationFormat = `--format={"Name":{{json .Name}},"Mounts":{{json .Mounts}},"HostConfig":{{json .HostConfig}},"EffectiveCaps":{{json .EffectiveCaps}},"BoundingCaps":{{json .BoundingCaps}},"User":{{json .Config.User}},"Workdir":{{json .Config.WorkingDir}}}`

type podmanCreatedWorkspace struct {
	runtimeID string
	path      string
	workdir   string
	user      string
}

type podmanIsolation struct {
	Name, User, Workdir string
	EffectiveCaps       json.RawMessage
	BoundingCaps        json.RawMessage
	Mounts              []struct {
		Type, Source, Destination string
		RW                        *bool
	}
	HostConfig struct {
		Privileged, ReadonlyRootfs                 *bool
		Memory, PidsLimit                          *int64
		CapAdd, SecurityOpt                        []string
		NetworkMode, PidMode, IpcMode, UTSMode     string
		UsernsMode, CgroupnsMode, CgroupMode       string
		Devices, DeviceRequests, DeviceCgroupRules []json.RawMessage
		PortBindings                             map[string]json.RawMessage
		PublishAllPorts                          bool
		Tmpfs                                      map[string]string
		IDMappings                                 *struct{ UidMap, GidMap []string }
	}
}

func validatePodmanIsolation(ctx context.Context, engine *DockerContainerLifecycle, execution operational.CLICommandExecution, expected podmanCreatedWorkspace) error {
	value, code, err := engine.capture(ctx, []string{"inspect", podmanIsolationFormat, execution.Name})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var actual podmanIsolation
	if err != nil || code != 0 || json.Unmarshal([]byte(value), &actual) != nil {
		return errors.New("hosted CLI Podman effective isolation evidence is unavailable")
	}
	host := actual.HostConfig
	if actual.Name != execution.Name || actual.User != expected.user || actual.Workdir != expected.workdir {
		return errors.New("hosted CLI Podman effective isolation identity does not match the created command")
	}
	if host.Privileged == nil || *host.Privileged || host.ReadonlyRootfs == nil || !*host.ReadonlyRootfs ||
		!emptyPodmanCapabilities(actual.EffectiveCaps) || !emptyPodmanCapabilities(actual.BoundingCaps) || len(host.CapAdd) != 0 ||
		len(host.SecurityOpt) != 1 || host.SecurityOpt[0] != "no-new-privileges" {
		return errors.New("hosted CLI Podman effective isolation security settings are unsafe")
	}
	if host.Memory == nil || *host.Memory != 1<<30 || host.PidsLimit == nil || *host.PidsLimit != 256 {
		return errors.New("hosted CLI Podman effective isolation resource limits are unsafe")
	}
	if host.NetworkMode != "bridge" || (host.UsernsMode != "keep-id" && host.UsernsMode != "private") ||
		!podmanKeepsUser(expected.user, host.IDMappings) ||
		host.PidMode != "private" || host.UTSMode != "private" ||
		!privatePodmanNamespace(host.CgroupnsMode) || !privatePodmanNamespace(host.CgroupMode) ||
		(host.CgroupMode != "private" && host.CgroupnsMode != "private") ||
		(host.IpcMode != "private" && host.IpcMode != "shareable") {
		return errors.New("hosted CLI Podman effective isolation namespaces are unsafe")
	}
	if len(host.Devices) != 0 || len(host.DeviceRequests) != 0 || len(host.DeviceCgroupRules) != 0 ||
		len(host.PortBindings) != 0 || host.PublishAllPorts {
		return errors.New("hosted CLI Podman effective isolation contains extra host devices or port bindings")
	}
	workspaceFound := false
	tmpfsFound := false
	for _, mount := range actual.Mounts {
		if mount.Type == "tmpfs" && mount.Destination == "/tmp" && !tmpfsFound {
			tmpfsFound = true
			continue
		}
		if mount.Type != "bind" || mount.Source != expected.path || mount.Destination != "/workspace" ||
			mount.RW == nil || !*mount.RW || workspaceFound {
			return errors.New("hosted CLI Podman effective isolation contains an unexpected host mount")
		}
		workspaceFound = true
	}
	if !workspaceFound || len(host.Tmpfs) != 1 || !safePodmanTmpfs(host.Tmpfs["/tmp"]) {
		return errors.New("hosted CLI Podman effective isolation workspace or bounded tmpfs is unavailable")
	}
	return nil
}

func podmanKeepsUser(user string, mappings *struct{ UidMap, GidMap []string }) bool {
	uid, gid, ok := strings.Cut(user, ":")
	return ok && mappings != nil && podmanRootlessMapping(mappings.UidMap, uid) && podmanRootlessMapping(mappings.GidMap, gid)
}

func podmanRootlessMapping(mappings []string, id string) bool {
	expected, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return false
	}
	found := false
	for _, mapping := range mappings {
		parts := strings.Split(mapping, ":")
		if len(parts) != 3 {
			return false
		}
		var values [3]uint64
		for i, part := range parts {
			values[i], err = strconv.ParseUint(part, 10, 32)
			if err != nil {
				return false
			}
		}
		if values[2] == 0 {
			return false
		}
		// Podman's host IDs here are relative to the intermediate rootless
		// namespace: its ID 0 is the executor's host UID/GID.
		if values[1] == 0 {
			if found || values[0] != expected || values[2] != 1 {
				return false
			}
			found = true
		} else if values[0] <= expected && expected < values[0]+values[2] {
			return false
		}
	}
	return found
}

func emptyPodmanCapabilities(value json.RawMessage) bool {
	var capabilities []string
	// Podman serializes an empty capability set as null, not necessarily [].
	return len(value) != 0 && json.Unmarshal(value, &capabilities) == nil && len(capabilities) == 0
}

func privatePodmanNamespace(mode string) bool {
	return mode == "" || mode == "private"
}

func safePodmanTmpfs(options string) bool {
	flags := make(map[string]bool)
	sizeFound := false
	for _, option := range strings.Split(options, ",") {
		if flags[option] {
			return false
		}
		switch option {
		case "rw", "nosuid", "nodev", "noexec", "rprivate", "tmpcopyup", "mode=1777":
		case "size=64m", "size=65536k", "size=67108864":
			if sizeFound {
				return false
			}
			sizeFound = true
		default:
			return false
		}
		flags[option] = true
	}
	return flags["rw"] && flags["noexec"] && flags["nosuid"] && flags["nodev"] && sizeFound
}
