package hostedcli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestPodmanRejectsUnsafeEffectiveIsolationBeforeStart(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"privileged default", func(_ map[string]any, host map[string]any) { host["Privileged"] = true }},
		{"missing privileged evidence", func(_ map[string]any, host map[string]any) { delete(host, "Privileged") }},
		{"writable root", func(_ map[string]any, host map[string]any) { host["ReadonlyRootfs"] = false }},
		{"extra capability", func(_ map[string]any, host map[string]any) { host["CapAdd"] = []string{"CAP_SYS_ADMIN"} }},
		{"effective capability", func(value map[string]any, _ map[string]any) { value["EffectiveCaps"] = []string{"CAP_SYS_ADMIN"} }},
		{"bounding capability", func(value map[string]any, _ map[string]any) { value["BoundingCaps"] = []string{"CAP_CHOWN"} }},
		{"missing capability evidence", func(value map[string]any, _ map[string]any) { delete(value, "EffectiveCaps") }},
		{"missing no new privileges", func(_ map[string]any, host map[string]any) { host["SecurityOpt"] = []string{} }},
		{"disabled SELinux", func(_ map[string]any, host map[string]any) {
			host["SecurityOpt"] = []string{"no-new-privileges", "label=disable"}
		}},
		{"unbounded memory", func(_ map[string]any, host map[string]any) { host["Memory"] = 0 }},
		{"unbounded pids", func(_ map[string]any, host map[string]any) { host["PidsLimit"] = -1 }},
		{"host network", func(_ map[string]any, host map[string]any) { host["NetworkMode"] = "host" }},
		{"host pid namespace", func(_ map[string]any, host map[string]any) { host["PidMode"] = "host" }},
		{"foreign ipc namespace", func(_ map[string]any, host map[string]any) { host["IpcMode"] = "container:foreign" }},
		{"host user namespace", func(_ map[string]any, host map[string]any) { host["UsernsMode"] = "host" }},
		{"missing user mapping", func(_ map[string]any, host map[string]any) { delete(host, "IDMappings") }},
		{"wrong user mapping", func(_ map[string]any, host map[string]any) {
			host["IDMappings"] = map[string]any{"UidMap": []string{"1001:1:1"}, "GidMap": []string{"1001:1:1"}}
		}},
		{"host device", func(_ map[string]any, host map[string]any) {
			host["Devices"] = []map[string]any{{"PathOnHost": "/dev/kvm", "PathInContainer": "/dev/kvm"}}
		}},
		{"automatic credential mount", func(value map[string]any, _ map[string]any) {
			mounts := value["Mounts"].([]map[string]any)
			value["Mounts"] = append(mounts, map[string]any{
				"Type": "bind", "Source": "/host/credentials", "Destination": "/run/secrets", "RW": false,
			})
		}},
		{"default named volume", func(value map[string]any, _ map[string]any) {
			value["Mounts"] = append(value["Mounts"].([]map[string]any),
				map[string]any{"Type": "volume", "Source": "/host/volume", "Destination": "/extra", "RW": true})
		}},
		{"wrong workspace", func(value map[string]any, _ map[string]any) {
			value["Mounts"].([]map[string]any)[0]["Source"] = "/host/home"
		}},
		{"readonly workspace", func(value map[string]any, _ map[string]any) {
			value["Mounts"].([]map[string]any)[0]["RW"] = false
		}},
		{"missing workspace", func(value map[string]any, _ map[string]any) { value["Mounts"] = nil }},
		{"extra tmpfs", func(_ map[string]any, host map[string]any) {
			host["Tmpfs"] = map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=64m", "/run": "rw"}
		}},
		{"executable tmpfs", func(_ map[string]any, host map[string]any) {
			host["Tmpfs"] = map[string]string{"/tmp": "rw,nosuid,nodev,size=64m"}
		}},
		{"overridden tmpfs size", func(_ map[string]any, host map[string]any) {
			host["Tmpfs"] = map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=64m,size=1g"}
		}},
		{"wrong working directory", func(value map[string]any, _ map[string]any) { value["Workdir"] = "/" }},
		{"wrong user", func(value map[string]any, _ map[string]any) { value["User"] = "other-user" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, commands, process := podmanTest(t)
			base := commands.run
			commands.run = func(ctx context.Context, p Process, out OutputFunc) (int, error) {
				if slices.Contains(p.Args, podmanIsolationFormat) {
					return base(ctx, p, func(stream string, _ []byte) {
						value := podmanIsolationFixture(process.Workspace, p.Args[len(p.Args)-1])
						test.mutate(value, value["HostConfig"].(map[string]any))
						encoded, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						out(stream, encoded)
					})
				}
				return base(ctx, p, out)
			}
			if _, err := runner.Run(t.Context(), process, nil); err == nil || !strings.Contains(err.Error(), "effective isolation") {
				t.Fatalf("unsafe effective configuration was not rejected: %v", err)
			}
			removed := false
			for _, p := range commands.processes {
				switch podmanOperation(p) {
				case "start", "logs":
					t.Fatal("unsafe effective configuration reached workload start/output")
				case "rm":
					removed = true
				}
			}
			if !removed {
				t.Fatal("rejected container was not cleaned up")
			}
		})
	}
}

func TestPodmanRequiresNewCreationBeforeStart(t *testing.T) {
	runner, commands, _ := podmanTest(t)
	id, err := runner.Lifecycle.RuntimeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	commands.processes = nil
	if err := runner.Lifecycle.Start(t.Context(), operational.CLICommandExecution{RuntimeID: id}); err == nil {
		t.Fatal("start accepted without adapter-owned creation metadata")
	}
	if len(commands.processes) != 0 {
		t.Fatal("unregistered start reached the engine")
	}
}
