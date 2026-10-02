//go:build windows

package privatefile

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileAllAccess = 0x001f01ff

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid.Copy()
}

func protectedDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	sddl := "O:" + user.String() + "D:P(A;;FA;;;" + user.String() + ")"
	if user.String() != "S-1-5-18" {
		sddl += "(A;;FA;;;SY)"
	}
	return windows.SecurityDescriptorFromString(sddl)
}

func openHandle(filename string, access uint32, disposition uint32, attributes *windows.SecurityAttributes) (*os.File, error) {
	path, err := windows.UTF16PtrFromString(filename)
	if err != nil {
		return nil, err
	}
	// Deny competing write/delete handles while checking and reading the object.
	// OPEN_REPARSE_POINT lets us reject the object rather than follow its target.
	handle, err := windows.CreateFile(path, access, windows.FILE_SHARE_READ, attributes,
		disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open private file", Path: filename, Err: err}
	}
	f := os.NewFile(uintptr(handle), filename)
	if f == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("private file handle is unavailable")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = f.Close()
		return nil, errors.New("private file cannot be a directory or reparse point")
	}
	return f, nil
}

func securityInfo(f *os.File) (*windows.SECURITY_DESCRIPTOR, error) {
	return windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
}

func checkOwner(sd *windows.SECURITY_DESCRIPTOR) (*windows.SID, error) {
	user, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.IsValid() || !windows.EqualSid(owner, user) {
		return nil, errors.New("private file must be owned by the current Windows user")
	}
	return user, nil
}

func validateACL(sd *windows.SECURITY_DESCRIPTOR) error {
	user, err := checkOwner(sd)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("private file requires a protected Windows ACL")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 || acl.AceCount > 2 {
		return errors.New("private file requires an explicit owner/SYSTEM ACL")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	ownerAllowed := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Header.AceSize < 20 || ace.Mask != fileAllAccess {
			return errors.New("private file has an unsupported or permissive Windows ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || int(ace.Header.AceSize) < 8+sid.Len() {
			return errors.New("private file has an invalid Windows ACL entry")
		}
		owner := windows.EqualSid(sid, user)
		if (!owner && !windows.EqualSid(sid, system)) || seen[sid.String()] {
			return errors.New("private file permits another account or duplicate Windows ACL entry")
		}
		seen[sid.String()] = true
		ownerAllowed = ownerAllowed || owner
	}
	runtime.KeepAlive(sd)
	if !ownerAllowed {
		return errors.New("private file ACL does not grant its current owner access")
	}
	return nil
}

func openPrivate(filename string) (*os.File, error) {
	f, err := openHandle(filename, windows.GENERIC_READ|windows.READ_CONTROL, windows.OPEN_EXISTING, nil)
	if err != nil {
		return nil, err
	}
	sd, err := securityInfo(f)
	if err == nil {
		err = validateACL(sd)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func createPrivate(filename string) (*os.File, error) {
	sd, err := protectedDescriptor()
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	f, err := openHandle(filename, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		windows.CREATE_NEW, &attributes)
	runtime.KeepAlive(sd)
	if err != nil {
		return nil, err
	}
	actual, err := securityInfo(f)
	if err == nil {
		err = validateACL(actual)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("establishing private Windows ACL: %w", err)
	}
	return f, nil
}

func validateReplacement(filename string) error {
	f, err := openHandle(filename, windows.READ_CONTROL, windows.OPEN_EXISTING, nil)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sd, err := securityInfo(f)
	if err != nil {
		return err
	}
	_, err = checkOwner(sd)
	return err
}

func replacePrivate(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// File.Sync uses FlushFileBuffers. Windows does not support POSIX directory
// fsync; credential replacements additionally use MOVEFILE_WRITE_THROUGH.
func SyncParent(_ string) error { return nil }
