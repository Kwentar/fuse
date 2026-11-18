package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fuse/internal/appinit"
	"fuse/internal/config"
	"fuse/internal/identity"
	"fuse/internal/securefs"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestMacProfileBackupKeepsIdentityAndIsIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	original, e := appinit.Init(dir, config.WithDefaultNetwork(config.DefaultClient()))
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "profile.json")
	if e = securefs.WriteJSON(path, map[string]string{"name": "before"}); e != nil {
		t.Fatal(e)
	}
	if e = prepareGUIProfile(dir); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(dir, "macos-version.json"))
	if e != nil {
		t.Fatal(e)
	}
	var marker map[string]string
	if e = json.Unmarshal(data, &marker); e != nil {
		t.Fatal(e)
	}
	key, e := identity.Load(marker["backup"])
	if e != nil {
		t.Fatal(e)
	}
	copied, e := peer.IDFromPrivateKey(key)
	if e != nil || copied != original {
		t.Fatal("identity changed", e)
	}
	if e = securefs.WriteJSON(path, map[string]string{"name": "after"}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(marker["backup"], "profile.json"))
	if e != nil {
		t.Fatal(e)
	}
	var saved map[string]string
	json.Unmarshal(b, &saved)
	if saved["name"] != "before" {
		t.Fatal("backup is not independent")
	}
	if e = prepareGUIProfile(dir); e != nil {
		t.Fatal(e)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "macos-version.json"))
	if string(again) != string(data) {
		t.Fatal("backup recreated")
	}
}
