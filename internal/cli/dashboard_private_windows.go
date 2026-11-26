//go:build windows

package cli

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows reports only a read-only bit through os.FileMode, so privacy is an
// explicit, protected DACL rather than POSIX permission bits.
func dashboardPrivateTrustees() (user *windows.SID, system *windows.SID, administrators *windows.SID, err error) {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, nil, err
	}
	if system, err = windows.CreateWellKnownSid(windows.WinLocalSystemSid); err != nil {
		return nil, nil, nil, err
	}
	if administrators, err = windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err != nil {
		return nil, nil, nil, err
	}
	return token.User.Sid, system, administrators, nil
}

// dashboardSecurePrivate replaces inherited access with full control for the
// current user and SYSTEM only.
func dashboardSecurePrivate(path string, directory bool) error {
	user, system, _, err := dashboardPrivateTrustees()
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entry := func(sid *windows.SID, kind windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  kind,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		entry(user, windows.TRUSTEE_IS_USER),
		entry(system, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
	}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// dashboardIsPrivate accepts a protected DACL whose allow entries grant only the
// current user, SYSTEM or Administrators, matching OpenSSH's private-key rule.
func dashboardIsPrivate(path string, _ os.FileInfo) bool {
	user, system, administrators, err := dashboardPrivateTrustees()
	if err != nil {
		return false
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	acl, _, err := descriptor.DACL()
	if err != nil || acl == nil {
		return false
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, index, &ace) != nil {
			return false
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.Equals(user) && !sid.Equals(system) && !sid.Equals(administrators) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
