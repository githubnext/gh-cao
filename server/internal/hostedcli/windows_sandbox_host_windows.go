//go:build windows

package hostedcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func newWindowsSandboxHost(binary string) (windowsSandboxHost, error) {
	if err := windowsSandboxRequireHCS(); err != nil {
		return windowsSandboxHost{}, err
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox requires a Windows user SID")
	}
	sid := tokenUser.User.Sid.String()
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox machine identity is unavailable")
	}
	machineGUID, _, err := key.GetStringValue("MachineGuid")
	_ = key.Close()
	if err != nil || !windowsSandboxGUID.MatchString(machineGUID) {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox machine identity is invalid")
	}
	namespace := sha256.Sum256([]byte(strings.ToLower(machineGUID) + "\x00" + sid))
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox machine-wide state location is unavailable")
	}
	// One machine-wide directory, NOT one per project, database, executable,
	// session, or user. The first configured account owns this local profile.
	// Another account cannot take it over: an ACL mismatch fails closed.
	root := filepath.Join(programData, "CAO.WindowsSandboxExecutor.v1")
	sddl := "O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox ownership ACL could not be constructed")
	}
	attributes := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox state path is invalid")
	}
	if err := windows.CreateDirectory(path, attributes); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox private machine-wide state could not be created")
	}
	flags, err := windows.GetFileAttributes(path)
	if err != nil || flags&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || flags&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox state must be a non-reparse local directory")
	}
	actual, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || actual == nil || actual.String() != sd.String() {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox machine-wide state belongs to another user or has an unsafe ACL")
	}
	if binary == "" {
		binary = "wsb.exe"
	}
	binary, err = exec.LookPath(binary)
	if err != nil || !filepath.IsAbs(binary) || !strings.EqualFold(filepath.Base(binary), "wsb.exe") || strings.HasPrefix(binary, `\\`) {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox requires a locally installed wsb.exe, not a script or remote command")
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return windowsSandboxHost{}, errors.New("hosted CLI Windows Sandbox system directory is unavailable")
	}
	// Neither GH_TOKEN nor any server credential is inherited by wsb.
	return windowsSandboxHost{
		root: root, id: "windows-sandbox:" + hex.EncodeToString(namespace[:]), binary: binary,
		env:  []string{"SystemRoot=" + filepath.Dir(system), "WINDIR=" + filepath.Dir(system), "PATH=" + system},
		lock: windowsSandboxGlobalLock, busy: windowsSandboxHostBusy, computeSystems: windowsSandboxHCSInventory,
		privateWorkspace: func(ctx context.Context, workspace string) (func(), error) {
			return windowsSandboxPrivateWorkspace(ctx, workspace, sid)
		},
	}, nil
}

func windowsSandboxGlobalLock(ctx context.Context) (func(), error) {
	// Windows mutex ownership belongs to an OS thread, not a goroutine.
	runtime.LockOSThread()
	name, err := windows.UTF16PtrFromString(`Global\CAO.HostedCLI.WindowsSandbox.v1`)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		runtime.UnlockOSThread()
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = windows.CloseHandle(mutex)
			runtime.UnlockOSThread()
			return nil, err
		}
		status, err := windows.WaitForSingleObject(mutex, 50)
		if err != nil {
			_ = windows.CloseHandle(mutex)
			runtime.UnlockOSThread()
			return nil, err
		}
		switch status {
		case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
			// Abandonment releases only the OS mutex. The durable owner/phase
			// remains authoritative and is reconciled by the lifecycle.
			return func() {
				_ = windows.ReleaseMutex(mutex)
				_ = windows.CloseHandle(mutex)
				runtime.UnlockOSThread()
			}, nil
		case uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			_ = windows.CloseHandle(mutex)
			runtime.UnlockOSThread()
			return nil, errors.New("hosted CLI Windows Sandbox global mutex wait failed")
		}
	}
}

func windowsSandboxHostBusy() (bool, error) {
	// wsb list is current-user scoped. Check process names across sessions as
	// an additional conservative preflight for GUI/user-started sandboxes and
	// outstanding CLI launches; never attach to or terminate these processes.
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if name == "wsb.exe" || strings.Contains(name, "windowssandbox") {
			return true, nil
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return false, err
	}
	return false, nil
}
