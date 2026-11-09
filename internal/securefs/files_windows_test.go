package securefs

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"testing"
)

func TestWindowsACLAndOSLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if e := EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	if e := Check(dir); e != nil {
		t.Fatal(e)
	}
	lock, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Acquire(dir); e == nil {
		second.Close()
		t.Fatal("second lock acquired")
	}
	lock.Close()
	lock, e = Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
	sd, e := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, _ := sd.DACL()
	if e = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if Check(dir) == nil {
		t.Fatal("Everyone ACL accepted")
	}
	if e = Protect(dir); e != nil {
		t.Fatal(e)
	}
}
