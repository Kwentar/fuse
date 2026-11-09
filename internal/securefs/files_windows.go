package securefs

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

func UserID() (string, error) {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return "", e
	}
	return u.User.Sid.String(), nil
}
func Descriptor() (string, error) { s, e := UserID(); return "D:P(A;OICI;FA;;;" + s + ")", e }
func Protect(path string) error {
	s, e := Descriptor()
	if e != nil {
		return e
	}
	sd, e := windows.SecurityDescriptorFromString(s)
	if e != nil {
		return e
	}
	d, _, e := sd.DACL()
	if e != nil {
		return e
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION, u.User.Sid, nil, d, nil)
}
func Check(path string) error {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	attrs, e := windows.GetFileAttributes(p)
	if e != nil {
		return e
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse points are not allowed")
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	user, e := UserID()
	if e != nil {
		return e
	}
	if owner.String() != user {
		return errors.New("unexpected owner")
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return e
	}
	if acl == nil || acl.AceCount == 0 {
		return errors.New("missing DACL")
	}
	allowed := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, i, &ace); e != nil {
			return e
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported ACL entry")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if sid != user && sid != "S-1-5-18" && sid != "S-1-5-32-544" {
			return errors.New("access granted to another principal")
		}
		if sid == user {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("owner access missing")
	}
	return nil
}
func replace(from, to string) error {
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

type Lock struct{ handle windows.Handle }

func Acquire(dir string) (*Lock, error) {
	path := filepath.Join(dir, "node.lock")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	if e = Protect(path); e != nil {
		return nil, e
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, errors.New("NODE_ALREADY_RUNNING: data directory is locked")
	}
	return &Lock{h}, nil
}
func (l *Lock) Close() error { return windows.CloseHandle(l.handle) }
