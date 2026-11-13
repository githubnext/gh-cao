package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The upstream installer and version-only extension are inert fixtures. The
// streamed CAO installer, gh extension discovery, native CAO binary, checksum
// verification still execute as real subprocesses.
const bootstrapAWInstaller = `#!/usr/bin/env bash
set -euo pipefail
[[ -z "${GH_TOKEN:-}${GITHUB_TOKEN:-}${GH_ENTERPRISE_TOKEN:-}${GITHUB_ENTERPRISE_TOKEN:-}${INPUT_VERSION:-}" ]] || exit 65
extension="$XDG_DATA_HOME/gh/extensions/gh-aw/gh-aw"
mkdir -p "$(dirname "$extension")"
cat > "$extension" <<EOF
#!/usr/bin/env bash
[[ "\$*" == version ]] || exit 64
printf '%s\n' 'gh aw version $1' >&2
EOF
chmod 700 "$extension"
printf '%s\n' "$1" >> "$XDG_DATA_HOME/gh-aw-install-requests"
`

func testBootstrapInstallerCompatibility(t *testing.T, ctx context.Context, catalog, consumer string, environment []string, api *BootstrapAPI) {
	t.Helper()
	data := ""
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "XDG_DATA_HOME="); ok {
			data = value
		}
	}
	if data == "" {
		t.Fatal("compatibility tests require isolated extension storage")
	}
	extension := filepath.Join(data, "gh/extensions/gh-aw/gh-aw")
	original := setupRead(t, extension)
	defer func() {
		if err := os.WriteFile(extension, original, 0o700); err != nil { // #nosec G306 -- restore test-owned executable.
			t.Error(err)
		}
	}()
	manifest := filepath.Join(consumer, "aw.yml")
	if _, err := os.Lstat(manifest); !os.IsNotExist(err) {
		t.Fatalf("compatibility fixture must not replace an existing manifest: %v", err)
	}
	defer func() {
		if err := os.Remove(manifest); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	}()
	policy := setupRead(t, filepath.Join(consumer, ".github/workflows/cao.json"))
	tree := setupCheckoutState(t, consumer)
	index := bootstrapRun(t, ctx, consumer, environment, "git", "ls-files", "--stage")
	head := bootstrapRun(t, ctx, consumer, environment, "git", "rev-parse", "HEAD")
	api.mu.Lock()
	state, err := json.Marshal(api.Repositories)
	api.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	installer := setupRead(t, filepath.Join(catalog, "install-gh-cao.sh"))
	t.Run("installer rejects a non-checkout before distribution", func(t *testing.T) {
		directory := t.TempDir()
		api.mu.Lock()
		start := len(api.Requests)
		api.mu.Unlock()
		command := exec.CommandContext(ctx, "bash", "-s")
		command.Dir, command.Env, command.Stdin = directory, environment, bytes.NewReader(installer)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "Run the installer at the root of a GitHub repository checkout") ||
			!strings.Contains(string(output), "No CAO files were written.") {
			t.Fatalf("non-checkout did not fail closed: %v\n%s", err, output)
		}
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 || !bytes.Equal(original, setupRead(t, extension)) {
			t.Fatalf("non-checkout rejection wrote files or changed gh-aw: %v", err)
		}
		api.mu.Lock()
		requests := append([]string(nil), api.Requests[start:]...)
		api.mu.Unlock()
		for _, request := range requests {
			if request != "POST /graphql" && request != "GET /" {
				t.Errorf("non-checkout rejection accessed distribution or repository settings: %s", request)
			}
		}
	})
	for _, channel := range []string{"stable", "prerelease"} {
		required := "v1.2.3"
		if channel == "prerelease" {
			required += "-rc.1"
		}
		for _, scenario := range []string{"missing", "current", "outdated", "newer"} {
			t.Run("installer compatibility/"+channel+"/"+scenario, func(t *testing.T) {
				setupWrite(t, manifest, []byte("min-version: "+required+"\n"))
				version := required
				switch scenario {
				case "missing":
					if err := os.Remove(extension); err != nil {
						t.Fatal(err)
					}
					// This must consult only the isolated XDG directory, not
					// an extension installed in the developer or runner home.
					command := exec.CommandContext(ctx, "gh", "aw", "version")
					command.Dir, command.Env = consumer, environment
					if output, err := command.CombinedOutput(); err == nil {
						t.Fatalf("missing extension resolved an ambient installation: %s", output)
					}
				case "outdated":
					version = "v1.2.2"
				case "newer":
					version = "v1.3.0"
				}
				var before []byte
				if scenario != "missing" {
					before = fmt.Appendf(nil, "#!/usr/bin/env bash\n[[ \"$*\" == version ]] || exit 64\nprintf 'gh aw version %s\\n' >&2\n", version)
					if err := os.WriteFile(extension, before, 0o700); err != nil { // #nosec G306 -- inert fixture executable in isolated extension storage.
						t.Fatal(err)
					}
				}
				api.mu.Lock()
				start := len(api.Requests)
				api.mu.Unlock()
				command := exec.CommandContext(ctx, "bash", "-s")
				command.Dir, command.Env, command.Stdin = consumer, environment, bytes.NewReader(installer)
				output, err := command.CombinedOutput()
				if scenario == "outdated" {
					if err == nil || !strings.Contains(string(output), "gh-aw was not upgraded.") ||
						!strings.Contains(string(output), "bash -s -- "+required) ||
						strings.Contains(string(output), "CAO installed and verified.") {
						t.Fatalf("outdated extension did not fail with exact upgrade guidance: %v\n%s", err, output)
					}
				} else if err != nil || !strings.Contains(string(output), "CAO installed and verified.") {
					t.Fatalf("compatible installer failed: %v\n%s", err, output)
				}
				if scenario == "missing" {
					if got := bootstrapRun(t, ctx, consumer, environment, "gh", "aw", "version"); strings.TrimSpace(got) != "gh aw version "+required {
						t.Fatalf("fresh install ignored requested version: %s", got)
					}
				} else if !bytes.Equal(before, setupRead(t, extension)) {
					t.Fatal("installer replaced a preinstalled extension")
				}
				if !bytes.Equal(policy, setupRead(t, filepath.Join(consumer, ".github/workflows/cao.json"))) ||
					index != bootstrapRun(t, ctx, consumer, environment, "git", "ls-files", "--stage") ||
					head != bootstrapRun(t, ctx, consumer, environment, "git", "rev-parse", "HEAD") {
					t.Fatal("installer compatibility check changed reviewed policy, index or HEAD")
				}
				api.mu.Lock()
				after, marshalErr := json.Marshal(api.Repositories)
				requests := append([]string(nil), api.Requests[start:]...)
				api.mu.Unlock()
				if marshalErr != nil || !bytes.Equal(state, after) {
					t.Fatalf("installer compatibility check changed remote state: %v", marshalErr)
				}
				installs := 0
				for _, request := range requests {
					if scenario == "outdated" && strings.Contains(request, "/releases/download/") {
						t.Errorf("outdated gh-aw did not stop before extension installation: %s", request)
					}
					if request == "GET /github/gh-aw/main/install-gh-aw.sh" {
						installs++
					} else if !strings.HasPrefix(request, "GET ") && request != "POST /graphql" {
						t.Errorf("installer attempted a mutation or dispatch: %s", request)
					}
				}
				wantInstalls := 0
				if scenario == "missing" {
					wantInstalls = 1
				}
				if installs != wantInstalls {
					t.Fatalf("upstream installer requested %d times, want %d", installs, wantInstalls)
				}
			})
		}
	}
	if got := string(setupRead(t, filepath.Join(data, "gh-aw-install-requests"))); got != "v1.2.3\nv1.2.3-rc.1\n" {
		t.Fatalf("fresh installs did not forward exact stable/prerelease versions: %q", got)
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tree, setupCheckoutState(t, consumer)) {
		t.Fatal("compatibility matrix changed installed runtime or consumer files")
	}
}
