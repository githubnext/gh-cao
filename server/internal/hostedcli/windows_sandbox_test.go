package hostedcli

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

type windowsSandboxTestMachine struct {
	mutex       sync.Mutex
	calls       []Process
	id          string
	workspace   string
	input       string
	raw         string
	startError  bool
	stopIgnored bool
	startGate   func()
	stopGate    func()
	guest       func(string, string) error
	hcsRaw      string
	hcsError    error
	hcsRetained bool
	hcsReads    int
}

func (m *windowsSandboxTestMachine) computeSystems(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.hcsReads++
	if m.hcsError != nil {
		return nil, m.hcsError
	}
	if m.hcsRaw != "" {
		return []byte(m.hcsRaw), nil
	}
	if m.id != "" || m.hcsRetained {
		// Deliberately not the wsb GUID: the adapter must not infer a mapping.
		return []byte(`[{"Id":"unmapped-host-compute-id","SystemType":"VirtualMachine","State":"Running"}]`), nil
	}
	return []byte("[]"), nil
}

func (m *windowsSandboxTestMachine) Run(ctx context.Context, process Process, output OutputFunc) (int, error) {
	m.mutex.Lock()
	m.calls = append(m.calls, process)
	m.mutex.Unlock()
	value := func(name string) string {
		index := slices.Index(process.Args, name)
		if index < 0 || index+1 >= len(process.Args) {
			return ""
		}
		return process.Args[index+1]
	}
	switch process.Args[0] {
	case "list":
		m.mutex.Lock()
		data := m.raw
		if data == "" {
			data = "[]"
			if m.id != "" {
				data = `[{"Id":"` + m.id + `","Status":"running","Uptime":"00:00:01"}]`
			}
		}
		m.mutex.Unlock()
		output("stdout", []byte(data))
	case "start":
		if m.startGate != nil {
			m.startGate()
		}
		if m.startError {
			return -1, errors.New("uncertain launcher: secret diagnostics must not escape")
		}
		var config struct {
			Folders []struct {
				HostFolder    string
				SandboxFolder string
			} `xml:"MappedFolders>MappedFolder"`
		}
		if err := xml.Unmarshal([]byte(value("--config")), &config); err != nil {
			return -1, err
		}
		m.mutex.Lock()
		m.id = value("--id")
		for _, folder := range config.Folders {
			if folder.SandboxFolder == `C:\cao-input` {
				m.input = folder.HostFolder
			} else {
				m.workspace = folder.HostFolder
			}
		}
		m.mutex.Unlock()
	case "exec":
		m.mutex.Lock()
		input, workspace, id := m.input, m.workspace, m.id
		m.mutex.Unlock()
		if id != value("--id") {
			return -1, errors.New("requested sandbox is absent")
		}
		if m.guest != nil {
			return 0, m.guest(input, workspace)
		}
		<-ctx.Done()
		return -1, ctx.Err()
	case "stop":
		if m.stopGate != nil {
			m.stopGate()
		}
		m.mutex.Lock()
		if m.id != value("--id") {
			m.mutex.Unlock()
			return -1, errors.New("cannot stop another sandbox")
		}
		if !m.stopIgnored {
			m.id = ""
		}
		m.mutex.Unlock()
	default:
		return -1, errors.New("unsupported control command")
	}
	return 0, nil
}

func (m *windowsSandboxTestMachine) operations() []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result := make([]string, len(m.calls))
	for i, call := range m.calls {
		result[i] = call.Args[0]
	}
	return result
}

type windowsSandboxFixture struct {
	base    string
	host    windowsSandboxHost
	options WindowsSandboxOptions
	machine *windowsSandboxTestMachine
}

func windowsSandboxTestDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(".", ".windows-sandbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(absolute); err != nil {
			t.Error(err)
		}
	})
	return absolute
}

func newWindowsSandboxFixture(t *testing.T) *windowsSandboxFixture {
	t.Helper()
	base := windowsSandboxTestDirectory(t)
	root := filepath.Join(base, "machine-state")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(base, "native-tools.zip")
	digest := writeWindowsSandboxTestArchive(t, archive, []string{"gh/gh.exe", "gh-aw/gh-aw.exe", "git/cmd/git.exe", "git/usr/bin/sh.exe"})
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	machine := &windowsSandboxTestMachine{}
	host := windowsSandboxHost{
		root: root, id: "windows-sandbox:machine-user-hash", binary: "wsb.exe",
		env: []string{`SystemRoot=C:\Windows`}, busy: func() (bool, error) { return false, nil },
		computeSystems: machine.computeSystems,
		privateWorkspace: func(context.Context, string) (func(), error) {
			return func() {}, nil
		},
		lock: func(ctx context.Context) (func(), error) {
			select {
			case <-gate:
				return func() { gate <- struct{}{} }, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	return &windowsSandboxFixture{
		base: base, host: host, options: WindowsSandboxOptions{ToolsArchive: archive, SHA256: digest},
		machine: machine,
	}
}

func writeWindowsSandboxTestArchive(t *testing.T, filename string, names []string) string {
	t.Helper()
	// #nosec G304 -- Test-only ZIP under the fixture's private project-relative directory.
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("MZ portable test executable")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- Hash the ZIP created immediately above in the private test fixture.
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (f *windowsSandboxFixture) runner(t *testing.T) *ContainerRunner {
	t.Helper()
	runner, err := newWindowsSandboxRunner(f.options, f.machine, f.host)
	if err != nil {
		t.Fatal(err)
	}
	runner.Lifecycle.(*windowsSandboxLifecycle).interval = time.Millisecond
	return runner
}

func (f *windowsSandboxFixture) execution(id int64) operational.CLICommandExecution {
	return operational.CLICommandExecution{
		CommandID: id, Name: fmt.Sprintf("cao-hosted-cli-%032x", id), RuntimeID: f.host.id,
	}
}

func (f *windowsSandboxFixture) spec(t *testing.T, suffix string) ContainerSpec {
	t.Helper()
	workspace := filepath.Join(f.base, "workspace-"+suffix)
	for _, directory := range []string{"home", "work", "data"} {
		if err := os.MkdirAll(filepath.Join(workspace, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return ContainerSpec{
		Image: "windows-bundle@sha256:" + f.options.SHA256, Workspace: workspace, Directory: filepath.Join(workspace, "work"),
		Program: "gh-aw", Args: []string{"update", "--pr-message", `quoted "message"; $(not-source) \`},
		Env: []string{"GH_TOKEN=scoped-test-secret", "HOME=/workspace/home", "XDG_DATA_HOME=/workspace/data", "GIT_CONFIG_SYSTEM=/dev/null"},
	}
}

func writeWindowsSandboxTestReceipt(input, workspace string, code int) error {
	root, err := os.OpenRoot(input)
	if err != nil {
		return err
	}
	var invocation windowsSandboxInvocation
	err = windowsSandboxReadJSON(root, "invocation.json", &invocation)
	_ = root.Close()
	if err != nil {
		return err
	}
	output, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = output.Close() }()
	if err := output.WriteFile(filepath.Join(".cao-output", "stdout"), []byte("native output\n"), 0o600); err != nil {
		return err
	}
	return windowsSandboxWriteJSON(output, filepath.Join(".cao-output", "result.json"),
		windowsSandboxReceipt{Execution: invocation.Execution, ExitCode: &code})
}

func TestWindowsSandboxConstructorAndPinnedArchive(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	if runtime.GOOS != "windows" {
		if _, err := NewWindowsSandboxRunner(f.options, f.machine); err == nil {
			t.Fatal("production constructor accepted a non-Windows host")
		}
	}
	runner := f.runner(t)
	if runner.Image != "windows-bundle@sha256:"+f.options.SHA256 || containerImagePattern.MatchString(runner.Image) {
		t.Fatal("native payload identity pretends to be OCI")
	}
	f.options.SHA256 = strings.Repeat("0", 64)
	if _, err := newWindowsSandboxRunner(f.options, f.machine, f.host); err == nil {
		t.Fatal("wrong ZIP hash accepted")
	}
	if len(f.machine.operations()) != 0 {
		t.Fatal("archive validation invoked the VM runtime")
	}
}

func TestWindowsSandboxExecutorUsesPinnedBundleIdentity(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	f.machine.guest = func(input, workspace string) error {
		return writeWindowsSandboxTestReceipt(input, workspace, 0)
	}
	runner := f.runner(t)
	queue := newFakeQueue()
	minter := newFakeMinter()
	broker := NewBroker(minter, BrokerOptions{})
	t.Cleanup(func() { _ = broker.Close(context.Background()) })
	executor, err := NewExecutor(queue, broker, fakeGitHub{permission: "write"}, runner, Options{
		Owner: "windows-executor", ContainerImage: runner.Image, WorkRoot: f.base,
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := queue.Enqueue(t.Context(), operational.CLICommandInvocation{
		ActionID: "run", Command: "gh workflow run collect.yml --repo octo/app",
		Actor: "octocat", ActorID: 101, Repository: "octo/app",
	}, "windows-run", 0)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := queue.Acquire(t.Context(), "windows-executor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim(t.Context(), lease)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	executor.execute(t.Context(), lease, *claimed)
	completed, err := queue.Detail(t.Context(), command.ID)
	if err != nil || completed.Status != operational.CLICommandCompleted || completed.ExecutionClaimed {
		t.Fatalf("Windows execution = %+v, %v", completed, err)
	}
	if minter.liveCount() != 0 {
		t.Fatal("Windows execution retained a work token")
	}
	if _, err := NewContainerRunner(runner.Image, newTestOCILifecycle(nil)); err == nil {
		t.Fatal("Windows tools bundle was accepted by the OCI runner constructor")
	}
}

func TestWindowsSandboxArchivePathsAndRequiredTools(t *testing.T) {
	for _, extra := range []string{"../escape", "git/../escape", "git/NUL.txt", "git/tool:stream", "git/dir./tool", `git\tool.exe`, "/git/tool.exe", "GH/GH.EXE"} {
		t.Run(extra, func(t *testing.T) {
			f := newWindowsSandboxFixture(t)
			f.options.SHA256 = writeWindowsSandboxTestArchive(t, f.options.ToolsArchive,
				[]string{"gh/gh.exe", "gh-aw/gh-aw.exe", "git/cmd/git.exe", "git/usr/bin/sh.exe", extra})
			if _, err := newWindowsSandboxRunner(f.options, f.machine, f.host); err == nil {
				t.Fatal("unsafe or duplicate native tool path accepted")
			}
		})
	}
	f := newWindowsSandboxFixture(t)
	f.options.SHA256 = writeWindowsSandboxTestArchive(t, f.options.ToolsArchive, []string{"gh/gh.exe"})
	if _, err := newWindowsSandboxRunner(f.options, f.machine, f.host); err == nil {
		t.Fatal("bundle without gh-aw and Git accepted")
	}
}

func TestWindowsSandboxInventoryFailsClosed(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"Sandboxes":[]}`, "[] trailing", "[null]", `[{}]`,
		`[{"Id":"bad","Status":"running"}]`,
		`[{"Id":"00000000-0000-0000-0000-000000000001","Status":null}]`,
		`[{"Id":"00000000-0000-0000-0000-000000000001","Status":"unknown"}]`,
		`[{"Id":"00000000-0000-0000-0000-000000000001","Status":"running","status":"stopped"}]`,
		`[{"Id":"00000000-0000-0000-0000-000000000001","Status":"running","Other":1}]`,
		`[{"Id":"00000000-0000-0000-0000-000000000001","Status":"running"},{"Id":"00000000-0000-0000-0000-000000000002","Status":"running"}]`,
	} {
		if _, err := parseWindowsSandboxInventory([]byte(raw)); err == nil {
			t.Fatalf("malformed or unsupported inventory accepted: %q", raw)
		}
	}
	for _, raw := range []string{"[]", `[{"id":"00000000-0000-0000-0000-000000000001","status":"Stopped"}]`} {
		if _, err := parseWindowsSandboxInventory([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowsSandboxStoppedInventoryDoesNotBecomeAnAdoptedVM(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	f.machine.raw = `[{"Id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","Status":"stopped"}]`
	runner := f.runner(t)
	if err := runner.Lifecycle.Create(t.Context(), f.execution(1), f.spec(t, "stopped-inventory")); err != nil {
		t.Fatal("a historical stopped entry prevented a new reservation:", err)
	}
	if err := runner.Lifecycle.Stop(t.Context(), f.execution(1)); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.machine.operations(), "stop") {
		t.Fatal("historical unrelated identity was touched")
	}
}

func TestWindowsSandboxPreexistingSandboxIsNeverTouched(t *testing.T) {
	for _, scenario := range []string{"inventory", "host-process", "unsupported-inventory"} {
		t.Run(scenario, func(t *testing.T) {
			f := newWindowsSandboxFixture(t)
			switch scenario {
			case "inventory":
				f.machine.id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			case "host-process":
				f.host.busy = func() (bool, error) { return true, nil }
			case "unsupported-inventory":
				f.machine.raw = `{"instances":[]}`
			}
			runner := f.runner(t)
			spec := f.spec(t, "preexisting")
			registered := false
			code, err := runner.Run(t.Context(), Process{
				CommandID: 1, Program: spec.Program, Args: spec.Args, Env: spec.Env, Workspace: spec.Workspace, Dir: spec.Directory,
				RegisterContainer: func(_ context.Context, _ operational.CLICommandExecution) error { registered = true; return nil },
			}, nil)
			if err == nil || code != -1 || !registered {
				t.Fatalf("pre-existing Sandbox was not rejected after registration: %d %v", code, err)
			}
			for _, operation := range f.machine.operations() {
				if operation != "list" {
					t.Fatalf("unrelated Sandbox was touched by %s", operation)
				}
			}
		})
	}
}

func TestWindowsSandboxRunRequiresIndependentVMTermination(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	f.machine.guest = func(input, workspace string) error { return writeWindowsSandboxTestReceipt(input, workspace, 7) }
	runner := f.runner(t)
	spec := f.spec(t, "success")
	registered := false
	f.machine.startGate = func() {
		if !registered {
			t.Error("VM launched before durable registration")
		}
	}
	var stdout strings.Builder
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	code, err := runner.Run(ctx, Process{
		CommandID: 1, Program: spec.Program, Args: spec.Args, Env: spec.Env, Workspace: spec.Workspace, Dir: spec.Directory,
		RegisterContainer: func(_ context.Context, _ operational.CLICommandExecution) error { registered = true; return nil },
	}, func(stream string, data []byte) {
		if stream == "stdout" {
			_, _ = stdout.Write(data)
		}
	})
	if code != 7 || err != nil || stdout.String() != "native output\n" {
		t.Fatalf("unexpected result: code=%d err=%v output=%q", code, err, stdout.String())
	}
	operations := f.machine.operations()
	stop := slices.Index(operations, "stop")
	if stop < 0 || stop == len(operations)-1 || operations[stop+1] != "list" {
		t.Fatalf("stop was not independently confirmed: %v", operations)
	}
	f.machine.mutex.Lock()
	defer f.machine.mutex.Unlock()
	for _, call := range f.machine.calls {
		if strings.Contains(strings.Join(call.Args, " ")+strings.Join(call.Env, " "), "scoped-test-secret") {
			t.Fatal("credential was exposed in a host control invocation")
		}
		if call.Args[0] == "exec" && (!slices.Contains(call.Args, "System") || strings.Contains(strings.Join(call.Args, " "), spec.Args[2])) {
			t.Fatal("guest command was interpolated or required interactive login")
		}
	}
}

func TestWindowsSandboxStagingConfigurationAndEnvironment(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	execution := f.execution(1)
	spec := f.spec(t, "staging")
	if err := runner.Lifecycle.Create(t.Context(), execution, spec); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Join(f.host.root, execution.Name, "input"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var invocation windowsSandboxInvocation
	if err := windowsSandboxReadJSON(root, "invocation.json", &invocation); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(invocation.Args, spec.Args) || invocation.Env["GH_TOKEN"] != "scoped-test-secret" ||
		invocation.Env["HOME"] != `C:\cao-workspace\home` || invocation.Env["GIT_CONFIG_SYSTEM"] != "NUL" ||
		invocation.Directory != `C:\cao-workspace\work` {
		t.Fatal("invocation did not preserve argv or translate the private environment")
	}
	config, err := windowsSandboxConfig(`C:\private\a&b`, `C:\private\work`)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"a&amp;b", "<ReadOnly>true</ReadOnly>", "<ReadOnly>false</ReadOnly>",
		"<vGPU>Disable</vGPU>", "<ClipboardRedirection>Disable</ClipboardRedirection>", "<AudioInput>Disable</AudioInput>",
		"<VideoInput>Disable</VideoInput>", "<PrinterRedirection>Disable</PrinterRedirection>", "<ProtectedClient>Enable</ProtectedClient>",
		"<MemoryInMB>2048</MemoryInMB>", "<Networking>Enable</Networking>"} {
		if !strings.Contains(config, fragment) {
			t.Fatalf("configuration missing %s", fragment)
		}
	}
	if err := runner.Reconcile(t.Context(), execution); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSandboxRejectsUnsafeEnvironmentAndRestagesDigest(t *testing.T) {
	for _, entries := range [][]string{
		{"PATH=host-tools"}, {"GH_TOKEN=one", "GH_TOKEN=two"}, {"GIT_CONFIG_VALUE_1=!host-command"},
		{"HOME=/workspace/../private"}, {"HOME=/workspace/home%VAR%"}, {"GIT_CONFIG_SYSTEM=C:/host"},
		{"GH_TOKEN=secret\nunexpected"},
	} {
		if _, err := windowsSandboxEnvironment(entries); err == nil {
			t.Fatal("unsafe environment accepted")
		}
	}
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	spec := f.spec(t, "restaged")
	if err := os.WriteFile(f.options.ToolsArchive, []byte("changed after constructor"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runner.Lifecycle.Create(t.Context(), f.execution(1), spec); err == nil {
		t.Fatal("changed staged archive accepted")
	}
	if slices.Contains(f.machine.operations(), "start") {
		t.Fatal("VM launched before staged bytes were verified")
	}
	if err := runner.Reconcile(t.Context(), f.execution(1)); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSandboxReservationsFenceDistinctAdaptersAndRecovery(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	first, second := f.runner(t), f.runner(t)
	a, b := f.execution(1), f.execution(2)
	b.CommandID = a.CommandID // Different queues can reuse their local integer ID.
	one, two := f.spec(t, "one"), f.spec(t, "two")
	if err := first.Lifecycle.Create(t.Context(), a, one); err != nil {
		t.Fatal(err)
	}
	if err := second.Lifecycle.Create(t.Context(), b, two); err == nil {
		t.Fatal("another adapter reused a reserved Sandbox")
	}
	if err := second.Reconcile(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := second.Lifecycle.Create(t.Context(), b, two); err == nil {
		t.Fatal("unrelated cleanup removed the first adapter's reservation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := first.Lifecycle.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	fresh := f.runner(t)
	if err := fresh.Reconcile(t.Context(), a); err != nil {
		t.Fatal("fresh-process recovery could not stop the durably owned VM:", err)
	}
	if err := second.Lifecycle.Create(t.Context(), b, two); err != nil {
		t.Fatal("confirmed cleanup did not release global ownership:", err)
	}
	if err := second.Reconcile(t.Context(), b); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSandboxConcurrentLaunchAndCleanupAreSerialized(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	first, recovery := f.runner(t), f.runner(t)
	execution := f.execution(1)
	if err := first.Lifecycle.Create(t.Context(), execution, f.spec(t, "concurrent")); err != nil {
		t.Fatal(err)
	}
	started, proceed := make(chan struct{}), make(chan struct{})
	f.machine.startGate = func() { close(started); <-proceed }
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	launchDone := make(chan error, 1)
	go func() { launchDone <- first.Lifecycle.Start(ctx, execution) }()
	<-started
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- recovery.Lifecycle.Stop(ctx, execution) }()
	select {
	case err := <-cleanupDone:
		t.Fatalf("cleanup crossed an outstanding create request: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	if err := <-launchDone; err != nil {
		t.Fatal(err)
	}
	if err := <-cleanupDone; err != nil {
		t.Fatal(err)
	}
	operations := f.machine.operations()
	if slices.Index(operations, "start") >= slices.Index(operations, "stop") {
		t.Fatalf("cleanup happened before the original launch completed: %v", operations)
	}
	f.machine.mutex.Lock()
	defer f.machine.mutex.Unlock()
	if f.machine.id != "" {
		t.Fatal("owned VM survived concurrent cleanup")
	}
}

func TestWindowsSandboxConcurrentCleanupCannotReleaseAnotherExecution(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	first, second := f.runner(t), f.runner(t)
	a, b := f.execution(1), f.execution(2)
	if err := first.Lifecycle.Create(t.Context(), a, f.spec(t, "cleanup-one")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := first.Lifecycle.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	stopping, proceed := make(chan struct{}), make(chan struct{})
	f.machine.stopGate = func() { close(stopping); <-proceed }
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- first.Lifecycle.Stop(ctx, a) }()
	<-stopping
	spec := f.spec(t, "cleanup-two")
	createDone := make(chan error, 1)
	go func() { createDone <- second.Lifecycle.Create(ctx, b, spec) }()
	select {
	case err := <-createDone:
		t.Fatalf("new ownership crossed an outstanding stop request: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(proceed)
	if err := <-cleanupDone; err != nil {
		t.Fatal(err)
	}
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	// A duplicate recovery attempt for A must not clear B's durable owner.
	if err := first.Lifecycle.Stop(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := first.Lifecycle.Create(ctx, f.execution(3), f.spec(t, "cleanup-three")); err == nil {
		t.Fatal("stale cleanup removed another execution's global fence")
	}
	if err := second.Lifecycle.Stop(ctx, b); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSandboxUserLaunchBetweenPrepareAndStartIsNotAdopted(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	execution := f.execution(1)
	if err := runner.Lifecycle.Create(t.Context(), execution, f.spec(t, "user-launch")); err != nil {
		t.Fatal(err)
	}
	f.machine.id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	if err := runner.Lifecycle.Start(t.Context(), execution); err == nil {
		t.Fatal("user-started Sandbox was adopted")
	}
	if err := runner.Lifecycle.Stop(t.Context(), execution); err == nil {
		t.Fatal("cleanup declared machine-wide absence while the unrelated HCS VM remained")
	}
	if slices.Contains(f.machine.operations(), "stop") || slices.Contains(f.machine.operations(), "start") {
		t.Fatal("preflight failure touched the unrelated user Sandbox")
	}
	// Only the unrelated user's own shutdown can unblock the exclusive-VM
	// profile. This executor must not issue any stop for its prepared record.
	f.machine.id = ""
	if err := runner.Lifecycle.Stop(t.Context(), execution); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSandboxUncertainStartRetainsGlobalFence(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	f.machine.startError = true
	first := f.runner(t)
	a, b := f.execution(1), f.execution(2)
	if err := first.Lifecycle.Create(t.Context(), a, f.spec(t, "uncertain")); err != nil {
		t.Fatal(err)
	}
	if err := first.Lifecycle.Start(t.Context(), a); err == nil {
		t.Fatal("uncertain start accepted")
	} else if strings.Contains(err.Error(), "secret diagnostics") {
		t.Fatal("raw runtime diagnostics leaked")
	}
	fresh := f.runner(t)
	if err := fresh.Reconcile(t.Context(), a); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatal("empty inventory improperly settled an outstanding start:", err)
	}
	if err := fresh.Lifecycle.Create(t.Context(), b, f.spec(t, "blocked")); err == nil {
		t.Fatal("another process bypassed the uncertain start fence")
	}
	if slices.Contains(f.machine.operations(), "stop") {
		t.Fatal("uncertain absent launch stopped an unrelated workload")
	}
}

func TestWindowsSandboxMalformedStopInventoryRetainsOwnership(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	execution := f.execution(1)
	if err := runner.Lifecycle.Create(t.Context(), execution, f.spec(t, "bad-stop-inventory")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := runner.Lifecycle.Start(ctx, execution); err != nil {
		t.Fatal(err)
	}
	f.machine.stopGate = func() {
		f.machine.mutex.Lock()
		f.machine.raw = `{"stopped":true}`
		f.machine.mutex.Unlock()
	}
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatal("unknown stop-inventory shape was mistaken for absence:", err)
	}
	root, err := os.OpenRoot(f.host.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	owner, err := windowsSandboxOwner(root)
	if err != nil || owner != execution {
		t.Fatal("unconfirmed cleanup released its durable ownership")
	}
}

func TestWindowsSandboxReceiptAndLauncherExitDoNotProveTermination(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	f.machine.stopIgnored = true
	f.machine.guest = func(input, workspace string) error { return writeWindowsSandboxTestReceipt(input, workspace, 0) }
	runner := f.runner(t)
	execution := f.execution(1)
	if err := runner.Lifecycle.Create(t.Context(), execution, f.spec(t, "still-running")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := runner.Lifecycle.Start(ctx, execution); err != nil {
		t.Fatal(err)
	}
	outputCtx, endOutput := context.WithTimeout(ctx, 40*time.Millisecond)
	defer endOutput()
	if err := runner.Lifecycle.CollectOutput(outputCtx, execution, nil); err == nil {
		t.Fatal("a guest receipt exposed an exit before the VM was stopped")
	}
	state, err := runner.Lifecycle.Inspect(t.Context(), execution)
	if err != nil || state.Terminated || state.ExitCode != nil {
		t.Fatalf("unconfirmed VM reported a workload exit: %#v %v", state, err)
	}
	fresh := f.runner(t)
	short, end := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer end()
	if err := fresh.Lifecycle.Stop(short, execution); err == nil {
		t.Fatal("stop acknowledgement was mistaken for VM termination:", err)
	}
}

func TestWindowsSandboxLostAndInvalidReceiptsNeverInventExitCodes(t *testing.T) {
	for _, code := range []int{-1, 256, 0} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := newWindowsSandboxFixture(t)
			f.machine.guest = func(input, workspace string) error {
				if code == 0 {
					return errors.New("lost guest output")
				}
				return writeWindowsSandboxTestReceipt(input, workspace, code)
			}
			runner := f.runner(t)
			spec := f.spec(t, "bad-receipt")
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			actual, err := runner.Run(ctx, Process{
				CommandID: 1, Program: spec.Program, Env: spec.Env, Workspace: spec.Workspace, Dir: spec.Directory,
				RegisterContainer: func(context.Context, operational.CLICommandExecution) error { return nil },
			}, nil)
			if err == nil || actual != -1 {
				t.Fatalf("invalid or lost result invented an exit code: %d %v", actual, err)
			}
			if !slices.Contains(f.machine.operations(), "stop") {
				t.Fatal("VM was not cleaned up after losing output")
			}
		})
	}
}

func TestWindowsSandboxOutputIsIncrementalBoundedAndRooted(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	execution := f.execution(1)
	spec := f.spec(t, "output")
	if err := runner.Lifecycle.Create(t.Context(), execution, spec); err != nil {
		t.Fatal(err)
	}
	root, streams, err := openWindowsSandboxOutput(spec.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = root.Close()
		for _, stream := range streams {
			_ = stream.file.Close()
		}
	}()
	filename := filepath.Join(spec.Workspace, ".cao-output", "stdout")
	// #nosec G304 -- Simulate guest writes to the fixture's private output file.
	writer, err := os.OpenFile(filename, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	var text strings.Builder
	for _, chunk := range []string{"one\n", "two\n"} {
		if _, err := writer.WriteString(chunk); err != nil {
			t.Fatal(err)
		}
		if err := readWindowsSandboxOutput(t.Context(), streams, func(_ string, data []byte) { _, _ = text.Write(data) }); err != nil {
			t.Fatal(err)
		}
	}
	if text.String() != "one\ntwo\n" {
		t.Fatal("output was reread or dropped:", text.String())
	}
	if err := writer.Truncate(windowsSandboxOutputLimit + 1); err != nil {
		t.Fatal(err)
	}
	if err := readWindowsSandboxOutput(t.Context(), streams, nil); err == nil {
		t.Fatal("unbounded output was accepted")
	}
}

func TestWindowsSandboxGuestOutputCannotFollowHostSymlinks(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	spec := f.spec(t, "symlink")
	if err := runner.Lifecycle.Create(t.Context(), f.execution(1), spec); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(f.base, "host-private")
	if err := os.WriteFile(external, []byte("host-private"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(spec.Workspace, ".cao-output", "stdout")
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, output); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink requires Windows developer mode or elevation")
		}
		t.Fatal(err)
	}
	if root, streams, err := openWindowsSandboxOutput(spec.Workspace); err == nil {
		_ = root.Close()
		for _, stream := range streams {
			_ = stream.file.Close()
		}
		t.Fatal("guest-controlled symlink escaped its private workspace")
	}
}

func TestWindowsSandboxForeignRuntimeAndOwnerCannotStopVM(t *testing.T) {
	f := newWindowsSandboxFixture(t)
	runner := f.runner(t)
	execution := f.execution(1)
	id, err := windowsSandboxID(execution)
	if err != nil {
		t.Fatal(err)
	}
	f.machine.id = id
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatal("a matching ID without ownership was stopped:", err)
	}
	execution.RuntimeID = "windows-sandbox:other-user"
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatal("foreign runtime recovery succeeded:", err)
	}
	if slices.Contains(f.machine.operations(), "stop") {
		t.Fatal("recovery addressed an unowned VM")
	}
}

func TestWindowsSandboxBootstrapIsFixedAndValidatesGuestBytes(t *testing.T) {
	for _, required := range []string{
		"Get-FileHash", "SHA256", "ExtractToDirectory", "Quote-NativeArgument", "UseShellExecute = $false",
		"EnvironmentVariables.Clear()", "--template=", "CopyToAsync", "FileShare]::ReadWrite",
		"result.next", "result.json", "$code -gt 255",
	} {
		if !strings.Contains(windowsSandboxBootstrap, required) {
			t.Fatalf("bootstrap lacks %q", required)
		}
	}
	if strings.Contains(windowsSandboxBootstrap, "Invoke-Expression") || strings.Contains(windowsSandboxBootstrap, "Start-Process") {
		t.Fatal("bootstrap uses shell evaluation or a detached launcher")
	}
	// The receipt carries an identity, not a bare integer that could be
	// confused with a previous command's result.
	data, err := json.Marshal(windowsSandboxReceipt{})
	if err != nil || !strings.Contains(string(data), `"execution"`) {
		t.Fatal("receipt omitted execution identity")
	}
}
