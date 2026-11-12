package hostedcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// WindowsSandboxOptions selects an installed wsb.exe and a locally supplied,
// SHA256-pinned portable native-tools ZIP. See validateWindowsSandboxArchive
// for its layout. This is an explicit native-payload exception, not an OCI
// image runtime. The caller must restrict it to explicit local development.
type WindowsSandboxOptions struct {
	Binary       string
	ToolsArchive string
	SHA256       string
}

// NewWindowsSandboxRunner is available only on Windows. A fixed machine-wide,
// ACL-protected state directory and named mutex fence all local executor
// namespaces/processes, independently of queue concurrency. The exclusive
// HCS-VM development profile also requires no other HCS VMs before launch or
// after stop; it never assumes that a wsb ID identifies an HCS compute system.
func NewWindowsSandboxRunner(options WindowsSandboxOptions, commands Runner) (*ContainerRunner, error) {
	host, err := newWindowsSandboxHost(options.Binary)
	if err != nil {
		return nil, err
	}
	return newWindowsSandboxRunner(options, commands, host)
}

type windowsSandboxHost struct {
	root             string
	id               string
	binary           string
	env              []string
	lock             func(context.Context) (func(), error)
	busy             func() (bool, error)
	computeSystems   func(context.Context) ([]byte, error)
	privateWorkspace func(context.Context, string) (func(), error)
}

type windowsSandboxLifecycle struct {
	host       windowsSandboxHost
	binary     string
	archive    string
	digest     string
	commands   Runner
	interval   time.Duration
	mutex      sync.Mutex
	transports map[string]<-chan error
}

var _ ContainerLifecycle = (*windowsSandboxLifecycle)(nil)

func newWindowsSandboxRunner(options WindowsSandboxOptions, commands Runner, host windowsSandboxHost) (*ContainerRunner, error) {
	if !windowsSandboxDigest.MatchString(options.SHA256) || options.ToolsArchive == "" {
		return nil, errors.New("hosted CLI Windows Sandbox requires a local tools ZIP and its exact lowercase SHA256")
	}
	archive, err := filepath.Abs(options.ToolsArchive)
	if err != nil {
		return nil, errors.New("hosted CLI Windows Sandbox tools archive path is invalid")
	}
	// #nosec G304 -- An operator-selected local bundle is opened only for digest validation.
	file, err := os.Open(archive)
	if err != nil {
		return nil, errors.New("hosted CLI Windows Sandbox tools archive is unavailable")
	}
	err = validateWindowsSandboxArchive(file, options.SHA256)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	if commands == nil {
		commands = ExecRunner{}
	}
	lifecycle := &windowsSandboxLifecycle{
		host: host, binary: host.binary, archive: archive, digest: options.SHA256,
		commands: commands, interval: 100 * time.Millisecond, transports: make(map[string]<-chan error),
	}
	return &ContainerRunner{Image: "windows-bundle@sha256:" + options.SHA256, Lifecycle: lifecycle}, nil
}

func (r *windowsSandboxLifecycle) RuntimeID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if r.host.id == "" {
		return "", errors.New("hosted CLI Windows Sandbox machine/user identity is unavailable")
	}
	return r.host.id, nil
}

type windowsSandboxRecord struct {
	Execution operational.CLICommandExecution `json:"execution"`
	Phase     string                          `json:"phase"`
	Workspace string                          `json:"workspace"`
	ExitCode  *int                            `json:"exitCode,omitempty"`
}

func (r *windowsSandboxLifecycle) openState(ctx context.Context, execution operational.CLICommandExecution) (*os.Root, func(), error) {
	if execution.Validate() != nil || execution.RuntimeID != r.host.id {
		return nil, nil, errors.New("hosted CLI Windows Sandbox recovery requires the original machine and user")
	}
	unlock, err := r.host.lock(ctx)
	if err != nil {
		return nil, nil, errors.New("hosted CLI Windows Sandbox global ownership lock is unavailable")
	}
	root, err := os.OpenRoot(r.host.root)
	if err != nil {
		unlock()
		return nil, nil, errors.New("hosted CLI Windows Sandbox global ownership state is unavailable")
	}
	return root, func() { _ = root.Close(); unlock() }, nil
}

func windowsSandboxOwner(root *os.Root) (operational.CLICommandExecution, error) {
	var owner operational.CLICommandExecution
	err := windowsSandboxReadJSON(root, "owner.json", &owner)
	if err == nil && owner.Validate() != nil {
		err = errors.New("hosted CLI Windows Sandbox ownership record is invalid")
	}
	return owner, err
}

func windowsSandboxRecordFor(root *os.Root, execution operational.CLICommandExecution) (windowsSandboxRecord, error) {
	var record windowsSandboxRecord
	err := windowsSandboxReadJSON(root, windowsSandboxRecordPath(execution), &record)
	if err != nil {
		return record, err
	}
	if record.Execution != execution || (record.Phase != "prepared" && record.Phase != "starting" &&
		record.Phase != "running" && record.Phase != "stopped") ||
		(record.ExitCode != nil && (record.Phase != "stopped" || *record.ExitCode < 0 || *record.ExitCode > 255)) {
		return record, errors.New("hosted CLI Windows Sandbox execution state is invalid")
	}
	return record, nil
}

func (r *windowsSandboxLifecycle) Create(ctx context.Context, execution operational.CLICommandExecution, spec ContainerSpec) error {
	if spec.Image != "windows-bundle@sha256:"+r.digest || (spec.Program != "gh" && spec.Program != "gh-aw") {
		return errors.New("hosted CLI Windows Sandbox rejected an unpinned payload or unsupported executable")
	}
	// Check the supplied path before containerPaths can resolve a junction or
	// symlink. Unix permission bits are not a Windows security boundary.
	releaseWorkspace, err := r.checkPrivateWorkspace(ctx, spec.Workspace)
	if err != nil {
		return err
	}
	defer releaseWorkspace()
	workspace, directory, err := windowsSandboxPaths(spec.Workspace, spec.Directory)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{workspace, r.host.root}, {r.host.root, workspace}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return errors.New("hosted CLI Windows Sandbox workspace must not overlap executor state")
		}
	}
	environment, err := windowsSandboxEnvironment(spec.Env)
	if err != nil {
		return err
	}
	if len(spec.Args) > 128 || len(strings.Join(spec.Args, "")) > 16000 || strings.Contains(strings.Join(spec.Args, ""), "\x00") {
		return errors.New("hosted CLI Windows Sandbox command arguments exceed their limit")
	}
	root, release, err := r.openState(ctx, execution)
	if err != nil {
		return err
	}
	defer release()
	if _, err := windowsSandboxOwner(root); !errors.Is(err, os.ErrNotExist) {
		return errors.New("hosted CLI Windows Sandbox is reserved by another execution or requires recovery")
	}
	if err := r.requireEmpty(ctx); err != nil {
		return err
	}
	// Registration in the queue precedes Create. This second durable record
	// fences unrelated queues before any VM or guest command can be created.
	if err := windowsSandboxWriteJSON(root, "owner.json", execution); err != nil {
		return errors.New("hosted CLI Windows Sandbox global ownership could not be persisted")
	}
	if err := root.Mkdir(execution.Name, 0o700); err != nil {
		return errors.New("hosted CLI Windows Sandbox execution staging already exists or is unavailable")
	}
	record := windowsSandboxRecord{Execution: execution, Phase: "prepared", Workspace: workspace}
	if err := windowsSandboxWriteJSON(root, windowsSandboxRecordPath(execution), record); err != nil {
		return err
	}
	input := filepath.Join(execution.Name, "input")
	if err := root.Mkdir(input, 0o700); err != nil {
		return errors.New("hosted CLI Windows Sandbox input staging is unavailable")
	}
	if err := r.stageArchive(ctx, root, filepath.Join(input, "tools.zip")); err != nil {
		return err
	}
	if err := root.WriteFile(filepath.Join(input, "bootstrap.ps1"), []byte(windowsSandboxBootstrap), 0o600); err != nil {
		return errors.New("hosted CLI Windows Sandbox bootstrap could not be staged")
	}
	invocation := windowsSandboxInvocation{
		Execution: execution, SHA256: r.digest, Program: spec.Program,
		Args: append([]string{}, spec.Args...), Directory: directory, Env: environment,
	}
	if err := windowsSandboxWriteJSON(root, filepath.Join(input, "invocation.json"), invocation); err != nil {
		return err
	}
	workRoot, err := os.OpenRoot(workspace)
	if err != nil {
		return errors.New("hosted CLI Windows Sandbox private workspace is unavailable")
	}
	defer func() { _ = workRoot.Close() }()
	if err := workRoot.Mkdir(".cao-output", 0o700); err != nil {
		return errors.New("hosted CLI Windows Sandbox output directory must be new")
	}
	for _, name := range []string{"stdout", "stderr"} {
		file, err := workRoot.OpenFile(filepath.Join(".cao-output", name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return errors.New("hosted CLI Windows Sandbox output could not be prepared")
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (r *windowsSandboxLifecycle) stageArchive(ctx context.Context, root *os.Root, name string) error {
	// #nosec G304 -- Operator-selected bundle; the staged bytes are revalidated below.
	source, err := os.Open(r.archive)
	if err != nil {
		return errors.New("hosted CLI Windows Sandbox tools archive is unavailable")
	}
	defer func() { _ = source.Close() }()
	target, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("hosted CLI Windows Sandbox tools archive cannot be staged")
	}
	defer func() { _ = target.Close() }()
	buffer := make([]byte, 32<<10)
	reader := io.LimitReader(source, windowsSandboxArchiveLimit+1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			if _, err := target.Write(buffer[:n]); err != nil {
				return errors.New("hosted CLI Windows Sandbox tools archive staging failed")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return errors.New("hosted CLI Windows Sandbox tools archive could not be read")
		}
	}
	if err := target.Sync(); err != nil {
		return errors.New("hosted CLI Windows Sandbox tools archive could not be flushed")
	}
	return validateWindowsSandboxArchive(target, r.digest)
}

func (r *windowsSandboxLifecycle) requireEmpty(ctx context.Context) error {
	instances, err := r.inventory(ctx)
	if err != nil {
		return err
	}
	busy, err := r.host.busy()
	if err != nil || busy {
		return errors.New("hosted CLI Windows Sandbox is already in use; unrelated sandboxes cannot be reused or stopped")
	}
	for _, instance := range instances {
		if instance.Status != "stopped" {
			return errors.New("hosted CLI Windows Sandbox is already in use; unrelated sandboxes cannot be reused or stopped")
		}
	}
	count, err := r.hcsVMCount(ctx)
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("hosted CLI Windows Sandbox exclusive-VM development requires zero existing HCS VMs")
	}
	return nil
}

func (r *windowsSandboxLifecycle) Start(ctx context.Context, execution operational.CLICommandExecution) error {
	root, release, err := r.openState(ctx, execution)
	if err != nil {
		return err
	}
	defer release()
	owner, err := windowsSandboxOwner(root)
	if err != nil || owner != execution {
		return errors.New("hosted CLI Windows Sandbox launch does not own the global reservation")
	}
	record, err := windowsSandboxRecordFor(root, execution)
	if err != nil || record.Phase != "prepared" {
		return errors.New("hosted CLI Windows Sandbox execution cannot be started or retried")
	}
	releaseWorkspace, err := r.checkPrivateWorkspace(ctx, record.Workspace)
	if err != nil {
		return err
	}
	defer releaseWorkspace()
	if err := r.requireEmpty(ctx); err != nil {
		return err
	}

	config, err := windowsSandboxConfig(filepath.Join(r.host.root, execution.Name, "input"), record.Workspace)
	if err != nil {
		return errors.New("hosted CLI Windows Sandbox configuration could not be encoded")
	}
	// Never clear an uncertain start merely because inventory is empty: an
	// interrupted wsb request can still create a VM later. Recovery leaves
	// this phase fenced for operator intervention rather than guessing.
	record.Phase = "starting"
	if err := windowsSandboxWriteJSON(root, windowsSandboxRecordPath(execution), record); err != nil {
		return err
	}
	id, _ := windowsSandboxID(execution) // openState validated the identity.
	if _, err := r.capture(ctx, "start", "--id", id, "--config", config); err != nil {
		return err
	}
	instances, err := r.inventory(ctx)
	if err != nil || len(instances) != 1 || instances[0].ID != id || instances[0].Status != "running" {
		return errors.New("hosted CLI Windows Sandbox launch did not confirm the requested VM identity")
	}
	record.Phase = "running"
	if err := windowsSandboxWriteJSON(root, windowsSandboxRecordPath(execution), record); err != nil {
		return err
	}
	done := make(chan error, 1)
	r.mutex.Lock()
	r.transports[execution.Name] = done
	r.mutex.Unlock()
	go func() {
		// Only this fixed source is interpreted. The guest loads all workload
		// arguments/environment as JSON and uses native process argv quoting.
		_, err := r.capture(ctx, "exec", "--id", id, "--run-as", "System",
			"--working-directory", `C:\cao-input`, "--command",
			`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:\cao-input\bootstrap.ps1`)
		done <- err
	}()
	return nil
}

func (r *windowsSandboxLifecycle) checkPrivateWorkspace(ctx context.Context, workspace string) (func(), error) {
	if r.host.privateWorkspace == nil {
		return nil, errors.New("hosted CLI Windows Sandbox requires Windows workspace ACL validation")
	}
	release, err := r.host.privateWorkspace(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, errors.New("hosted CLI Windows Sandbox workspace validation did not pin its private boundary")
	}
	return release, nil
}

func (r *windowsSandboxLifecycle) Inspect(ctx context.Context, execution operational.CLICommandExecution) (ContainerState, error) {
	root, release, err := r.openState(ctx, execution)
	if err != nil {
		return ContainerState{}, err
	}
	defer release()
	record, err := windowsSandboxRecordFor(root, execution)
	if err != nil || record.Phase != "stopped" {
		return ContainerState{}, err
	}
	absent, err := r.absent(ctx, execution)
	if err != nil || !absent {
		return ContainerState{}, errors.New("hosted CLI Windows Sandbox VM termination is not confirmed")
	}
	return ContainerState{Terminated: true, ExitCode: record.ExitCode}, nil
}

func (r *windowsSandboxLifecycle) absent(ctx context.Context, execution operational.CLICommandExecution) (bool, error) {
	id, err := windowsSandboxID(execution)
	if err != nil {
		return false, err
	}
	instances, err := r.inventory(ctx)
	if err != nil {
		return false, err
	}
	count, err := r.hcsVMCount(ctx)
	if err != nil {
		return false, err
	}
	for _, instance := range instances {
		if instance.ID == id && instance.Status != "stopped" {
			return false, nil
		}
	}
	return count == 0, nil
}

// terminate is called only with the global mutex held and a matching durable
// owner/record. A stop acknowledgement, launcher exit, and guest receipt are
// all insufficient; fresh wsb inventory must confirm this exact VM stopped
// AND independent unfiltered HCS inventory must contain zero VMs. There is no
// verified wsb/HCS ID binding, so even an unrelated HCS VM prevents settlement.
func (r *windowsSandboxLifecycle) terminate(ctx context.Context, root *os.Root, record *windowsSandboxRecord) error {
	absent, err := r.absent(ctx, record.Execution)
	if err != nil {
		return err
	}
	if !absent {
		if record.Phase == "prepared" {
			return errors.New("hosted CLI Windows Sandbox cannot stop an unlaunched, unowned VM")
		}
		id, _ := windowsSandboxID(record.Execution)
		_, _ = r.capture(ctx, "stop", "--id", id)
		for {
			absent, err = r.absent(ctx, record.Execution)
			if err != nil {
				return err
			}
			if absent {
				break
			}
			if !sleep(ctx, r.interval) {
				return errors.New("hosted CLI Windows Sandbox stop was not independently confirmed")
			}
		}
	}
	if record.Phase == "starting" {
		return errors.New("hosted CLI Windows Sandbox start outcome is uncertain; global ownership remains fenced")
	}
	record.Phase = "stopped"
	return windowsSandboxWriteJSON(root, windowsSandboxRecordPath(record.Execution), record)
}

func (r *windowsSandboxLifecycle) Stop(ctx context.Context, execution operational.CLICommandExecution) error {
	root, release, err := r.openState(ctx, execution)
	if err != nil {
		return err
	}
	defer release()
	owner, ownerErr := windowsSandboxOwner(root)
	if ownerErr != nil && !errors.Is(ownerErr, os.ErrNotExist) {
		return errors.New("hosted CLI Windows Sandbox ownership cannot be recovered")
	}
	if owner != execution {
		// A rejected Create has no authority over a pre-existing reservation,
		// even when the other execution has the same queue-local command ID.
		absent, err := r.absent(ctx, execution)
		if err != nil || !absent {
			return errors.New("hosted CLI Windows Sandbox refuses to stop an unowned VM")
		}
		return nil
	}
	record, err := windowsSandboxRecordFor(root, execution)
	if errors.Is(err, os.ErrNotExist) {
		// Crash while reserving, before a prepared record: Start could not
		// have run. Do not address any VM, even one with a matching ID.
		absent, err := r.absent(ctx, execution)
		if err != nil || !absent {
			return errors.New("hosted CLI Windows Sandbox has no durable launch ownership")
		}
	} else if err != nil {
		return err
	} else if err := r.terminate(ctx, root, &record); err != nil {
		return err
	}
	if err := root.RemoveAll(execution.Name); err != nil {
		return errors.New("hosted CLI Windows Sandbox stopped but private staging could not be removed")
	}
	if err := root.Remove("owner.json"); err != nil {
		return errors.New("hosted CLI Windows Sandbox stopped but its reservation could not be released")
	}
	r.mutex.Lock()
	delete(r.transports, execution.Name)
	r.mutex.Unlock()
	return nil
}

type windowsSandboxReceipt struct {
	Execution operational.CLICommandExecution `json:"execution"`
	ExitCode  *int                            `json:"exitCode"`
}

type windowsSandboxOutput struct {
	file   *os.File
	stream string
	offset int64
}

func openWindowsSandboxOutput(workspace string) (*os.Root, []windowsSandboxOutput, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, nil, errors.New("hosted CLI Windows Sandbox output workspace is unavailable")
	}
	streams := make([]windowsSandboxOutput, 0, 2)
	for _, stream := range []string{"stdout", "stderr"} {
		name := filepath.Join(".cao-output", stream)
		info, statErr := root.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() {
			err = errors.New("hosted CLI Windows Sandbox output must be a regular, non-link file")
			break
		}
		file, openErr := root.Open(name)
		if openErr != nil {
			err = errors.New("hosted CLI Windows Sandbox output path escaped its workspace or could not be opened")
			break
		}
		actual, statErr := file.Stat()
		if statErr != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
			_ = file.Close()
			err = errors.New("hosted CLI Windows Sandbox output changed while opening")
			break
		}
		streams = append(streams, windowsSandboxOutput{file: file, stream: stream})
	}
	if err != nil {
		for _, stream := range streams {
			_ = stream.file.Close()
		}
		_ = root.Close()
		return nil, nil, err
	}
	return root, streams, nil
}

func readWindowsSandboxOutput(ctx context.Context, streams []windowsSandboxOutput, output OutputFunc) error {
	buffer := make([]byte, 32<<10)
	for index := range streams {
		stream := &streams[index]
		info, err := stream.file.Stat()
		if err != nil || info.Size() < stream.offset || info.Size() > windowsSandboxOutputLimit {
			return errors.New("hosted CLI Windows Sandbox output was lost or exceeded its limit")
		}
		// Snapshot the extent: a continuously writing guest cannot monopolize
		// this read loop. Open handles and offsets avoid full-file rereads.
		remaining := info.Size() - stream.offset
		for remaining > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := stream.file.Read(buffer[:min(int64(len(buffer)), remaining)])
			if n > 0 {
				stream.offset += int64(n)
				remaining -= int64(n)
				if output != nil {
					output(stream.stream, append([]byte(nil), buffer[:n]...))
				}
			}
			if err != nil || n == 0 {
				return errors.New("hosted CLI Windows Sandbox output changed while reading")
			}
		}
	}
	return nil
}

func (r *windowsSandboxLifecycle) CollectOutput(ctx context.Context, execution operational.CLICommandExecution, output OutputFunc) error {
	root, release, err := r.openState(ctx, execution)
	if err != nil {
		return err
	}
	record, err := windowsSandboxRecordFor(root, execution)
	release()
	if err != nil || record.Phase != "running" {
		return errors.New("hosted CLI Windows Sandbox output has no running execution")
	}
	work, streams, err := openWindowsSandboxOutput(record.Workspace)
	if err != nil {
		return err
	}
	defer func() {
		for _, stream := range streams {
			_ = stream.file.Close()
		}
		_ = work.Close()
	}()
	r.mutex.Lock()
	done := r.transports[execution.Name]
	r.mutex.Unlock()
	for {
		if err := readWindowsSandboxOutput(ctx, streams, output); err != nil {
			return err
		}
		var receipt windowsSandboxReceipt
		err := windowsSandboxReadJSON(work, filepath.Join(".cao-output", "result.json"), &receipt)
		if err == nil {
			if receipt.Execution != execution || receipt.ExitCode == nil || *receipt.ExitCode < 0 || *receipt.ExitCode > 255 {
				return errors.New("hosted CLI Windows Sandbox workload exit evidence is invalid")
			}
			if err := r.complete(ctx, execution, *receipt.ExitCode); err != nil {
				return err
			}
			// Freeze the VM before the final drain, including any descendants.
			return readWindowsSandboxOutput(ctx, streams, output)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return errors.New("hosted CLI Windows Sandbox workload exit evidence is unavailable")
		}
		select {
		case <-done:
			// Read once more after the transport ends; the receipt can have
			// been atomically published between the read and channel receive.
			done = nil
			if windowsSandboxReadJSON(work, filepath.Join(".cao-output", "result.json"), &receipt) != nil {
				return errors.New("hosted CLI Windows Sandbox command transport ended without workload evidence")
			}
			continue
		default:
		}
		if !sleep(ctx, r.interval) {
			return ctx.Err()
		}
	}
}

func (r *windowsSandboxLifecycle) complete(ctx context.Context, execution operational.CLICommandExecution, exitCode int) error {
	root, release, err := r.openState(ctx, execution)
	if err != nil {
		return err
	}
	defer release()
	owner, err := windowsSandboxOwner(root)
	if err != nil || owner != execution {
		return errors.New("hosted CLI Windows Sandbox completion lost global ownership")
	}
	record, err := windowsSandboxRecordFor(root, execution)
	if err != nil || record.Phase != "running" {
		return errors.New("hosted CLI Windows Sandbox completion has no acknowledged launch")
	}
	if err := r.terminate(ctx, root, &record); err != nil {
		return fmt.Errorf("%w: Windows Sandbox VM is still unconfirmed", ErrContainerTerminationUnconfirmed)
	}
	record.ExitCode = &exitCode
	return windowsSandboxWriteJSON(root, windowsSandboxRecordPath(execution), record)
}
